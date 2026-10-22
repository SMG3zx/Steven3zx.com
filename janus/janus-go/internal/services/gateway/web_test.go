package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func send(app *mux, method, target string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest(method, target, nil))
	return rec
}

func expectBody(t *testing.T, rec *httptest.ResponseRecorder, want string) {
	t.Helper()
	if got := rec.Body.String(); got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
}

func TestHandleRoot(t *testing.T) {
	app := newMux("").Get("/", func(c *call) any { return "Hi" })
	expectBody(t, send(app, http.MethodGet, "/"), "Hi")
}

func TestHandleMultipleLevel(t *testing.T) {
	app := newMux("").Get("/this/is/my/deep/nested/root", func(c *call) any { return "Ok" })
	expectBody(t, send(app, http.MethodGet, "/this/is/my/deep/nested/root"), "Ok")
}

func TestReturnBoolean(t *testing.T) {
	app := newMux("").Get("/", func(c *call) any { return true })
	expectBody(t, send(app, http.MethodGet, "/"), "true")
}

func TestReturnNumber(t *testing.T) {
	app := newMux("").Get("/", func(c *call) any { return 617 })
	expectBody(t, send(app, http.MethodGet, "/"), "617")
}

func TestReturnJSON(t *testing.T) {
	app := newMux("").Get("/", func(c *call) any {
		return map[string]string{"name": "takodachi"}
	})
	rec := send(app, http.MethodGet, "/")
	expectBody(t, rec, `{"name":"takodachi"}`)
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("content-type = %q, want application/json", ct)
	}
}

func TestParseSingleParam(t *testing.T) {
	app := newMux("").Get("/id/:id", func(c *call) any { return c.Param("id") })
	expectBody(t, send(app, http.MethodGet, "/id/123"), "123")
}

func TestParseMultipleParams(t *testing.T) {
	app := newMux("").Get("/id/:id/:name", func(c *call) any {
		return c.Param("id") + "/" + c.Param("name")
	})
	expectBody(t, send(app, http.MethodGet, "/id/fubuki/Elysia"), "fubuki/Elysia")
}

func TestParseOptionalParams(t *testing.T) {
	app := newMux("").Get("/id/:id?", func(c *call) any { return c.Param("id") })

	expectBody(t, send(app, http.MethodGet, "/id"), "")
	expectBody(t, send(app, http.MethodGet, "/id/fubuki"), "fubuki")
}

func TestParseMultipleOptionalParams(t *testing.T) {
	app := newMux("").Get("/id/:id?/:name?", func(c *call) any {
		return c.Param("id") + "/" + c.Param("name")
	})

	cases := []struct{ target, want string }{
		{"/id", "/"},
		{"/id/fubuki", "fubuki/"},
		{"/id/fubuki/shirakami", "fubuki/shirakami"},
	}
	for _, tc := range cases {
		t.Run(tc.target, func(t *testing.T) {
			expectBody(t, send(app, http.MethodGet, tc.target), tc.want)
		})
	}
}

func TestAcceptWildcard(t *testing.T) {
	app := newMux("").Get("/wildcard/*", func(c *call) any { return "Wildcard" })
	expectBody(t, send(app, http.MethodGet, "/wildcard/okayu"), "Wildcard")
}

