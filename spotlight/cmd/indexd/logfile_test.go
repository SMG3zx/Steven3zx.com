package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOpenLogRotates(t *testing.T) {
	p := filepath.Join(t.TempDir(), "sub", "indexd.log")
	f, err := openLog(p)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("first\n")
	f.Close()

	f, err = openLog(p)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("second\n")
	f.Close()
	if b, _ := os.ReadFile(p); string(b) != "first\nsecond\n" {
		t.Fatalf("small log should append, got %q", b)
	}

	if err := os.WriteFile(p, make([]byte, maxLogBytes+1), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err = openLog(p)
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	if fi, _ := os.Stat(p); fi.Size() != 0 {
		t.Fatalf("oversized log should start fresh, size %d", fi.Size())
	}
	if fi, err := os.Stat(p + ".1"); err != nil || fi.Size() != maxLogBytes+1 {
		t.Fatalf("old log should be kept as .1: %v", err)
	}
}
