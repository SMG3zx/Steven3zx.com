package httpserver

import (
	"context"
	"errors"
	"net"
	"net/http"
	"time"

	goakt "github.com/tochemey/goakt/v4/actor"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	actortrace "janus/actors/telemetry"
	"janus/web"
)

// HttpServerActor owns an HTTP server and reports unexpected listener failures
// to its supervisor.
type HttpServerActor struct {
	addr       string
	app        *web.App
	server     *http.Server
	supervisor *goakt.PID
}

var _ goakt.Actor = (*HttpServerActor)(nil)

// New creates an HTTP server actor that listens on addr and serves app. The
// supervisor receives a ServeFailed message if serving stops unexpectedly.
func New(addr string, app *web.App, supervisor *goakt.PID) *HttpServerActor {
	return &HttpServerActor{addr: addr, app: app, supervisor: supervisor}
}

func (a *HttpServerActor) PreStart(*goakt.Context) error {
	listener, err := net.Listen("tcp", a.addr)
	if err != nil {
		return err
	}
	a.addr = listener.Addr().String()
	a.server = &http.Server{
		Addr: a.addr, Handler: serverHandler{app: a.app},
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}
	go func() {
		if err := a.server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			if a.supervisor != nil {
				_ = goakt.Tell(context.Background(), a.supervisor, &ServeFailed{Err: err})
			}
		}
	}()
	return nil
}

func (a *HttpServerActor) Receive(ctx *goakt.ReceiveContext) {
	switch msg := ctx.Message().(type) {
	case *Stop:
		err := a.server.Shutdown(msg.Context)
		ctx.Response(&Stopped{Err: err})
	default:
		ctx.Unhandled()
	}
}

func (*HttpServerActor) PostStop(*goakt.Context) error { return nil }

type serverHandler struct{ app *web.App }

func (h serverHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	ctx, span := actortrace.StartSpan(r.Context(), "http.edge", trace.WithSpanKind(trace.SpanKindServer))
	writer := &statusWriter{ResponseWriter: w, status: http.StatusOK}
	defer func() {
		actortrace.RecordHTTPRequest(ctx, r.Method, writer.status, time.Since(started))
		span.End()
	}()
	span.SetAttributes(
		attribute.String("http.request.method", r.Method),
		attribute.String("url.path", r.URL.Path),
		attribute.String("actor.type", "http-server"),
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
