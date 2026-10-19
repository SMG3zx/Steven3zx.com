package wasmbuilder

// Build asks a builder to compile Workspace for Target. Entrypoint is the file
// the Account confirmed, relative to the workspace root; builders that need one
// find their own when it is empty.
type Build struct{ Workspace, Target, Entrypoint string }
type Built struct {
	Name          string
	Artifact, Log []byte
	Err           error
}
