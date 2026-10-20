package app

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
			if got := dominantLanguage(c.languages); got != c.want {
				t.Fatalf("dominantLanguage(%v) = %q, want %q", c.languages, got, c.want)
			}
		})
	}
}

func TestStoredFilesMatchRequiresEveryStoredFileToMatchTheCommit(t *testing.T) {
	manifest := []manifestEntry{{Path: "a.go", Hash: "h-a"}, {Path: "b.go", Hash: "h-b"}}
	if !storedFilesMatch(map[string]string{}, manifest) {
		t.Fatal("empty staging should match")
	}
	if !storedFilesMatch(map[string]string{"a.go": "h-a"}, manifest) {
		t.Fatal("matching stored file should match")
	}
	if storedFilesMatch(map[string]string{"a.go": "other"}, manifest) {
		t.Fatal("hash mismatch should not match")
	}
	if storedFilesMatch(map[string]string{"gone.go": "h-a"}, manifest) {
		t.Fatal("file not in the commit should not match")
	}
}
