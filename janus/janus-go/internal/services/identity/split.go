package identity

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"

	_ "turso.tech/database/tursogo"

	"janus/internal/platform/tursodb"
)

// splitTables are the identity tables of janus.db, parents first, with the
// columns identity.db's first migration gives them.
var splitTables = []struct{ name, columns string }{
	{"users", "id,email,display_name,created_at"},
	{"local_credentials", "user_id,email,password_hash"},
	{"oauth_identities", "provider,subject,user_id"},
	{"github_connections", "user_id,subject,token_ciphertext,connected_at"},
}

// SplitResult counts the rows Split copied, by table.
type SplitResult map[string]int

// Split copies the identity tables of the single-process database at legacy
// into a new identity.db at path, in one transaction. It refuses when path
// already exists, so it runs once. legacy is only read: its tables stay, as
// janus.db still uses users to own Projects until those move to their services.
func Split(ctx context.Context, legacy, path string) (SplitResult, error) {
	if _, err := os.Stat(path); err == nil {
		return nil, fmt.Errorf("%s already exists; the split has run", path)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	if _, err := os.Stat(legacy); err != nil {
		return nil, fmt.Errorf("the database to split: %w", err)
	}
	source, err := sql.Open("turso", legacy)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", legacy, err)
	}
	defer source.Close()
	source.SetMaxOpenConns(1)

	target, err := tursodb.Open(ctx, path, migrations)
	if err != nil {
		return nil, err
	}
	result := SplitResult{}
	err = tursodb.InTx(ctx, target, func(tx *sql.Tx) error {
		for _, table := range splitTables {
			n, err := copyTable(ctx, source, tx, table.name, table.columns)
			if err != nil {
				return fmt.Errorf("copy %s: %w", table.name, err)
			}
			result[table.name] = n
		}
		return nil
	})
	closeErr := target.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		for _, suffix := range []string{"", "-wal", "-shm"} {
			_ = os.Remove(path + suffix)
		}
		return nil, err
	}
	return result, nil
}

func copyTable(ctx context.Context, source *sql.DB, tx *sql.Tx, table, columns string) (int, error) {
	rows, err := source.QueryContext(ctx, `SELECT `+columns+` FROM `+table)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	width := strings.Count(columns, ",") + 1
	insert := `INSERT INTO ` + table + `(` + columns + `) VALUES(` + strings.TrimSuffix(strings.Repeat("?,", width), ",") + `)`
	n := 0
	for rows.Next() {
		values := make([]any, width)
		pointers := make([]any, width)
		for i := range values {
			pointers[i] = &values[i]
		}
		if err := rows.Scan(pointers...); err != nil {
			return n, err
		}
		if _, err := tx.ExecContext(ctx, insert, values...); err != nil {
			return n, err
		}
		n++
	}
	return n, rows.Err()
}

// NeedsSplit reports whether identity.db at path is missing while the
// single-process database at legacy still holds Users. Starting the identity
// service then would lock everyone out, so the caller should run Split first.
func NeedsSplit(ctx context.Context, legacy, path string) (bool, error) {
	if _, err := os.Stat(path); err == nil {
		return false, nil
	}
	if _, err := os.Stat(legacy); errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	source, err := sql.Open("turso", legacy)
	if err != nil {
		return false, err
	}
	defer source.Close()
	var tables int
	if err := source.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='local_credentials'`).Scan(&tables); err != nil || tables == 0 {
		return false, err
	}
	var users int
	err = source.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM local_credentials)+(SELECT COUNT(*) FROM oauth_identities)`).Scan(&users)
	return users > 0, err
}
