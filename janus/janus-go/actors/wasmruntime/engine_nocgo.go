//go:build !cgo

package wasmruntime

import (
	"errors"
	"time"
)

// Available reports that the CGO-backed Wasmtime runtime is unavailable.
func Available() bool { return false }

// ExecuteWASI reports that WASI execution is unavailable in a CGO-disabled build.
func ExecuteWASI([]byte, []string, time.Duration) ([]byte, []byte, Usage, error) {
	return nil, nil, Usage{}, errors.New("Wasmtime runner requires a CGO-enabled build")
}
