package routes

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/sessions"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/oauth2"

	"janus/actors/persistence"
	actortrace "janus/actors/telemetry"
	"janus/web"
)

const githubOAuthCookie = "janus_github_oauth"

type githubProfile struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Login string `json:"login"`
}
type githubEmail struct {
	Email    string `json:"email"`
	Primary  bool   `json:"primary"`
	Verified bool   `json:"verified"`
}

func (a *Actor) githubOAuthConfig(scopes ...string) *oauth2.Config {
	return &oauth2.Config{ClientID: a.dependencies.GitHubClientID, ClientSecret: a.dependencies.GitHubSecret, RedirectURL: a.dependencies.GitHubCallback, Scopes: scopes, Endpoint: oauth2.Endpoint{AuthURL: "https://github.com/login/oauth/authorize", TokenURL: "https://github.com/login/oauth/access_token"}}
}

func (a *Actor) githubStart(c *web.Context) any { return a.beginGitHubOAuth(c, "login", "") }

func (a *Actor) githubConnect(c *web.Context) any {
	ctx := c.Request.Context()
	owner, ok := a.subject(ctx, c.Request)
	if !ok {
		return jsonResponse(http.StatusUnauthorized, map[string]string{"error": "authentication required"})
	}
	allowed, err := a.allowed(ctx, owner, "account.github.connect", map[string]any{"owner_id": owner})
	if err != nil {
		return internalError()
	}
	if !allowed {
		return jsonResponse(http.StatusForbidden, map[string]string{"error": "GitHub connection is disabled"})
	}
	return a.beginGitHubOAuth(c, "connect", owner)
}

func (a *Actor) beginGitHubOAuth(c *web.Context, flow, owner string) any {
	ctx, span := actortrace.StartSpan(c.Request.Context(), "auth.github.begin", trace.WithSpanKind(trace.SpanKindInternal))
	defer span.End()
	span.SetAttributes(
		attribute.String("auth.provider", "github"),
		attribute.String("auth.flow", flow),
		attribute.String("http.route", "/auth/github"),
	)
	if !a.dependencies.GitHubEnabled {
		span.SetAttributes(attribute.String("auth.github.failure_stage", "configuration"))
		return jsonResponse(http.StatusServiceUnavailable, map[string]string{"error": "GitHub OAuth is not configured"})
	}
	if flow == "login" {
		_, authSpan := actortrace.StartSpan(ctx, "auth.github.authorize", trace.WithSpanKind(trace.SpanKindInternal))
		allowed, err := a.allowed(ctx, "", "account.github.login", nil)
		if err != nil {
			actortrace.RecordError(authSpan, err)
			a.logGitHubOAuthFailure(ctx, "authorization", err)
			span.SetAttributes(attribute.String("auth.github.failure_stage", "authorization"))
		}
		authSpan.End()
		if err != nil {
			return internalError()
		}
		if !allowed {
			return jsonResponse(http.StatusForbidden, map[string]string{"error": "GitHub sign-in is disabled"})
		}
	}
	state := uuid.NewString()
	store := a.githubOAuthStore()
	session, err := store.Get(c.Request, githubOAuthCookie)
	if err != nil {
		actortrace.RecordError(span, err)
		a.logGitHubOAuthFailure(ctx, "session_read", err)
		span.SetAttributes(attribute.String("auth.github.failure_stage", "session_read"))
		return internalError()
	}
	session.Values["state"] = state
	session.Values["flow"] = flow
	session.Values["owner"] = owner
	writer := httptest.NewRecorder()
	if err = session.Save(c.Request, writer); err != nil {
		actortrace.RecordError(span, err)
		a.logGitHubOAuthFailure(ctx, "session_write", err)
		span.SetAttributes(attribute.String("auth.github.failure_stage", "session_write"))
		return internalError()
	}
	scopes := []string{"read:user", "user:email"}
	if flow == "connect" {
		scopes = append(scopes, "repo")
	}
	location := a.githubOAuthConfig(scopes...).AuthCodeURL(state, oauth2.SetAuthURLParam("allow_signup", "true"))
	span.SetAttributes(attribute.String("auth.github.result", "redirect"))
	return web.Response{StatusCode: http.StatusTemporaryRedirect, Header: http.Header{"Location": {location}, "Set-Cookie": writer.Header().Values("Set-Cookie")}}
}

