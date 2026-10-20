package app

import (
	"context"
	"fmt"
)

// JobCanceller stops the running jobs of an Account.
type JobCanceller interface {
	CancelOwner(owner string) int
}

// Accounts deletes Accounts. Deleting an Account removes everything it owns:
// its Projects, Snapshots, WASM Builds and Artifacts, its Provider Identities
// and its GitHub Connection, and, when Agents are set, its Agents' tailnet nodes.
type Accounts struct {
	store   Store
	running []JobCanceller
	agents  *Agents
}

// NewAccounts returns an Accounts that deletes through store and, when the
// Owner chooses to cancel unfinished jobs, also stops the ones that are running
// in running.
func NewAccounts(store Store, running ...JobCanceller) *Accounts {
	return &Accounts{store: store, running: running}
}

// WithAgents makes a deleted Account's Agents leave the tailnet too. It returns
// a, so it chains onto NewAccounts.
func (a *Accounts) WithAgents(agents *Agents) *Accounts {
	a.agents = agents
	return a
}

// Delete deletes an Account. It is refused, and the result lists the active
// imports and builds, while the Account has unfinished jobs, unless
// cancelActiveJobs is set: then those jobs end as cancelled, never resume, and
// the ones running are stopped. Deleted is false when there was no such Account.
//
// The Account's Agents are revoked on the tailnet after the Account is gone. If
// that fails the Account is still deleted, Deleted is true, and the error comes
// back so the caller can report the nodes that were left behind.
func (a *Accounts) Delete(ctx context.Context, owner string, cancelActiveJobs bool) (AccountDeleted, error) {
	var agents []AgentRecord
	if a.agents != nil {
		var err error
		if agents, err = a.store.ListAgents(ctx, owner); err != nil {
			return AccountDeleted{}, err
		}
	}
	result, err := a.store.DeleteAccount(ctx, owner, cancelActiveJobs)
	if err != nil {
		return result, err
	}
	if result.Deleted && cancelActiveJobs {
		for _, jobs := range a.running {
			jobs.CancelOwner(owner)
		}
	}
	if result.Deleted && a.agents != nil {
		if err = a.agents.Revoke(ctx, agents); err != nil {
			return result, fmt.Errorf("the Account was deleted but its Agents were not all removed from the tailnet: %w", err)
		}
	}
	return result, nil
}
