package wasmruntime

import "time"

type RequestRun struct {
	OwnerID, BuildID string
	Args             []string
}
type RunRequested struct {
	RunID string
	Err   error
}
type GetRun struct{ OwnerID, RunID string }
type RunFound struct {
	Run   Run
	Found bool
	Err   error
}
type Drain struct{}
type Drained struct{ Err error }
type Run struct {
	ID        string    `json:"id"`
	BuildID   string    `json:"build_id"`
	Module    string    `json:"module,omitempty"`
	Status    string    `json:"status"`
	Stdout    string    `json:"stdout,omitempty"`
	Stderr    string    `json:"stderr,omitempty"`
	Error     string    `json:"error,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	ownerID   string
}
type completed struct {
	ID, Module     string
	Stdout, Stderr []byte
	Err            error
}
