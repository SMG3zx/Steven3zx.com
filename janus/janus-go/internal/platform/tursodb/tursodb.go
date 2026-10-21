// Package tursodb opens a service's Turso database: one file, one connection,
// and a schema brought up to date by numbered migrations.
package tursodb

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	_ "turso.tech/database/tursogo"
)

// Open opens the database at path, creating it and its directory when they do
// not exist, and applies the migrations it has not had yet, in order, each in
// its own transaction. migrations only ever grows: an applied migration is
// never edited, and a change is a new entry at the end.
func Open(ctx context.Context, path string, migrations []string) (*sql.DB, error) {
	if path != ":memory:" && !strings.HasPrefix(path, "file:") {
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return nil, fmt.Errorf("create database directory: %w", err)
		}
	}
	db, err := sql.Open("turso", path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	db.SetMaxOpenConns(1)
	if err := migrate(ctx, db, migrations); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate %s: %w", path, err)
	}
	return db, nil
}

func migrate(ctx context.Context, db *sql.DB, migrations []string) error {
	if _, err := db.ExecContext(ctx, `PRAGMA journal_mode=WAL; PRAGMA foreign_keys=ON; PRAGMA busy_timeout=5000;`); err != nil {
		return fmt.Errorf("configure: %w", err)
	}
	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY)`); err != nil {
		return err
	}
	var applied int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations`).Scan(&applied); err != nil {
		return err
	}
	if applied > len(migrations) {
		return fmt.Errorf("the database has %d migrations and this build knows %d; it was written by a newer build", applied, len(migrations))
	}
	for version := applied; version < len(migrations); version++ {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, migrations[version]); err == nil {
			_, err = tx.ExecContext(ctx, `INSERT INTO schema_migrations(version) VALUES(?)`, version+1)
		}
		if err == nil {
			err = tx.Commit()
		} else {
			_ = tx.Rollback()
		}
		if err != nil {
			return fmt.Errorf("migration %d: %w", version+1, err)
		}
	}
	return nil
}

// InTx runs fn in a transaction, which commits when fn returns nil.
func InTx(ctx context.Context, db *sql.DB, fn func(tx *sql.Tx) error) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}
