// Package api exposes the VM manager over HTTP for the web UI.
package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"firecracker-manager/internal/images"
	"firecracker-manager/internal/vm"
)

const (
	maxBodyBytes = 64 << 10
	// consoleTailBytes is how much existing console output a new stream replays.
	consoleTailBytes = 64 << 10
	consolePoll      = 250 * time.Millisecond
)

// Server routes HTTP requests to the VM manager and serves the built web UI.
type Server struct {
	mgr       *vm.Manager
	fcBin     string
	webDir    string
	imagesDir string
}

// NewHandler returns the HTTP handler. If webDir is non-empty, files under it
// are served for every non-API path, falling back to index.html. imagesDir is
// scanned for kernel and rootfs suggestions.
func NewHandler(mgr *vm.Manager, fcBin, webDir, imagesDir string) http.Handler {
	s := &Server{mgr: mgr, fcBin: fcBin, webDir: webDir, imagesDir: imagesDir}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/host", s.handleHost)
	mux.HandleFunc("GET /api/images", s.handleImages)
	mux.HandleFunc("GET /api/vms", s.handleList)
	mux.HandleFunc("POST /api/vms", s.handleCreate)
	mux.HandleFunc("GET /api/vms/{id}", s.handleGet)
	mux.HandleFunc("DELETE /api/vms/{id}", s.handleDelete)
	mux.HandleFunc("POST /api/vms/{id}/start", s.action(s.mgr.Start))
	mux.HandleFunc("POST /api/vms/{id}/stop", s.action(s.mgr.Stop))
	mux.HandleFunc("POST /api/vms/{id}/pause", s.action(s.mgr.Pause))
	mux.HandleFunc("POST /api/vms/{id}/resume", s.action(s.mgr.Resume))
	mux.HandleFunc("POST /api/vms/{id}/console/input", s.handleInput)
	mux.HandleFunc("GET /api/vms/{id}/console", s.handleConsole)
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "no such endpoint")
	})
	mux.HandleFunc("/", s.handleStatic)

	return logRequests(mux)
}

func (s *Server) handleHost(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, vm.Host(r.Context(), s.fcBin))
}

func (s *Server) handleImages(w http.ResponseWriter, r *http.Request) {
	catalog, err := images.Scan(s.imagesDir)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, catalog)
}

func (s *Server) handleList(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.mgr.List())
}

func (s *Server) handleCreate(w http.ResponseWriter, r *http.Request) {
	var spec vm.Spec
	if !decodeBody(w, r, &spec) {
		return
	}
	created, err := s.mgr.Create(spec)
	if err != nil {
		writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (s *Server) handleGet(w http.ResponseWriter, r *http.Request) {
	v, err := s.mgr.Get(r.PathValue("id"))
	if err != nil {
		writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) handleDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.mgr.Delete(r.PathValue("id")); err != nil {
		writeFailure(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// action wraps a lifecycle method and responds with the VM's updated view.
func (s *Server) action(run func(id string) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if err := run(id); err != nil {
			writeFailure(w, err)
			return
		}
		v, err := s.mgr.Get(id)
		if err != nil {
			writeFailure(w, err)
			return
		}
		writeJSON(w, http.StatusOK, v)
	}
}

func (s *Server) handleInput(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Data string `json:"data"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	if err := s.mgr.SendInput(r.PathValue("id"), []byte(body.Data)); err != nil {
		writeFailure(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleConsole streams a VM's serial console as server-sent events. Each
// event's data is a JSON string holding the next chunk of output.
func (s *Server) handleConsole(w http.ResponseWriter, r *http.Request) {
	path, err := s.mgr.ConsolePath(r.PathValue("id"))
	if err != nil {
		writeFailure(w, err)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	// The log file may not exist until the VM has been started, so the file
	// is opened lazily and the stream waits for it.
	var (
		f      *os.File
		offset int64
		tailed bool
		chunk  = make([]byte, 32<<10)
		ticker = time.NewTicker(consolePoll)
	)
	defer ticker.Stop()
	defer func() {
		if f != nil {
			_ = f.Close()
		}
	}()

	for {
		if f == nil {
			f, _ = os.Open(path)
		}
		if f != nil {
			if !tailed {
				if info, err := f.Stat(); err == nil && info.Size() > consoleTailBytes {
					offset = info.Size() - consoleTailBytes
				}
				tailed = true
			}
			// A restarted VM truncates its log, so the offset can run past the end.
			if info, err := f.Stat(); err == nil && info.Size() < offset {
				offset = 0
			}
			for {
				n, _ := f.ReadAt(chunk, offset)
				if n == 0 {
					break
				}
				if err := writeEvent(w, chunk[:n]); err != nil {
					return
				}
				offset += int64(n)
			}
			flusher.Flush()
		}

		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *Server) handleStatic(w http.ResponseWriter, r *http.Request) {
	if s.webDir == "" {
		http.NotFound(w, r)
		return
	}
	name := filepath.Join(s.webDir, filepath.Clean("/"+r.URL.Path))
	if info, err := os.Stat(name); err == nil && info.IsDir() {
		name = filepath.Join(name, "index.html")
	}
	// Unknown paths fall back to the root page so the client-side router can take over.
	if info, err := os.Stat(name); err != nil || info.IsDir() {
		name = filepath.Join(s.webDir, "index.html")
	}
	http.ServeFile(w, r, name)
}

func writeEvent(w io.Writer, chunk []byte) error {
	data, err := json.Marshal(string(chunk))
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "data: %s\n\n", data)
	return err
}

func decodeBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return false
	}
	return true
}

// writeFailure maps manager errors onto HTTP status codes.
func writeFailure(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	switch {
	case errors.Is(err, vm.ErrNotFound):
		status = http.StatusNotFound
	case errors.Is(err, vm.ErrConflict):
		status = http.StatusConflict
	case errors.Is(err, vm.ErrInvalid):
		status = http.StatusBadRequest
	}
	writeError(w, status, err.Error())
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		slog.Debug("request", "method", r.Method, "path", r.URL.Path, "took", time.Since(start).String())
	})
}
