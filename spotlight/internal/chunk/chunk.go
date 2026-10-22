// Package chunk splits text into overlapping windows for embedding and search.
package chunk

import (
	"strings"
	"unicode"
)

type Chunk struct {
	Offset int // rune offset of the chunk's first character in the source
	Text   string
}

// Split cuts text into windows of about size runes with overlap runes shared
// between neighbours. Windows end on a paragraph or word boundary when one is
// close enough.
func Split(text string, size, overlap int) []Chunk {
	if size <= 0 {
		size = 1200
	}
	if overlap < 0 || overlap >= size {
		overlap = size / 6
	}
	r := []rune(text)
	var out []Chunk
	for start := 0; start < len(r); {
		end := min(start+size, len(r))
		if end < len(r) {
			end = boundary(r, start, end)
		}
		if t := strings.TrimSpace(string(r[start:end])); t != "" {
			out = append(out, Chunk{Offset: start, Text: t})
		}
		if end >= len(r) {
			break
		}
		next := end - overlap
		if next <= start {
			next = end
		}
		start = next
	}
	return out
}

// boundary looks back from end (no further than the last quarter of the
// window) for a paragraph break, then for any whitespace.
func boundary(r []rune, start, end int) int {
	floor := start + (end-start)*3/4
	for i := end; i > floor; i-- {
		if r[i-1] == '\n' && i >= 2 && r[i-2] == '\n' {
			return i
		}
	}
	for i := end; i > floor; i-- {
		if unicode.IsSpace(r[i-1]) {
			return i
		}
	}
	return end
}
