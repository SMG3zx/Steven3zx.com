package httpserver

import "context"

type Stop struct {
	Context context.Context
}

type Stopped struct{ Err error }

type ServeFailed struct{ Err error }
