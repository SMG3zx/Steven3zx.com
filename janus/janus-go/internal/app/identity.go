package app

import "context"

// Identity finds and creates the Users behind a sign-in: local accounts, Google
// identities and GitHub logins. Each sign-in method resolves to the same User
// through Identities. Every User it creates or signs in is also added as an
// Owner in the Store, so an Owner whose first write failed is added on the
// next sign-in.
type Identity struct {
	store      Store
	identities Identities
}

// NewIdentity returns an Identity that keeps Users in identities and adds them
// as Owners to store.
func NewIdentity(store Store, identities Identities) *Identity {
	return &Identity{store: store, identities: identities}
}

// CreateLocalUser stores a new local account. A duplicate email fails with a
// *Conflict.
func (i *Identity) CreateLocalUser(ctx context.Context, user CreateLocalUser) error {
	if err := i.identities.CreateLocalUser(ctx, user); err != nil {
		return err
	}
	return i.store.AddOwner(ctx, user.ID)
}

// FindLocalUser returns the local account with the given email, if there is
// one, for signing in.
func (i *Identity) FindLocalUser(ctx context.Context, email string) (LocalUserFound, error) {
	found, err := i.identities.FindLocalUser(ctx, email)
	if err != nil || !found.Found {
		return found, err
	}
	return found, i.store.AddOwner(ctx, found.ID)
}

// FindUser returns the User with the given ID, if there is one.
func (i *Identity) FindUser(ctx context.Context, id string) (UserFound, error) {
	return i.identities.FindUser(ctx, id)
}

// ResolveGoogleIdentity returns the User signed in with Google, creating it on
// first sign-in.
func (i *Identity) ResolveGoogleIdentity(ctx context.Context, identity ResolveGoogleIdentity) (GoogleIdentityResolved, error) {
	resolved, err := i.identities.ResolveGoogleIdentity(ctx, identity)
	if err != nil {
		return resolved, err
	}
	return resolved, i.store.AddOwner(ctx, resolved.ID)
}

// ResolveGitHubLogin returns the User signed in with GitHub, creating it on
// first sign-in. EmailInUse reports that the email already belongs to another
// account, so the caller must not sign in.
func (i *Identity) ResolveGitHubLogin(ctx context.Context, login ResolveGitHubLogin) (GitHubLoginResolved, error) {
	resolved, err := i.identities.ResolveGitHubLogin(ctx, login)
	if err != nil || resolved.EmailInUse {
		return resolved, err
	}
	return resolved, i.store.AddOwner(ctx, resolved.ID)
}
