package extract

import (
	"errors"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestKindOf(t *testing.T) {
	cases := map[string]Kind{
		"a/main.go":        KindText,
		"a/go.mod":         KindText,
		"app.MJS":          KindText,
		"Dockerfile":       KindText,
		"sub/Caddyfile":    KindText,
		"LICENSE":          KindText,
		"notes.md":         KindText,
		"data.json":        KindData,
		"server.log":       KindData,
		"board.excalidraw": KindExcalidraw,
		"photo.png":        KindNone,
		"doc.pdf":          KindNone,
		"archive.zip":      KindNone,
		".gitignore":       KindNone,
		"somefile":         KindNone,
	}
	for path, want := range cases {
		if got := KindOf(path); got != want {
			t.Errorf("KindOf(%q) = %v, want %v", path, got, want)
		}
	}
}

func TestTextRejectsBinaryAndUnsupported(t *testing.T) {
	if _, err := Text("a.png", []byte("x"), 0); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("png: got %v", err)
	}
	if _, err := Text("a.go", []byte("pack\x00age"), 0); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("NUL byte should mean binary, got %v", err)
	}
}

func TestTextRejectsMinifiedButKeepsProse(t *testing.T) {
	minified := strings.Repeat("a=1;", 2000) // one 8000-byte line
	if _, err := Text("bundle.js", []byte(minified), 4000); !errors.Is(err, ErrGenerated) {
		t.Fatalf("minified js: got %v, want ErrGenerated", err)
	}
	if !errors.Is(ErrGenerated, ErrUnsupported) {
		t.Fatal("ErrGenerated must also satisfy ErrUnsupported so callers treat it as a skip")
	}
	if _, err := Text("one-line.json", []byte(minified), 4000); !errors.Is(err, ErrGenerated) {
		t.Fatalf("single-line json: got %v, want ErrGenerated", err)
	}
	if _, err := Text("essay.md", []byte(minified), 4000); err != nil {
		t.Fatalf("a long prose line is fine: %v", err)
	}
	if _, err := Text("bundle.js", []byte(minified), 0); err != nil {
		t.Fatalf("maxLine 0 disables the check: %v", err)
	}
	normal := strings.Repeat("line of code;\n", 1000)
	if _, err := Text("ok.js", []byte(normal), 4000); err != nil {
		t.Fatalf("normal file: %v", err)
	}
}

func TestTextReplacesInvalidUTF8(t *testing.T) {
	got, err := Text("a.txt", []byte("ok \xff\xfe end"), 0)
	if err != nil {
		t.Fatal(err)
	}
	if !utf8.ValidString(got) || !strings.HasPrefix(got, "ok ") || !strings.HasSuffix(got, " end") {
		t.Fatalf("want valid UTF-8 with the surrounding text kept, got %q", got)
	}
}

func TestExcalidrawKeepsOnlyLiveTextLabels(t *testing.T) {
	doc := `{"type":"excalidraw","version":2,
	  "elements":[
	    {"type":"rectangle","id":"r1"},
	    {"type":"text","text":"Auth service"},
	    {"type":"text","text":"  "},
	    {"type":"text","text":"removed label","isDeleted":true},
	    {"type":"text","text":"Postgres"}],
	  "files":{"img1":{"dataURL":"data:image/png;base64,AAAA"}}}`
	got, err := Text("arch.excalidraw", []byte(doc), 4000)
	if err != nil {
		t.Fatal(err)
	}
	if got != "Auth service\n\nPostgres" {
		t.Fatalf("got %q", got)
	}
	if _, err := Text("empty.excalidraw", []byte(`{"elements":[{"type":"rectangle"}]}`), 0); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("a drawing with no text has nothing to index, got %v", err)
	}
	if _, err := Text("broken.excalidraw", []byte(`{not json`), 0); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("invalid JSON should be skipped, got %v", err)
	}
}
