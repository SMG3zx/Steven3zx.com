package gateway

import (
	"context"
	"errors"
	"fmt"
	"janus/internal/platform/tracing"
	"net"
	"net/http"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// listenState holds the running http.Server.
type listenState struct {
	mu     sync.Mutex
	server *http.Server
	addr   string
	failed chan error
}

// Start listens on the configured address and serves requests in the
// background. It returns the listen error, if any. Once Start has returned
// nil, an unexpected serve failure arrives on Failed, which is closed when
// serving ends.
func (s *Server) Start() error {
	l := s.listener
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.server != nil {
		return errors.New("http server already started")
	}
	listener, err := net.Listen("tcp", s.dependencies.Addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", s.dependencies.Addr, err)
	}
	l.addr = listener.Addr().String()
	l.failed = make(chan error, 1)
	l.server = &http.Server{
		Addr: l.addr, Handler: serverHandler{app: s.handler, record: s.dependencies.RecordRequest},
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}
	server, failed := l.server, l.failed
	go func() {
		defer close(failed)
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			failed <- err
		}
	}()
	return nil
}

// Addr is the address the server listens on, or "" before Start.
func (s *Server) Addr() string {
	s.listener.mu.Lock()
	defer s.listener.mu.Unlock()
	return s.listener.addr
}

// Failed receives the error if serving stops unexpectedly after Start, and is
// closed once serving ends. It is nil before Start.
func (s *Server) Failed() <-chan error {
	s.listener.mu.Lock()
	defer s.listener.mu.Unlock()
	return s.listener.failed
}

// Shutdown stops accepting connections and waits for requests in flight to
// finish, until ctx ends. It does nothing if the server was never started.
func (s *Server) Shutdown(ctx context.Context) error {
	s.listener.mu.Lock()
	server := s.listener.server
	s.listener.mu.Unlock()
	if server == nil {
		return nil
	}
	return server.Shutdown(ctx)
}

type serverHandler struct {
	app    http.Handler
	record func(ctx context.Context, method string, status int, duration time.Duration)
}

func (h serverHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	ctx, span := tracing.StartSpan(r.Context(), "http.edge", trace.WithSpanKind(trace.SpanKindServer))
	writer := &statusWriter{ResponseWriter: w, status: http.StatusOK}
	defer func() {
		if h.record != nil {
			h.record(ctx, r.Method, writer.status, time.Since(started))
		}
		span.End()
	}()
	span.SetAttributes(
		attribute.String("http.request.method", r.Method),
		attribute.String("url.path", r.URL.Path),
	)
	r.Body = http.MaxBytesReader(writer, r.Body, 1<<20)
	h.app.ServeHTTP(writer, r.WithContext(ctx))
	span.SetAttributes(attribute.Int("http.response.status_code", writer.status))
	if writer.status >= http.StatusInternalServerError {
		span.SetStatus(codes.Error, http.StatusText(writer.status))
	}
}

type statusWriter struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (w *statusWriter) WriteHeader(code int) {
	if w.wrote || code < 100 || code > 599 {
		return
	}
	w.status = code
	w.wrote = true
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(body []byte) (int, error) {
	if !w.wrote {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(body)
}
