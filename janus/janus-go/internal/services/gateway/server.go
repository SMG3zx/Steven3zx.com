package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"janus/internal/contract"
	contractaudit "janus/internal/contract/audit"
	contractid "janus/internal/contract/identity"
	"janus/internal/domain/authn"
	"janus/internal/domain/authz"
	"net/http"
	"net/http/httptest"
	"net/mail"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/sessions"
	"github.com/markbates/goth"
	"github.com/markbates/goth/gothic"
	"github.com/markbates/goth/providers/google"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"janus/internal/platform/tracing"
)

// Config is everything the adapter needs from the outside: the use cases and
// modules its handlers call, and its settings. The adapter reads no
// environment variables.
type Config struct {
	// Addr is the listen address; "127.0.0.1:0" picks a free port.
	Addr           string
	Authentication *authn.Authentication
	Authorization  *authz.Policy
	Audit          auditRecorder
	Identity       identityService
	Catalog        catalogService
	Builds         buildsService
	// Fleet is the fleet service: Agents, their Deployments and their Usage. It is
	// nil when the control plane is not on the tailnet; Agent and Deployment
	// routes then answer 503.
	Fleet          fleetService
	Runs           runsService
	JWTExpiry      time.Duration
	CookieSecure   bool
	GoogleEnabled  bool
	GoogleClientID string
	GoogleSecret   string
	GoogleCallback string
	JWTSecret      string
	GitHubEnabled  bool
	GitHubClientID string
	GitHubSecret   string
	GitHubCallback string
	// RecordRequest, when set, is told the method, status and duration of each
	// request the listener serves.
	RecordRequest func(ctx context.Context, method string, status int, duration time.Duration)
	// WebUpstream is the address of the web frontend's container. When it is set,
	// page and static-file requests are forwarded there (see webProxy).
	WebUpstream string
}

// Server is the HTTP driving adapter: it turns requests into use-case calls
// and results into responses, and owns the HTTP listener.
type Server struct {
	dependencies Config
	mux          *mux
	handler      http.Handler
	listener     *listenState
}

var gothSetup sync.Once

// New builds the server and its routes. Nothing listens until Start.
func New(config Config) *Server {
	s := &Server{dependencies: config, listener: &listenState{}}
	s.mux = s.routes()
	s.handler = s.mux
	if config.WebUpstream != "" {
		handler, err := webProxy(config.WebUpstream, s.mux)
		if err != nil {
			panic("gateway: WebUpstream is not a URL: " + err.Error())
		}
		s.handler = handler
	}
	return s
}

// Handler returns the routed application without the listener's edge
// middleware, for tests that drive it with httptest.
func (s *Server) Handler() http.Handler { return s.handler }

func (s *Server) routes() *mux {
	_, span := tracing.StartSpan(context.Background(), "routes.build", trace.WithSpanKind(trace.SpanKindInternal))
	defer span.End()
	app := newMux("")
	setupGoogle(s.dependencies)
	app.Get("/healthz", func(c *call) any {
		s.log(c.Request.Context(), "info", "http.health.check", map[string]string{"status": "ok"})
		return map[string]string{"status": "ok"}
	})
	s.registerAuthenticationRoutes(app)
	span.SetAttributes(attribute.Int("routes.count", 24), attribute.Bool("auth.google.enabled", s.dependencies.GoogleEnabled), attribute.Bool("auth.github.enabled", s.dependencies.GitHubEnabled))
	return app
}

func (a *Server) registerAuthenticationRoutes(app *mux) {
	app.Post("/auth/register", a.register)
	app.Post("/auth/login", a.login)
	app.Post("/auth/logout", a.logout)
	app.Get("/api/me", a.me)
	app.Get("/auth/google", a.googleStart)
	app.Get("/auth/google/callback", a.googleCallback)
	app.Get("/auth/github", a.githubStart)
	app.Get("/auth/github/callback", a.githubCallback)
	app.Get("/auth/github/connect", a.githubConnect)
	app.Post("/auth/github/disconnect", a.githubDisconnect)
	a.registerProjectRoutes(app)
	a.registerAgentRoutes(app)
	a.registerLifecycleRoutes(app)
}

