//go:build cgo

package wasmrun

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	wasmtime "github.com/bytecodealliance/wasmtime-go/v49"

	"janus/internal/domain/templates"
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
	result, err := execute(context.Background(), moduleBytes, Input{Args: args, Limits: limits})
	return result.Stdout, result.Stderr, result.Usage, err
}

// execute runs a WASI module as Run describes. The run stops at
// input.Limits.Timeout or when ctx ends, whichever comes first.
func execute(ctx context.Context, moduleBytes []byte, input Input) (result Result, err error) {
	limits := input.Limits
	dir, err := os.MkdirTemp("", "janus-wasm-run-")
	if err != nil {
		return Result{}, err
	}
	defer os.RemoveAll(dir)
	outPath, errPath := filepath.Join(dir, "stdout"), filepath.Join(dir, "stderr")
	stdinPath := filepath.Join(dir, "stdin")
	if err = os.WriteFile(stdinPath, input.Stdin, 0600); err != nil {
		return Result{}, err
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
		return Result{}, err
	}
	store.SetEpochDeadline(1)
	wasi := wasmtime.NewWasiConfig()
	wasi.SetArgv(input.Args)
	keys, values := splitEnv(input.Env)
	wasi.SetEnv(keys, values)
	if err = wasi.SetStdoutFile(outPath); err != nil {
		return Result{}, err
	}
	if err = wasi.SetStderrFile(errPath); err != nil {
		return Result{}, err
	}
	if err = wasi.SetStdinFile(stdinPath); err != nil {
		return Result{}, err
	}
	if input.Dir != "" {
		// The folder is the module's only preopened directory, and WASI resolves
		// every path against a preopen, so nothing outside it can be named.
		if err = wasi.PreopenDir(input.Dir, "/", true); err != nil {
			return Result{}, fmt.Errorf("open the host folder: %w", err)
		}
	}
	store.SetWasi(wasi)
	linker := wasmtime.NewLinker(engine)
	defer linker.Close()
	if err = linker.DefineWasi(); err != nil {
		return Result{}, err
	}
	module, err := loadModule(engine, moduleBytes)
	if err != nil {
		return Result{}, err
	}
	defer module.Close()
	instance, err := linker.Instantiate(store, module)
	if err != nil {
		return Result{}, err
	}
	start := instance.GetFunc(store, "_start")
	if start == nil {
		return Result{}, errors.New("WASM module does not export the WASI _start function")
	}
	var timedOut atomic.Bool
	timer := time.AfterFunc(limits.Timeout, func() {
		timedOut.Store(true)
		engine.IncrementEpoch()
	})
	stopOnCancel := context.AfterFunc(ctx, engine.IncrementEpoch)
	wall, cpu, err := measureCall(func() error {
		_, callErr := start.Call(store)
		return callErr
	})
	timer.Stop()
	stopOnCancel()
	err = classify(normalizeExit(err), timedOut.Load())
	result.Usage.DurationMS = wall.Milliseconds()
	result.Usage.CPUTimeMicros = cpu.Microseconds()
	// Read the meters before the deferred store.Close releases the store.
	result.Usage.Fuel = fuelConsumed(store, limits.Fuel)
	result.Usage.PeakMemoryBytes = memoryBytes(instance, store)
	maxStdout := input.MaxStdout
	if maxStdout <= 0 {
		maxStdout = maxOutput
	}
	result.Stdout, result.StdoutTruncated, _ = readBounded(outPath, maxStdout)
	result.Stderr, _, _ = readBounded(errPath, maxOutput)
	return result, err
}

// splitEnv turns "NAME=value" entries into the names and values Wasmtime takes.
// An entry with no "=" is a name with an empty value.
func splitEnv(env []string) (keys, values []string) {
	for _, entry := range env {
		key, value, _ := strings.Cut(entry, "=")
		keys, values = append(keys, key), append(values, value)
	}
	return keys, values
}

// classify maps the traps that mean a limit was hit to the app errors for them.
// An interrupt is a timeout only when the timer fired; otherwise ctx ended.
func classify(err error, timedOut bool) error {
	var trap *wasmtime.Trap
	if !errors.As(err, &trap) {
		return err
	}
	code := trap.Code()
	switch {
	case code == nil:
		return err
	case *code == wasmtime.OutOfFuel:
		return ErrOutOfFuel
	case *code == wasmtime.Interrupt && timedOut:
		return ErrRunTimeout
	case *code == wasmtime.Interrupt:
		return fmt.Errorf("the WebAssembly run was stopped: %w", err)
	}
	return err
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

// readBounded reads at most limit bytes of the named file, and reports whether
// there was more.
func readBounded(name string, limit int) (data []byte, truncated bool, err error) {
	f, err := os.Open(name)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	defer f.Close()
	data, err = io.ReadAll(io.LimitReader(f, int64(limit+1)))
	if err != nil {
		return nil, false, err
	}
	if len(data) > limit {
		return data[:limit], true, nil
	}
	return data, false, nil
}
