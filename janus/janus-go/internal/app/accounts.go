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
	store       Store
	running     []JobCanceller
	agents      *Agents
	deployments *Deployments
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

// WithDeployments makes a deleted Account's Deployments stop first. It returns
// a, so it chains onto NewAccounts.
func (a *Accounts) WithDeployments(deployments *Deployments) *Accounts {
	a.deployments = deployments
	return a
}

// Delete deletes an Account. It is refused, and the result lists the active
// imports and builds, while the Account has unfinished jobs, unless
// cancelActiveJobs is set: then those jobs end as cancelled, never resume, and
// the ones running are stopped. Deleted is false when there was no such Account.
//
// The Deployments stop and the Account's Agents leave the tailnet first, unless
// the deletion is going to be refused. If either fails, the Account is kept and
// the error comes back, so deleting again finishes the job and no node is left
// behind without a record.
func (a *Accounts) Delete(ctx context.Context, owner string, cancelActiveJobs bool) (AccountDeleted, error) {
	if a.agents != nil || a.deployments != nil {
		active, err := a.store.ListActiveJobs(ctx, owner)
		if err != nil {
			return AccountDeleted{}, err
		}
		if len(active) == 0 || cancelActiveJobs {
			// Deployments stop while their Agents can still be reached, so before
			// the Agents leave the tailnet.
			if a.deployments != nil {
				if err = a.deployments.Teardown(ctx, owner); err != nil {
					return AccountDeleted{}, fmt.Errorf("stop the Account's Deployments: %w", err)
				}
			}
			if a.agents != nil {
				if err = a.agents.RevokeAll(ctx, owner); err != nil {
					return AccountDeleted{}, fmt.Errorf("remove the Account's Agents from the tailnet: %w", err)
				}
			}
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
	return result, nil
}