type credentials struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (a *Server) register(c *call) any {
	ctx, span := tracing.StartSpan(c.Request.Context(), "auth.local.register", trace.WithSpanKind(trace.SpanKindInternal))
	defer span.End()
	var input credentials
	if err := decodeJSON(c.Request, &input); err != nil || !validEmail(input.Email) || len(input.Password) < 12 || len(input.Password) > 1024 {
		a.log(ctx, "warn", "auth.register.rejected", nil)
		return jsonResponse(http.StatusBadRequest, map[string]string{"error": "provide a valid email and a password of at least 12 bytes"})
	}
	allowed, err := a.allowed(ctx, "", "account.register", nil)
	if err != nil {
		return internalError()
	}
	if !allowed {
		return jsonResponse(http.StatusForbidden, map[string]string{"error": "registration is disabled"})
	}
	encoded, err := a.dependencies.Authentication.HashPassword(ctx, input.Password)
	if err != nil {
		return internalError()
	}
	userID := newUserID()
	err = a.dependencies.Identity.CreateLocalUser(ctx, contractid.CreateLocalUser{ID: userID, Email: strings.TrimSpace(input.Email), PasswordHash: encoded})
	if err != nil {
		if errors.Is(err, contract.ErrConflict) {
			return jsonResponse(http.StatusConflict, map[string]string{"error": "account already exists"})
		}
		return internalError()
	}
	a.log(ctx, "info", "auth.register.succeeded", map[string]string{"user_id": userID, "provider": "local"})
	return a.issueCookie(ctx, userID, http.StatusCreated, map[string]string{"id": userID, "email": strings.TrimSpace(input.Email)})
}

func (a *Server) login(c *call) any {
	ctx, span := tracing.StartSpan(c.Request.Context(), "auth.local.login", trace.WithSpanKind(trace.SpanKindInternal))
	defer span.End()
	var input credentials
	if err := decodeJSON(c.Request, &input); err != nil || !validEmail(input.Email) || len(input.Password) == 0 || len(input.Password) > 1024 {
		return jsonResponse(http.StatusBadRequest, map[string]string{"error": "provide an email and password"})
	}
	allowed, err := a.allowed(ctx, "", "account.login", nil)
	if err != nil {
		return internalError()
	}
	if !allowed {
		return jsonResponse(http.StatusForbidden, map[string]string{"error": "login is disabled"})
	}
	user, err := a.dependencies.Identity.FindLocalUser(ctx, input.Email)
	if err != nil {
		return internalError()
	}
	if !user.Found {
		a.log(ctx, "warn", "auth.login.denied", map[string]string{"provider": "local", "reason": "invalid_credentials"})
		return jsonResponse(http.StatusUnauthorized, map[string]string{"error": "invalid email or password"})
	}
	valid, err := a.dependencies.Authentication.VerifyPassword(ctx, input.Password, user.PasswordHash)
	if err != nil {
		return internalError()
	}
	if !valid {
		a.log(ctx, "warn", "auth.login.denied", map[string]string{"provider": "local", "reason": "invalid_credentials"})
		return jsonResponse(http.StatusUnauthorized, map[string]string{"error": "invalid email or password"})
	}
	a.log(ctx, "info", "auth.login.succeeded", map[string]string{"user_id": user.ID, "provider": "local"})
	return a.issueCookie(ctx, user.ID, http.StatusOK, map[string]string{"id": user.ID, "email": user.Email})
}

func (a *Server) logout(c *call) any {
	ctx, span := tracing.StartSpan(c.Request.Context(), "auth.logout", trace.WithSpanKind(trace.SpanKindInternal))
	defer span.End()
	if cookie, err := c.Request.Cookie(sessionCookieName); err == nil && cookie.Value != "" {
		if subject, valid := a.dependencies.Authentication.ValidateToken(ctx, cookie.Value); valid {
			a.log(ctx, "info", "auth.logout", map[string]string{"user_id": subject})
		}
	}
	return reply{StatusCode: http.StatusSeeOther, Header: http.Header{
		"Location":   {"/"},
		"Set-Cookie": {a.cookie("", -1).String()},
	}}
}

