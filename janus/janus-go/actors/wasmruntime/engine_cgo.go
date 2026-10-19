//go:build cgo

package wasmruntime

import (
	"errors"
	"fmt"
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
	limits := templates.Resources{Fuel: fuelLimit, MemoryBytes: templates.DefaultMemoryBytes, Timeout: timeout}
	return ExecuteWASIWith(moduleBytes, args, limits)
}

// ExecuteWASIWith is ExecuteWASI with the fuel, memory, and time limits of a
// template's Resources.
func ExecuteWASIWith(moduleBytes []byte, args []string, limits templates.Resources) (stdout, stderr []byte, usage Usage, err error) {
	timeout := limits.Timeout
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
	store.Limiter(limits.MemoryBytes, 10000, 1, 1, 1)
	if err = store.SetFuel(limits.Fuel); err != nil {
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
	module, err := loadModule(engine, moduleBytes)
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
	err = normalizeExit(err)
	usage.DurationMS = wall.Milliseconds()
	usage.CPUTimeMicros = cpu.Microseconds()
	timer.Stop()
	// Read the meters before the deferred store.Close releases the store.
	usage.Fuel = fuelConsumed(store, limits.Fuel)
	usage.PeakMemoryBytes = memoryBytes(instance, store)
	stdout, _ = readBounded(outPath, maxOutput)
	stderr, _ = readBounded(errPath, maxOutput)
	return stdout, stderr, usage, err
}

// normalizeExit turns a WASI proc_exit into the outcome it means. Wasmtime
// reports every proc_exit as an error, including exit status 0, which is how Go
// programs finish: their runtime calls proc_exit(0) after main returns. Status 0
// is a success; any other status is a failure that names the status.
func normalizeExit(err error) error {
	var wasmErr *wasmtime.Error
	if !errors.As(err, &wasmErr) {
		return err
	}
	status, ok := wasmErr.ExitStatus()
	switch {
	case !ok:
		return err
	case status == 0:
		return nil
	default:
		return fmt.Errorf("program exited with status %d", status)
	}
}

// fuelConsumed returns the fuel the store has spent since SetFuel(limit).
func fuelConsumed(store *wasmtime.Store, limit uint64) uint64 {
	left, err := store.GetFuel()
	if err != nil || left > limit {
		return 0
	}
	return limit - left
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
