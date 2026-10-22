// Package store owns the SQLite index: file metadata, chunk text, the FTS5
// keyword index, and chunk embeddings stored as float32 BLOBs.
package store

import (
	"context"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"

	_ "modernc.org/sqlite" // pure-Go driver, no CGO needed on Windows
)

const schema = `
CREATE TABLE IF NOT EXISTS files (
	id    INTEGER PRIMARY KEY,
	path  TEXT NOT NULL UNIQUE,
	mtime INTEGER NOT NULL,
	size  INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS chunks (
	id        INTEGER PRIMARY KEY,
	file_id   INTEGER NOT NULL,
	ord       INTEGER NOT NULL,
	start_off INTEGER NOT NULL,
	text      TEXT NOT NULL,
	embedding BLOB
);
CREATE INDEX IF NOT EXISTS chunks_file ON chunks(file_id);
CREATE VIRTUAL TABLE IF NOT EXISTS chunks_fts USING fts5(
	text, content='chunks', content_rowid='id'
);
CREATE TRIGGER IF NOT EXISTS chunks_ai AFTER INSERT ON chunks BEGIN
	INSERT INTO chunks_fts(rowid, text) VALUES (new.id, new.text);
END;
CREATE TRIGGER IF NOT EXISTS chunks_ad AFTER DELETE ON chunks BEGIN
	INSERT INTO chunks_fts(chunks_fts, rowid, text) VALUES ('delete', old.id, old.text);
END;
`

type Store struct {
	db  *sql.DB
	gen atomic.Uint64 // bumped on every write; lets readers cache vectors
}

type FileState struct {
	Mtime int64 // UnixNano
	Size  int64
}

// NewChunk is a chunk to store. Vec is nil when the chunk has no embedding.
type NewChunk struct {
	Offset int
	Text   string
	Vec    []float32
}

// Hit is a chunk joined with its file path.
type Hit struct {
	ID     int64
	Path   string
	Offset int
	Text   string
}

// EmbRow is a chunk id with its stored embedding.
type EmbRow struct {
	ID  int64
	Vec []float32
}

