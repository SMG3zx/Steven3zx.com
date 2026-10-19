package artifact

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"context"
	"strings"
	"testing"
)

func tarOf(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := tar.NewWriter(&buffer)
	for name, content := range files {
		if err := writer.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: int64(len(content)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func TestZipFromTarKeepsOnlyWasmModules(t *testing.T) {
	archive, err := ZipFromTar(tarOf(t, map[string]string{"./app.wasm": "module", "nested/worker.wasm": "worker", "notes.txt": "ignored"}))
	if err != nil {
		t.Fatal(err)
	}
	reader, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		t.Fatal(err)
	}
	if len(reader.File) != 2 {
		t.Fatalf("archive holds %d entries, want 2", len(reader.File))
	}
}

func TestZipFromTarRejectsUnsafeAndEmptyOutput(t *testing.T) {
	if _, err := ZipFromTar(tarOf(t, map[string]string{"../escape.wasm": "was"})); err == nil || !strings.Contains(err.Error(), "unsafe path") {
		t.Fatalf("unsafe path error = %v", err)
	}
	if _, err := ZipFromTar(tarOf(t, map[string]string{"notes.txt": "x"})); err == nil || !strings.Contains(err.Error(), "without producing") {
		t.Fatalf("empty output error = %v", err)
	}
}

func TestCompressedArtifactYieldsItsFirstModule(t *testing.T) {
	archive, err := ZipFromTar(tarOf(t, map[string]string{"b.wasm": "second", "a.wasm": "first"}))
	if err != nil {
		t.Fatal(err)
	}
	compressed, err := Compress(context.Background(), archive)
	if err != nil {
		t.Fatal(err)
	}
	module, name, err := Module(compressed.Data, "wasm-artifacts.zip"+ZstdExtension)
	if err != nil || string(module) != "first" || name != "a.wasm" {
		t.Fatalf("module = %q %q, %v", module, name, err)
	}
}

func TestSafePath(t *testing.T) {
	for _, name := range []string{"../escape", "src/../../escape", `C:\outside`, "src\\x", "/absolute", "."} {
		if SafePath(name) {
			t.Errorf("SafePath(%q) = true", name)
		}
	}
	if !SafePath("target/release/app.wasm") {
		t.Error("SafePath rejected a plain relative path")
	}
}
