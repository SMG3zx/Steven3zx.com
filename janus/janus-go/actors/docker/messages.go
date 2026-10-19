package docker

type Health struct{}

// EnsureRunning checks the Docker Engine and starts Docker Desktop if needed.
type EnsureRunning struct{}

// PrepareWASMBuilders makes sure both Janus WASM builder images exist locally.
type PrepareWASMBuilders struct {
	RustImage, GoImage, JSImage string
}

type WASMBuildersReady struct {
	BuiltImages []string
	Err         error
}

type Healthy struct{ Err error }
