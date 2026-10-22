package identitynats

import (
	"context"
	"errors"

	"github.com/nats-io/nats.go"

	"janus/internal/contract"
	contractid "janus/internal/contract/identity"
	"janus/internal/platform/natsx"
)

// Gateway is the identity service as the gateway calls it: the contract's types,
// with a failure the service reported as a *contract.Error.
type Gateway struct{ conn *nats.Conn }

// NewGateway returns a Gateway that calls the identity service over conn.
func NewGateway(conn *nats.Conn) *Gateway { return &Gateway{conn: conn} }

func (g *Gateway) call(ctx context.Context, subject string, request, reply any) error {
	err := natsx.Call(ctx, g.conn, subject, request, reply)
	var remote *natsx.RemoteError
	if !errors.As(err, &remote) {
		return err
	}
	return contract.FromStatus(remote.Status, remote.Message)
}

// CreateLocalUser stores a new local account. A duplicate email is a
// contract.ErrConflict.
func (g *Gateway) CreateLocalUser(ctx context.Context, user contractid.CreateLocalUser) error {
	return g.call(ctx, contractid.SubjectCreateLocalUser, user, nil)
}

// FindLocalUser looks up the local account with an email, ignoring case.
func (g *Gateway) FindLocalUser(ctx context.Context, email string) (contractid.LocalUser, error) {
	var found contractid.LocalUser
	err := g.call(ctx, contractid.SubjectFindLocalUser, contractid.FindLocalUser{Email: email}, &found)
	return found, err
}

// FindUser looks up a User by ID.
func (g *Gateway) FindUser(ctx context.Context, id string) (contractid.User, error) {
	var found contractid.User
	err := g.call(ctx, contractid.SubjectFindUser, contractid.FindUser{ID: id}, &found)
	return found, err
}

// ResolveGoogleIdentity returns the User behind a Google identity, creating one on
// first sign-in.
func (g *Gateway) ResolveGoogleIdentity(ctx context.Context, identity contractid.ResolveIdentity) (contractid.Resolved, error) {
	var resolved contractid.Resolved
	err := g.call(ctx, contractid.SubjectResolveGoogle, identity, &resolved)
	return resolved, err
}

// ResolveGitHubLogin returns the User behind a GitHub login, creating one on first
// sign-in. EmailInUse reports that the email belongs to another Account.
func (g *Gateway) ResolveGitHubLogin(ctx context.Context, login contractid.ResolveIdentity) (contractid.Resolved, error) {
	var resolved contractid.Resolved
	err := g.call(ctx, contractid.SubjectResolveGitHubLogin, login, &resolved)
	return resolved, err
}