func (a *Server) me(c *call) any {
	ctx, span := tracing.StartSpan(c.Request.Context(), "auth.profile.read", trace.WithSpanKind(trace.SpanKindInternal))
	defer span.End()
	subject, ok := a.subject(ctx, c.Request)
	if !ok {
		return jsonResponse(http.StatusUnauthorized, map[string]string{"error": "authentication required"})
	}
	user, err := a.dependencies.Identity.FindUser(ctx, subject)
	if err != nil {
		return internalError()
	}
	if !user.Found {
		return jsonResponse(http.StatusUnauthorized, map[string]string{"error": "account no longer exists"})
	}
	allowed, err := a.allowed(ctx, subject, "profile.read", map[string]any{"subject": user.ID})
	if err != nil {
		return internalError()
	}
	if !allowed {
		return jsonResponse(http.StatusForbidden, map[string]string{"error": "forbidden"})
	}
	a.log(ctx, "info", "authz.profile.allowed", map[string]string{"user_id": user.ID})
	return map[string]string{"id": user.ID, "email": user.Email, "display_name": user.DisplayName}
}

func (a *Server) googleStart(c *call) any {
	if !a.dependencies.GoogleEnabled {
		return jsonResponse(http.StatusServiceUnavailable, map[string]string{"error": "Google sign-in is not configured"})
	}
	ctx, span := tracing.StartSpan(c.Request.Context(), "auth.google.begin", trace.WithSpanKind(trace.SpanKindInternal))
	defer span.End()
	allowed, err := a.allowed(ctx, "", "account.google.login", nil)
	if err != nil {
		return internalError()
	}
	if !allowed {
		return jsonResponse(http.StatusForbidden, map[string]string{"error": "Google sign-in is disabled"})
	}
	request := withGoogleProvider(c.Request)
	writer := httptest.NewRecorder()
	gothic.BeginAuthHandler(writer, request)
	if writer.Code >= 400 {
		a.log(ctx, "warn", "auth.google.begin.failed", map[string]string{"status": fmt.Sprint(writer.Code)})
		return jsonResponse(http.StatusBadGateway, map[string]string{"error": "Google sign-in could not be started"})
	}
	span.SetAttributes(attribute.String("auth.provider", "google"))
	return reply{StatusCode: http.StatusTemporaryRedirect, Header: http.Header{"Location": {writer.Header().Get("Location")}, "Set-Cookie": writer.Header().Values("Set-Cookie")}}
}

func (a *Server) googleCallback(c *call) any {
	if !a.dependencies.GoogleEnabled {
		return jsonResponse(http.StatusServiceUnavailable, map[string]string{"error": "Google sign-in is not configured"})
	}
	ctx, span := tracing.StartSpan(c.Request.Context(), "auth.google.callback", trace.WithSpanKind(trace.SpanKindInternal))
	defer span.End()
	allowed, err := a.allowed(ctx, "", "account.google.login", nil)
	if err != nil {
		return internalError()
	}
	if !allowed {
		return jsonResponse(http.StatusForbidden, map[string]string{"error": "Google sign-in is disabled"})
	}
	writer := httptest.NewRecorder()
	user, err := gothic.CompleteUserAuth(writer, withGoogleProvider(c.Request))
	if err != nil || user.UserID == "" {
		a.log(ctx, "warn", "auth.google.callback.denied", map[string]string{"reason": "provider_authentication_failed"})
		return jsonResponse(http.StatusUnauthorized, map[string]string{"error": "Google sign-in failed"})
	}
	identity, err := a.dependencies.Identity.ResolveGoogleIdentity(ctx, contractid.ResolveIdentity{Subject: user.UserID, Email: user.Email, DisplayName: user.Name})
	if err != nil {
		return internalError()
	}
	issued := a.issueCookie(ctx, identity.ID, http.StatusTemporaryRedirect, nil)
	redirect, ok := issued.(reply)
	if !ok {
		return internalError()
	}
	redirect.Header.Set("Location", "/")
	for _, cookie := range writer.Header().Values("Set-Cookie") {
		redirect.Header.Add("Set-Cookie", cookie)
	}
	a.log(ctx, "info", "auth.login.succeeded", map[string]string{"user_id": identity.ID, "provider": "google"})
	return redirect
}

