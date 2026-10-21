package tursodb

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestMigrationsApplyOnceAndInOrder(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "nested", "test.db")
	first := []string{`CREATE TABLE things (id TEXT PRIMARY KEY)`}
	db, err := Open(ctx, path, first)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO things(id) VALUES('a')`); err != nil {
		t.Fatal(err)
	}
	db.Close()

	second := append(first, `ALTER TABLE things ADD COLUMN name TEXT NOT NULL DEFAULT ''`)
	db, err = Open(ctx, path, second)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var id, name string
	if err := db.QueryRowContext(ctx, `SELECT id,name FROM things`).Scan(&id, &name); err != nil || id != "a" {
		t.Fatalf("row = %q %q, %v; want the row kept through the second migration", id, name, err)
	}
	var versions int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations`).Scan(&versions); err != nil || versions != 2 {
		t.Fatalf("versions = %d, %v", versions, err)
	}
}

func TestADatabaseFromANewerBuildIsRefused(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "test.db")
	db, err := Open(ctx, path, []string{`CREATE TABLE a (id TEXT)`, `CREATE TABLE b (id TEXT)`})
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
	if _, err := Open(ctx, path, []string{`CREATE TABLE a (id TEXT)`}); err == nil || !strings.Contains(err.Error(), "newer build") {
		t.Fatalf("err = %v, want a refusal", err)
	}
}

func TestAFailedMigrationLeavesTheVersionUnchanged(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "test.db")
	if _, err := Open(ctx, path, []string{`CREATE TABLE a (id TEXT)`, `NOT SQL`}); err == nil {
		t.Fatal("a broken migration was applied")
	}
	db, err := Open(ctx, path, []string{`CREATE TABLE a (id TEXT)`})
	if err != nil {
		t.Fatalf("reopen after a failed migration: %v", err)
	}
	db.Close()
}
