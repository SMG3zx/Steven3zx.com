package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestMain lets the test binary stand in for the wrapped program: a runner
// test starts it again with SVCWRAP_HELPER set to pick a behaviour.
func TestMain(m *testing.M) {
	switch os.Getenv("SVCWRAP_HELPER") {
	case "exit":
		os.Stdout.WriteString("helper ran with " + os.Getenv("SVCWRAP_MARK") + "\n")
		os.Exit(3)
	case "sleep":
		time.Sleep(time.Minute)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// lockedBuffer is a bytes.Buffer safe to share with the child's output copier.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func TestRunnerRestartsAProgramThatExits(t *testing.T) {
	out := &lockedBuffer{}
	r := &runner{
		exe:          os.Args[0],
		env:          []string{"SVCWRAP_HELPER=exit", "SVCWRAP_MARK=file-env"},
		out:          out,
		respawnDelay: 20 * time.Millisecond,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- r.run(ctx) }()

	deadline := time.After(1500 * time.Millisecond)
	for strings.Count(out.String(), "helper ran with file-env") < 3 {
		select {
		case <-deadline:
			t.Fatalf("program restarted fewer than 3 times; output:\n%s", out.String())
		case <-time.After(10 * time.Millisecond):
		}
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("run returned %v after a clean stop", err)
	}
}

func TestRunnerStopsTheProgramWhenCancelled(t *testing.T) {
	r := &runner{
		exe:          os.Args[0],
		env:          []string{"SVCWRAP_HELPER=sleep"},
		out:          &lockedBuffer{},
		respawnDelay: time.Hour,
	}
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() { done <- r.run(ctx) }()
	time.Sleep(200 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run returned %v after a clean stop", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("run did not return after the context was cancelled")
	}
}

func TestLoadEnvFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "traefik.env")
	content := "# comment\n\nCF_DNS_API_TOKEN=abc123\nQUOTED=\"two words\"\nEMPTY=\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := loadEnvFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"CF_DNS_API_TOKEN=abc123", "QUOTED=two words", "EMPTY="}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestLoadEnvFileRejectsALineWithoutAnEqualsSign(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.env")
	if err := os.WriteFile(path, []byte("not a pair\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadEnvFile(path); err == nil {
		t.Fatal("expected an error for a line that is not KEY=VALUE")
	}
}
