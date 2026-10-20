package routes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
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
	goakt "github.com/tochemey/goakt/v4/actor"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	actortrace "janus/actors/telemetry"
	"janus/internal/app"
	"janus/web"
)

type Actor struct{ dependencies Dependencies }

var _ goakt.Actor = (*Actor)(nil)

var gothSetup sync.Once

func New(dependencies Dependencies) *Actor { return &Actor{dependencies: dependencies} }

func (*Actor) PreStart(*goakt.Context) error { return nil }

func (a *Actor) Receive(ctx *goakt.ReceiveContext) {
	if _, ok := ctx.Message().(*Build); !ok {
		ctx.Unhandled()
		return
	}
	_, span := actortrace.StartSpan(ctx.Context(), "routes.build", trace.WithSpanKind(trace.SpanKindInternal))
	defer span.End()
	app := web.New("")
	setupGoogle(a.dependencies)
	app.Get("/", a.home)
	app.Get("/healthz", func(c *web.Context) any {
		a.log(c.Request.Context(), "info", "http.health.check", map[string]string{"status": "ok"})
		return map[string]string{"status": "ok"}
	})
	a.registerAuthenticationRoutes(app)
	span.SetAttributes(attribute.Int("routes.count", 24), attribute.Bool("auth.google.enabled", a.dependencies.GoogleEnabled), attribute.Bool("auth.github.enabled", a.dependencies.GitHubEnabled))
	ctx.Response(&Built{App: app})
}

type homePageData struct {
	GoogleEnabled bool
	GitHubEnabled bool
	Identity      string
	AccountExists bool
}

var homeTemplate = template.Must(template.New("home").Parse(`<!doctype html>
<html lang="en">
<head>
	<meta charset="utf-8">
	<meta name="viewport" content="width=device-width, initial-scale=1">
	<title>Janus</title>
	<style>
		:root { color-scheme: dark; font-family: system-ui, sans-serif; background: #101827; color: #e6edf7; }
		body { min-height: 100vh; margin: 0; display: grid; place-items: center; }
		main { width: min(24rem, calc(100% - 3rem)); padding: 2rem; border: 1px solid #34445e; border-radius: 1rem; background: #182438; }
		h1 { margin-top: 0; }
		.google-button, button { display: block; width: 100%; box-sizing: border-box; padding: .85rem 1rem; border: 0; border-radius: .5rem; background: #fff; color: #202124; text-align: center; text-decoration: none; font: inherit; font-weight: 650; cursor: pointer; }
		.google-button:hover { background: #e9eef6; }
		button:hover { background: #e9eef6; }
		.status { padding: .85rem 1rem; border: 1px solid #2c8068; border-radius: .5rem; background: #163a35; }
		.note { color: #aab8cc; font-size: .9rem; }
	</style>
</head>
<body>
	<main>
		<h1>Welcome to Janus</h1>
		{{if .Identity}}
		<p class="status" role="status">Signed in as <strong>{{.Identity}}</strong></p>
		<p><a href="/projects">Open your projects</a></p>
		<form method="post" action="/auth/logout"><button type="submit">Sign out</button></form>
		{{else}}
		<p>Sign in to continue.</p>
		{{if .GoogleEnabled}}<a class="google-button" href="/auth/google">Continue with Google</a>{{end}}
		{{if .GitHubEnabled}}<a class="google-button" href="/auth/github">Continue with GitHub</a>{{end}}
		{{if .AccountExists}}<p class="status" role="status">That GitHub email belongs to an existing Janus account. Sign in to that account, then connect GitHub from Projects.</p>{{end}}
		<p class="note">Google sign-in is available when configured by the administrator.</p>
		{{end}}
	</main>
</body>

</html>`))

func homePage(googleEnabled bool, identity string) web.Response {
	return homePageWithProviders(googleEnabled, false, identity, false)
}

func homePageWithProviders(googleEnabled, githubEnabled bool, identity string, accountExists bool) web.Response {
	var body strings.Builder
	if err := homeTemplate.Execute(&body, homePageData{GoogleEnabled: googleEnabled, GitHubEnabled: githubEnabled, Identity: identity, AccountExists: accountExists}); err != nil {
		return internalError()
	}
	return web.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"text/html; charset=utf-8"}},
		Body:       body.String(),
	}
}

func (a *Actor) home(c *web.Context) any {
	profileResponse := a.me(c)
	if profile, ok := profileResponse.(map[string]string); ok {
		identity := strings.TrimSpace(profile["display_name"])
		if identity == "" {
			identity = profile["email"]
		}
		return homePageWithProviders(a.dependencies.GoogleEnabled, a.dependencies.GitHubEnabled, identity, false)
	}
	if response, ok := profileResponse.(web.Response); ok && response.StatusCode != http.StatusUnauthorized {
		return response
	}
	return homePageWithProviders(a.dependencies.GoogleEnabled, a.dependencies.GitHubEnabled, "", c.Query.Get("github") == "account_exists")
}

func (a *Actor) registerAuthenticationRoutes(app *web.App) {
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
	a.registerLifecycleRoutes(app)
}

type credentials struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (a *Actor) register(c *web.Context) any {
	ctx, span := actortrace.StartSpan(c.Request.Context(), "auth.local.register", trace.WithSpanKind(trace.SpanKindInternal))
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
	err = a.dependencies.Store.CreateLocalUser(ctx, app.CreateLocalUser{ID: userID, Email: strings.TrimSpace(input.Email), PasswordHash: encoded})
	if err != nil {
		if isConflict(err) {
			return jsonResponse(http.StatusConflict, map[string]string{"error": "account already exists"})
		}
		return internalError()
	}
	a.log(ctx, "info", "auth.register.succeeded", map[string]string{"user_id": userID, "provider": "local"})
	return a.issueCookie(ctx, userID, http.StatusCreated, map[string]string{"id": userID, "email": strings.TrimSpace(input.Email)})
}

