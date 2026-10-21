package app

import "context"

// RepositoryList is the repositories an Account can import. Connected is false
// when the Account has not connected GitHub.
type RepositoryList struct {
	Repositories []Repository `json:"repositories"`
	Connected    bool         `json:"connected"`
}

// GitHub is an Account's GitHub Connection, kept by Identities, and the
// repositories it gives access to, read by the projects service. It keeps no
// state, so callers use it directly.
type GitHub struct {
	identities Identities
	catalog    Catalog
}

// NewGitHub returns a GitHub that keeps tokens in identities and lists
// repositories through catalog.
func NewGitHub(identities Identities, catalog Catalog) *GitHub {
	return &GitHub{identities: identities, catalog: catalog}
}

// Token returns the Account's GitHub access token, and whether the Account is
// connected at all.
func (s *GitHub) Token(ctx context.Context, ownerID string) (token string, connected bool, err error) {
	return s.identities.FindGitHubToken(ctx, ownerID)
}

// Disconnect removes the Account's stored token. Its sign-in identities stay.
func (s *GitHub) Disconnect(ctx context.Context, ownerID string) error {
	return s.identities.DisconnectGitHub(ctx, ownerID)
}

// Repositories lists the repositories the Account can see. An Account that is
// not connected gets an empty list and no error.
func (s *GitHub) Repositories(ctx context.Context, ownerID string) (RepositoryList, error) {
	return s.catalog.Repositories(ctx, ownerID)
}

// Branches lists the branches of a repository, default branch first.
func (s *GitHub) Branches(ctx context.Context, ownerID, repository string) ([]Branch, error) {
	return s.catalog.Branches(ctx, ownerID, repository)
}

// Commits lists the 50 newest commits of a branch.
func (s *GitHub) Commits(ctx context.Context, ownerID, repository, branch string) ([]Commit, error) {
	return s.catalog.Commits(ctx, ownerID, repository, branch)
}

// Connect stores the GitHub Connection of an Account, replacing any earlier one.
//
// A GitHub account linked to another Account, or a different GitHub account
// already connected, fails with a *Conflict.
func (s *GitHub) Connect(ctx context.Context, connection ConnectGitHub) error {
	return s.identities.ConnectGitHub(ctx, connection)
}
