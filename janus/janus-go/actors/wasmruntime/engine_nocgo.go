//go:build !cgo

package wasmruntime

import (
	"errors"
	"time"
)

// Available reports that the CGO-backed Wasmtime runtime is unavailable.
func Available() bool { return false }

// ExecuteWASI reports that WASI execution is unavailable in a CGO-disabled build.
func ExecuteWASI([]byte, []string, time.Duration) ([]byte, []byte, error) {
	return nil, nil, errors.New("Wasmtime runner requires a CGO-enabled build")
}
