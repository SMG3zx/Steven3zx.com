package wasm

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tochemey/goakt/v4/actor"
	golog "github.com/tochemey/goakt/v4/log"
	"janus/actors/persistence"
)

type fakeRunner struct{ calls int }

func (r *fakeRunner) Build(_ context.Context, workspace, target string) (string, []byte, []byte, error) {
	r.calls++
	if target != "wasm32-unknown-unknown" {
		return "", nil, nil, os.ErrInvalid
	}
	if _, err := os.Stat(filepath.Join(workspace, "Cargo.toml")); err != nil {
		return "", nil, nil, err
	}
	return "wasm-artifacts.zip", []byte("zip fixture"), []byte("Finished release build"), nil
}

func TestBuildActorExportsSnapshotBuildsAndReturnsOwnerScopedArtifact(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	system, err := actor.NewActorSystem("wasm-build-test", actor.WithLogger(golog.DiscardLogger))
	if err != nil {
		t.Fatal(err)
	}
	if err = system.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		stop, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer stopCancel()
		_ = system.Stop(stop)
	})
	dbPath := filepath.Join(t.TempDir(), "wasm.db")
	dbPID, err := system.Spawn(ctx, "persistence", persistence.New(dbPath, strings.Repeat("s", 32)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		closeCtx, closeCancel := context.WithTimeout(context.Background(), time.Second)
		defer closeCancel()
		_, _ = actor.Ask(closeCtx, dbPID, &persistence.CloseDatabase{}, time.Second)
	})
	ask := func(message any) any {
		t.Helper()
		response, askErr := actor.Ask(ctx, dbPID, message, 5*time.Second)
		if askErr != nil {
			t.Fatal(askErr)
		}
		return response
	}
	if got := ask(&persistence.CreateLocalUser{ID: "owner", Email: "owner@example.com", PasswordHash: "hash"}).(*persistence.LocalUserCreated); got.Err != nil {
		t.Fatal(got.Err)
	}
	if got := ask(&persistence.CreateProject{ID: "project", OwnerID: "owner", Name: "Rust WASM"}).(*persistence.ProjectCreated); got.Err != nil {
		t.Fatal(got.Err)
	}
	if got := ask(&persistence.CreateImportJob{ID: "import", OwnerID: "owner", ProjectID: "project", Repository: "owner/rust", Branch: "main", Commit: strings.Repeat("a", 40)}).(*persistence.ImportJobCreated); got.Err != nil {
		t.Fatal(got.Err)
	}
	if got := ask(&persistence.BeginSnapshot{ID: "snapshot", JobID: "import", OwnerID: "owner", ProjectID: "project", Repository: "owner/rust", Branch: "main", Commit: strings.Repeat("a", 40), MetadataJSON: "{}", Language: "Rust"}).(*persistence.BeginSnapshotResult); got.Err != nil {
		t.Fatal(got.Err)
	}
	if got := ask(&persistence.AddSnapshotFiles{SnapshotID: "snapshot", Files: []persistence.SnapshotFile{
		{Path: "Cargo.toml", Content: []byte("[package]\nname='fixture'\nversion='0.1.0'\nedition='2021'\n"), Mode: "100644"},
		{Path: "src/lib.rs", Content: []byte("#![no_std]\n"), Mode: "100644"},
	}}).(*persistence.SnapshotFilesAdded); got.Err != nil {
		t.Fatal(got.Err)
	}
	for _, status := range []string{"cloning", "scanning", "storing"} {
		ask(&persistence.UpdateImportJob{ID: "import", Status: status, Progress: 50})
	}
	if got := ask(&persistence.CompleteSnapshot{SnapshotID: "snapshot", JobID: "import", FileCount: 2, TotalBytes: 70}).(*persistence.SnapshotCompleted); got.Err != nil {
		t.Fatal(got.Err)
	}
	runner := &fakeRunner{}
	buildPID, err := system.Spawn(ctx, "wasm", NewWithRunner(dbPID, nil, runner))
	if err != nil {
		t.Fatal(err)
	}
	response, err := actor.Ask(ctx, buildPID, &RequestBuild{OwnerID: "owner", ProjectID: "project", Target: "wasm32-unknown-unknown"}, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	queued := response.(*BuildRequested)
	if queued.Err != nil || queued.JobID == "" {
		t.Fatalf("build request = %#v: %v", queued, queued.Err)
	}
	var result *persistence.WASMBuildFound
	for {
		response, err = actor.Ask(ctx, buildPID, &Build{OwnerID: "owner", JobID: queued.JobID}, 5*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		result = response.(*BuildFound).BuildFound()
		if result.Build.Status == "complete" || result.Build.Status == "failed" {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatalf("build did not finish: %#v", result.Build)
		case <-time.After(10 * time.Millisecond):
		}
	}
	if result.Err != nil || result.Build.Status != "complete" || result.Build.SnapshotID != "snapshot" || runner.calls != 1 {
		t.Fatalf("build result = %#v, runner calls=%d", result, runner.calls)
	}
	response, err = actor.Ask(ctx, dbPID, &persistence.FindWASMBuildArtifact{ID: queued.JobID, OwnerID: "owner"}, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	artifact := response.(*persistence.WASMBuildArtifact)
	if artifact.Err != nil || !artifact.Found || artifact.Name != "wasm-artifacts.zip" || string(artifact.Data) != "zip fixture" {
		t.Fatalf("artifact result = %#v", artifact)
	}
	response, err = actor.Ask(ctx, dbPID, &persistence.FindWASMBuildArtifact{ID: queued.JobID, OwnerID: "other"}, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if response.(*persistence.WASMBuildArtifact).Found {
		t.Fatal("another owner could retrieve the build artifact")
	}
}

func (f *BuildFound) BuildFound() *persistence.WASMBuildFound {
	return &persistence.WASMBuildFound{Build: f.Build, Found: f.Found, Err: f.Err}
}

func TestWriteSourceFileRejectsTraversal(t *testing.T) {
	for _, name := range []string{"../escape", "src/../../escape", `C:\outside`, "src\\..\\escape", "/absolute"} {
		t.Run(name, func(t *testing.T) {
			if err := writeSourceFile(t.TempDir(), persistence.SnapshotFile{Path: name}); err == nil {
				t.Fatalf("accepted unsafe source path %q", name)
			}
		})
	}
}

func TestDockerRunnerRequiresConfiguredTrustedImageAndKnownTarget(t *testing.T) {
	runner := dockerRunner{}
	if _, _, _, err := runner.Build(context.Background(), t.TempDir(), "wasm32-unknown-unknown"); err == nil || !strings.Contains(err.Error(), "JANUS_WASM_BUILDER_IMAGE") {
		t.Fatalf("unconfigured builder image error = %v", err)
	}
	runner.image = "rust:stable; malicious"
	if _, _, _, err := runner.Build(context.Background(), t.TempDir(), "wasm32-unknown-unknown"); err == nil || !strings.Contains(err.Error(), "trusted") {
		t.Fatalf("invalid builder image error = %v", err)
	}
	runner.image = "rust:stable"
	if _, _, _, err := runner.Build(context.Background(), t.TempDir(), "--privileged"); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("invalid target error = %v", err)
	}
}

func TestDockerRunnerCompilesRustWASM(t *testing.T) {
	image := os.Getenv("JANUS_WASM_BUILDER_IMAGE")
	if image == "" {
		t.Skip("set JANUS_WASM_BUILDER_IMAGE to run the Docker integration test")
	}
	workspace := t.TempDir()
	for name, content := range map[string]string{
		"Cargo.toml": "[package]\nname = \"wasm_fixture\"\nversion = \"0.1.0\"\nedition = \"2021\"\n[lib]\ncrate-type = [\"cdylib\"]\n",
		"Cargo.lock": "version = 4\n\n[[package]]\nname = \"wasm_fixture\"\nversion = \"0.1.0\"\n",
		"src/lib.rs": "#![no_std]\n#[panic_handler]\nfn panic(_: &core::panic::PanicInfo) -> ! { loop {} }\n#[no_mangle]\npub extern \"C\" fn sum(a: i32, b: i32) -> i32 { a + b }\n",
	} {
		fullPath := filepath.Join(workspace, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(fullPath), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(fullPath, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	name, archive, log, err := (dockerRunner{image: image}).Build(context.Background(), workspace, "wasm32-unknown-unknown")
	if err != nil {
		t.Fatalf("Docker Rust WASM build failed: %v\n%s", err, log)
	}
	if name != "wasm-artifacts.zip" {
		t.Fatalf("artifact name=%q", name)
	}
	reader, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		t.Fatal(err)
	}
	if len(reader.File) == 0 {
		t.Fatalf("archive entries=%v", reader.File)
	}
	foundModule := false
	for _, file := range reader.File {
		if strings.HasSuffix(file.Name, "wasm_fixture.wasm") {
			foundModule = true
		}
	}
	if !foundModule {
		t.Fatalf("compiled crate WASM module missing from archive: %v", reader.File)
	}
}

func TestZipWASMArtifactsBundlesOutputs(t *testing.T) {
	root := filepath.Join(t.TempDir(), "target", "release")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{"app.wasm": "module", "nested/worker.wasm": "worker", "not-wasm.txt": "ignored"} {
		fullPath := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(fullPath), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(fullPath, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	bundle, err := zipWASMArtifacts(root)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := zip.NewReader(bytes.NewReader(bundle), int64(len(bundle)))
	if err != nil {
		t.Fatal(err)
	}
	if len(reader.File) != 2 {
		t.Fatalf("archive file count=%d; want 2", len(reader.File))
	}
	file, err := reader.File[0].Open()
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(file)
	_ = file.Close()
	if err != nil || !bytes.Contains(data, []byte("module")) {
		t.Fatalf("archive content=%q, err=%v", data, err)
	}
	if _, err = zipWASMArtifacts(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("missing output directory should fail")
	}
	empty := t.TempDir()
	if _, err = zipWASMArtifacts(empty); err == nil || !strings.Contains(err.Error(), "without producing") {
		t.Fatalf("empty output directory error=%v", err)
	}
}

func TestLimitedBufferKeepsOnlyConfiguredBytes(t *testing.T) {
	buffer := &limitedBuffer{limit: 4}
	if written, err := buffer.Write([]byte("abcdefgh")); err != nil || written != 8 {
		t.Fatalf("limited writer returned written=%d err=%v", written, err)
	}
	if string(buffer.Bytes()) != "abcd" {
		t.Fatalf("bounded output=%q; want %q", buffer.Bytes(), "abcd")
	}
}

func TestWASMArtifactTarRejectsUnsafePath(t *testing.T) {
	var buffer bytes.Buffer
	writer := tar.NewWriter(&buffer)
	if err := writer.WriteHeader(&tar.Header{Name: "../escape.wasm", Mode: 0600, Size: 3, Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write([]byte("was")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := zipWASMArtifactsFromTar(buffer.Bytes()); err == nil || !strings.Contains(err.Error(), "unsafe path") {
		t.Fatalf("unsafe artifact path error=%v", err)
	}
}
