package main

import (
	"context"
	"crypto/subtle"
	"errors"
	"log"
	"net/http"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	// workerAddr is where the agent serves the admin container.
	workerAddr = "127.0.0.1:9180"
	// controlAddr is where the supervisor accepts restart requests. It is a
	// separate listener so it still answers while the worker is hung or gone.
	controlAddr = "127.0.0.1:9181"
	// respawnDelay paces restarts after the worker exits on its own, so a
	// worker that fails at startup does not spin.
	defaultRespawnDelay = 5 * time.Second
)

func bearerOK(r *http.Request, token string) bool {
	got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	return subtle.ConstantTimeCompare([]byte(got), []byte(token)) == 1
}

// supervisor runs the agent worker (this same binary, started without
// -supervise) as a child process and restarts it when asked or when it dies.
type supervisor struct {
	exe          string
	args         []string
	respawnDelay time.Duration
	token        string
	restart      chan struct{}
}

func newSupervisor(exe, envFile, token string) *supervisor {
	return &supervisor{
		exe:          exe,
		args:         []string{"-env", envFile, "-addr", workerAddr},
		respawnDelay: defaultRespawnDelay,
		token:        token,
		restart:      make(chan struct{}, 1),
	}
}

// requestRestart asks run to restart the worker. Requests that arrive while
// one is already pending merge into it, so a double click restarts once.
func (s *supervisor) requestRestart() {
	select {
	case s.restart <- struct{}{}:
	default:
	}
}

// run keeps one worker running until ctx is done, then stops it.
func (s *supervisor) run(ctx context.Context) {
	for {
		cmd := exec.Command(s.exe, s.args...)
		cmd.Dir = filepath.Dir(s.exe)
		cmd.Stdout = log.Writer()
		cmd.Stderr = log.Writer()
		if err := cmd.Start(); err != nil {
			log.Printf("start host agent: %v", err)
			if !s.pause(ctx, s.respawnDelay) {
				return
			}
			continue
		}
		log.Printf("host agent worker started (pid %d)", cmd.Process.Pid)

		exited := make(chan error, 1)
		go func() { exited <- cmd.Wait() }()
		select {
		case err := <-exited:
			log.Printf("host agent worker exited (%v); restarting in %s", err, s.respawnDelay)
			if !s.pause(ctx, s.respawnDelay) {
				return
			}
		case <-s.restart:
			log.Print("restart requested; stopping host agent worker")
			killWorker(cmd)
			<-exited
		case <-ctx.Done():
			killWorker(cmd)
			<-exited
			return
		}
	}
}

// pause waits before the next start. A restart request ends the wait early,
// because the worker is about to start anyway. It reports false if ctx ends first.
func (s *supervisor) pause(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-s.restart:
		return true
	case <-ctx.Done():
		return false
	}
}

func killWorker(cmd *exec.Cmd) {
	if err := cmd.Process.Kill(); err != nil {
		log.Printf("stop host agent worker: %v", err)
	}
}

func (s *supervisor) controlHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("ok"))
	})
	mux.HandleFunc("POST /restart", func(w http.ResponseWriter, r *http.Request) {
		if !bearerOK(r, s.token) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		s.requestRestart()
		w.WriteHeader(http.StatusAccepted)
	})
	return mux
}

// runSupervisor serves restart requests on controlAddr and keeps the worker
// running until ctx is done. exe is the path of this binary.
func runSupervisor(ctx context.Context, exe, envFile, token string) error {
	ctx, stop := context.WithCancel(ctx)
	s := newSupervisor(exe, envFile, token)
	done := make(chan struct{})
	go func() {
		s.run(ctx)
		close(done)
	}()
	defer func() {
		stop()
		<-done
	}()

	srv := &http.Server{Addr: controlAddr, Handler: s.controlHandler(), ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		srv.Close()
	}()
	log.Printf("supervisor control listening on %s", controlAddr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
