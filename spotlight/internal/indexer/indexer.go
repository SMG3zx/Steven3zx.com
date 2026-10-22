// Package indexer ties crawling, extraction, chunking, embedding and storage
// together for a single file or a full scan.
package indexer

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"sync"

	"spotlight/internal/chunk"
	"spotlight/internal/config"
	"spotlight/internal/crawl"
	"spotlight/internal/embed"
	"spotlight/internal/extract"
	"spotlight/internal/store"
)

const embedBatch = 16

type Indexer struct {
	st  *store.Store
	emb embed.Embedder // nil disables vectors; keyword search still works
	cfg config.Config
	log *slog.Logger
	mu  sync.Mutex // serialises index work; the store uses one connection
}

func New(st *store.Store, emb embed.Embedder, cfg config.Config, log *slog.Logger) *Indexer {
	return &Indexer{st: st, emb: emb, cfg: cfg, log: log}
}

// IndexFile (re)indexes one file. Files whose mtime and size are unchanged are
// skipped. Files that are gone, unsupported, or too large are removed from the index.
func (ix *Indexer) IndexFile(ctx context.Context, path string) error {
	ix.mu.Lock()
	defer ix.mu.Unlock()

	info, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return ix.st.DeleteFile(ctx, path)
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || crawl.Skip(info) || info.Size() > ix.cfg.MaxFileBytes {
		return ix.st.DeleteFile(ctx, path)
	}

	state := store.FileState{Mtime: info.ModTime().UnixNano(), Size: info.Size()}
	prev, known, err := ix.st.FileState(ctx, path)
	if err != nil {
		return err
	}
	if known && prev == state {
		return nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	text, err := extract.Text(path, data)
	if errors.Is(err, extract.ErrUnsupported) {
		return ix.st.DeleteFile(ctx, path)
	}
	if err != nil {
		return err
	}

	pieces := chunk.Split(text, ix.cfg.ChunkSize, ix.cfg.ChunkOverlap)
	rows := make([]store.NewChunk, len(pieces))
	for i, c := range pieces {
		rows[i] = store.NewChunk{Offset: c.Offset, Text: c.Text}
	}
	if ix.emb != nil {
		ix.embedRows(ctx, path, rows)
	}
	return ix.st.ReplaceFile(ctx, path, state, rows)
}

// embedRows fills in Vec for as many rows as possible. On failure the rest stay
// without vectors, so they remain keyword-searchable.
func (ix *Indexer) embedRows(ctx context.Context, path string, rows []store.NewChunk) {
	for start := 0; start < len(rows); start += embedBatch {
		end := min(start+embedBatch, len(rows))
		inputs := make([]string, 0, end-start)
		for _, r := range rows[start:end] {
			inputs = append(inputs, r.Text)
		}
		vecs, err := ix.emb.Embed(ctx, inputs)
		if err != nil {
			ix.log.Warn("embedding failed; file is keyword-only until re-indexed", "path", path, "err", err)
			return
		}
		for i := range vecs {
			rows[start+i].Vec = vecs[i]
		}
	}
}

// Scan indexes every file under the configured roots and removes index
// entries for files that no longer qualify. It returns the number of files
// processed.
func (ix *Indexer) Scan(ctx context.Context) (int, error) {
	seen := make(map[string]struct{})
	count := 0
	err := crawl.Walk(ix.cfg.Roots, ix.cfg.IgnoreDirs, func(path string, _ fs.FileInfo) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		seen[path] = struct{}{}
		if err := ix.IndexFile(ctx, path); err != nil {
			ix.log.Warn("index failed", "path", path, "err", err)
			return nil
		}
		count++
		return nil
	})
	if err != nil {
		return count, err
	}

	// Pruning is unsafe if a root is offline (e.g. an unplugged drive): the
	// walk would see nothing and wipe the index.
	for _, root := range ix.cfg.Roots {
		if _, err := os.Stat(root); err != nil {
			ix.log.Warn("root unavailable; skipping prune", "root", root, "err", err)
			return count, nil
		}
	}
	existing, err := ix.st.Paths(ctx)
	if err != nil {
		return count, err
	}
	for _, p := range existing {
		if _, ok := seen[p]; ok {
			continue
		}
		if err := ix.st.DeleteFile(ctx, p); err != nil {
			ix.log.Warn("prune failed", "path", p, "err", err)
		}
	}
	return count, nil
}
