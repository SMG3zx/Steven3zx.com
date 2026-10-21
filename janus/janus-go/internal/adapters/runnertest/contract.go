package runnertest

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"janus/internal/app"
)

// Run runs the Runner contract against adapters made by newRunner. Each case
// gets a fresh Runner.
func Run(t *testing.T, newRunner func(t *testing.T) app.Runner) {
	t.Helper()
	for _, c := range []struct {
		name string
		test func(t *testing.T, r app.Runner)
	}{
		{"CapturesStdout", testCapturesStdout},
		{"ReportsFuelAndPeakMemory", testReportsUsage},
		{"FuelIsRepeatable", testFuelIsRepeatable},
		{"StopsARunThatExhaustsItsFuel", testExhaustsFuel},
		{"RefusesBytesThatAreNotAModule", testRefusesGarbage},
		{"RefusesAModuleWithoutStart", testRefusesNoStart},
		{"ReportsRunningOutOfFuel", testOutOfFuelError},
		{"ReportsATimeout", testTimeoutError},
		{"PassesStdin", testPassesStdin},
		{"PassesTheEnvironment", testPassesEnvironment},
		{"CutsStdoutAtItsLimit", testCutsStdout},
		{"ReadsAFileInsideTheFolder", testReadsInsideFolder},
		{"CannotReachOutsideTheFolder", testCannotLeaveFolder},
		{"HasNoFilesWithoutAFolder", testNoFolderNoFiles},
	} {
		t.Run(c.name, func(t *testing.T) { c.test(t, newRunner(t)) })
	}
}

func limits() app.RunLimits {
	return app.RunLimits{Fuel: 1_000_000, MemoryBytes: 16 << 20, Timeout: 5 * time.Second}
}

func run(t *testing.T, r app.Runner, module []byte, l app.RunLimits) (app.RunResult, error) {
	t.Helper()
	return r.Run(context.Background(), module, app.RunInput{Limits: l})
}

func testCapturesStdout(t *testing.T, r app.Runner) {
	got, err := run(t, r, Hello, limits())
	if err != nil {
		t.Fatal(err)
	}
	if string(got.Stdout) != "hi\n" || len(got.Stderr) != 0 {
		t.Fatalf("stdout = %q, stderr = %q", got.Stdout, got.Stderr)
	}
}

func testReportsUsage(t *testing.T, r app.Runner) {
	got, err := run(t, r, Hello, limits())
	if err != nil {
		t.Fatal(err)
	}
	if got.Usage.Fuel == 0 || got.Usage.Fuel >= limits().Fuel {
		t.Errorf("fuel = %d, want between 1 and %d", got.Usage.Fuel, limits().Fuel-1)
	}
	if got.Usage.PeakMemoryBytes != 64<<10 {
		t.Errorf("peak memory = %d, want one 64 KiB page", got.Usage.PeakMemoryBytes)
	}
}

func testFuelIsRepeatable(t *testing.T, r app.Runner) {
	first, err := run(t, r, Hello, limits())
	if err != nil {
		t.Fatal(err)
	}
	second, err := run(t, r, Hello, limits())
	if err != nil {
		t.Fatal(err)
	}
	if first.Usage.Fuel != second.Usage.Fuel {
		t.Fatalf("fuel differs between identical runs: %d then %d", first.Usage.Fuel, second.Usage.Fuel)
	}
}

func testExhaustsFuel(t *testing.T, r app.Runner) {
	l := limits()
	l.Fuel = 10_000
	got, err := run(t, r, Spins, l)
	if err == nil {
		t.Fatal("a module that loops forever finished")
	}
	if got.Usage.Fuel == 0 || got.Usage.Fuel > l.Fuel {
		t.Fatalf("fuel = %d, want between 1 and %d", got.Usage.Fuel, l.Fuel)
	}
}

func testRefusesGarbage(t *testing.T, r app.Runner) {
	if _, err := run(t, r, []byte("this is not WebAssembly"), limits()); err == nil {
		t.Fatal("garbage ran")
	}
}

func testRefusesNoStart(t *testing.T, r app.Runner) {
	// A module with no exports at all: the header only.
	if _, err := run(t, r, []byte("\x00asm\x01\x00\x00\x00"), limits()); err == nil {
		t.Fatal("a module without _start ran")
	}
}

