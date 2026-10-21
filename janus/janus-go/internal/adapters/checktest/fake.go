// Package checktest holds an in-memory app.Checker for tests that need the
// checks of a WASM Build without a container.
package checktest

import (
	"context"
	"sync"

	"janus/internal/app"
)

// Fake is an app.Checker that runs no commands. It answers with the result
// set by Answer, or the error set by Break, and records each request. It is
// safe for concurrent use.
type Fake struct {
	mu       sync.Mutex
	result   app.CheckResult
	err      error
	requests []app.CheckRequest
}

var _ app.Checker = (*Fake)(nil)

// NewFake returns a Fake whose checks pass.
func NewFake() *Fake {
	return &Fake{result: app.CheckResult{Passed: true, Log: []byte("checks passed\n")}}
}

// Answer makes later checks return result.
func (f *Fake) Answer(result app.CheckResult) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.result, f.err = result, nil
}

// Break makes later checks fail with err, as a container that crashed would.
func (f *Fake) Break(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.err = err
}

// Requests returns every request the Fake has answered.
func (f *Fake) Requests() []app.CheckRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]app.CheckRequest(nil), f.requests...)
}

// Check records request and returns the configured answer.
func (f *Fake) Check(_ context.Context, _ string, request app.CheckRequest) (app.CheckResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, request)
	if f.err != nil {
		return app.CheckResult{}, f.err
	}
	return f.result, nil
}
