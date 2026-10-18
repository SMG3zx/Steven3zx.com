package application

import (
	"context"
	"time"
)

const (
	StartupTimeout  = 90 * time.Second
	ShutdownTimeout = 10 * time.Second
)

type Start struct {
	Errors chan error
}

type Started struct{ Err error }

type Stop struct{ Context context.Context }

type Stopped struct{ Err error }
