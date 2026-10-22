// Package server exposes the search and status API on localhost.
package server

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"spotlight/internal/search"
	"spotlight/internal/store"
)

type Server struct {
	st   *store.Store
	srch *search.Searcher
	log  *slog.Logger
}

func New(st *store.Store, srch *search.Searcher, log *slog.Logger) *Server {
	return &Server{st: st, srch: srch, log: log}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /search", s.handleSearch)
	mux.HandleFunc("GET /status", s.handleStatus)
	return mux
}

// GET /search?q=...&limit=20
func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing q"})
		return
	}
	limit := 20
	if v, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && v > 0 && v <= 100 {
		limit = v
	}
	results, err := s.srch.Search(r.Context(), q, limit)
	if err != nil {
		s.log.Error("search failed", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "search failed"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"query": q, "results": results})
}

// GET /status
func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	files, chunks, embedded, err := s.st.Counts(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]int64{
		"files":           files,
		"chunks":          chunks,
		"embedded_chunks": embedded,
	})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
