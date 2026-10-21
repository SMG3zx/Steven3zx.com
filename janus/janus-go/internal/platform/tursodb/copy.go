package tursodb

import (
	"context"
	"database/sql"
	"strings"
)

// CopyTable copies the named columns of every row of table in source into the
// same table of tx, and returns how many rows it copied. Split uses it to move
// a table out of the single-process database as it is.
func CopyTable(ctx context.Context, source *sql.DB, tx *sql.Tx, table, columns string) (int, error) {
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
