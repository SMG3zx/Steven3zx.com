// Package deployments holds the state machine of a Deployment. Persistence
// adapters compare and swap the stored state; whether a move is legal is
// decided here and nowhere else (GLOSSARY.md, Deployment states).
package deployments

import "slices"

// State is where a Deployment is in its life.
type State string

const (
	Queued   State = "queued"
	Starting State = "starting"
	Healthy  State = "healthy"
	Failed   State = "failed"
	Stopped  State = "stopped"
)

// machine lists, for each non-terminal state, the states a Deployment may move
// to. Failed and Stopped are terminal and have no entry.
var machine = map[State][]State{
	Queued:   {Starting, Failed, Stopped},
	Starting: {Healthy, Failed, Stopped},
	Healthy:  {Stopped},
}

// CanMove reports whether a Deployment may move from one state to another.
// Staying where it is is never a move.
func CanMove(from, to State) bool {
	return slices.Contains(machine[from], to)
}

// InFlight reports whether a Deployment is still being brought up. A Project has
// at most one in-flight Deployment.
func InFlight(state State) bool {
	return state == Queued || state == Starting
}
