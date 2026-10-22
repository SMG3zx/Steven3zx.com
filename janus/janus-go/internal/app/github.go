package app

import "context"

// RepositoryList is the repositories an Account can import. Connected is false
// when the Account has not connected GitHub.
type RepositoryList struct {
	Repositories []Repository `json:"repositories"`
	Connected    bool         `json:"connected"`
}

// GitHub lists the repositories, branches and commits an Account can import,
// read by the projects service with the Account's GitHub Connection. The
// connection itself is the identity service's; it keeps no state.
type GitHub struct {
	catalog Catalog
}

// NewGitHub returns a GitHub that lists repositories through catalog.
func NewGitHub(catalog Catalog) *GitHub {
	return &GitHub{catalog: catalog}
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