func TestUnknownRouteIs404(t *testing.T) {
	app := newMux("").Get("/", func(c *call) any { return "Hi" })
	rec := send(app, http.MethodGet, "/wildcard/okayu")
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestParseQueryString(t *testing.T) {
	app := newMux("").Get("/", func(c *call) any { return c.Query.Get("id") })
	expectBody(t, send(app, http.MethodGet, "/?id=123"), "123")
}

func TestParseMultipleQueryStrings(t *testing.T) {
	app := newMux("").Get("/", func(c *call) any {
		return c.Query.Get("last") + " " + c.Query.Get("first")
	})
	expectBody(t, send(app, http.MethodGet, "/?first=Fubuki&last=Shirakami"), "Shirakami Fubuki")
}

func TestParseQueryStringWithSpace(t *testing.T) {
	app := newMux("").Get("/", func(c *call) any { return c.Query.Get("id") })
	expectBody(t, send(app, http.MethodGet, "/?id=test+123%2B"), "test 123+")
}

func TestParseHeaders(t *testing.T) {
	app := newMux("").Post("/", func(c *call) any {
		return c.Request.Header.Get("x-powered-by")
	})
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("x-powered-by", "Elysia")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	expectBody(t, rec, "Elysia")
}

func TestDecodeURI(t *testing.T) {
	app := newMux("").Get("/", func(c *call) any {
		return c.Query.Get("name") + "|" + c.Query.Get("c")
	})
	expectBody(t, send(app, http.MethodGet, "/?name=a%20b&c=d%20e"), "a b|d e")
}

func TestHandleGroup(t *testing.T) {
	app := newMux("").Group("/gamer", func(g *mux) {
		g.Get("/korone", func(c *call) any { return "Yubi Yubi!" })
	})
	expectBody(t, send(app, http.MethodGet, "/gamer/korone"), "Yubi Yubi!")
}

func TestHandlePlugin(t *testing.T) {
	plugin := newMux("")
	plugin.Get("/korone", func(c *call) any { return "Yubi Yubi!" })
	app := newMux("").Use(plugin)
	expectBody(t, send(app, http.MethodGet, "/korone"), "Yubi Yubi!")
}

func TestHandleRouteStartingWithSameLetter(t *testing.T) {
	app := newMux("").
		Get("/aa", func(c *call) any { return "route 1" }).
		Get("/ab", func(c *call) any { return "route 2" })
	expectBody(t, send(app, http.MethodGet, "/ab"), "route 2")
}

func TestHandleWildcard(t *testing.T) {
	app := newMux("").Get("/*", func(c *call) any { return "Hi" })
	expectBody(t, send(app, http.MethodGet, "/"), "Hi")
	expectBody(t, send(app, http.MethodGet, "/anything/should/match"), "Hi")
}

func TestHandleWildcardOnMultipleMethods(t *testing.T) {
	app := newMux("").
		Get("/part", func(c *call) any { return "Part" }).
		Route(http.MethodOptions, "*", func(c *call) any { return "Hi" })

	expectBody(t, send(app, http.MethodGet, "/part"), "Part")
	expectBody(t, send(app, http.MethodOptions, "/part"), "Hi")
}

func TestHandleAllMethod(t *testing.T) {
	app := newMux("").All("/", func(c *call) any { return "Hi" })
	expectBody(t, send(app, http.MethodGet, "/"), "Hi")
	expectBody(t, send(app, http.MethodPost, "/"), "Hi")
}

func TestServerSpanHasPositiveDuration(t *testing.T) {
	previousProvider := otel.GetTracerProvider()
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	otel.SetTracerProvider(provider)
	t.Cleanup(func() {
		otel.SetTracerProvider(previousProvider)
		_ = provider.Shutdown(context.Background())
	})

	app := newMux("").Get("/healthz", func(c *call) any {
		return "ok"
	})
	_ = send(app, http.MethodGet, "/healthz")

	spans := exporter.GetSpans()
	if len(spans) != 2 {
		t.Fatalf("exported spans = %d, want 2", len(spans))
	}
	byName := make(map[string]tracetest.SpanStub, len(spans))
	for _, span := range spans {
		byName[span.Name] = span
		if duration := span.EndTime.Sub(span.StartTime); duration < time.Microsecond {
			t.Errorf("%s duration = %s, want at least %s", span.Name, duration, time.Microsecond)
		}
	}
	serverSpan, ok := byName["GET /healthz"]
	if !ok {
		t.Fatal("HTTP server span was not exported")
	}
	handlerSpan, ok := byName["http.route.handler"]
	if !ok {
		t.Fatal("route handler span was not exported")
	}
	if got, want := handlerSpan.Parent.SpanID(), serverSpan.SpanContext.SpanID(); got != want {
		t.Errorf("route handler parent span ID = %s, want HTTP server span ID %s", got, want)
	}
}
