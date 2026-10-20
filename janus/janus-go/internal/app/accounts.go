package app

import "context"

// JobCanceller stops the running jobs of an Account.
type JobCanceller interface {
	CancelOwner(owner string) int
}

// Accounts deletes Accounts. Deleting an Account removes everything it owns:
// its Projects, Snapshots, WASM Builds and Artifacts, its Provider Identities
// and its GitHub Connection.
type Accounts struct {
	store   Store
	running []JobCanceller
}

// NewAccounts returns an Accounts that deletes through store and, when the
// Owner chooses to cancel unfinished jobs, also stops the ones that are running
// in running.
func NewAccounts(store Store, running ...JobCanceller) *Accounts {
	return &Accounts{store: store, running: running}
}

// Delete deletes an Account. It is refused, and the result lists the active
// imports and builds, while the Account has unfinished jobs, unless
// cancelActiveJobs is set: then those jobs end as cancelled, never resume, and
// the ones running are stopped. Deleted is false when there was no such Account.
func (a *Accounts) Delete(ctx context.Context, owner string, cancelActiveJobs bool) (AccountDeleted, error) {
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
