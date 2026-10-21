// Package identitynats is the app.Identities adapter that asks the identity
// service over NATS. It translates app types to the identity contract and a
// 409 answer to an *app.Conflict.
package identitynats

import (
	"context"
	"errors"

	"github.com/nats-io/nats.go"

	"janus/internal/app"
	"janus/internal/contract"
	contractid "janus/internal/contract/identity"
	"janus/internal/platform/natsx"
)

// Client is an app.Identities over a NATS connection.
type Client struct{ conn *nats.Conn }

var _ app.Identities = (*Client)(nil)

// New returns a Client that sends its requests on conn.
func New(conn *nats.Conn) *Client { return &Client{conn: conn} }

// call sends one request and turns a failure the service reported back into
// the error the port documents.
func (c *Client) call(ctx context.Context, subject string, request, reply any) error {
	err := natsx.Call(ctx, c.conn, subject, request, reply)
	var remote *natsx.RemoteError
	if !errors.As(err, &remote) {
		return err
	}
	if remote.Status == contract.StatusConflict {
		return &app.Conflict{Reason: remote.Message}
	}
	return contract.FromStatus(remote.Status, remote.Message)
}

func (c *Client) CreateLocalUser(ctx context.Context, user app.CreateLocalUser) error {
	return c.call(ctx, contractid.SubjectCreateLocalUser, contractid.CreateLocalUser{ID: user.ID, Email: user.Email, PasswordHash: user.PasswordHash}, nil)
}

func (c *Client) FindLocalUser(ctx context.Context, email string) (app.LocalUserFound, error) {
	var found contractid.LocalUser
	if err := c.call(ctx, contractid.SubjectFindLocalUser, contractid.FindLocalUser{Email: email}, &found); err != nil {
		return app.LocalUserFound{}, err
	}
	return app.LocalUserFound{ID: found.ID, Email: found.Email, PasswordHash: found.PasswordHash, Found: found.Found}, nil
}

func (c *Client) FindUser(ctx context.Context, id string) (app.UserFound, error) {
	var found contractid.User
	if err := c.call(ctx, contractid.SubjectFindUser, contractid.FindUser{ID: id}, &found); err != nil {
		return app.UserFound{}, err
	}
	return app.UserFound{ID: found.ID, Email: found.Email, DisplayName: found.DisplayName, Found: found.Found}, nil
}

func (c *Client) DeleteUser(ctx context.Context, id string) (bool, error) {
	var deleted contractid.UserDeleted
	err := c.call(ctx, contractid.SubjectDeleteUser, contractid.DeleteUser{ID: id}, &deleted)
	return deleted.Deleted, err
}

func (c *Client) ResolveGoogleIdentity(ctx context.Context, identity app.ResolveGoogleIdentity) (app.GoogleIdentityResolved, error) {
	var resolved contractid.Resolved
	if err := c.call(ctx, contractid.SubjectResolveGoogle, contractid.ResolveIdentity(identity), &resolved); err != nil {
		return app.GoogleIdentityResolved{}, err
	}
	return app.GoogleIdentityResolved{ID: resolved.ID, Email: resolved.Email, DisplayName: resolved.DisplayName}, nil
}

func (c *Client) ResolveGitHubLogin(ctx context.Context, login app.ResolveGitHubLogin) (app.GitHubLoginResolved, error) {
	var resolved contractid.Resolved
	if err := c.call(ctx, contractid.SubjectResolveGitHubLogin, contractid.ResolveIdentity(login), &resolved); err != nil {
		return app.GitHubLoginResolved{}, err
	}
	return app.GitHubLoginResolved{ID: resolved.ID, Email: resolved.Email, DisplayName: resolved.DisplayName, EmailInUse: resolved.EmailInUse}, nil
}

func (c *Client) ConnectGitHub(ctx context.Context, connection app.ConnectGitHub) error {
	return c.call(ctx, contractid.SubjectConnectGitHub, contractid.ConnectGitHub(connection), nil)
}

func (c *Client) FindGitHubToken(ctx context.Context, userID string) (string, bool, error) {
	var found contractid.GitHubTokenFound
	err := c.call(ctx, contractid.SubjectGitHubToken, contractid.GitHubToken{UserID: userID}, &found)
	return found.Token, found.Connected, err
}

func (c *Client) DisconnectGitHub(ctx context.Context, userID string) error {
	return c.call(ctx, contractid.SubjectDisconnectGitHub, contractid.DisconnectGitHub{UserID: userID}, nil)
}
