package runnertest

import (
	"context"
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
	} {
		t.Run(c.name, func(t *testing.T) { c.test(t, newRunner(t)) })
	}
}

func limits() app.RunLimits {
	return app.RunLimits{Fuel: 1_000_000, MemoryBytes: 16 << 20, Timeout: 5 * time.Second}
}

func run(t *testing.T, r app.Runner, module []byte, l app.RunLimits) (app.RunResult, error) {
	t.Helper()
	return r.Run(context.Background(), module, nil, l)
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