// logGitHubOAuthFailure records the failing phase and error type without logging
// OAuth cookies, state, authorization codes, tokens, or provider credentials.
func (a *Actor) logGitHubOAuthFailure(ctx context.Context, stage string, err error) {
	if err == nil {
		return
	}
	a.log(ctx, "error", "auth.github.begin.failed", map[string]string{
		"stage":      stage,
		"error_type": fmt.Sprintf("%T", err),
	})
}

func (a *Actor) githubCallback(c *web.Context) any {
	if !a.dependencies.GitHubEnabled {
		return jsonResponse(http.StatusServiceUnavailable, map[string]string{"error": "GitHub OAuth is not configured"})
	}
	ctx, span := actortrace.StartSpan(c.Request.Context(), "auth.github.callback", trace.WithSpanKind(trace.SpanKindInternal))
	defer span.End()
	span.SetAttributes(attribute.String("auth.provider", "github"))
	store := a.githubOAuthStore()
	session, err := store.Get(c.Request, githubOAuthCookie)
	if err != nil {
		return jsonResponse(http.StatusBadRequest, map[string]string{"error": "GitHub authorization state is invalid; try again"})
	}
	wantState, _ := session.Values["state"].(string)
	gotState := c.Request.URL.Query().Get("state")
	if wantState == "" || gotState == "" || subtle.ConstantTimeCompare([]byte(wantState), []byte(gotState)) != 1 {
		return jsonResponse(http.StatusBadRequest, map[string]string{"error": "GitHub authorization state is invalid; try again"})
	}
	flow, _ := session.Values["flow"].(string)
	owner, _ := session.Values["owner"].(string)
	if providerErr := c.Request.URL.Query().Get("error"); providerErr != "" {
		return web.Response{StatusCode: http.StatusSeeOther, Header: http.Header{"Location": {"/?github=cancelled"}, "Set-Cookie": {a.clearOAuthCookie(c.Request, session).String()}}}
	}
	code := c.Request.URL.Query().Get("code")
	if code == "" || (flow != "login" && flow != "connect") {
		return jsonResponse(http.StatusBadRequest, map[string]string{"error": "GitHub authorization response is incomplete"})
	}
	scopes := []string{"read:user", "user:email"}
	if flow == "connect" {
		scopes = append(scopes, "repo")
	}
	token, err := a.githubOAuthConfig(scopes...).Exchange(ctx, code)
	if err != nil {
		actortrace.RecordError(span, err)
		return jsonResponse(http.StatusBadGateway, map[string]string{"error": "GitHub authorization could not be completed"})
	}
	profile, email, grantedScopes, err := fetchGitHubIdentity(ctx, token.AccessToken)
	if err != nil {
		actortrace.RecordError(span, err)
		return jsonResponse(http.StatusBadGateway, map[string]string{"error": "GitHub profile could not be verified"})
	}
	if flow == "connect" && !hasGitHubScope(token.Extra("scope"), "repo") && !hasGitHubScope(grantedScopes, "repo") {
		return jsonResponse(http.StatusForbidden, map[string]string{"error": "GitHub repository access was not granted"})
	}
	if flow == "connect" {
		current, ok := a.subject(ctx, c.Request)
		if !ok || current != owner {
			return jsonResponse(http.StatusUnauthorized, map[string]string{"error": "sign in to the same Janus account and reconnect GitHub"})
		}
		if connectErr := a.dependencies.Store.ConnectGitHub(ctx, persistence.ConnectGitHub{UserID: owner, Subject: fmt.Sprint(profile.ID), Email: email, DisplayName: githubName(profile), Token: token.AccessToken}); connectErr != nil {
			if persistence.IsReply(connectErr) {
				return jsonResponse(http.StatusConflict, map[string]string{"error": connectErr.Error()})
			}
			return internalError()
		}
		a.log(ctx, "info", "github.connected", map[string]string{"user_id": owner})
		return web.Response{StatusCode: http.StatusSeeOther, Header: http.Header{"Location": {"/projects?github=connected"}, "Set-Cookie": {a.clearOAuthCookie(c.Request, session).String()}}}
	}
	identity, resolveErr := a.dependencies.Store.ResolveGitHubLogin(ctx, persistence.ResolveGitHubLogin{Subject: fmt.Sprint(profile.ID), Email: email, DisplayName: githubName(profile)})
	if resolveErr != nil {
		return internalError()
	}
	if identity.EmailInUse {
		return web.Response{StatusCode: http.StatusSeeOther, Header: http.Header{"Location": {"/?github=account_exists"}, "Set-Cookie": {a.clearOAuthCookie(c.Request, session).String()}}}
	}
	issued := a.issueCookie(ctx, identity.ID, http.StatusSeeOther, nil)
	redirect, ok := issued.(web.Response)
	if !ok {
		return internalError()
	}
	redirect.Header.Set("Location", "/projects")
	redirect.Header.Add("Set-Cookie", a.clearOAuthCookie(c.Request, session).String())
	a.log(ctx, "info", "auth.login.succeeded", map[string]string{"user_id": identity.ID, "provider": "github"})
	return redirect
}

