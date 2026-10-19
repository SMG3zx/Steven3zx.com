package github

import (
	"context"
	"errors"
	"sort"

	"janus/actors/persistence"
)

// RepositoryList is the repositories an Account can import. Connected is false
// when the Account has not connected GitHub.
type RepositoryList struct {
	Repositories []Repository `json:"repositories"`
	Connected    bool         `json:"connected"`
}

// ErrNotConnected means the Account has no GitHub Connection.
var ErrNotConnected = errors.New("connect GitHub before importing a repository")

// Service reads from GitHub on behalf of an Account, using the access token the
// GitHub Connection stores. It keeps no state, so callers use it directly.
type Service struct {
	// API is the GitHub API client. Tests point it at a local server.
	API   *Client
	store persistence.Store
}

// NewService returns a Service that reads tokens from store.
func NewService(store persistence.Store) *Service {
	return &Service{API: NewClient(), store: store}
}

// Token returns the Account's GitHub access token, and whether the Account is
// connected at all.
func (s *Service) Token(ctx context.Context, ownerID string) (token string, connected bool, err error) {
	return s.store.FindGitHubToken(ctx, ownerID)
}

// Disconnect removes the Account's stored token. Its sign-in identities stay.
func (s *Service) Disconnect(ctx context.Context, ownerID string) error {
	return s.store.DisconnectGitHub(ctx, ownerID)
}

// Repositories lists the repositories the Account can see. An Account that is
// not connected gets an empty list and no error.
func (s *Service) Repositories(ctx context.Context, ownerID string) (RepositoryList, error) {
	token, connected, err := s.Token(ctx, ownerID)
	if err != nil || !connected {
		return RepositoryList{Connected: connected}, err
	}
	repositories, err := s.API.Repositories(ctx, token)
	return RepositoryList{Repositories: repositories, Connected: true}, err
}

// Branches lists the branches of a repository, default branch first.
func (s *Service) Branches(ctx context.Context, ownerID, repository string) ([]Branch, error) {
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
func (s *Service) Commits(ctx context.Context, ownerID, repository, branch string) ([]Commit, error) {
	token, err := s.connectedToken(ctx, ownerID)
	if err != nil {
		return nil, err
	}
	return s.API.Commits(ctx, token, repository, branch)
}

func (s *Service) connectedToken(ctx context.Context, ownerID string) (string, error) {
	token, connected, err := s.Token(ctx, ownerID)
	if err != nil {
		return "", err
	}
	if !connected {
		return "", ErrNotConnected
	}
	return token, nil
}
