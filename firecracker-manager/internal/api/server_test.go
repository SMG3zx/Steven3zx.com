package api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"firecracker-manager/internal/vm"
)

func newTestServer(t *testing.T) (*httptest.Server, *vm.Manager) {
	t.Helper()
	mgr, err := vm.NewManager(vm.Options{
		DataDir:        t.TempDir(),
		FirecrackerBin: filepath.Join(t.TempDir(), "no-such-firecracker"),
	})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(NewHandler(mgr, "no-such-firecracker", "", t.TempDir()))
	t.Cleanup(srv.Close)
	return srv, mgr
}

func testSpecBody(t *testing.T, name string) []byte {
	t.Helper()
	dir := t.TempDir()
	kernel := filepath.Join(dir, "vmlinux")
	rootfs := filepath.Join(dir, "rootfs.ext4")
	for _, p := range []string{kernel, rootfs} {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	body, _ := json.Marshal(map[string]any{
		"name":       name,
		"kernelPath": kernel,
		"rootfsPath": rootfs,
		"vcpus":      1,
		"memoryMiB":  256,
	})
	return body
}

func do(t *testing.T, method, url string, body []byte) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, url, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func TestCreateListGetDelete(t *testing.T) {
	srv, _ := newTestServer(t)

	resp := do(t, http.MethodPost, srv.URL+"/api/vms", testSpecBody(t, "api-vm"))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d", resp.StatusCode)
	}
	var created vm.VM
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}

	resp = do(t, http.MethodGet, srv.URL+"/api/vms", nil)
	var list []vm.VM
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != created.ID {
		t.Fatalf("list = %+v, want the created VM", list)
	}

	resp = do(t, http.MethodGet, srv.URL+"/api/vms/"+created.ID, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get status = %d", resp.StatusCode)
	}

	resp = do(t, http.MethodDelete, srv.URL+"/api/vms/"+created.ID, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete status = %d", resp.StatusCode)
	}
}

func TestErrorStatusCodes(t *testing.T) {
	srv, _ := newTestServer(t)

	if resp := do(t, http.MethodGet, srv.URL+"/api/vms/missing", nil); resp.StatusCode != http.StatusNotFound {
		t.Errorf("unknown VM: status = %d, want 404", resp.StatusCode)
	}
	if resp := do(t, http.MethodPost, srv.URL+"/api/vms", []byte(`{"name":"x","bogus":1}`)); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("unknown field: status = %d, want 400", resp.StatusCode)
	}
	if resp := do(t, http.MethodPost, srv.URL+"/api/vms", []byte(`{"name":"Bad Name"}`)); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("invalid spec: status = %d, want 400", resp.StatusCode)
	}
	if resp := do(t, http.MethodGet, srv.URL+"/api/nothing", nil); resp.StatusCode != http.StatusNotFound {
		t.Errorf("unknown API path: status = %d, want 404", resp.StatusCode)
	}
}

func TestStartWithMissingBinaryReportsError(t *testing.T) {
	srv, _ := newTestServer(t)

	resp := do(t, http.MethodPost, srv.URL+"/api/vms", testSpecBody(t, "doomed"))
	var created vm.VM
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}

	resp = do(t, http.MethodPost, srv.URL+"/api/vms/"+created.ID+"/start", nil)
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("start status = %d, want 500", resp.StatusCode)
	}
	var body map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body["error"], "start firecracker") {
		t.Errorf("error = %q, want it to mention starting firecracker", body["error"])
	}
}

func TestConsoleStreamReplaysLog(t *testing.T) {
	srv, mgr := newTestServer(t)

	resp := do(t, http.MethodPost, srv.URL+"/api/vms", testSpecBody(t, "console-vm"))
	var created vm.VM
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	logPath, err := mgr.ConsolePath(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(logPath, []byte("Linux version 6.1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/api/vms/"+created.ID+"/console", nil)
	stream, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Body.Close()

	if ct := stream.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content type = %q", ct)
	}
	scanner := bufio.NewScanner(stream.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data: ") {
			want := `data: "Linux version 6.1\n"`
			if line != want {
				t.Fatalf("event = %q, want %q", line, want)
			}
			return
		}
	}
	t.Fatalf("no event received: %v", scanner.Err())
}

func TestImagesEndpointListsCatalog(t *testing.T) {
	imagesDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(imagesDir, "kernels"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(imagesDir, "kernels", "vmlinux-6.1"), []byte("k"), 0o644); err != nil {
		t.Fatal(err)
	}
	mgr, err := vm.NewManager(vm.Options{DataDir: t.TempDir(), FirecrackerBin: "no-such-firecracker"})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(NewHandler(mgr, "no-such-firecracker", "", imagesDir))
	t.Cleanup(srv.Close)

	resp := do(t, http.MethodGet, srv.URL+"/api/images", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var catalog struct {
		Kernels  []struct{ Path string } `json:"kernels"`
		RootFS   []struct{ Path string } `json:"rootfs"`
		BootArgs []string                `json:"bootArgs"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&catalog); err != nil {
		t.Fatal(err)
	}
	if len(catalog.Kernels) != 1 || !strings.HasSuffix(catalog.Kernels[0].Path, "vmlinux-6.1") {
		t.Errorf("kernels = %+v", catalog.Kernels)
	}
	if len(catalog.RootFS) != 0 {
		t.Errorf("rootfs = %+v, want none", catalog.RootFS)
	}
	if len(catalog.BootArgs) == 0 {
		t.Error("expected boot arg presets")
	}
}
