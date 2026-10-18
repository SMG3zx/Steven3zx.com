package web

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	actortrace "janus/actors/telemetry"
)

// otel.Tracer returns a delegating tracer, so this works even if it's
// declared before telemetry.Setup runs.
var tracer = otel.Tracer("github.com/SMG3zx/Janus/web")

// Handler responds to a matched request. Returned strings are sent as text,
// []byte values as raw bytes, nil as an empty 200 response, and other values as
// JSON. A handler may also write directly through Context.
type Handler func(c *Context) any

// Context provides a handler with the request, route parameters, query values,
// response headers, and response-writing helpers.
type Context struct {
	Request *http.Request
	Params  map[string]string
	Query   url.Values
}

// Param returns a path parameter by name, or "" if it isn't present
func (c *Context) Param(name string) string {
	return c.Params[name]
}

// App is a chainable route collection that serves registered handlers as an
// http.Handler.
type App struct {
	prefix string
	r      *router
}

// New creates an app whose prefix is prepended to every route registered on it.
func New(prefix string) *App {
	return &App{prefix: prefix, r: newRouter()}
}

// GET, POST, PUT, PATCH, DELETE verb methods
func (a *App) Get(path string, h Handler) *App    { return a.Route(http.MethodGet, path, h) }
func (a *App) Post(path string, h Handler) *App   { return a.Route(http.MethodPost, path, h) }
func (a *App) Put(path string, h Handler) *App    { return a.Route(http.MethodPut, path, h) }
func (a *App) Patch(path string, h Handler) *App  { return a.Route(http.MethodPatch, path, h) }
func (a *App) Delete(path string, h Handler) *App { return a.Route(http.MethodDelete, path, h) }

// All matches any HTTP method
// It is the fallback when no method-specific route matches the same path.
func (a *App) All(path string, h Handler) *App { return a.Route("*", path, h) }

// Route registers a handler for an arbitrary method.
// Methods are case-sensitive, so use uppercase, ex. "M-SEARCH"
func (a *App) Route(method, path string, h Handler) *App {
	a.r.insert(method, joinPath(a.prefix, path), h)
	return a
}

// Group registers routes under a shared prefix inside fn.
func (a *App) Group(prefix string, fn func(g *App)) *App {
	fn(&App{prefix: joinPath(a.prefix, prefix), r: a.r})
	return a
}

// Use merges the routes of another App (built with New) into this one.
// Routes keep the prefix they were defined with.
func (a *App) Use(sub *App) *App {
	for _, rt := range sub.r.routes {
		a.r.insert(rt.method, rt.path, rt.handler)
	}
	return a
}

// ServeHttp makes App and http.Handler.
func (a *App) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	// Continue an upstream trace if the caller sent a traceparent header.
	ctx := req.Context()
	if !trace.SpanFromContext(ctx).SpanContext().IsValid() {
		ctx = otel.GetTextMapPropagator().Extract(ctx, propagation.HeaderCarrier(req.Header))
	}

	ctx, span := actortrace.StartSpanWithScope(ctx, tracer, req.Method,
		trace.WithSpanKind(trace.SpanKindServer))
	defer span.End()

	h, params, pattern := a.r.match(req.Method, req.URL.Path)

	route := pattern
	if h == nil {
		route = "NOT_FOUND"
	}
	// Set the name now that the route is known. "GET /id/:id" is low cardinality.
	span.SetName(req.Method + " " + route)
	span.SetAttributes(
		attribute.String("http.request.method", req.Method),
		attribute.String("http.route", route),
		attribute.String("url.path", req.URL.Path),
	)

	sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}

	// Deferred calls run in reverse order, so this runs before span.End().
	defer func() {
		span.SetAttributes(attribute.Int("http.response.status_code", sw.status))
		if sw.status >= 500 {
			span.SetStatus(codes.Error, http.StatusText(sw.status))
		}
	}()

	if h == nil {
		sw.WriteHeader(http.StatusNotFound)
		io.WriteString(sw, "NOT_FOUND")
		return
	}

	handlerCtx, handlerSpan := actortrace.StartSpanWithScope(ctx, tracer, "http.route.handler",
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(attribute.String("http.route", route)),
	)
	func() {
		defer handlerSpan.End()
		// Pass the traced context to handlers so they can start child spans
		// with c.Request.Context().
		writeResult(sw, h(&Context{
			Request: req.WithContext(handlerCtx),
			Params:  params,
			Query:   url.Values(req.URL.Query()),
		}))
		handlerSpan.SetAttributes(attribute.Int("http.response.status_code", sw.status))
		if sw.status >= http.StatusInternalServerError {
			handlerSpan.SetStatus(codes.Error, http.StatusText(sw.status))
		}
	}()
}

// statusWriter records the status code so it can go on the span.
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (s *statusWriter) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

// Listen starts an HTTP server on addr. It blocks until the server stops.
func (a *App) Listen(addr string) error {
	return http.ListenAndServe(addr, a)
}

func writeResult(w http.ResponseWriter, out any) {
	switch v := out.(type) {
	case nil:
		w.WriteHeader(http.StatusOK)
	case string:
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		io.WriteString(w, v)
	case []byte:
		w.Write(v)
	case Response:
		writeResponse(w, v)
	case *Response:
		if v == nil {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		writeResponse(w, *v)
	default:
		b, err := json.Marshal(v)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(b)
	}
}

// Response lets a route set an explicit status, headers, cookies, and body.
// Response describes an explicit HTTP response returned by a Handler.
type Response struct {
	StatusCode int
	Header     http.Header
	Body       any
}

func writeResponse(w http.ResponseWriter, response Response) {
	for key, values := range response.Header {
		w.Header()[key] = append([]string(nil), values...)
	}
	status := response.StatusCode
	if status < 100 || status > 599 {
		status = http.StatusOK
	}
	switch body := response.Body.(type) {
	case nil:
	case string:
		if w.Header().Get("Content-Type") == "" {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		}
	case []byte:
	default:
		encoded, err := json.Marshal(body)
		if err != nil {
			http.Error(w, "failed to encode response", http.StatusInternalServerError)
			return
		}
		if w.Header().Get("Content-Type") == "" {
			w.Header().Set("Content-Type", "application/json")
		}
		response.Body = encoded
	}
	w.WriteHeader(status)
	if status == http.StatusNoContent || status == http.StatusNotModified {
		return
	}
	switch body := response.Body.(type) {
	case string:
		_, _ = io.WriteString(w, body)
	case []byte:
		_, _ = w.Write(body)
	}
}

func joinPath(prefix, path string) string {
	return strings.TrimRight(prefix, "/") + "/" + strings.TrimLeft(path, "/")
}
