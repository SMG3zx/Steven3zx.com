package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"janus/internal/agent/disk"
)

func TestPauseAndResumeFromTheCommandLineWriteTheKeptState(t *testing.T) {
	state := t.TempDir()
	if err := saveSettings(state, settings{AgentID: "a1", Name: "laptop", Folder: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	disk, err := disk.Open(deploymentsDir(state))
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err = run(ctx, []string{"pause", "-state", state}, &out); err != nil {
		t.Fatal(err)
	}
	if paused, _ := disk.Paused(ctx); !paused {
		t.Fatal("pause did not keep the pause")
	}
	if err = run(ctx, []string{"status", "-state", state}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Paused: true") {
		t.Fatalf("status said %q", out.String())
	}
	if err = run(ctx, []string{"resume", "-state", state}, &out); err != nil {
		t.Fatal(err)
	}
	if paused, _ := disk.Paused(ctx); paused {
		t.Fatal("resume did not clear the pause")
	}
}

func TestCommandsNeedAnEnrolledAgent(t *testing.T) {
	for _, command := range []string{"run", "pause", "resume", "status"} {
		err := run(context.Background(), []string{command, "-state", t.TempDir()}, &bytes.Buffer{})
		if err == nil || !strings.Contains(err.Error(), "not enrolled") {
			t.Errorf("%s without enrolling = %v", command, err)
		}
	}
}

func TestEnrollChecksItsArgumentsBeforeCallingTheServer(t *testing.T) {
	ctx := context.Background()
	for _, args := range [][]string{
		{"enroll", "-state", t.TempDir(), "-folder", t.TempDir(), "code"},
		{"enroll", "-state", t.TempDir(), "-server", "http://127.0.0.1:1", "code"},
		{"enroll", "-state", t.TempDir(), "-server", "http://127.0.0.1:1", "-folder", "does-not-exist", "code"},
		{"enroll", "-state", t.TempDir(), "-server", "http://127.0.0.1:1", "-folder", t.TempDir()},
	} {
		if err := run(ctx, args, &bytes.Buffer{}); err == nil {
			t.Errorf("%v succeeded", args)
		}
	}
	if err := run(ctx, []string{"dance"}, &bytes.Buffer{}); err == nil {
		t.Error("an unknown command succeeded")
	}
}
