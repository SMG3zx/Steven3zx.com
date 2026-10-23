// Package search runs hybrid retrieval: FTS5 keyword matches and brute-force
// cosine similarity over embeddings, fused with Reciprocal Rank Fusion.
package search

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"sort"
	"strings"
	"sync"

	"spotlight/internal/embed"
	"spotlight/internal/store"
)

const (
	candidates = 50 // results taken from each retriever before fusion
	rrfK       = 60.0
	snippetLen = 280
)

type Result struct {
	Path    string   `json:"path"`
	Offset  int      `json:"offset"` // rune offset of the matched chunk in the file
	Snippet string   `json:"snippet"`
	Score   float64  `json:"score"`
	Sources []string `json:"sources"` // "keyword", "vector", or both
}

type Searcher struct {
	st  *store.Store
	emb embed.Embedder // nil disables vector search
	log *slog.Logger

	mu    sync.Mutex
	gen   uint64
	valid bool
	ids   []int64
	vecs  [][]float32 // L2-normalised, cached until the index changes
}

func New(st *store.Store, emb embed.Embedder, log *slog.Logger) *Searcher {
	return &Searcher{st: st, emb: emb, log: log}
}

// Search returns up to limit results for q, best first.
func (s *Searcher) Search(ctx context.Context, q string, limit int) ([]Result, error) {
	var lists [][]store.Hit
	var names []string

	if fq := ftsQuery(q); fq != "" {
		kw, err := s.st.KeywordSearch(ctx, fq, candidates)
		if err != nil {
			return nil, fmt.Errorf("keyword search: %w", err)
		}
		lists = append(lists, kw)
		names = append(names, "keyword")
	}
	if s.emb != nil {
		vec, err := s.vectorSearch(ctx, q)
		if err != nil {
			s.log.Warn("vector search unavailable; returning keyword results only", "err", err)
		} else {
			lists = append(lists, vec)
			names = append(names, "vector")
		}
	}

	type entry struct {
		hit     store.Hit
		score   float64
		sources []string
	}
	fused := make(map[int64]*entry)
	for li, list := range lists {
		for rank, h := range list {
			e, ok := fused[h.ID]
			if !ok {
				e = &entry{hit: h}
				fused[h.ID] = e
			}
			e.score += 1.0 / (rrfK + float64(rank+1))
			e.sources = append(e.sources, names[li])
		}
	}

	out := make([]Result, 0, len(fused))
	for _, e := range fused {
		out = append(out, Result{
			Path:    e.hit.Path,
			Offset:  e.hit.Offset,
			Snippet: snippet(e.hit.Text, snippetLen),
			Score:   e.score,
			Sources: e.sources,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// vectorSearch returns the most similar chunks to q, best first.
func (s *Searcher) vectorSearch(ctx context.Context, q string) ([]store.Hit, error) {
	vecs, err := s.emb.Embed(ctx, []string{q})
	if err != nil {
		return nil, err
	}
	if len(vecs) != 1 || len(vecs[0]) == 0 {
		return nil, fmt.Errorf("empty query embedding")
	}
	qv := normalize(vecs[0])

	ids, mat, err := s.loadIndex(ctx)
	if err != nil {
		return nil, err
	}

	type scored struct {
		id    int64
		score float32
	}
	scores := make([]scored, 0, len(ids))
	for i, v := range mat {
		if len(v) != len(qv) {
			continue // embedded with a different model
		}
		scores = append(scores, scored{ids[i], dot(qv, v)})
	}
	sort.Slice(scores, func(i, j int) bool { return scores[i].score > scores[j].score })
	if len(scores) > candidates {
		scores = scores[:candidates]
	}

	top := make([]int64, len(scores))
	for i, sc := range scores {
		top[i] = sc.id
	}
	byID, err := s.st.ChunksByID(ctx, top)
	if err != nil {
		return nil, err
	}
	out := make([]store.Hit, 0, len(top))
	for _, id := range top {
		if h, ok := byID[id]; ok {
			out = append(out, h)
		}
	}
	return out, nil
}

// loadIndex returns cached normalised vectors, reloading after any write.
// The generation is read before the query, so a concurrent write only causes
// an extra reload later, never a stale cache that is kept.
func (s *Searcher) loadIndex(ctx context.Context) ([]int64, [][]float32, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	gen := s.st.Generation()
	if s.valid && s.gen == gen {
		return s.ids, s.vecs, nil
	}
	rows, err := s.st.Embeddings(ctx)
	if err != nil {
		return nil, nil, err
	}
	ids := make([]int64, 0, len(rows))
	vecs := make([][]float32, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.ID)
		vecs = append(vecs, normalize(r.Vec))
	}
	s.ids, s.vecs, s.gen, s.valid = ids, vecs, gen, true
	return ids, vecs, nil
}

// ftsQuery turns free text into an FTS5 expression: each term is quoted (so
// punctuation can't break the syntax) and terms are OR-ed, letting bm25
// rank documents that contain more of them higher.
func ftsQuery(q string) string {
	terms := strings.Fields(q)
	parts := make([]string, 0, len(terms))
	for _, t := range terms {
		t = strings.ReplaceAll(t, `"`, `""`)
		parts = append(parts, `"`+t+`"`)
	}
	return strings.Join(parts, " OR ")
}

func normalize(v []float32) []float32 {
	var sum float64
	for _, x := range v {
		sum += float64(x) * float64(x)
	}
	out := make([]float32, len(v))
	if sum == 0 {
		return out
	}
	inv := float32(1 / math.Sqrt(sum))
	for i, x := range v {
		out[i] = x * inv
	}
	return out
}

func dot(a, b []float32) float32 {
	var s float32
	for i := range a {
		s += a[i] * b[i]
	}
	return s
}

func snippet(text string, max int) string {
	s := strings.Join(strings.Fields(text), " ")
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}
