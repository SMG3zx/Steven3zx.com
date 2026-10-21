// Package identity is the contract of the identity service: Users, their
// Provider Identities and local credentials, and their GitHub Connection.
//
// Failures are contract.Error values: StatusConflict when an email already has
// a local account or a GitHub account clashes with another Account,
// StatusInvalid for an incomplete request.
package identity

// ServiceName is the service's name in NATS service discovery.
const ServiceName = "identity"

// The subjects the identity service answers.
const (
	SubjectCreateLocalUser    = "janus.identity.local_user.create"
	SubjectFindLocalUser      = "janus.identity.local_user.find"
	SubjectFindUser           = "janus.identity.user.find"
	SubjectDeleteUser         = "janus.identity.user.delete"
	SubjectResolveGoogle      = "janus.identity.google.resolve"
	SubjectResolveGitHubLogin = "janus.identity.github_login.resolve"
	SubjectConnectGitHub      = "janus.identity.github.connect"
	SubjectGitHubToken        = "janus.identity.github.token"
	SubjectDisconnectGitHub   = "janus.identity.github.disconnect"
)

// Empty is the reply of a request that returns nothing.
type Empty struct{}

// CreateLocalUser creates a User with a local credential. The caller chooses
// the ID and hashes the password.
type CreateLocalUser struct {
	ID           string `json:"id"`
	Email        string `json:"email"`
	PasswordHash string `json:"password_hash"`
}

// FindLocalUser looks up a local credential by email, ignoring case.
type FindLocalUser struct {
	Email string `json:"email"`
}

// LocalUser is the answer to FindLocalUser. Found is false when no local
// account has the email.
type LocalUser struct {
	Found        bool   `json:"found"`
	ID           string `json:"id,omitempty"`
	Email        string `json:"email,omitempty"`
	PasswordHash string `json:"password_hash,omitempty"`
}

// FindUser looks up a User by ID.
type FindUser struct {
	ID string `json:"id"`
}

// User is the answer to FindUser. Found is false when there is no such User.
type User struct {
	Found       bool   `json:"found"`
	ID          string `json:"id,omitempty"`
	Email       string `json:"email,omitempty"`
	DisplayName string `json:"display_name,omitempty"`
}

// DeleteUser removes a User with its credentials, Provider Identities and
// GitHub Connection. It does not touch what other services keep for the User.
type DeleteUser struct {
	ID string `json:"id"`
}

// UserDeleted answers DeleteUser. Deleted is false when there was no such User.
type UserDeleted struct {
	Deleted bool `json:"deleted"`
}

// ResolveIdentity asks for the User behind a Google identity or a GitHub
// login, matched by the provider's subject, never by email.
type ResolveIdentity struct {
	Subject     string `json:"subject"`
	Email       string `json:"email"`
	DisplayName string `json:"display_name"`
}

// Resolved answers ResolveIdentity. A new subject creates a User. For a GitHub
// login, EmailInUse means the email belongs to another Account, so nothing was
// created and the caller must not sign in.
type Resolved struct {
	ID          string `json:"id,omitempty"`
	Email       string `json:"email,omitempty"`
	DisplayName string `json:"display_name,omitempty"`
	EmailInUse  bool   `json:"email_in_use,omitempty"`
}

// ConnectGitHub stores a User's GitHub Connection, replacing an earlier one
// for the same GitHub account. The token is encrypted at rest.
type ConnectGitHub struct {
	UserID      string `json:"user_id"`
	Subject     string `json:"subject"`
	Email       string `json:"email"`
	DisplayName string `json:"display_name"`
	Token       string `json:"token"`
}

// GitHubToken asks for a User's GitHub access token.
type GitHubToken struct {
	UserID string `json:"user_id"`
}

// GitHubTokenFound answers GitHubToken. Connected is false when the User has no
// GitHub Connection.
type GitHubTokenFound struct {
	Connected bool   `json:"connected"`
	Token     string `json:"token,omitempty"`
}

// DisconnectGitHub removes a User's GitHub Connection. Its Provider Identities
// stay.
type DisconnectGitHub struct {
	UserID string `json:"user_id"`
}
