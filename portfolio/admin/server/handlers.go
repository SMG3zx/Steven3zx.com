package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

const (
	cookieName = "admin_session"
	sessionTTL = 12 * time.Hour
)

type server struct {
	store        *store
	ipLimiter    *limiter
	userLimiter  *limiter
	agentURL     string
	agentToken   string
	webDir       string
	cookieSecure bool
	agent        *http.Client
}

func (s *server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /admin/api/login", s.login)
	mux.HandleFunc("POST /admin/api/logout", s.logout)
	mux.HandleFunc("GET /admin/api/me", s.requireUser(s.me))
	mux.HandleFunc("GET /admin/api/snapshot", s.requireUser(s.snapshot))
	mux.HandleFunc("GET /admin/api/", func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusNotFound, "not found")
	})
	mux.HandleFunc("GET /admin/{path...}", s.spa)
	mux.HandleFunc("GET /admin", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/admin/", http.StatusMovedPermanently)
	})
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) })
	return securityHeaders(mux)
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy",
			"default-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Frame-Options", "DENY")
		h.Set("X-Robots-Tag", "noindex, nofollow")
		next.ServeHTTP(w, r)
	})
}

type ctxKey struct{}

func (s *server) requireUser(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(cookieName)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "not signed in")
			return
		}
		u, err := s.store.sessionUser(r.Context(), c.Value)
		if err != nil {
			if !errors.Is(err, errNotFound) {
				log.Printf("session lookup: %v", err)
			}
			writeError(w, http.StatusUnauthorized, "not signed in")
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, u)))
	}
}

// sameOrigin rejects cross-site POSTs. The cookie is SameSite=Strict as well;
// this is the second lock on the door.
func sameOrigin(r *http.Request) bool {
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		return false
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return r.Header.Get("Sec-Fetch-Site") == "" || r.Header.Get("Sec-Fetch-Site") == "same-origin"
	}
	u, err := url.Parse(origin)
	return err == nil && u.Host == r.Host
}

func clientIP(r *http.Request) string {
	if ip := r.Header.Get("CF-Connecting-IP"); ip != "" {
		return ip
	}
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		return strings.TrimSpace(strings.Split(xff, ",")[0])
	}
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	return host
}

func (s *server) login(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !sameOrigin(r) {
		writeError(w, http.StatusForbidden, "cross-site request refused")
		return
	}
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	username := strings.TrimSpace(body.Username)
	ipKey, userKey := clientIP(r), strings.ToLower(username)
	if s.ipLimiter.blocked(ipKey) || s.userLimiter.blocked(userKey) {
		writeError(w, http.StatusTooManyRequests, "too many failed attempts, try again later")
		return
	}

	u, err := s.store.userByName(r.Context(), username)
	hash := u.PasswordHash
	if err != nil {
		if !errors.Is(err, errNotFound) {
			log.Printf("login lookup: %v", err)
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
		hash = dummyHash
	}
	ok, verr := verifyPassword(hash, body.Password)
	if err != nil || verr != nil || !ok {
		s.ipLimiter.fail(ipKey)
		s.userLimiter.fail(userKey)
		log.Printf("failed login for %q from %s", username, clientIP(r))
		writeError(w, http.StatusUnauthorized, "invalid username or password")
		return
	}
	s.userLimiter.reset(userKey)

	token, expires, err := s.store.createSession(r.Context(), u.ID, sessionTTL)
	if err != nil {
		log.Printf("create session: %v", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: cookieName, Value: token, Path: "/admin", Expires: expires,
		HttpOnly: true, Secure: s.cookieSecure, SameSite: http.SameSiteStrictMode,
	})
	writeJSON(w, http.StatusOK, map[string]string{"username": u.Username})
}

func (s *server) logout(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeError(w, http.StatusForbidden, "cross-site request refused")
		return
	}
	if c, err := r.Cookie(cookieName); err == nil {
		if err := s.store.deleteSession(r.Context(), c.Value); err != nil {
			log.Printf("delete session: %v", err)
		}
	}
	http.SetCookie(w, &http.Cookie{
		Name: cookieName, Value: "", Path: "/admin", MaxAge: -1,
		HttpOnly: true, Secure: s.cookieSecure, SameSite: http.SameSiteStrictMode,
	})
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) me(w http.ResponseWriter, r *http.Request) {
	u := r.Context().Value(ctxKey{}).(user)
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]string{"username": u.Username})
}

func (s *server) snapshot(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, s.agentURL+"/snapshot", nil)
	req.Header.Set("Authorization", "Bearer "+s.agentToken)
	resp, err := s.agent.Do(req)
	if err != nil {
		log.Printf("host agent: %v", err)
		writeError(w, http.StatusBadGateway, "host agent unreachable")
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		log.Printf("host agent: %s", resp.Status)
		writeError(w, http.StatusBadGateway, "host agent returned "+resp.Status)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	io.Copy(w, resp.Body)
}

// spa serves the built React app, falling back to index.html for client routes.
func (s *server) spa(w http.ResponseWriter, r *http.Request) {
	rel := path.Clean("/" + r.PathValue("path"))
	file := filepath.Join(s.webDir, filepath.FromSlash(rel))
	if info, err := os.Stat(file); err == nil && !info.IsDir() {
		if strings.HasPrefix(rel, "/assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		http.ServeFile(w, r, file)
		return
	}
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeFile(w, r, filepath.Join(s.webDir, "index.html"))
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
