package scan

import "testing"

func TestDominantLanguagePicksTheMostFrequentLanguage(t *testing.T) {
	cases := []struct {
		name      string
		languages map[string]int
		want      string
	}{
		{"empty", map[string]int{}, ""},
		{"single", map[string]int{"Go": 2}, "Go"},
		{"most files wins", map[string]int{"Go": 2, "Rust": 5}, "Rust"},
		{"tie goes to the first name", map[string]int{"Rust": 3, "Go": 3}, "Go"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := DominantLanguage(c.languages); got != c.want {
				t.Fatalf("DominantLanguage(%v) = %q, want %q", c.languages, got, c.want)
			}
		})
	}
}

func TestAcceptsEntrypointAllowsNoneOrADetectedCandidate(t *testing.T) {
	result := Result{Candidates: []Candidate{{Path: "cmd/server/main.go"}}}
	if !result.AcceptsEntrypoint("") {
		t.Fatal("no entrypoint should be accepted")
	}
	if !result.AcceptsEntrypoint("cmd/server/main.go") {
		t.Fatal("a detected candidate should be accepted")
	}
	if result.AcceptsEntrypoint("other.go") {
		t.Fatal("a path that is not a candidate should be rejected")
	}
}
