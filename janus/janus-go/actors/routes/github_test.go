package routes

import (
	"context"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/tochemey/goakt/v4/actor"
	golog "github.com/tochemey/goakt/v4/log"
	authzactor "janus/actors/authorization"
	"janus/web"
)

func TestGitHubOAuthUsesSeparateIdentityAndRepositoryScopesAndChecksState(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	system, err := actor.NewActorSystem("github-oauth-test", actor.WithLogger(golog.DiscardLogger))
	if err != nil {
		t.Fatal(err)
	}
	if err = system.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		stop, c := context.WithTimeout(context.Background(), 3*time.Second)
		defer c()
		_ = system.Stop(stop)
	})
	authorization, err := authzactor.New(ctx)
	if err != nil {
		t.Fatal(err)
	}
	a := &Actor{dependencies: Dependencies{Authorization: authorization, GitHubEnabled: true, GitHubClientID: "client-id", GitHubSecret: "client-secret", GitHubCallback: "https://janus.example/auth/github/callback", JWTSecret: strings.Repeat("k", 32), CookieSecure: true}}
	request := httptest.NewRequest("GET", "https://janus.example/auth/github", nil)
	started, ok := a.beginGitHubOAuth(&web.Context{Request: request}, "login", "").(web.Response)
	if !ok {
		t.Fatalf("login response %T", a.beginGitHubOAuth(&web.Context{Request: request}, "login", ""))
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

	connect, ok := a.beginGitHubOAuth(&web.Context{Request: request}, "connect", "user-123").(web.Response)
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
	response := a.githubCallback(&web.Context{Request: callback}).(web.Response)
	if response.StatusCode != 400 {
		t.Fatalf("invalid OAuth state status = %d", response.StatusCode)
	}
}
