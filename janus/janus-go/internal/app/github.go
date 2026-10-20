package app

import (
	"context"
	"errors"
	"sort"
)

// RepositoryList is the repositories an Account can import. Connected is false
// when the Account has not connected GitHub.
type RepositoryList struct {
	Repositories []Repository `json:"repositories"`
	Connected    bool         `json:"connected"`
}

// ErrNotConnected means the Account has no GitHub Connection.
var ErrNotConnected = errors.New("connect GitHub before importing a repository")

// GitHub reads from GitHub on behalf of an Account, using the access token the
// GitHub Connection stores. It keeps no state, so callers use it directly.
type GitHub struct {
	// API is the code host the Service reads from.
	API   GitHost
	store Store
}

// NewGitHub returns a GitHub that reads tokens from store and repository
// metadata from host.
func NewGitHub(store Store, host GitHost) *GitHub {
	return &GitHub{API: host, store: store}
}

// Token returns the Account's GitHub access token, and whether the Account is
// connected at all.
func (s *GitHub) Token(ctx context.Context, ownerID string) (token string, connected bool, err error) {
	return s.store.FindGitHubToken(ctx, ownerID)
}

// Disconnect removes the Account's stored token. Its sign-in identities stay.
func (s *GitHub) Disconnect(ctx context.Context, ownerID string) error {
	return s.store.DisconnectGitHub(ctx, ownerID)
}

// Repositories lists the repositories the Account can see. An Account that is
// not connected gets an empty list and no error.
func (s *GitHub) Repositories(ctx context.Context, ownerID string) (RepositoryList, error) {
	token, connected, err := s.Token(ctx, ownerID)
	if err != nil || !connected {
		return RepositoryList{Connected: connected}, err
	}
	repositories, err := s.API.Repositories(ctx, token)
	return RepositoryList{Repositories: repositories, Connected: true}, err
}

// Branches lists the branches of a repository, default branch first.
func (s *GitHub) Branches(ctx context.Context, ownerID, repository string) ([]Branch, error) {
	token, err := s.connectedToken(ctx, ownerID)
	if err != nil {
		return nil, err
	}
	details, err := s.API.Repository(ctx, token, repository)
	if err != nil {
		return nil, err
	}
	branches, err := s.API.Branches(ctx, token, repository)
	if err != nil {
		return nil, err
	}
	sort.SliceStable(branches, func(i, j int) bool {
		return branches[i].Name == details.DefaultBranch && branches[j].Name != details.DefaultBranch
	})
	return branches, nil
}

// Commits lists the 50 newest commits of a branch.
func (s *GitHub) Commits(ctx context.Context, ownerID, repository, branch string) ([]Commit, error) {
	token, err := s.connectedToken(ctx, ownerID)
	if err != nil {
		return nil, err
	}
	return s.API.Commits(ctx, token, repository, branch)
}

func (s *GitHub) connectedToken(ctx context.Context, ownerID string) (string, error) {
	token, connected, err := s.Token(ctx, ownerID)
	if err != nil {
		return "", err
	}
	if !connected {
		return "", ErrNotConnected
	}
	return token, nil
}

// Connect stores the GitHub Connection of an Account, replacing any earlier one.
func (s *GitHub) Connect(ctx context.Context, connection ConnectGitHub) error {
	return s.store.ConnectGitHub(ctx, connection)
}
