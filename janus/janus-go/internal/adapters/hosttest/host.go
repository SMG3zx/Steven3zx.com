package hosttest

import (
	"context"
	"errors"
	"slices"

	"janus/internal/app"
)

// World is what a code host knows: one Account's token and the repositories
// that token can see.
type World struct {
	Token        string
	Repositories []Repo
}

// Repo is a repository with its branches and, for each branch, its commits
// newest first.
type Repo struct {
	Repository app.Repository
	Branches   []app.Branch
	Commits    map[string][]app.Commit
}

// maxCommits is how many commits a host lists for a branch.
const maxCommits = 50

// Host is an app.GitHost that answers from a World.
type Host struct{ world World }

var _ app.GitHost = (*Host)(nil)

// NewHost returns a Host serving world.
func NewHost(world World) *Host { return &Host{world: world} }

var errDenied = errors.New("the host denied the request")

func (h *Host) find(token, fullName string) (Repo, error) {
	if token != h.world.Token {
		return Repo{}, errDenied
	}
	if !splitName(fullName) {
		return Repo{}, errors.New("invalid repository")
	}
	for _, repo := range h.world.Repositories {
		if repo.Repository.FullName == fullName {
			return repo, nil
		}
	}
	return Repo{}, errNotRepository
}

// Repositories lists every repository of the World.
func (h *Host) Repositories(_ context.Context, token string) ([]app.Repository, error) {
	if token != h.world.Token {
		return nil, errDenied
	}
	repositories := make([]app.Repository, 0, len(h.world.Repositories))
	for _, repo := range h.world.Repositories {
		repositories = append(repositories, repo.Repository)
	}
	return repositories, nil
}

// Repository returns one repository's metadata.
func (h *Host) Repository(_ context.Context, token, fullName string) (app.Repository, error) {
	repo, err := h.find(token, fullName)
	return repo.Repository, err
}

// Branches lists a repository's branches.
func (h *Host) Branches(_ context.Context, token, fullName string) ([]app.Branch, error) {
	repo, err := h.find(token, fullName)
	return slices.Clone(repo.Branches), err
}

// Commits lists the newest 50 commits of a branch.
func (h *Host) Commits(_ context.Context, token, fullName, branch string) ([]app.Commit, error) {
	repo, err := h.find(token, fullName)
	if err != nil {
		return nil, err
	}
	commits, ok := repo.Commits[branch]
	if !ok {
		return nil, errors.New("branch not found")
	}
	return slices.Clone(commits[:min(len(commits), maxCommits)]), nil
}
