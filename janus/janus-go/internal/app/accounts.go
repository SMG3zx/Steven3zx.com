package app

import (
	"context"
	"fmt"
)

// BuildOwners deletes the WASM Builds an Account owns, and cancels the ones
// running.
type BuildOwners interface {
	DeleteOwner(ctx context.Context, owner string) error
}

// Accounts deletes Accounts. Deleting an Account removes everything it owns:
// its Projects, Snapshots, WASM Builds and Artifacts, its Provider Identities
// and its GitHub Connection, and, when Agents are set, its Agents' tailnet nodes.
type Accounts struct {
	store      Store
	identities Identities
	catalog    Catalog
	builds     BuildOwners
	fleet      Fleet
}

// NewAccounts returns an Accounts that deletes what an Account owns through
// store and catalog and the User itself through identities.
func NewAccounts(store Store, identities Identities, catalog Catalog) *Accounts {
	return &Accounts{store: store, identities: identities, catalog: catalog}
}

// WithBuilds makes a deleted Account's WASM Builds go with it, and their running
// Builds stop. It returns a, so it chains onto NewAccounts.
func (a *Accounts) WithBuilds(builds BuildOwners) *Accounts {
	a.builds = builds
	return a
}

// WithFleet makes a deleted Account's Deployments stop, its Agents leave the
// tailnet, and its records in the fleet service go. It returns a, so it chains
// onto NewAccounts.
func (a *Accounts) WithFleet(fleet Fleet) *Accounts {
	a.fleet = fleet
	return a
}

// Delete deletes an Account. It is refused, and the result lists the active
// Imports and Builds, while the Account has unfinished jobs, unless
// cancelActiveJobs is set: then those jobs end as cancelled, never resume, and
// the ones running are stopped. Deleted is false when there was no such Account.
//
// The steps run in an order that can be repeated after any of them fails: the
// Deployments stop and the Agents leave the tailnet, the projects service
// deletes the Projects, the builds service deletes the Builds, the Store deletes
// the rest, and the User and its sign-in methods go last. A failure keeps the Account, which can
// still sign in, and the error comes back, so deleting again finishes the job
// and no node is left behind without a record.
func (a *Accounts) Delete(ctx context.Context, owner string, cancelActiveJobs bool) (AccountDeleted, error) {
	user, err := a.identities.FindUser(ctx, owner)
	if err != nil || !user.Found {
		return AccountDeleted{}, err
	}
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
	// Deployments stop while their Agents can still be reached, so before the
	// Agents leave the tailnet.
	if a.fleet != nil {
		if err = a.fleet.TeardownDeployments(ctx, owner); err != nil {
			return AccountDeleted{}, fmt.Errorf("stop the Account's Deployments: %w", err)
		}
		if err = a.fleet.RevokeAgents(ctx, owner); err != nil {
			return AccountDeleted{}, fmt.Errorf("remove the Account's Agents from the tailnet: %w", err)
		}
	}
	if err = a.catalog.DeleteOwner(ctx, owner); err != nil {
		return AccountDeleted{}, fmt.Errorf("delete the Account's Projects: %w", err)
	}
	if a.builds != nil {
		if err = a.builds.DeleteOwner(ctx, owner); err != nil {
			return AccountDeleted{}, fmt.Errorf("delete the Account's WASM builds: %w", err)
		}
	}
	if a.fleet != nil {
		if err = a.fleet.DeleteOwner(ctx, owner); err != nil {
			return AccountDeleted{}, fmt.Errorf("delete the Account's fleet records: %w", err)
		}
	}
	result, err := a.store.DeleteAccount(ctx, owner, cancelActiveJobs)
	if err != nil || len(result.ActiveJobs) > 0 {
		return result, err
	}
	// An Owner that never had records here has nothing in the Store to delete.
	result.Deleted, err = a.identities.DeleteUser(ctx, owner)
	if err != nil {
		return AccountDeleted{}, fmt.Errorf("delete the Account's sign-in methods: %w", err)
	}
	return result, nil
}
