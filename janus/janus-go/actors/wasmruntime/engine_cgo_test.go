//go:build cgo

package wasmruntime

import (
	"fmt"
	"strings"
	"testing"
	"time"

	wasmtime "github.com/bytecodealliance/wasmtime-go/v49"
)

func compileWAT(t *testing.T, wat string) []byte {
	t.Helper()
	module, err := wasmtime.Wat2Wasm(wat)
	if err != nil {
		t.Fatalf("compile WAT: %v", err)
	}
	return module
}

// countLoop runs a fixed loop, so the fuel it spends does not depend on timing.
const countLoop = `(module
  (memory (export "memory") 1)
  (func (export "_start")
    (local $i i32)
    (loop $again
      (local.set $i (i32.add (local.get $i) (i32.const 1)))
      (br_if $again (i32.lt_u (local.get $i) (i32.const 1000))))))`

func TestExecuteWASIReportsFuelSpent(t *testing.T) {
	module := compileWAT(t, countLoop)
	_, _, usage, err := ExecuteWASI(module, nil, 5*time.Second)
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if usage.Fuel == 0 || usage.Fuel >= fuelLimit {
		t.Fatalf("fuel = %d, want between 1 and %d", usage.Fuel, fuelLimit-1)
	}
}

func TestExecuteWASIFuelIsRepeatable(t *testing.T) {
	module := compileWAT(t, countLoop)
	_, _, first, err := ExecuteWASI(module, nil, 5*time.Second)
	if err != nil {
		t.Fatalf("first run failed: %v", err)
	}
	_, _, second, err := ExecuteWASI(module, nil, 5*time.Second)
	if err != nil {
		t.Fatalf("second run failed: %v", err)
	}
	if first.Fuel != second.Fuel {
		t.Fatalf("fuel differs between identical runs: %d then %d", first.Fuel, second.Fuel)
	}
}

func TestExecuteWASIReportsPeakMemory(t *testing.T) {
	// Starts at 2 pages and grows by 3, so linear memory ends at 5 pages of 64 KiB.
	module := compileWAT(t, `(module
  (memory (export "memory") 2)
  (func (export "_start")
    (drop (memory.grow (i32.const 3)))))`)
	_, _, usage, err := ExecuteWASI(module, nil, 5*time.Second)
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if want := uint64(5 * 65536); usage.PeakMemoryBytes != want {
		t.Fatalf("peak memory = %d, want %d", usage.PeakMemoryBytes, want)
	}
}

func TestExecuteWASIReportsZeroMemoryWhenNoneIsExported(t *testing.T) {
	module := compileWAT(t, `(module
  (memory 1)
  (func (export "_start")))`)
	_, _, usage, err := ExecuteWASI(module, nil, 5*time.Second)
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if usage.PeakMemoryBytes != 0 {
		t.Fatalf("peak memory = %d, want 0 when no memory is exported", usage.PeakMemoryBytes)
	}
}

func TestExecuteWASIReportsAllFuelWhenTheRunExhaustsIt(t *testing.T) {
	module := compileWAT(t, `(module
  (memory (export "memory") 1)
  (func (export "_start")
    (loop $forever (br $forever))))`)
	_, _, usage, err := ExecuteWASI(module, nil, 30*time.Second)
	if err == nil {
		t.Fatal("run succeeded, want a trap when fuel runs out")
	}
	if usage.Fuel != fuelLimit {
		t.Fatalf("fuel = %d, want the whole %d spent", usage.Fuel, fuelLimit)
	}
}

func TestExecuteWASIMeasuresWallTimeOnTheHost(t *testing.T) {
	module := compileWAT(t, `(module
  (memory (export "memory") 1)
  (func (export "_start")
    (loop $forever (br $forever))))`)
	// Burning all the fuel takes real time, and the module is only part of what
	// the call spends, so the host-measured run time must fit inside the call.
	began := time.Now()
	_, _, usage, err := ExecuteWASI(module, nil, 30*time.Second)
	elapsed := time.Since(began)
	if err == nil {
		t.Fatal("run succeeded, want a trap when fuel runs out")
	}
	if usage.DurationMS <= 0 {
		t.Fatalf("duration = %d ms, want a positive host-measured time", usage.DurationMS)
	}
	if usage.DurationMS > elapsed.Milliseconds() {
		t.Fatalf("duration = %d ms, longer than the %d ms the whole call took", usage.DurationMS, elapsed.Milliseconds())
	}
}

func TestExecuteWASIReportsCPUTimeForACPUBoundRun(t *testing.T) {
	if _, ok := threadCPUTime(); !ok {
		t.Skip("thread CPU time is not available on this platform")
	}
	module := compileWAT(t, `(module
  (memory (export "memory") 1)
  (func (export "_start")
    (loop $forever (br $forever))))`)
	_, _, usage, err := ExecuteWASI(module, nil, 30*time.Second)
	if err == nil {
		t.Fatal("run succeeded, want a trap when fuel runs out")
	}
	// One thread cannot use more CPU than the wall time it ran for. Windows reads
	// CPU time in 15.6 ms ticks and a fuel-limited run lasts tens of milliseconds,
	// so allow two ticks. TestMeasureFuelPerSecond checks the other direction on
	// runs long enough to read accurately.
	wall := usage.DurationMS * 1000
	if usage.CPUTimeMicros > wall+32_000 {
		t.Fatalf("cpu = %d us, more than the %d us of wall time", usage.CPUTimeMicros, wall)
	}
}

func exitModule(status int) string {
	return fmt.Sprintf(`(module
  (import "wasi_snapshot_preview1" "proc_exit" (func $exit (param i32)))
  (memory (export "memory") 1)
  (func (export "_start") (call $exit (i32.const %d))))`, status)
}

func TestExitStatusZeroIsASuccess(t *testing.T) {
	_, _, _, err := ExecuteWASI(compileWAT(t, exitModule(0)), nil, 5*time.Second)
	if err != nil {
		t.Fatalf("a program that exits with status 0 failed: %v", err)
	}
}

func TestNonzeroExitStatusIsAFailureThatNamesTheStatus(t *testing.T) {
	_, _, usage, err := ExecuteWASI(compileWAT(t, exitModule(3)), nil, 5*time.Second)
	if err == nil || !strings.Contains(err.Error(), "status 3") {
		t.Fatalf("err = %v, want a failure naming status 3", err)
	}
	if usage.Fuel == 0 {
		t.Fatal("a program that exited non-zero still reports the fuel it spent")
	}
}
