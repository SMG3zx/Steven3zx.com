package manifest

import "testing"

func TestStoredMatchRequiresEveryStoredFileToMatchTheCommit(t *testing.T) {
	entries := []Entry{{Path: "a.go", Hash: "h-a"}, {Path: "b.go", Hash: "h-b"}}
	if !StoredMatch(map[string]string{}, entries) {
		t.Fatal("empty staging should match")
	}
	if !StoredMatch(map[string]string{"a.go": "h-a"}, entries) {
		t.Fatal("matching stored file should match")
	}
	if StoredMatch(map[string]string{"a.go": "other"}, entries) {
		t.Fatal("hash mismatch should not match")
	}
	if StoredMatch(map[string]string{"gone.go": "h-a"}, entries) {
		t.Fatal("file not in the commit should not match")
	}
}

func TestSameComparesPathsAndHashesNotModesOrSizes(t *testing.T) {
	a := []Entry{{Path: "a.go", Hash: "h-a", Mode: "100644", Size: 3}, {Path: "b.go", Hash: "h-b"}}
	b := []Entry{{Path: "b.go", Hash: "h-b", Mode: "100755"}, {Path: "a.go", Hash: "h-a", Size: 9}}
	if !Same(a, b) {
		t.Fatal("same paths and hashes should match whatever the mode or size")
	}
	if Same(a, b[:1]) {
		t.Fatal("different counts should not match")
	}
	if Same(a, []Entry{{Path: "a.go", Hash: "h-a"}, {Path: "c.go", Hash: "h-b"}}) {
		t.Fatal("a different path should not match")
	}
	if Same(a, []Entry{{Path: "a.go", Hash: "other"}, {Path: "b.go", Hash: "h-b"}}) {
		t.Fatal("a different hash should not match")
	}
}

func TestParseReportsAnEmptyOrBrokenManifestAsNotOK(t *testing.T) {
	if _, ok := Parse(""); ok {
		t.Fatal("empty manifest should not be ok")
	}
	if _, ok := Parse("{not json"); ok {
		t.Fatal("broken manifest should not be ok")
	}
	entries, ok := Parse(`[{"path":"a.go","mode":"100644","hash":"h-a","size":3}]`)
	if !ok || len(entries) != 1 || entries[0].Path != "a.go" || entries[0].Size != 3 {
		t.Fatalf("Parse = %v, %v", entries, ok)
	}
}
