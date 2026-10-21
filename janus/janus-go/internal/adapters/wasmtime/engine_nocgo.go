//go:build !cgo

package wasmtime

import (
	"context"
	"errors"
	"time"

	"janus/internal/app"
)

var errNoCGO = errors.New("Wasmtime runner requires a CGO-enabled build")

// Available reports that the CGO-backed Wasmtime runtime is unavailable.
func Available() bool { return false }

// ExecuteWASI reports that WASI execution is unavailable in a CGO-disabled build.
func ExecuteWASI([]byte, []string, time.Duration) ([]byte, []byte, Usage, error) {
	return nil, nil, Usage{}, errNoCGO
}

func execute(context.Context, []byte, app.RunInput) (app.RunResult, error) {
	return app.RunResult{}, errNoCGO
}
