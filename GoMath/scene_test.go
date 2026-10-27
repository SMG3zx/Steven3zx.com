package main

import (
	"testing"
	"unicode/utf8"
)

// TestParseScenesAreRectangular checks that every line of every parse scene
// is the same width, so the frames' borders line up.
func TestParseScenesAreRectangular(t *testing.T) {
	for _, input := range []string{"3 + 3", "1 + 2 * 3", "a + b * c * d + e", "(x + 1)(x + 2)"} {
		rep := Solve(input, OpSimplify)
		if rep.Err != nil {
			t.Fatalf("Solve(%q) returned error: %v", input, rep.Err)
		}
		for i, ev := range rep.Scenes {
			lines := splitLines(renderParseScene(ev, 70))
			want := utf8.RuneCountInString(lines[0])
			for _, line := range lines {
				if got := utf8.RuneCountInString(line); got != want {
					t.Errorf("%q step %d: line width %d, want %d:\n%s", input, i, got, want, renderParseScene(ev, 70))
					break
				}
			}
		}
	}
}

// TestFrameLinesLineUp checks each frame on its own, since an outer frame
// pads its contents and would hide a narrow inner frame's misaligned border.
func TestFrameLinesLineUp(t *testing.T) {
	for _, body := range [][]string{{"ab"}, {"3"}, {"a long line of body text"}} {
		lines := frame(body, parseCallTitle)
		want := utf8.RuneCountInString(lines[0])
		for _, line := range lines {
			if got := utf8.RuneCountInString(line); got != want {
				t.Errorf("frame of %q: line %q is %d wide, want %d", body, line, got, want)
			}
		}
	}
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i, r := range s {
		if r == '\n' {
			lines = append(lines, s[start:i])
			start = i + len("\n")
		}
		_ = r
	}
	return append(lines, s[start:])
}
