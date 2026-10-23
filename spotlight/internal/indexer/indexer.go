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
	"sync/atomic"
	"time"

	"spotlight/internal/chunk"
	"spotlight/internal/config"
	"spotlight/internal/crawl"
	"spotlight/internal/embed"
	"spotlight/internal/extract"
	"spotlight/internal/store"
	"spotlight/internal/throttle"
)

// excalidrawMaxBytes caps drawing files. They are mostly embedded images, so
// they run far larger than the text inside them.
const excalidrawMaxBytes = 64 << 20

// Store is the part of *store.Store that the indexer uses.
type Store interface {
	FileState(ctx context.Context, path string) (store.FileState, bool, error)
	ReplaceFile(ctx context.Context, path string, st store.FileState, chunks []store.NewChunk) error
	DeleteFile(ctx context.Context, path string) error
	Paths(ctx context.Context) ([]string, error)
	CachedVectors(ctx context.Context, hashes [][]byte) (map[string][]float32, error)
	CompactIfWasteful(ctx context.Context) (bool, error)
}

type Indexer struct {
	st    Store
	emb   embed.Embedder     // nil disables vectors; keyword search still works
	gov   *throttle.Governor // nil means no resource limits
	match *crawl.Matcher
	cfg   config.Config
	log   *slog.Logger
	mu    sync.Mutex // serialises index work; the store uses one connection

	embedded  atomic.Int64 // chunks sent to the embedding model
	reused    atomic.Int64 // chunks that reused an existing vector
	truncated atomic.Int64 // files cut off at max_chunks_per_file

	indexed   atomic.Int64 // files (re)indexed
	unchanged atomic.Int64 // files skipped because mtime and size matched
	dropped   atomic.Int64 // files removed or skipped as gone, ignored, unsupported or too large
	current   atomic.Value // string: file being processed, for progress lines
}

// progressEvery is how often a long scan prints a status line.
const progressEvery = 5 * time.Second

func New(st Store, emb embed.Embedder, gov *throttle.Governor, match *crawl.Matcher, cfg config.Config, log *slog.Logger) *Indexer {
	return &Indexer{st: st, emb: emb, gov: gov, match: match, cfg: cfg, log: log}
}

// IndexFile (re)indexes one file. Files whose mtime and size are unchanged are
// skipped. Files that are gone, ignored, unsupported, generated or too large
// are removed from the index.
func (ix *Indexer) IndexFile(ctx context.Context, path string) error {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	ix.current.Store(path)

	e0, r0 := ix.embedded.Load(), ix.reused.Load()
	i0, u0, d0 := ix.indexed.Load(), ix.unchanged.Load(), ix.dropped.Load()
	err := ix.indexFile(ctx, path)
	if err != nil {
		return err
	}
	switch {
	case ix.indexed.Load() > i0:
		ix.log.Debug("indexed", "path", path,
			"chunks_embedded", ix.embedded.Load()-e0, "chunks_reused", ix.reused.Load()-r0)
	case ix.unchanged.Load() > u0:
		ix.log.Debug("unchanged", "path", path)
	case ix.dropped.Load() > d0:
		ix.log.Debug("skipped or removed", "path", path)
	}
	return nil
}

