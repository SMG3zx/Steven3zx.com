package telemetry

import (
	"context"

	goakt "github.com/tochemey/goakt/v4/actor"
)

type Setup struct {
	PyroscopeServerAddress string
	Accounting             *goakt.PID
}

type SDKLog struct {
	Level   string
	Message string
}

type Ready struct{ Err error }

type Shutdown struct {
	Context context.Context
}

type Stopped struct{ Err error }
