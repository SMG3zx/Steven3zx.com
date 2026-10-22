package gateway

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

func startServer(t *testing.T, s *Server) string {
	t.Helper()
	if err := s.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.Shutdown(ctx)
	})
	return "http://" + s.Addr()
}

func TestServerServesRoutesAndShutsDown(t *testing.T) {
	s := New(Config{Addr: "127.0.0.1:0"})
	s.mux.Post("/echo", func(c *call) any {
		body, err := io.ReadAll(c.Request.Body)
		if err != nil {
			return "could not read body"
		}
		return c.Request.Header.Get("X-Test") + ":" + string(body)
	})
	baseURL := startServer(t, s)

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
	body, _ := io.ReadAll(resp.Body)
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
	missingBody, _ := io.ReadAll(missing.Body)
	if missing.StatusCode != http.StatusNotFound || string(missingBody) != "NOT_FOUND" {
		t.Errorf("missing route = %d %q, want 404 NOT_FOUND", missing.StatusCode, missingBody)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.Shutdown(ctx); err != nil {
		t.Errorf("shutdown: %v", err)
	}
	if err := <-s.Failed(); err != nil {
		t.Errorf("a clean shutdown reported a failure: %v", err)
	}
	if _, err := http.Get(baseURL + "/echo"); err == nil {
		t.Error("server still accepts connections after shutdown")
	}
}

func TestStartReturnsTheListenError(t *testing.T) {
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()

	s := New(Config{Addr: busy.Addr().String()})
	if err := s.Start(); err == nil {
		t.Fatal("Start on an address in use returned nil")
	} else if !strings.Contains(err.Error(), busy.Addr().String()) {
		t.Errorf("error %q does not name the address", err)
	}
	if s.Failed() != nil {
		t.Error("Failed is set although the server never started")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := s.Shutdown(ctx); err != nil {
		t.Errorf("Shutdown of a server that never started: %v", err)
	}
}

func TestShutdownLetsARequestInFlightFinish(t *testing.T) {
	s := New(Config{Addr: "127.0.0.1:0"})
	entered, release := make(chan struct{}), make(chan struct{})
	s.mux.Get("/slow", func(c *call) any {
		close(entered)
		<-release
		return "finished"
	})
	baseURL := startServer(t, s)

	type result struct {
		body string
		err  error
	}
	got := make(chan result, 1)
	go func() {
		resp, err := http.Get(baseURL + "/slow")
		if err != nil {
			got <- result{err: err}
			return
		}
		defer resp.Body.Close()
		b, err := io.ReadAll(resp.Body)
		got <- result{body: string(b), err: err}
	}()
	<-entered

	shutdownDone := make(chan error, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go func() { shutdownDone <- s.Shutdown(ctx) }()

	select {
	case err := <-shutdownDone:
		t.Fatalf("Shutdown returned (%v) while a request was still in flight", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(release)

	if r := <-got; r.err != nil || r.body != "finished" {
		t.Fatalf("in-flight request = %q, %v; want it to finish", r.body, r.err)
	}
	if err := <-shutdownDone; err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
}
