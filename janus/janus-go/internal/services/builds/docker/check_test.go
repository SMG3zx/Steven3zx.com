package docker

import (
	"context"
	"errors"
	"testing"

	"janus/internal/services/builds"
)

// checkExecutor answers every container run with stdout and stderr, and
// records the command it was asked to run.
func checkExecutor(stdout, stderr string, err error, commands *[]string) Executor {
	return func(_ context.Context, run Run) Ran {
		*commands = append(*commands, run.Command)
		return Ran{Stdout: []byte(stdout), Stderr: []byte(stderr), Err: err}
	}
}

var checkImages = Images{Rust: "janus-rust-builder:test", Go: "janus-go-builder:test"}

func TestCheckPassesWhenTheTestCommandExitsZero(t *testing.T) {
	var commands []string
	docker := newDocker(checkImages, checkExecutor("0\n", "test result: ok. 3 passed; 0 failed\n", nil, &commands))

	result, err := docker.Check(context.Background(), "Rust", builds.CheckRequest{Workspace: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Passed || result.NoTests || string(result.Log) != "test result: ok. 3 passed; 0 failed\n" {
		t.Fatalf("result = %+v", result)
	}
	if len(commands) != 1 || commands[0] != rustCheckCommand {
		t.Fatalf("commands = %q", commands)
	}
}

func TestCheckFailsWhenTheTestCommandReportsANonZeroStatus(t *testing.T) {
	var commands []string
	docker := newDocker(checkImages, checkExecutor("101\n", "test result: FAILED\n", nil, &commands))

	result, err := docker.Check(context.Background(), "rust", builds.CheckRequest{Workspace: t.TempDir()})
	if err != nil {
		t.Fatalf("a failing test is a result, not an error: %v", err)
	}
	if result.Passed {
		t.Fatalf("a test that exited 101 passed: %+v", result)
	}
}

func TestCheckErrorsWhenTheContainerCannotRun(t *testing.T) {
	var commands []string
	docker := newDocker(checkImages, checkExecutor("", "container killed\n", errors.New("container exited with OOM"), &commands))

	if _, err := docker.Check(context.Background(), "Go", builds.CheckRequest{Workspace: t.TempDir()}); err == nil {
		t.Fatal("a container that could not run gave no error")
	}
}

func TestCheckReportsNoTestsWhenNothingRan(t *testing.T) {
	var commands []string
	docker := newDocker(checkImages, checkExecutor("0\n", "running 0 tests\n", nil, &commands))

	result, err := docker.Check(context.Background(), "Rust", builds.CheckRequest{Workspace: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Passed || !result.NoTests {
		t.Fatalf("a Rust crate with no tests should pass with NoTests: %+v", result)
	}
	docker = newDocker(checkImages, checkExecutor("0\n", "?   \texample.com/app\t[no test files]\n", nil, &commands))
	result, err = docker.Check(context.Background(), "Go", builds.CheckRequest{Workspace: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Passed || !result.NoTests {
		t.Fatalf("a Go module with no tests should pass with NoTests: %+v", result)
	}
}

func TestCheckRefusesALanguageWithoutChecks(t *testing.T) {
	var commands []string
	docker := newDocker(checkImages, checkExecutor("0\n", "", nil, &commands))

	if _, err := docker.Check(context.Background(), "JavaScript/TypeScript", builds.CheckRequest{Workspace: t.TempDir()}); err == nil {
		t.Fatal("checks ran for a language with no check commands")
	}
	if len(commands) != 0 {
		t.Fatalf("a container ran for a language with no checks: %q", commands)
	}
}
