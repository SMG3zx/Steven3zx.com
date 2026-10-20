package httpapi

import (
	"context"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	authzactor "janus/actors/authorization"
)

func TestGitHubOAuthUsesSeparateIdentityAndRepositoryScopesAndChecksState(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	authorization, err := authzactor.New(ctx)
	if err != nil {
		t.Fatal(err)
	}
	a := &Server{dependencies: Config{Authorization: authorization, GitHubEnabled: true, GitHubClientID: "client-id", GitHubSecret: "client-secret", GitHubCallback: "https://janus.example/auth/github/callback", JWTSecret: strings.Repeat("k", 32), CookieSecure: true}}
	request := httptest.NewRequest("GET", "https://janus.example/auth/github", nil)
	started, ok := a.beginGitHubOAuth(&call{Request: request}, "login", "").(reply)
	if !ok {
		t.Fatalf("login response %T", a.beginGitHubOAuth(&call{Request: request}, "login", ""))
	}
	loginURL, err := url.Parse(started.Header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	if scope := loginURL.Query().Get("scope"); scope != "read:user user:email" {
		t.Fatalf("GitHub login scopes = %q", scope)
	}
	if started.Header.Get("Set-Cookie") == "" {
		t.Fatal("OAuth state cookie was not issued")
	}
	cookies := httptest.NewRecorder()
	cookies.Header().Set("Set-Cookie", started.Header.Get("Set-Cookie"))
	parsed := cookies.Result().Cookies()
	if len(parsed) == 0 {
		t.Fatal("could not parse OAuth state cookie")
	}

	connect, ok := a.beginGitHubOAuth(&call{Request: request}, "connect", "user-123").(reply)
	if !ok {
		t.Fatalf("connect response %T", connect)
	}
	connectURL, err := url.Parse(connect.Header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	if scope := connectURL.Query().Get("scope"); scope != "read:user user:email repo" {
		t.Fatalf("GitHub connection scopes = %q", scope)
	}

	callback := httptest.NewRequest("GET", "https://janus.example/auth/github/callback?state=attacker&code=ignored", nil)
	callback.AddCookie(parsed[0])
	response := a.githubCallback(&call{Request: callback}).(reply)
	if response.StatusCode != 400 {
		t.Fatalf("invalid OAuth state status = %d", response.StatusCode)
	}
}
