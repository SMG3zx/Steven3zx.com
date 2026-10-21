//go:build cgo

package wasmrun

import (
	"os"
	"sort"
	"testing"
	"time"

	wasmtime "github.com/bytecodealliance/wasmtime-go/v49"
)

// measureFuel is a fuel budget large enough that no measured run can exhaust it.
const measureFuel uint64 = 1 << 40

// measuredRun runs a module's _start once with the same engine settings as
// ExecuteWASI (fuel metering and epoch interruption on) but a far larger fuel
// budget, so a run lasts long enough for the host clock to time it accurately.
// A real run is capped at fuelLimit and finishes in a few milliseconds.
func measuredRun(t *testing.T, module []byte) (fuel uint64, elapsed, cpu time.Duration) {
	t.Helper()
	cfg := wasmtime.NewConfig()
	cfg.SetConsumeFuel(true)
	cfg.SetEpochInterruption(true)
	engine := wasmtime.NewEngineWithConfig(cfg)
	defer engine.Close()
	store := wasmtime.NewStore(engine)
	defer store.Close()
	if err := store.SetFuel(measureFuel); err != nil {
		t.Fatalf("set fuel: %v", err)
	}
	store.SetEpochDeadline(1 << 40)
	compiled, err := wasmtime.NewModule(engine, module)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	defer compiled.Close()
	instance, err := wasmtime.NewInstance(store, compiled, nil)
	if err != nil {
		t.Fatalf("instantiate: %v", err)
	}
	start := instance.GetFunc(store, "_start")
	elapsed, cpu, err = measureCall(func() error {
		_, callErr := start.Call(store)
		return callErr
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	left, err := store.GetFuel()
	if err != nil {
		t.Fatalf("get fuel: %v", err)
	}
	return measureFuel - left, elapsed, cpu
}

// TestMeasureFuelPerSecond reports how much Wasmtime fuel a run spends per second
// of wall time, for a few kinds of work. It is a measurement, not a check, so it
// only runs when JANUS_MEASURE_FUEL=1:
//
//	JANUS_MEASURE_FUEL=1 CGO_ENABLED=1 go test ./actors/wasmruntime -run Measure -v
//
// Run it on the hardware that will serve runs. The numbers only hold for that machine.
func TestMeasureFuelPerSecond(t *testing.T) {
	if os.Getenv("JANUS_MEASURE_FUEL") != "1" {
		t.Skip("set JANUS_MEASURE_FUEL=1 to measure")
	}
	workloads := []struct{ name, wat string }{
		{"integer arithmetic", `(module
  (memory (export "memory") 1)
  (func (export "_start") (local $i i32) (local $a i32)
    (loop $again
      (local.set $a (i32.add (i32.mul (local.get $a) (i32.const 31)) (local.get $i)))
      (local.set $i (i32.add (local.get $i) (i32.const 1)))
      (br_if $again (i32.lt_u (local.get $i) (i32.const 400000000))))))`},
		{"memory loads and stores", `(module
  (memory (export "memory") 1)
  (func (export "_start") (local $i i32) (local $a i32) (local $at i32)
    (loop $again
      (local.set $at (i32.and (i32.shl (local.get $i) (i32.const 2)) (i32.const 65532)))
      (i32.store (local.get $at) (local.get $i))
      (local.set $a (i32.add (local.get $a) (i32.load (local.get $at))))
      (local.set $i (i32.add (local.get $i) (i32.const 1)))
      (br_if $again (i32.lt_u (local.get $i) (i32.const 300000000))))))`},
		{"function calls", `(module
  (memory (export "memory") 1)
  (func $fib (param $n i32) (result i32)
    (if (result i32) (i32.lt_u (local.get $n) (i32.const 2))
      (then (local.get $n))
      (else (i32.add
        (call $fib (i32.sub (local.get $n) (i32.const 1)))
        (call $fib (i32.sub (local.get $n) (i32.const 2)))))))
  (func (export "_start") (drop (call $fib (i32.const 40)))))`},
		{"floating point", `(module
  (memory (export "memory") 1)
  (func (export "_start") (local $i i32) (local $x f64)
    (local.set $x (f64.const 1.5))
    (loop $again
      (local.set $x (f64.add (f64.sqrt (local.get $x)) (f64.mul (local.get $x) (f64.const 0.5))))
      (local.set $i (i32.add (local.get $i) (i32.const 1)))
      (br_if $again (i32.lt_u (local.get $i) (i32.const 300000000))))))`},
	}
	const repeats = 7
	for _, w := range workloads {
		module := compileWAT(t, w.wat)
		var rates, cpuShare []float64
		var fuel uint64
		for i := 0; i < repeats; i++ {
			spent, elapsed, cpu := measuredRun(t, module)
			if elapsed < 100*time.Millisecond {
				t.Fatalf("%s: run took %v, grow the workload so the clock can time it", w.name, elapsed)
			}
			fuel = spent
			rates = append(rates, float64(spent)/elapsed.Seconds())
			cpuShare = append(cpuShare, cpu.Seconds()/elapsed.Seconds())
		}
		sort.Float64s(rates)
		sort.Float64s(cpuShare)
		t.Logf("%-24s fuel/run %6.2fG  fuel/second: min %.2fG  median %.2fG  max %.2fG  cpu/wall: median %.3f", w.name, float64(fuel)/1e9,
			rates[0]/1e9, rates[len(rates)/2]/1e9, rates[len(rates)-1]/1e9, cpuShare[len(cpuShare)/2])
	}
}
