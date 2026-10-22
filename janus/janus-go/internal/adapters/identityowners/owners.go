// Package identityowners makes every User that signs in an Owner of the Store.
//
// It is transitional. The Store's Owner registry is what the owned tables of
// janus.db refer to. It goes with those references (ENG-124 step 3), and this
// package goes with it.
package identityowners

import (
	"context"

	"janus/internal/app"
	contractid "janus/internal/contract/identity"
)

// Identity is the identity service as the gateway reaches it.
type Identity interface {
	CreateLocalUser(ctx context.Context, user contractid.CreateLocalUser) error
	FindLocalUser(ctx context.Context, email string) (contractid.LocalUser, error)
	FindUser(ctx context.Context, id string) (contractid.User, error)
	ResolveGoogleIdentity(ctx context.Context, identity contractid.ResolveIdentity) (contractid.Resolved, error)
	ResolveGitHubLogin(ctx context.Context, login contractid.ResolveIdentity) (contractid.Resolved, error)
}

// Registry passes sign-ins to identity and adds each User they find or create to
// the Store as an Owner. An Owner whose first write failed is added again on the
// User's next sign-in.
type Registry struct {
	store    app.Store
	identity Identity
}

// New returns a Registry that adds Users it sees to store.
func New(store app.Store, identity Identity) *Registry {
	return &Registry{store: store, identity: identity}
}

// CreateLocalUser stores a new local account. A duplicate email is a
// contract.ErrConflict.
func (r *Registry) CreateLocalUser(ctx context.Context, user contractid.CreateLocalUser) error {
	if err := r.identity.CreateLocalUser(ctx, user); err != nil {
		return err
	}
	return r.store.AddOwner(ctx, user.ID)
}

// FindLocalUser returns the local account with the given email, if there is one.
func (r *Registry) FindLocalUser(ctx context.Context, email string) (contractid.LocalUser, error) {
	found, err := r.identity.FindLocalUser(ctx, email)
	if err != nil || !found.Found {
		return found, err
	}
	return found, r.store.AddOwner(ctx, found.ID)
}

// FindUser returns the User with the given ID, if there is one.
func (r *Registry) FindUser(ctx context.Context, id string) (contractid.User, error) {
	return r.identity.FindUser(ctx, id)
}

// ResolveGoogleIdentity returns the User signed in with Google, creating it on
// first sign-in.
func (r *Registry) ResolveGoogleIdentity(ctx context.Context, identity contractid.ResolveIdentity) (contractid.Resolved, error) {
	resolved, err := r.identity.ResolveGoogleIdentity(ctx, identity)
	if err != nil {
		return resolved, err
	}
	return resolved, r.store.AddOwner(ctx, resolved.ID)
}

// ResolveGitHubLogin returns the User signed in with GitHub, creating it on first
// sign-in. EmailInUse means the email belongs to another account, so nothing is
// added.
func (r *Registry) ResolveGitHubLogin(ctx context.Context, login contractid.ResolveIdentity) (contractid.Resolved, error) {
	resolved, err := r.identity.ResolveGitHubLogin(ctx, login)
	if err != nil || resolved.EmailInUse {
		return resolved, err
	}
	return resolved, r.store.AddOwner(ctx, resolved.ID)
}
