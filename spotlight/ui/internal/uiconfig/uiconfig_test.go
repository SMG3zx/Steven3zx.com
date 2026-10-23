package uiconfig

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultsAndOverride(t *testing.T) {
	p := filepath.Join(t.TempDir(), "ui.json")
	if err := os.WriteFile(p, []byte(`{"hotkey":"alt+space","max_results":5}`), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.Hotkey != "alt+space" || c.MaxResults != 5 || c.DaemonURL != "http://127.0.0.1:7788" || !c.HideOnBlur {
		t.Fatalf("got %+v", c)
	}
}

func TestRejectsBadValues(t *testing.T) {
	p := filepath.Join(t.TempDir(), "ui.json")
	os.WriteFile(p, []byte(`{"max_results":500}`), 0o600)
	if _, err := Load(p); err == nil {
		t.Fatal("max_results 500 should fail")
	}
	os.WriteFile(p, []byte(`{not json`), 0o600)
	if _, err := Load(p); err == nil {
		t.Fatal("bad json should fail")
	}
	if _, err := Load(filepath.Join(t.TempDir(), "missing.json")); err == nil {
		t.Fatal("an explicitly named missing file should fail")
	}
}
