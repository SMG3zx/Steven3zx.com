package indexer

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"spotlight/internal/config"
	"spotlight/internal/crawl"
	"spotlight/internal/store"
)

type fakeStore struct {
	mu    sync.Mutex
	files map[string]store.FileState
	rows  map[string][]store.NewChunk
}

func newFake() *fakeStore {
	return &fakeStore{files: map[string]store.FileState{}, rows: map[string][]store.NewChunk{}}
}

func (f *fakeStore) FileState(_ context.Context, p string) (store.FileState, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.files[p]
	return s, ok, nil
}
func (f *fakeStore) ReplaceFile(_ context.Context, p string, s store.FileState, c []store.NewChunk) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.files[p], f.rows[p] = s, c
	return nil
}
func (f *fakeStore) DeleteFile(_ context.Context, p string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.files, p)
	delete(f.rows, p)
	return nil
}
func (f *fakeStore) Paths(context.Context) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for p := range f.files {
		out = append(out, p)
	}
	return out, nil
}
func (f *fakeStore) CachedVectors(_ context.Context, hashes [][]byte) (map[string][]float32, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[string][]float32{}
	want := map[string]bool{}
	for _, h := range hashes {
		want[string(h)] = true
	}
	for _, rows := range f.rows {
		for _, r := range rows {
			if want[string(r.Hash)] && r.Vec != nil {
				out[string(r.Hash)] = r.Vec
			}
		}
	}
	return out, nil
}
func (f *fakeStore) CompactIfWasteful(context.Context) (bool, error) { return false, nil }

type fakeEmb struct {
	mu    sync.Mutex
	texts []string
}

func (e *fakeEmb) Embed(_ context.Context, in []string) ([][]float32, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([][]float32, len(in))
	for i, s := range in {
		e.texts = append(e.texts, s)
		out[i] = []float32{float32(len(s)), 1}
	}
	return out, nil
}

func setup(t *testing.T, cfgMod func(*config.Config)) (*Indexer, *fakeStore, *fakeEmb, string) {
	t.Helper()
	root := t.TempDir()
	cfg := config.Config{
		Roots: []string{root}, RespectGitignore: true, MaxFileBytes: 1 << 20, MaxDataBytes: 64 << 10,
		MaxLineBytes: 4000, MaxChunksPerFile: 300, ChunkSize: 200, ChunkOverlap: 20, EmbedBatch: 4,
	}
	if cfgMod != nil {
		cfgMod(&cfg)
	}
	m := crawl.NewMatcher(crawl.Options{
		Roots: cfg.Roots, RespectGitignore: cfg.RespectGitignore,
		ExactFiles: []string{filepath.Join(root, "index.db")},
	})
	st, em := newFake(), &fakeEmb{}
	ix := New(st, em, nil, m, cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	return ix, st, em, root
}

func put(t *testing.T, root, rel, body string) string {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestScanSkipsJunkSecretsAndOwnDB(t *testing.T) {
	ix, st, _, root := setup(t, nil)
	put(t, root, "src/main.go", "package main\n")
	put(t, root, "node_modules/x/index.js", "x = 1\n")
	put(t, root, ".env", "KEY=1\n")
	put(t, root, "GITEA/custom/conf/app.ini", "[security]\nSECRET_KEY=abc\n")
	put(t, root, "index.db", "text pretending to be a db")
	put(t, root, "app.min.js", "a=1;\n")
	put(t, root, ".gitignore", "ignored.txt\n")
	put(t, root, "ignored.txt", "hello\n")
	put(t, root, "bundle.js", strings.Repeat("a=1;", 3000))

	if _, err := ix.Scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(st.files) != 1 { // only main.go; .gitignore has no text extractor
		var got []string
		for p := range st.files {
			got = append(got, p)
		}
		t.Fatalf("indexed %v", got)
	}
}

func TestEmbeddingCacheDedupeAndIncrementalEdit(t *testing.T) {
	ix, st, em, root := setup(t, func(c *config.Config) { c.ChunkSize = 100; c.ChunkOverlap = 0 })
	block := strings.Repeat("alpha beta gamma ", 6)[:100]
	other := strings.Repeat("delta epsilon zeta ", 6)[:100]
	a := put(t, root, "a.txt", block+other)
	ctx := context.Background()
	if err := ix.IndexFile(ctx, a); err != nil {
		t.Fatal(err)
	}
	first := len(em.texts)
	if first == 0 {
		t.Fatal("nothing embedded")
	}
	// Copy with identical text: nothing new to embed.
	put(t, root, "b.txt", block+other)
	if err := ix.IndexFile(ctx, filepath.Join(root, "b.txt")); err != nil {
		t.Fatal(err)
	}
	if len(em.texts) != first {
		t.Fatalf("identical file re-embedded %d chunks", len(em.texts)-first)
	}
	for _, r := range st.rows[filepath.Join(root, "b.txt")] {
		if r.Vec == nil {
			t.Fatal("reused chunk lost its vector")
		}
	}
	// Repeated chunks in one file are embedded once.
	put(t, root, "c.txt", strings.Repeat(block, 5))
	before := len(em.texts)
	if err := ix.IndexFile(ctx, filepath.Join(root, "c.txt")); err != nil {
		t.Fatal(err)
	}
	rows := st.rows[filepath.Join(root, "c.txt")]
	distinct := map[string]bool{}
	for _, r := range rows {
		distinct[r.Text] = true
	}
	if got := len(em.texts) - before; got > len(distinct) {
		t.Fatalf("embedded %d chunks but only %d are distinct", got, len(distinct))
	}
}

func TestChunkCapAndOversize(t *testing.T) {
	ix, st, _, root := setup(t, func(c *config.Config) { c.MaxChunksPerFile = 3; c.MaxDataBytes = 100 })
	big := put(t, root, "big.txt", strings.Repeat("word ", 2000))
	data := put(t, root, "d.json", `{"k":"`+strings.Repeat("v", 500)+`"}`)
	ctx := context.Background()
	if err := ix.IndexFile(ctx, big); err != nil {
		t.Fatal(err)
	}
	if n := len(st.rows[big]); n != 3 {
		t.Fatalf("want 3 chunks, got %d", n)
	}
	if err := ix.IndexFile(ctx, data); err != nil {
		t.Fatal(err)
	}
	if _, ok := st.files[data]; ok {
		t.Fatal("oversized data file indexed")
	}
}

func TestPruneNewlyIgnoredAndDeleted(t *testing.T) {
	ix, st, _, root := setup(t, nil)
	keep := put(t, root, "keep.txt", "keep me\n")
	gone := put(t, root, "gone.txt", "bye\n")
	ctx := context.Background()
	if _, err := ix.Scan(ctx); err != nil {
		t.Fatal(err)
	}
	// A previously indexed path that is now ignored, and one deleted from disk.
	secret := filepath.Join(root, ".env")
	st.files[secret] = store.FileState{}
	os.Remove(gone)
	if _, err := ix.Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if _, ok := st.files[keep]; !ok {
		t.Fatal("kept file pruned")
	}
	if _, ok := st.files[secret]; ok {
		t.Fatal("ignored entry not pruned")
	}
	if _, ok := st.files[gone]; ok {
		t.Fatal("deleted file not pruned")
	}
}

func TestNilEmbedderStillIndexes(t *testing.T) {
	ix, st, _, root := setup(t, nil)
	ix.emb = nil
	p := put(t, root, "n.txt", "keyword only\n")
	if err := ix.IndexFile(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	if len(st.rows[p]) == 0 || st.rows[p][0].Vec != nil {
		t.Fatal("expected keyword-only rows")
	}
}