func (ix *Indexer) indexFile(ctx context.Context, path string) error {
	info, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		ix.dropped.Add(1)
		return ix.st.DeleteFile(ctx, path)
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || crawl.Skip(info) || ix.match.Ignored(path, false) ||
		info.Size() > ix.sizeLimit(path) {
		ix.dropped.Add(1)
		return ix.st.DeleteFile(ctx, path)
	}

	state := store.FileState{Mtime: info.ModTime().UnixNano(), Size: info.Size()}
	prev, known, err := ix.st.FileState(ctx, path)
	if err != nil {
		return err
	}
	if known && prev == state {
		ix.unchanged.Add(1)
		return nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	text, err := extract.Text(path, data, ix.cfg.MaxLineBytes)
	if errors.Is(err, extract.ErrUnsupported) {
		ix.dropped.Add(1)
		return ix.st.DeleteFile(ctx, path)
	}
	if err != nil {
		return err
	}

	pieces := chunk.Split(text, ix.cfg.ChunkSize, ix.cfg.ChunkOverlap)
	if limit := ix.cfg.MaxChunksPerFile; limit > 0 && len(pieces) > limit {
		pieces = pieces[:limit]
		ix.truncated.Add(1)
	}
	rows := make([]store.NewChunk, len(pieces))
	for i, c := range pieces {
		rows[i] = store.NewChunk{Offset: c.Offset, Text: c.Text, Hash: store.HashText(c.Text)}
	}
	if ix.emb != nil {
		ix.embedRows(ctx, path, rows)
	}
	if err := ix.st.ReplaceFile(ctx, path, state, rows); err != nil {
		return err
	}
	ix.indexed.Add(1)
	return nil
}

// sizeLimit is the largest file of path's kind that is worth reading.
func (ix *Indexer) sizeLimit(path string) int64 {
	switch extract.KindOf(path) {
	case extract.KindExcalidraw:
		return excalidrawMaxBytes
	case extract.KindData:
		if ix.cfg.MaxDataBytes > 0 {
			return ix.cfg.MaxDataBytes
		}
	}
	return ix.cfg.MaxFileBytes
}

// embedRows fills in Vec for as many rows as possible. On failure the rest stay
// without vectors, so they remain keyword-searchable.
//
// Chunks whose text is already in the index (another file, or the previous
// version of this one) reuse that vector, and identical chunks within the file
// are embedded once. Only new text reaches the model, in batches that each go
// through the resource governor: it waits while the machine is over its CPU,
// memory or GPU limits, and idles after each batch so this process keeps the
// GPU at or under its share.
func (ix *Indexer) embedRows(ctx context.Context, path string, rows []store.NewChunk) {
	hashes := make([][]byte, len(rows))
	for i := range rows {
		hashes[i] = rows[i].Hash
	}
	cached, err := ix.st.CachedVectors(ctx, hashes)
	if err != nil {
		ix.log.Warn("embedding cache lookup failed; embedding everything", "path", path, "err", err)
		cached = nil
	}

	// Rows still needing a vector, grouped by identical text.
	waiting := make(map[string][]int)
	var order []string // distinct texts in first-seen order
	for i := range rows {
		key := string(rows[i].Hash)
		if v, ok := cached[key]; ok {
			rows[i].Vec = v
			ix.reused.Add(1)
			continue
		}
		if _, dup := waiting[key]; !dup {
			order = append(order, key)
		}
		waiting[key] = append(waiting[key], i)
	}

	batch := max(ix.cfg.EmbedBatch, 1)
	for start := 0; start < len(order); start += batch {
		if err := ix.gov.Wait(ctx); err != nil {
			return // shutting down
		}
		keys := order[start:min(start+batch, len(order))]
		inputs := make([]string, len(keys))
		for i, k := range keys {
			inputs[i] = rows[waiting[k][0]].Text
		}

		began := time.Now()
		vecs, err := ix.emb.Embed(ctx, inputs)
		busy := time.Since(began)
		if err != nil {
			ix.log.Warn("embedding failed; file is keyword-only until re-indexed", "path", path, "err", err)
			return
		}
		for i, k := range keys {
			for _, idx := range waiting[k] {
				rows[idx].Vec = vecs[i]
			}
		}
		ix.embedded.Add(int64(len(keys)))
		// Rows beyond the first for a text were satisfied without model work.
		for _, k := range keys {
			ix.reused.Add(int64(len(waiting[k]) - 1))
		}

		if err := ix.gov.Pace(ctx, busy); err != nil {
			return // shutting down
		}
	}
}

// Scan indexes every eligible file under the configured roots and removes
// index entries that no longer qualify. It returns the number of files
// processed.
func (ix *Indexer) Scan(ctx context.Context) (int, error) {
	embedded0, reused0, truncated0 := ix.embedded.Load(), ix.reused.Load(), ix.truncated.Load()
	indexed0, unchanged0, dropped0 := ix.indexed.Load(), ix.unchanged.Load(), ix.dropped.Load()
	began := time.Now()
	ix.log.Info("scan started", "roots", ix.match.Roots())

	done := make(chan struct{})
	defer close(done)
	go ix.reportProgress(done, began, indexed0, unchanged0, dropped0, embedded0, reused0)

	// Drop entries that are now ignored or outside the roots first, so stale
	// results disappear right away rather than at the end of a long scan.
	removed := ix.pruneIneligible(ctx)

	seen := make(map[string]struct{})
	count := 0
	err := ix.match.Walk(func(path string, _ fs.FileInfo) error {
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
	rootsOnline := true
	for _, root := range ix.match.Roots() {
		if _, err := os.Stat(root); err != nil {
			ix.log.Warn("root unavailable; skipping prune", "root", root, "err", err)
			rootsOnline = false
			break
		}
	}
	if rootsOnline {
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
				continue
			}
			removed++
		}
	}

	ix.log.Info("scan finished",
		"files", count,
		"indexed", ix.indexed.Load()-indexed0,
		"unchanged", ix.unchanged.Load()-unchanged0,
		"skipped", ix.dropped.Load()-dropped0,
		"elapsed", time.Since(began).Round(time.Second),
		"chunks_embedded", ix.embedded.Load()-embedded0,
		"chunks_reused", ix.reused.Load()-reused0,
		"files_truncated", ix.truncated.Load()-truncated0,
		"entries_removed", removed)

	if ran, err := ix.st.CompactIfWasteful(ctx); err != nil {
		ix.log.Warn("compacting index failed", "err", err)
	} else if ran {
		ix.log.Info("index compacted")
	}
	return count, nil
}

// pruneIneligible deletes index entries for paths the matcher would now skip,
// and returns how many it removed.
func (ix *Indexer) pruneIneligible(ctx context.Context) int {
	paths, err := ix.st.Paths(ctx)
	if err != nil {
		ix.log.Warn("listing indexed files failed", "err", err)
		return 0
	}
	removed := 0
	for _, p := range paths {
		if ctx.Err() != nil {
			break
		}
		if !ix.match.Ignored(p, false) {
			continue
		}
		if err := ix.st.DeleteFile(ctx, p); err != nil {
			ix.log.Warn("prune failed", "path", p, "err", err)
			continue
		}
		removed++
	}
	return removed
}

// reportProgress prints a status line every few seconds until done closes, so
// a long first scan visibly makes progress.
func (ix *Indexer) reportProgress(done <-chan struct{}, began time.Time, i0, u0, d0, e0, r0 int64) {
	t := time.NewTicker(progressEvery)
	defer t.Stop()
	for {
		select {
		case <-done:
			return
		case <-t.C:
			cur, _ := ix.current.Load().(string)
			embedded := ix.embedded.Load() - e0
			ix.log.Info("scanning",
				"indexed", ix.indexed.Load()-i0,
				"unchanged", ix.unchanged.Load()-u0,
				"skipped", ix.dropped.Load()-d0,
				"chunks_embedded", embedded,
				"chunks_reused", ix.reused.Load()-r0,
				"chunks_per_sec", int(float64(embedded)/time.Since(began).Seconds()),
				"current", cur)
		}
	}
}
