package app

import (
	"context"
	"fmt"
	"time"
)

// accountPoll is how often Delete asks identity whether a deletion has finished.
const accountPoll = 50 * time.Millisecond

// Accounts deletes Accounts. Deleting an Account removes everything it owns:
// its Projects, Snapshots, WASM Builds and Artifacts, its Provider Identities,
// its GitHub Connection and its Agents' tailnet nodes. The identity service
// runs that deletion: it announces it, each service removes what it keeps and
// confirms, and identity deletes the User once all have confirmed.
type Accounts struct {
	store      Store
	identities Identities
	catalog    Catalog
}

// NewAccounts returns an Accounts that deletes an Account's records in store and
// catalog and its User through identities.
func NewAccounts(store Store, identities Identities, catalog Catalog) *Accounts {
	return &Accounts{store: store, identities: identities, catalog: catalog}
}

// Delete deletes an Account. It is refused, and the result lists the active
// Imports and Builds, while the Account has unfinished jobs, unless
// cancelActiveJobs is set: then those jobs end as cancelled, never resume, and
// the ones running are stopped. Deleted is false when there was no such Account.
//
// Delete returns once the User is gone. A deletion that fails part way is left
// for identity to resume, and deleting again finishes it.
func (a *Accounts) Delete(ctx context.Context, owner string, cancelActiveJobs bool) (AccountDeleted, error) {
	imports, err := a.catalog.ActiveImports(ctx, owner)
	if err != nil {
		return AccountDeleted{}, err
	}
	builds, err := a.store.ListActiveJobs(ctx, owner)
	if err != nil {
		return AccountDeleted{}, err
	}
	if active := append(imports, builds...); len(active) > 0 && !cancelActiveJobs {
		return AccountDeleted{ActiveJobs: active}, nil
	}
	deletion, err := a.identities.DeleteAccount(ctx, owner, cancelActiveJobs)
	if err != nil {
		return AccountDeleted{}, fmt.Errorf("start deleting the Account: %w", err)
	}
	switch deletion.Status {
	case DeletionMissing:
		return AccountDeleted{}, nil
	case DeletionRefused:
		return AccountDeleted{ActiveJobs: deletion.ActiveJobs}, nil
	}
	result, err := a.store.DeleteAccount(ctx, owner, cancelActiveJobs)
	if err != nil || len(result.ActiveJobs) > 0 {
		return result, err
	}
	if err := a.awaitDeletion(ctx, owner, cancelActiveJobs); err != nil {
		return AccountDeleted{}, err
	}
	result.Deleted = true
	return result, nil
}

// awaitDeletion waits until identity has deleted the User. Asking again resumes a
// deletion that has stalled, so each poll is a request.
func (a *Accounts) awaitDeletion(ctx context.Context, owner string, cancelActiveJobs bool) error {
	for {
		deletion, err := a.identities.DeleteAccount(ctx, owner, cancelActiveJobs)
		if err != nil {
			return fmt.Errorf("wait for the Account's deletion: %w", err)
		}
		if deletion.Status == DeletionMissing {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("the Account is still being deleted: %w", ctx.Err())
		case <-time.After(accountPoll):
		}
	}
}
