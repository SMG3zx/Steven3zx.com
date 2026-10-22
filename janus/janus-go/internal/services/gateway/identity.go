package gateway

import (
	"context"

	contractid "janus/internal/contract/identity"
)

// identityService is the identity service as the gateway reaches it: the sign-ins that
// start a session, and the User a session belongs to. A duplicate registration
// is a contract.ErrConflict.
type identityService interface {
	CreateLocalUser(ctx context.Context, user contractid.CreateLocalUser) error
	FindLocalUser(ctx context.Context, email string) (contractid.LocalUser, error)
	FindUser(ctx context.Context, id string) (contractid.User, error)
	ResolveGoogleIdentity(ctx context.Context, identity contractid.ResolveIdentity) (contractid.Resolved, error)
	ResolveGitHubLogin(ctx context.Context, login contractid.ResolveIdentity) (contractid.Resolved, error)
	ConnectGitHub(ctx context.Context, connection contractid.ConnectGitHub) error
	FindGitHubToken(ctx context.Context, userID string) (contractid.GitHubTokenFound, error)
	DisconnectGitHub(ctx context.Context, userID string) error
	// DeleteAccount starts deleting the User's Account, or reports how far a
	// deletion has got. Without cancelActiveJobs an Account with unfinished jobs
	// is refused and the answer lists them.
	DeleteAccount(ctx context.Context, userID string, cancelActiveJobs bool) (contractid.AccountDeletion, error)
}
