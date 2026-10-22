package chunk

import (
	"strings"
	"testing"
)

func TestSplitRespectsSizeAndCoversEnd(t *testing.T) {
	text := strings.Repeat("alpha beta gamma delta epsilon. ", 200)
	chunks := Split(text, 300, 50)
	if len(chunks) < 2 {
		t.Fatalf("expected multiple chunks, got %d", len(chunks))
	}
	for i, c := range chunks {
		if n := len([]rune(c.Text)); n > 300 {
			t.Fatalf("chunk %d has %d runes, want <= 300", i, n)
		}
	}
	last := chunks[len(chunks)-1].Text
	if !strings.HasSuffix(strings.TrimSpace(text), last) {
		t.Fatalf("last chunk %q does not end the text", last)
	}
}

func TestSplitEmpty(t *testing.T) {
	if got := Split("   \n  ", 100, 10); len(got) != 0 {
		t.Fatalf("expected no chunks for whitespace, got %d", len(got))
	}
}
