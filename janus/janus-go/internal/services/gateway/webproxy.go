package gateway

import (
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
)

// webProxy sends page and static-file requests to the web frontend, and passes
// every other request to the gateway's own routes. Only GET and HEAD are
// forwarded: writes stay with the gateway, which owns the JSON API and the
// sign-in endpoints. The gateway's API, its auth paths and its health check are
// never forwarded, so the web container cannot answer for them.
func webProxy(upstream string, gateway http.Handler) (http.Handler, error) {
	target, err := url.Parse(upstream)
	if err != nil {
		return nil, err
	}
	proxy := &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			r.SetURL(target)
			// Keep the public host, so the web container sees the address the
			// visitor used.
			r.Out.Host = r.In.Host
			r.SetXForwarded()
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, _ error) {
			http.Error(w, "the web frontend is unavailable", http.StatusBadGateway)
		},
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if forwardedToWeb(r) {
			proxy.ServeHTTP(w, r)
			return
		}
		gateway.ServeHTTP(w, r)
	}), nil
}

func forwardedToWeb(r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	path := r.URL.Path
	return path != "/healthz" && !strings.HasPrefix(path, "/api/") && !strings.HasPrefix(path, "/auth/")
}
