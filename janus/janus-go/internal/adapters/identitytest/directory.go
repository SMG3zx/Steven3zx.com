package identitytest

import (
	"context"

	"janus/internal/app"
	"janus/internal/contract"
	contractid "janus/internal/contract/identity"
)

// Directory is the identity service as the gateway sees it, served by a Memory.
// A duplicate registration is a contract.ErrConflict, as it is over NATS.
type Directory struct{ memory *Memory }

// NewDirectory returns a Directory over memory.
func NewDirectory(memory *Memory) *Directory { return &Directory{memory: memory} }

// CreateLocalUser stores a new local account.
func (d *Directory) CreateLocalUser(ctx context.Context, user contractid.CreateLocalUser) error {
	err := d.memory.CreateLocalUser(ctx, app.CreateLocalUser{ID: user.ID, Email: user.Email, PasswordHash: user.PasswordHash})
	return asContract(err)
}

// FindLocalUser looks up the local account with an email.
func (d *Directory) FindLocalUser(ctx context.Context, email string) (contractid.LocalUser, error) {
	found, err := d.memory.FindLocalUser(ctx, email)
	return contractid.LocalUser{Found: found.Found, ID: found.ID, Email: found.Email, PasswordHash: found.PasswordHash}, err
}

// FindUser looks up a User by ID.
func (d *Directory) FindUser(ctx context.Context, id string) (contractid.User, error) {
	found, err := d.memory.FindUser(ctx, id)
	return contractid.User{Found: found.Found, ID: found.ID, Email: found.Email, DisplayName: found.DisplayName}, err
}

// ResolveGoogleIdentity returns the User behind a Google identity.
func (d *Directory) ResolveGoogleIdentity(ctx context.Context, identity contractid.ResolveIdentity) (contractid.Resolved, error) {
	resolved, err := d.memory.ResolveGoogleIdentity(ctx, app.ResolveGoogleIdentity{Subject: identity.Subject, Email: identity.Email, DisplayName: identity.DisplayName})
	return contractid.Resolved{ID: resolved.ID, Email: resolved.Email, DisplayName: resolved.DisplayName}, err
}

// ResolveGitHubLogin returns the User behind a GitHub login.
func (d *Directory) ResolveGitHubLogin(ctx context.Context, login contractid.ResolveIdentity) (contractid.Resolved, error) {
	resolved, err := d.memory.ResolveGitHubLogin(ctx, app.ResolveGitHubLogin{Subject: login.Subject, Email: login.Email, DisplayName: login.DisplayName})
	return contractid.Resolved{ID: resolved.ID, Email: resolved.Email, DisplayName: resolved.DisplayName, EmailInUse: resolved.EmailInUse}, err
}

// asContract turns the port's conflict into the contract's, so callers match
// one error whichever way the identity service is reached.
func asContract(err error) error {
	if app.IsConflict(err) {
		return contract.Errorf(contract.StatusConflict, "%s", err.Error())
	}
	return err
}
