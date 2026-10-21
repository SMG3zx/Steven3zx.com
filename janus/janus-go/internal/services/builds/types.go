package builds

import (
	"context"
)

// CompileRequest asks for a workspace to be compiled for a Target. Entrypoint is
// the file the Account confirmed, relative to the workspace root; compilers that
// need one find their own when it is empty.
type CompileRequest struct{ Workspace, Target, Entrypoint string }

// CompileResult is a finished compile. Artifact is a compressed ZIP of the WASM
// modules, and Log is the bounded compiler output. Log is set on failure too.
type CompileResult struct {
	Name          string
	Artifact, Log []byte
}

// ManifestError means the workspace lacks the manifest its Language requires.
// Message is the sentence an Account sees; Err is the underlying cause.
type ManifestError struct {
	Message string
	Err     error
}

func (e *ManifestError) Error() string { return e.Message + ": " + e.Err.Error() }
func (e *ManifestError) Unwrap() error { return e.Err }

// CheckRequest asks for the checks of a compiled workspace. The workspace is the
// same source tree the build compiled.
type CheckRequest struct{ Workspace string }

// CheckResult is the outcome of a Check. Passed is false when a check or test
// command failed. NoTests is true when the Language's test command ran no tests,
// so the Check rests on the compile-level check alone. Log is bounded output.
type CheckResult struct {
	Passed, NoTests bool
	Log             []byte
}

// CompileFunc compiles a workspace of one Language, the way the Docker compiler
// does. A failing compile is an error; a workspace without the manifest its
// Language requires fails with a *ManifestError.
type CompileFunc func(ctx context.Context, language string, request CompileRequest) (CompileResult, error)

// CheckFunc runs the checks of a compiled workspace. A failing command is a
// Passed of false, not an error; an error means the checks could not run to an
// answer.
type CheckFunc func(ctx context.Context, language string, request CheckRequest) (CheckResult, error)