const sessionCookieName = "janus_session"

func (a *Server) subject(ctx context.Context, request *http.Request) (string, bool) {
	cookie, err := request.Cookie(sessionCookieName)
	if err != nil || cookie.Value == "" {
		return "", false
	}
	subject, valid := a.dependencies.Authentication.ValidateToken(ctx, cookie.Value)
	if !valid {
		return "", false
	}
	// Sessions are stateless tokens, so an Account that was deleted is rejected here.
	found, err := a.dependencies.Identity.FindUser(ctx, subject)
	return subject, err == nil && found.Found
}

func (a *Server) issueCookie(ctx context.Context, subject string, status int, body any) any {
	issued, err := a.dependencies.Authentication.IssueToken(ctx, subject)
	if err != nil {
		return internalError()
	}
	maxAge := int(time.Until(time.Unix(issued.ExpiresAt, 0)).Seconds())
	cookie := a.cookie(issued.Value, maxAge)
	return reply{StatusCode: status, Header: http.Header{"Set-Cookie": {cookie.String()}}, Body: body}
}

func (a *Server) cookie(value string, maxAge int) *http.Cookie {
	return &http.Cookie{Name: sessionCookieName, Value: value, Path: "/", HttpOnly: true, Secure: a.dependencies.CookieSecure, SameSite: http.SameSiteLaxMode, MaxAge: maxAge}
}

func (a *Server) allowed(ctx context.Context, subject, action string, resource map[string]any) (bool, error) {
	return a.dependencies.Authorization.Allowed(ctx, subject, action, resource)
}

func (a *Server) log(ctx context.Context, level, name string, fields map[string]string) {
	a.dependencies.Audit.Record(ctx, contractaudit.Event{Level: level, Name: name, Fields: fields})
}

func decodeJSON(request *http.Request, output any) error {
	decoder := json.NewDecoder(io.LimitReader(request.Body, (1<<20)+1))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(output); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("request contains multiple JSON values")
		}
		return err
	}
	return nil
}

func validEmail(value string) bool {
	if len(value) > 254 {
		return false
	}
	address, err := mail.ParseAddress(strings.TrimSpace(value))
	return err == nil && strings.EqualFold(address.Address, strings.TrimSpace(value))
}

func jsonResponse(status int, body any) reply {
	return reply{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}}, Body: body}
}

func internalError() reply {
	return jsonResponse(http.StatusInternalServerError, map[string]string{"error": "internal server error"})
}

func newUserID() string {
	return uuid.NewString()
}

func withGoogleProvider(request *http.Request) *http.Request {
	query := make(url.Values)
	for key, values := range request.URL.Query() {
		query[key] = append([]string(nil), values...)
	}
	query.Set("provider", "google")
	clonedURL := *request.URL
	clonedURL.RawQuery = query.Encode()
	cloned := request.Clone(request.Context())
	cloned.URL = &clonedURL
	return cloned
}

func setupGoogle(dependencies Config) {
	if !dependencies.GoogleEnabled {
		return
	}
	gothSetup.Do(func() {
		goth.UseProviders(google.New(dependencies.GoogleClientID, dependencies.GoogleSecret, dependencies.GoogleCallback, "email", "profile"))
		gothic.SetState = func(*http.Request) string { return uuid.NewString() }
		store := sessions.NewCookieStore([]byte(dependencies.JWTSecret))
		store.Options = &sessions.Options{Path: "/", MaxAge: 10 * 60, HttpOnly: true, Secure: dependencies.CookieSecure, SameSite: http.SameSiteLaxMode}
		gothic.Store = store
	})
}
