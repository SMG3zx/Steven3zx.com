package runnertest

import (
	"context"
	"errors"
	"testing"

	"janus/internal/app"
)

func TestFakePassesTheRunnerContract(t *testing.T) {
	Run(t, func(*testing.T) app.Runner { return NewFake() })
}

func TestFakeRunsRegisteredPrograms(t *testing.T) {
	f := NewFake()
	module := []byte("\x00asm\x01\x00\x00\x00custom")
	if _, err := f.Run(context.Background(), module, app.RunInput{}); err == nil {
		t.Fatal("an unregistered module ran")
	}
	boom := errors.New("boom")
	f.Handle(module, func(input app.RunInput) (app.RunResult, error) {
		return app.RunResult{Stdout: []byte(input.Args[0])}, boom
	})
	got, err := f.Run(context.Background(), module, app.RunInput{Args: []string{"arg"}})
	if !errors.Is(err, boom) || string(got.Stdout) != "arg" {
		t.Fatalf("got %q, %v", got.Stdout, err)
	}
}
