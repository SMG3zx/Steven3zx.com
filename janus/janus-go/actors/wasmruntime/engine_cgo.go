//go:build cgo

package wasmruntime

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"

	wasmtime "github.com/bytecodealliance/wasmtime-go/v49"
)

const maxOutput = 64 << 10

// Available reports whether this build includes the CGO-backed Wasmtime runtime.
func Available() bool { return true }

// ExecuteWASI runs a WASI module with args and a time limit, returning captured
// stdout, stderr, and any runtime error. Captured output is bounded.
func ExecuteWASI(moduleBytes []byte, args []string, timeout time.Duration) (stdout, stderr []byte, err error) {
	dir, err := os.MkdirTemp("", "janus-wasm-run-")
	if err != nil {
		return nil, nil, err
	}
	defer os.RemoveAll(dir)
	outPath, errPath := filepath.Join(dir, "stdout"), filepath.Join(dir, "stderr")
	stdinPath := filepath.Join(dir, "stdin")
	if err = os.WriteFile(stdinPath, nil, 0600); err != nil {
		return nil, nil, err
	}
	cfg := wasmtime.NewConfig()
	cfg.SetConsumeFuel(true)
	cfg.SetEpochInterruption(true)
	cfg.SetMaxWasmStack(2 << 20)
	engine := wasmtime.NewEngineWithConfig(cfg)
	defer engine.Close()
	store := wasmtime.NewStore(engine)
	defer store.Close()
	store.Limiter(128<<20, 10000, 1, 1, 1)
	if err = store.SetFuel(100_000_000); err != nil {
		return nil, nil, err
	}
	store.SetEpochDeadline(1)
	wasi := wasmtime.NewWasiConfig()
	wasi.SetArgv(args)
	if err = wasi.SetStdoutFile(outPath); err != nil {
		return nil, nil, err
	}
	if err = wasi.SetStderrFile(errPath); err != nil {
		return nil, nil, err
	}
	if err = wasi.SetStdinFile(stdinPath); err != nil {
		return nil, nil, err
	}
	store.SetWasi(wasi)
	linker := wasmtime.NewLinker(engine)
	defer linker.Close()
	if err = linker.DefineWasi(); err != nil {
		return nil, nil, err
	}
	module, err := wasmtime.NewModule(engine, moduleBytes)
	if err != nil {
		return nil, nil, err
	}
	defer module.Close()
	instance, err := linker.Instantiate(store, module)
	if err != nil {
		return nil, nil, err
	}
	start := instance.GetFunc(store, "_start")
	if start == nil {
		return nil, nil, errors.New("WASM module does not export the WASI _start function")
	}
	timer := time.AfterFunc(timeout, engine.IncrementEpoch)
	_, err = start.Call(store)
	timer.Stop()
	stdout, _ = readBounded(outPath, maxOutput)
	stderr, _ = readBounded(errPath, maxOutput)
	if err != nil {
		return stdout, stderr, err
	}
	return stdout, stderr, nil
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