func testOutOfFuelError(t *testing.T, r app.Runner) {
	l := limits()
	l.Fuel = 10_000
	_, err := run(t, r, Spins, l)
	if !errors.Is(err, app.ErrOutOfFuel) {
		t.Fatalf("err = %v, want ErrOutOfFuel", err)
	}
}

func testTimeoutError(t *testing.T, r app.Runner) {
	l := limits()
	l.Fuel = 1 << 62
	l.Timeout = 100 * time.Millisecond
	started := time.Now()
	_, err := run(t, r, Spins, l)
	if !errors.Is(err, app.ErrRunTimeout) {
		t.Fatalf("err = %v, want ErrRunTimeout", err)
	}
	if took := time.Since(started); took > 5*time.Second {
		t.Fatalf("a 100 ms timeout took %s", took)
	}
}

func testPassesStdin(t *testing.T, r app.Runner) {
	got, err := r.Run(context.Background(), Echo, app.RunInput{Stdin: []byte("request body"), Limits: limits()})
	if err != nil {
		t.Fatal(err)
	}
	if string(got.Stdout) != "request body" || got.StdoutTruncated {
		t.Fatalf("stdout = %q, truncated %v", got.Stdout, got.StdoutTruncated)
	}
}

func testPassesEnvironment(t *testing.T, r app.Runner) {
	got, err := r.Run(context.Background(), Environ, app.RunInput{Env: []string{"REQUEST_METHOD=GET", "PATH_INFO=/a b"}, Limits: limits()})
	if err != nil {
		t.Fatal(err)
	}
	nul := string([]byte{0})
	vars := strings.Split(strings.TrimSuffix(string(got.Stdout), nul), nul)
	if len(vars) != 2 || vars[0] != "REQUEST_METHOD=GET" || vars[1] != "PATH_INFO=/a b" {
		t.Fatalf("environment = %q", vars)
	}
}

func testCutsStdout(t *testing.T, r app.Runner) {
	stdin := bytes.Repeat([]byte("x"), 10_000)
	got, err := r.Run(context.Background(), Echo, app.RunInput{Stdin: stdin, MaxStdout: 1000, Limits: limits()})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Stdout) != 1000 || !got.StdoutTruncated {
		t.Fatalf("stdout is %d bytes, truncated %v; want 1000 bytes, truncated", len(got.Stdout), got.StdoutTruncated)
	}
}

// folder makes a directory holding inside.txt, next to a file outside.txt that
// a module given the directory must not reach.
func folder(t *testing.T) string {
	t.Helper()
	parent := t.TempDir()
	dir := filepath.Join(parent, "folder")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "inside.txt"), []byte("inside"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(parent, "outside.txt"), []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func readFixture(r app.Runner, dir, path string) (app.RunResult, error) {
	return r.Run(context.Background(), ReadFile, app.RunInput{Stdin: []byte(path), Dir: dir, Limits: limits()})
}

func testReadsInsideFolder(t *testing.T, r app.Runner) {
	got, err := readFixture(r, folder(t), "inside.txt")
	if err != nil {
		t.Fatal(err)
	}
	if string(got.Stdout) != "inside" {
		t.Fatalf("stdout = %q", got.Stdout)
	}
}

func testCannotLeaveFolder(t *testing.T, r app.Runner) {
	dir := folder(t)
	outside := filepath.Join(filepath.Dir(dir), "outside.txt")
	for _, path := range []string{"../outside.txt", "/../outside.txt", "./../outside.txt", outside, filepath.ToSlash(outside)} {
		got, err := readFixture(r, dir, path)
		if err == nil || strings.Contains(string(got.Stdout), "outside") {
			t.Errorf("%q: read %q, err %v; want it refused", path, got.Stdout, err)
		}
	}
}

func testNoFolderNoFiles(t *testing.T, r app.Runner) {
	got, err := readFixture(r, "", "inside.txt")
	if err == nil || len(got.Stdout) != 0 {
		t.Fatalf("read %q, err %v, with no folder", got.Stdout, err)
	}
}