func hasGitHubScope(value any, want string) bool {
	raw, ok := value.(string)
	if !ok {
		return false
	}
	for _, scope := range strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' }) {
		if scope == want {
			return true
		}
	}
	return false
}

func (a *Actor) githubDisconnect(c *web.Context) any {
	ctx := c.Request.Context()
	owner, ok := a.subject(ctx, c.Request)
	if !ok {
		return jsonResponse(http.StatusUnauthorized, map[string]string{"error": "authentication required"})
	}
	allowed, authErr := a.allowed(ctx, owner, "account.github.connect", map[string]any{"owner_id": owner})
	if authErr != nil {
		return internalError()
	}
	if !allowed {
		return jsonResponse(http.StatusForbidden, map[string]string{"error": "forbidden"})
	}
	if err := a.dependencies.GitHub.Disconnect(ctx, owner); err != nil {
		return internalError()
	}
	a.log(ctx, "info", "github.disconnected", map[string]string{"user_id": owner})
	return web.Response{StatusCode: http.StatusSeeOther, Header: http.Header{"Location": {"/projects"}}}
}

func (a *Actor) githubOAuthStore() *sessions.CookieStore {
	store := sessions.NewCookieStore([]byte(a.dependencies.JWTSecret))
	store.Options = &sessions.Options{Path: "/", MaxAge: 600, HttpOnly: true, Secure: a.dependencies.CookieSecure, SameSite: http.SameSiteLaxMode}
	return store
}
func (a *Actor) clearOAuthCookie(req *http.Request, session *sessions.Session) *http.Cookie {
	session.Options = &sessions.Options{Path: "/", MaxAge: -1, HttpOnly: true, Secure: a.dependencies.CookieSecure, SameSite: http.SameSiteLaxMode}
	writer := httptest.NewRecorder()
	_ = session.Save(req, writer)
	cookies := writer.Result().Cookies()
	if len(cookies) > 0 {
		return cookies[0]
	}
	return &http.Cookie{Name: githubOAuthCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: a.dependencies.CookieSecure, SameSite: http.SameSiteLaxMode}
}

func fetchGitHubIdentity(ctx context.Context, accessToken string) (githubProfile, string, string, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	var grantedScopes string
	fetch := func(endpoint string, out any) error {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+accessToken)
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("GitHub returned %d", resp.StatusCode)
		}
		if endpoint == "https://api.github.com/user" {
			grantedScopes = resp.Header.Get("X-OAuth-Scopes")
		}
		return json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(out)
	}
	var profile githubProfile
	if err := fetch("https://api.github.com/user", &profile); err != nil {
		return profile, "", "", err
	}
	if profile.ID == 0 {
		return profile, "", "", errors.New("GitHub user id missing")
	}
	var emails []githubEmail
	if err := fetch("https://api.github.com/user/emails", &emails); err != nil {
		return profile, "", "", err
	}
	for _, email := range emails {
		if email.Primary && email.Verified {
			return profile, strings.TrimSpace(email.Email), grantedScopes, nil
		}
	}
	return profile, "", "", errors.New("no verified primary GitHub email")
}
func githubName(p githubProfile) string {
	if strings.TrimSpace(p.Name) != "" {
		return strings.TrimSpace(p.Name)
	}
	return p.Login
}
