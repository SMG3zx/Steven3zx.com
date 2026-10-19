//go:build cgo

package wasmruntime

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"

	wasmtime "github.com/bytecodealliance/wasmtime-go/v49"

	"janus/actors/templates"
)

const maxOutput = 64 << 10

// Available reports whether this build includes the CGO-backed Wasmtime runtime.
func Available() bool { return true }

// ExecuteWASI runs a WASI module with args and a time limit, returning captured
// stdout, stderr, what the run consumed, and any runtime error. Captured output
// is bounded. Usage is filled in whenever the module started running, including
// when it traps, so a run that exhausts its fuel still reports the fuel spent.
func ExecuteWASI(moduleBytes []byte, args []string, timeout time.Duration) (stdout, stderr []byte, usage Usage, err error) {
	dir, err := os.MkdirTemp("", "janus-wasm-run-")
	if err != nil {
		return nil, nil, Usage{}, err
	}
	defer os.RemoveAll(dir)
	outPath, errPath := filepath.Join(dir, "stdout"), filepath.Join(dir, "stderr")
	stdinPath := filepath.Join(dir, "stdin")
	if err = os.WriteFile(stdinPath, nil, 0600); err != nil {
		return nil, nil, Usage{}, err
	}
	cfg := wasmtime.NewConfig()
	cfg.SetConsumeFuel(true)
	cfg.SetEpochInterruption(true)
	cfg.SetMaxWasmStack(2 << 20)
	engine := wasmtime.NewEngineWithConfig(cfg)
	defer engine.Close()
	store := wasmtime.NewStore(engine)
	defer store.Close()
	store.Limiter(templates.DefaultMemoryBytes, 10000, 1, 1, 1)
	if err = store.SetFuel(fuelLimit); err != nil {
		return nil, nil, Usage{}, err
	}
	store.SetEpochDeadline(1)
	wasi := wasmtime.NewWasiConfig()
	wasi.SetArgv(args)
	if err = wasi.SetStdoutFile(outPath); err != nil {
		return nil, nil, Usage{}, err
	}
	if err = wasi.SetStderrFile(errPath); err != nil {
		return nil, nil, Usage{}, err
	}
	if err = wasi.SetStdinFile(stdinPath); err != nil {
		return nil, nil, Usage{}, err
	}
	store.SetWasi(wasi)
	linker := wasmtime.NewLinker(engine)
	defer linker.Close()
	if err = linker.DefineWasi(); err != nil {
		return nil, nil, Usage{}, err
	}
	module, err := wasmtime.NewModule(engine, moduleBytes)
	if err != nil {
		return nil, nil, Usage{}, err
	}
	defer module.Close()
	instance, err := linker.Instantiate(store, module)
	if err != nil {
		return nil, nil, Usage{}, err
	}
	start := instance.GetFunc(store, "_start")
	if start == nil {
		return nil, nil, Usage{}, errors.New("WASM module does not export the WASI _start function")
	}
	timer := time.AfterFunc(timeout, engine.IncrementEpoch)
	wall, cpu, err := measureCall(func() error {
		_, callErr := start.Call(store)
		return callErr
	})
	usage.DurationMS = wall.Milliseconds()
	usage.CPUTimeMicros = cpu.Microseconds()
	timer.Stop()
	// Read the meters before the deferred store.Close releases the store.
	usage.Fuel = fuelConsumed(store)
	usage.PeakMemoryBytes = memoryBytes(instance, store)
	stdout, _ = readBounded(outPath, maxOutput)
	stderr, _ = readBounded(errPath, maxOutput)
	return stdout, stderr, usage, err
}

// fuelConsumed returns the fuel the store has spent since SetFuel(fuelLimit).
func fuelConsumed(store *wasmtime.Store) uint64 {
	left, err := store.GetFuel()
	if err != nil || left > fuelLimit {
		return 0
	}
	return fuelLimit - left
}

// memoryBytes returns the current size of the instance's exported linear memory.
func memoryBytes(instance *wasmtime.Instance, store *wasmtime.Store) uint64 {
	export := instance.GetExport(store, "memory")
	if export == nil {
		return 0
	}
	mem := export.Memory()
	if mem == nil {
		return 0
	}
	return uint64(mem.DataSize(store))
}

func readBounded(name string, limit int) ([]byte, error) {
	f, err := os.Open(name)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, int64(limit+1)))
	if err != nil {
		return nil, err
	}
	if len(data) > limit {
		data = data[:limit]
		data = append(data, []byte("\n[output truncated]")...)
	}
	return data, nil
}
