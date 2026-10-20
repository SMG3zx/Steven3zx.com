package wasmtime

import (
	"janus/internal/domain/templates"
	"janus/internal/domain/usage"
)

// fuelLimit is the Wasmtime fuel a single run starts with. A run that spends all
// of it traps, so it is also the most fuel one run can consume.
const fuelLimit = templates.DefaultFuel

// Usage is kept under its old name for callers of this package.
type Usage = usage.Usage
