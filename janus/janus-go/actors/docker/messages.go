package docker

// Run requests one isolated container execution. The actor owns the Docker CLI
// boundary and applies the same resource and filesystem limits to every job.
type Run struct {
	Image, Workspace, Command string
	MaxLog, MaxOutput         int
}

type Ran struct {
	Stdout, Stderr []byte
	Err            error
}

type Health struct{}

// EnsureRunning checks the Docker Engine and starts Docker Desktop if needed.
type EnsureRunning struct{}

// PrepareWASMBuilders makes sure both Janus WASM builder images exist locally.
type PrepareWASMBuilders struct {
	RustImage, GoImage string
}

type WASMBuildersReady struct {
	BuiltImages []string
	Err         error
}

type Healthy struct{ Err error }