func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create db dir: %w", err)
	}
	dsn := "file:" + filepath.ToSlash(path) + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// One connection keeps writes serialized and avoids SQLITE_BUSY surprises.
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("init schema: %w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

// Generation changes whenever the index is written.
func (s *Store) Generation() uint64 { return s.gen.Load() }

// FileState returns the stored stamp for path, if the file is indexed.
func (s *Store) FileState(ctx context.Context, path string) (FileState, bool, error) {
	var st FileState
	err := s.db.QueryRowContext(ctx, `SELECT mtime, size FROM files WHERE path = ?`, path).
		Scan(&st.Mtime, &st.Size)
	if errors.Is(err, sql.ErrNoRows) {
		return FileState{}, false, nil
	}
	if err != nil {
		return FileState{}, false, err
	}
	return st, true, nil
}

// ReplaceFile atomically swaps all chunks for path with chunks.
func (s *Store) ReplaceFile(ctx context.Context, path string, st FileState, chunks []NewChunk) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	fileID, err := upsertFile(ctx, tx, path, st)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM chunks WHERE file_id = ?`, fileID); err != nil {
		return err
	}
	stmt, err := tx.PrepareContext(ctx,
		`INSERT INTO chunks (file_id, ord, start_off, text, embedding) VALUES (?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for i, c := range chunks {
		var emb any // NULL when nil
		if c.Vec != nil {
			emb = encodeVec(c.Vec)
		}
		if _, err := stmt.ExecContext(ctx, fileID, i, c.Offset, c.Text, emb); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.gen.Add(1)
	return nil
}

// DeleteFile removes a file and its chunks. Missing paths are not an error.
func (s *Store) DeleteFile(ctx context.Context, path string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var id int64
	err = tx.QueryRowContext(ctx, `SELECT id FROM files WHERE path = ?`, path).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM chunks WHERE file_id = ?`, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM files WHERE id = ?`, id); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.gen.Add(1)
	return nil
}

// Paths lists every indexed file path.
func (s *Store) Paths(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT path FROM files`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Counts returns file, chunk, and embedded-chunk totals.
func (s *Store) Counts(ctx context.Context) (files, chunks, embedded int64, err error) {
	err = s.db.QueryRowContext(ctx, `
		SELECT (SELECT COUNT(*) FROM files),
		       (SELECT COUNT(*) FROM chunks),
		       (SELECT COUNT(*) FROM chunks WHERE embedding IS NOT NULL)`).
		Scan(&files, &chunks, &embedded)
	return
}

// KeywordSearch runs an FTS5 MATCH query ranked by bm25.
func (s *Store) KeywordSearch(ctx context.Context, match string, limit int) ([]Hit, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT c.id, f.path, c.start_off, c.text
		FROM chunks_fts
		JOIN chunks c ON c.id = chunks_fts.rowid
		JOIN files f ON f.id = c.file_id
		WHERE chunks_fts MATCH ?
		ORDER BY bm25(chunks_fts)
		LIMIT ?`, match, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Hit
	for rows.Next() {
		var h Hit
		if err := rows.Scan(&h.ID, &h.Path, &h.Offset, &h.Text); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// Embeddings loads every stored vector for brute-force similarity search.
func (s *Store) Embeddings(ctx context.Context) ([]EmbRow, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, embedding FROM chunks WHERE embedding IS NOT NULL`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []EmbRow
	for rows.Next() {
		var id int64
		var blob []byte
		if err := rows.Scan(&id, &blob); err != nil {
			return nil, err
		}
		vec, err := decodeVec(blob)
		if err != nil {
			return nil, fmt.Errorf("chunk %d: %w", id, err)
		}
		out = append(out, EmbRow{ID: id, Vec: vec})
	}
	return out, rows.Err()
}

// ChunksByID fetches chunk text and paths for the given ids.
func (s *Store) ChunksByID(ctx context.Context, ids []int64) (map[int64]Hit, error) {
	out := make(map[int64]Hit, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	placeholders := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		placeholders[i] = "?"
		args[i] = id
	}
	q := `SELECT c.id, f.path, c.start_off, c.text
		FROM chunks c JOIN files f ON f.id = c.file_id
		WHERE c.id IN (` + strings.Join(placeholders, ",") + `)`
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var h Hit
		if err := rows.Scan(&h.ID, &h.Path, &h.Offset, &h.Text); err != nil {
			return nil, err
		}
		out[h.ID] = h
	}
	return out, rows.Err()
}

func upsertFile(ctx context.Context, tx *sql.Tx, path string, st FileState) (int64, error) {
	var id int64
	err := tx.QueryRowContext(ctx, `SELECT id FROM files WHERE path = ?`, path).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		res, err := tx.ExecContext(ctx,
			`INSERT INTO files (path, mtime, size) VALUES (?, ?, ?)`, path, st.Mtime, st.Size)
		if err != nil {
			return 0, err
		}
		return res.LastInsertId()
	}
	if err != nil {
		return 0, err
	}
	_, err = tx.ExecContext(ctx,
		`UPDATE files SET mtime = ?, size = ? WHERE id = ?`, st.Mtime, st.Size, id)
	return id, err
}

// encodeVec stores float32 values little-endian.
func encodeVec(v []float32) []byte {
	buf := make([]byte, 4*len(v))
	for i, x := range v {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(x))
	}
	return buf
}

func decodeVec(b []byte) ([]float32, error) {
	if len(b)%4 != 0 {
		return nil, errors.New("embedding blob length is not a multiple of 4")
	}
	v := make([]float32, len(b)/4)
	for i := range v {
		v[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[i*4:]))
	}
	return v, nil
}