func (a *Actor) login(c *web.Context) any {
	ctx, span := actortrace.StartSpan(c.Request.Context(), "auth.local.login", trace.WithSpanKind(trace.SpanKindInternal))
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
	user, err := a.dependencies.Store.FindLocalUser(ctx, input.Email)
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

func (a *Actor) logout(c *web.Context) any {
	ctx, span := actortrace.StartSpan(c.Request.Context(), "auth.logout", trace.WithSpanKind(trace.SpanKindInternal))
	defer span.End()
	if cookie, err := c.Request.Cookie(sessionCookieName); err == nil && cookie.Value != "" {
		if subject, valid := a.dependencies.Authentication.ValidateToken(ctx, cookie.Value); valid {
			a.log(ctx, "info", "auth.logout", map[string]string{"user_id": subject})
		}
	}
	return web.Response{StatusCode: http.StatusSeeOther, Header: http.Header{
		"Location":   {"/"},
		"Set-Cookie": {a.cookie("", -1).String()},
	}}
}

func (a *Actor) me(c *web.Context) any {
	ctx, span := actortrace.StartSpan(c.Request.Context(), "auth.profile.read", trace.WithSpanKind(trace.SpanKindInternal))
	defer span.End()
	subject, ok := a.subject(ctx, c.Request)
	if !ok {
		return jsonResponse(http.StatusUnauthorized, map[string]string{"error": "authentication required"})
	}
	user, err := a.dependencies.Store.FindUser(ctx, subject)
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

func (a *Actor) googleStart(c *web.Context) any {
	if !a.dependencies.GoogleEnabled {
		return jsonResponse(http.StatusServiceUnavailable, map[string]string{"error": "Google sign-in is not configured"})
	}
	ctx, span := actortrace.StartSpan(c.Request.Context(), "auth.google.begin", trace.WithSpanKind(trace.SpanKindInternal))
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
	return web.Response{StatusCode: http.StatusTemporaryRedirect, Header: http.Header{"Location": {writer.Header().Get("Location")}, "Set-Cookie": writer.Header().Values("Set-Cookie")}}
}

func (a *Actor) googleCallback(c *web.Context) any {
	if !a.dependencies.GoogleEnabled {
		return jsonResponse(http.StatusServiceUnavailable, map[string]string{"error": "Google sign-in is not configured"})
	}
	ctx, span := actortrace.StartSpan(c.Request.Context(), "auth.google.callback", trace.WithSpanKind(trace.SpanKindInternal))
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
	identity, err := a.dependencies.Store.ResolveGoogleIdentity(ctx, app.ResolveGoogleIdentity{Subject: user.UserID, Email: user.Email, DisplayName: user.Name})
	if err != nil {
		return internalError()
	}
	issued := a.issueCookie(ctx, identity.ID, http.StatusTemporaryRedirect, nil)
	redirect, ok := issued.(web.Response)
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

func (a *Actor) subject(ctx context.Context, request *http.Request) (string, bool) {
	cookie, err := request.Cookie(sessionCookieName)
	if err != nil || cookie.Value == "" {
		return "", false
	}
	subject, valid := a.dependencies.Authentication.ValidateToken(ctx, cookie.Value)
	if !valid {
		return "", false
	}
	// Sessions are stateless tokens, so an Account that was deleted is rejected here.
	found, err := a.dependencies.Store.FindUser(ctx, subject)
	return subject, err == nil && found.Found
}

func (a *Actor) issueCookie(ctx context.Context, subject string, status int, body any) any {
	issued, err := a.dependencies.Authentication.IssueToken(ctx, subject)
	if err != nil {
		return internalError()
	}
	maxAge := int(time.Until(time.Unix(issued.ExpiresAt, 0)).Seconds())
	cookie := a.cookie(issued.Value, maxAge)
	return web.Response{StatusCode: status, Header: http.Header{"Set-Cookie": {cookie.String()}}, Body: body}
}

func (a *Actor) cookie(value string, maxAge int) *http.Cookie {
	return &http.Cookie{Name: sessionCookieName, Value: value, Path: "/", HttpOnly: true, Secure: a.dependencies.CookieSecure, SameSite: http.SameSiteLaxMode, MaxAge: maxAge}
}

func (a *Actor) allowed(ctx context.Context, subject, action string, resource map[string]any) (bool, error) {
	return a.dependencies.Authorization.Allowed(ctx, subject, action, resource)
}

func (a *Actor) ask(ctx context.Context, pid *goakt.PID, message any) (any, error) {
	requestCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return goakt.Ask(requestCtx, pid, message, 5*time.Second)
}

func (a *Actor) log(ctx context.Context, level, name string, fields map[string]string) {
	a.dependencies.Audit.Record(ctx, app.AuditEvent{Level: level, Name: name, Fields: fields})
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

func jsonResponse(status int, body any) web.Response {
	return web.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}}, Body: body}
}

func internalError() web.Response {
	return jsonResponse(http.StatusInternalServerError, map[string]string{"error": "internal server error"})
}

func isConflict(err error) bool {
	return err != nil && (strings.Contains(strings.ToLower(err.Error()), "unique constraint") || strings.Contains(strings.ToLower(err.Error()), "constraint failed"))
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

func setupGoogle(dependencies Dependencies) {
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

func (*Actor) PostStop(*goakt.Context) error { return nil }
