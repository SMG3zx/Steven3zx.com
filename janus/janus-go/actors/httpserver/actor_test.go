package httpserver

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	goakt "github.com/tochemey/goakt/v4/actor"
	"janus/web"
)

func TestHTTPServerActorServesImmutableRouterAndStops(t *testing.T) {
	system, err := goakt.NewActorSystem("httpserver-test")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := system.Start(ctx); err != nil {
		t.Fatalf("start actor system: %v", err)
	}
	t.Cleanup(func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := system.Stop(stopCtx); err != nil {
			t.Errorf("stop actor system: %v", err)
		}
	})

	app := web.New("").Post("/echo", func(c *web.Context) any {
		body, err := io.ReadAll(c.Request.Body)
		if err != nil {
			return "could not read body"
		}
		return c.Request.Header.Get("X-Test") + ":" + string(body)
	})
	serverActor := New("127.0.0.1:0", app, nil)
	pid, err := system.Spawn(ctx, "http-server", serverActor)
	if err != nil {
		t.Fatalf("spawn HTTP server actor: %v", err)
	}
	baseURL := "http://" + serverActor.addr

	req, err := http.NewRequest(http.MethodPost, baseURL+"/echo", bytes.NewBufferString("payload"))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Test", "copied-header")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("send request: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	if got, want := string(body), "copied-header:payload"; got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
	if got := resp.Header.Get("Content-Type"); !strings.HasPrefix(got, "text/plain") {
		t.Errorf("Content-Type = %q, want text/plain", got)
	}

	missing, err := http.Get(baseURL + "/missing")
	if err != nil {
		t.Fatalf("send missing-route request: %v", err)
	}
	defer missing.Body.Close()
	if missing.StatusCode != http.StatusNotFound {
		t.Errorf("missing-route status = %d, want %d", missing.StatusCode, http.StatusNotFound)
	}
	missingBody, err := io.ReadAll(missing.Body)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(missingBody), "NOT_FOUND"; got != want {
		t.Errorf("missing-route body = %q, want %q", got, want)
	}

	stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	response, err := goakt.Ask(stopCtx, pid, &Stop{Context: stopCtx}, 5*time.Second)
	if err != nil {
		t.Fatalf("stop HTTP server actor: %v", err)
	}
	stopped, ok := response.(*Stopped)
	if !ok {
		t.Fatalf("stop response type = %T, want *Stopped", response)
	}
	if stopped.Err != nil {
		t.Errorf("shutdown HTTP server: %v", stopped.Err)
	}
}
