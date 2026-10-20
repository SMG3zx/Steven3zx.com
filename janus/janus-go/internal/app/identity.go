package app

import "context"

// Identity finds and creates the Users behind a sign-in: local accounts, Google
// identities and GitHub logins. Each sign-in method resolves to the same User
// through the Store.
type Identity struct {
	store Store
}

// NewIdentity returns an Identity that reads and writes users through store.
func NewIdentity(store Store) *Identity {
	return &Identity{store: store}
}

// CreateLocalUser stores a new local account. A duplicate email fails with the
// Store's conflict error.
func (i *Identity) CreateLocalUser(ctx context.Context, user CreateLocalUser) error {
	return i.store.CreateLocalUser(ctx, user)
}

// FindLocalUser returns the local account with the given email, if there is one.
func (i *Identity) FindLocalUser(ctx context.Context, email string) (LocalUserFound, error) {
	return i.store.FindLocalUser(ctx, email)
}

// FindUser returns the User with the given ID, if there is one.
func (i *Identity) FindUser(ctx context.Context, id string) (UserFound, error) {
	return i.store.FindUser(ctx, id)
}

// ResolveGoogleIdentity returns the User signed in with Google, creating it on
// first sign-in.
func (i *Identity) ResolveGoogleIdentity(ctx context.Context, identity ResolveGoogleIdentity) (GoogleIdentityResolved, error) {
	return i.store.ResolveGoogleIdentity(ctx, identity)
}

// ResolveGitHubLogin returns the User signed in with GitHub, creating it on
// first sign-in. EmailInUse reports that the email already belongs to another
// account, so the caller must not sign in.
func (i *Identity) ResolveGitHubLogin(ctx context.Context, login ResolveGitHubLogin) (GitHubLoginResolved, error) {
	return i.store.ResolveGitHubLogin(ctx, login)
}
