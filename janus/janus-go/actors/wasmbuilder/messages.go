package wasmbuilder

type Build struct{ Workspace, Target string }
type Built struct {
	Name          string
	Artifact, Log []byte
	Err           error
}
