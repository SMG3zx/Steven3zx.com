package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestDefaults(t *testing.T) {
	c, err := Load(write(t, `{"roots":["/x"],"db_path":"/tmp/i.db"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !c.RespectGitignore || c.MaxDataBytes != 256<<10 || c.MaxLineBytes != 4000 || c.MaxChunksPerFile != 300 {
		t.Fatalf("unexpected defaults: %+v", c)
	}
	if len(c.IgnoreDirs) != 0 {
		t.Fatalf("ignore_dirs should default to empty (additive to built-ins), got %v", c.IgnoreDirs)
	}
	if c.MaxCPUPercent != 80 {
		t.Fatalf("throttle default lost: %v", c.MaxCPUPercent)
	}
}

func TestOverridesAndValidation(t *testing.T) {
	c, err := Load(write(t, `{"roots":["/x"],"db_path":"/d","respect_gitignore":false,"max_line_bytes":0,"force_include":["/index.md"]}`))
	if err != nil {
		t.Fatal(err)
	}
	if c.RespectGitignore || c.MaxLineBytes != 0 || len(c.ForceInclude) != 1 {
		t.Fatalf("overrides not applied: %+v", c)
	}
	_, err = Load(write(t, `{"roots":["/x"],"db_path":"/d","max_chunks_per_file":-1}`))
	if err == nil || !strings.Contains(err.Error(), "max_chunks_per_file") {
		t.Fatalf("want negative-value error, got %v", err)
	}
}
