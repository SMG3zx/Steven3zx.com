package gateway

import (
	"context"
	"io"
	"janus/cmd/janus/identityfixture"
	"janus/internal/domain/authn"
	"janus/internal/domain/authz"
	"janus/internal/services/gateway/audittest"
	"janus/internal/services/gateway/projectstest"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// upstreamRecorder stands in for the web container and remembers what reached it.
type upstreamRecorder struct {
	mu       sync.Mutex
	requests []*http.Request
	server   *httptest.Server
}

func newUpstream(t *testing.T) *upstreamRecorder {
	t.Helper()
	recorder := &upstreamRecorder{}
	recorder.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recorder.mu.Lock()
		recorder.requests = append(recorder.requests, r.Clone(r.Context()))
		recorder.mu.Unlock()
		w.Header().Set("X-Upstream", "web")
		w.WriteHeader(http.StatusTeapot)
		_, _ = io.WriteString(w, "rendered by web for "+r.URL.Path)
	}))
	t.Cleanup(recorder.server.Close)
	return recorder
}

func (u *upstreamRecorder) hits() int {
	u.mu.Lock()
	defer u.mu.Unlock()
	return len(u.requests)
}

func (u *upstreamRecorder) last(t *testing.T) *http.Request {
	t.Helper()
	u.mu.Lock()
	defer u.mu.Unlock()
	if len(u.requests) == 0 {
		t.Fatal("the upstream received no request")
	}
	return u.requests[len(u.requests)-1]
}

// serverWithUpstream builds the gateway with every dependency its routes touch,
// so the gateway's own routes answer as they do in production.
func serverWithUpstream(t *testing.T, upstreamURL string) http.Handler {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	authorization, err := authz.New(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return New(Config{
		Audit: audittest.NewRecorder(), Identity: identityfixture.Start(t), Catalog: projectstest.NewMemory(),
		Authentication: authn.New([]byte(strings.Repeat("k", 32)), "janus-test", 30*time.Minute), Authorization: authorization,
		WebUpstream: upstreamURL,
	}).Handler()
}

func TestPageRequestsAreForwardedToTheWebContainer(t *testing.T) {
	upstream := newUpstream(t)
	handler := serverWithUpstream(t, upstream.server.URL)

	request := httptest.NewRequest(http.MethodGet, "/projects/p1?tab=builds", nil)
	request.Host = "janus.steven3zx.com"
	request.Header.Set("Cookie", "janus_session=abc")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusTeapot || recorder.Body.String() != "rendered by web for /projects/p1" {
		t.Fatalf("response = %d %q, want the web container's answer", recorder.Code, recorder.Body.String())
	}
	if recorder.Header().Get("X-Upstream") != "web" {
		t.Fatalf("response did not come from the web container: %v", recorder.Header())
	}
	got := upstream.last(t)
	if got.URL.Path != "/projects/p1" || got.URL.RawQuery != "tab=builds" {
		t.Fatalf("upstream saw %s?%s, want /projects/p1?tab=builds", got.URL.Path, got.URL.RawQuery)
	}
	if got.Header.Get("Cookie") != "janus_session=abc" {
		t.Fatalf("upstream did not receive the session cookie: %q", got.Header.Get("Cookie"))
	}
	if got.Header.Get("X-Forwarded-Host") != "janus.steven3zx.com" {
		t.Fatalf("X-Forwarded-Host = %q, want the public host", got.Header.Get("X-Forwarded-Host"))
	}
}

func TestStaticAssetsAreForwardedToo(t *testing.T) {
	upstream := newUpstream(t)
	handler := serverWithUpstream(t, upstream.server.URL)

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/build/q-abc.js", nil))

	if upstream.hits() != 1 || upstream.last(t).URL.Path != "/build/q-abc.js" {
		t.Fatalf("upstream hits = %d, want the asset forwarded once", upstream.hits())
	}
}

func TestGatewayRoutesNeverReachTheWebContainer(t *testing.T) {
	for _, path := range []string{"/api/me", "/api/projects", "/auth/login", "/auth/github", "/healthz"} {
		t.Run(path, func(t *testing.T) {
			upstream := newUpstream(t)
			handler := serverWithUpstream(t, upstream.server.URL)

			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))

			if upstream.hits() != 0 {
				t.Fatalf("%s reached the web container", path)
			}
			if recorder.Header().Get("X-Upstream") != "" {
				t.Fatalf("%s answered from the web container", path)
			}
		})
	}
}

func TestWriteRequestsStayWithTheGateway(t *testing.T) {
	upstream := newUpstream(t)
	handler := serverWithUpstream(t, upstream.server.URL)

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/projects", strings.NewReader("name=x")))

	if upstream.hits() != 0 {
		t.Fatal("a POST to a page path reached the web container")
	}
}

func TestAnUnreachableWebContainerIsABadGateway(t *testing.T) {
	closed := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	address := closed.URL
	closed.Close()
	handler := serverWithUpstream(t, address)

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/projects", nil))

	if recorder.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502 when the web container is down", recorder.Code)
	}
}

func TestWithoutAnUpstreamTheGatewayServesItsOwnPages(t *testing.T) {
	handler := serverWithUpstream(t, "")

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))

	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "Welcome to Janus") {
		t.Fatalf("status = %d, want the gateway's own homepage", recorder.Code)
	}
}
