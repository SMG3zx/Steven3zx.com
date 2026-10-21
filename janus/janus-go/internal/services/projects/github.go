package projects

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"janus/internal/contract"
	contractid "janus/internal/contract/identity"
	contractprojects "janus/internal/contract/projects"
	"janus/internal/platform/natsx"
)

// gitHub reads repository metadata from the GitHub REST API with an Account's
// token.
type gitHub struct {
	http *http.Client
	base string
}

func (g *gitHub) get(ctx context.Context, token, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(g.base, "/")+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := g.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			return fmt.Errorf("GitHub API denied the request (status %d); reconnect GitHub if access was revoked", resp.StatusCode)
		}
		return fmt.Errorf("GitHub API returned status %d", resp.StatusCode)
	}
	if err = json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(out); err != nil {
		return fmt.Errorf("decode GitHub API response: %w", err)
	}
	return nil
}

func splitRepository(fullName string) (string, string, error) {
	owner, repo, ok := strings.Cut(fullName, "/")
	if !ok || owner == "" || repo == "" || strings.Contains(repo, "/") {
		return "", "", invalid("invalid repository")
	}
	return owner, repo, nil
}

func (g *gitHub) repositories(ctx context.Context, token string) ([]contractprojects.Repository, error) {
	all := make([]contractprojects.Repository, 0)
	for page := 1; page <= 10; page++ {
		var batch []contractprojects.Repository
		path := fmt.Sprintf("/user/repos?per_page=100&sort=updated&affiliation=owner,collaborator,organization_member&page=%d", page)
		if err := g.get(ctx, token, path, &batch); err != nil {
			return nil, err
		}
		all = append(all, batch...)
		if len(batch) < 100 {
			break
		}
	}
	return all, nil
}

func (g *gitHub) repository(ctx context.Context, token, fullName string) (contractprojects.Repository, error) {
	owner, repo, err := splitRepository(fullName)
	if err != nil {
		return contractprojects.Repository{}, err
	}
	var result contractprojects.Repository
	err = g.get(ctx, token, fmt.Sprintf("/repos/%s/%s", url.PathEscape(owner), url.PathEscape(repo)), &result)
	return result, err
}

func (g *gitHub) branches(ctx context.Context, token, fullName string) ([]contractprojects.Branch, error) {
	owner, repo, err := splitRepository(fullName)
	if err != nil {
		return nil, err
	}
	var result []contractprojects.Branch
	err = g.get(ctx, token, fmt.Sprintf("/repos/%s/%s/branches?per_page=100", url.PathEscape(owner), url.PathEscape(repo)), &result)
	return result, err
}

func (g *gitHub) commits(ctx context.Context, token, fullName, branch string) ([]contractprojects.Commit, error) {
	owner, repo, err := splitRepository(fullName)
	if err != nil {
		return nil, err
	}
	var result []struct {
		SHA    string `json:"sha"`
		Commit struct {
			Message string `json:"message"`
			Author  struct {
				Date time.Time `json:"date"`
			} `json:"author"`
		} `json:"commit"`
	}
	path := fmt.Sprintf("/repos/%s/%s/commits?per_page=50&sha=%s", url.PathEscape(owner), url.PathEscape(repo), url.QueryEscape(branch))
	if err := g.get(ctx, token, path, &result); err != nil {
		return nil, err
	}
	commits := make([]contractprojects.Commit, 0, len(result))
	for _, c := range result {
		commits = append(commits, contractprojects.Commit{SHA: c.SHA, Message: c.Commit.Message, Date: c.Commit.Author.Date})
	}
	return commits, nil
}

// errNotConnected is the answer for an Account without a GitHub Connection.
var errNotConnected = invalid("connect GitHub before importing a repository")

// token asks the identity service for the Owner's GitHub token.
func (s *Service) token(ctx context.Context, owner string) (string, bool, error) {
	var found contractid.GitHubTokenFound
	if err := natsx.Call(ctx, s.conn, contractid.SubjectGitHubToken, contractid.GitHubToken{UserID: owner}, &found); err != nil {
		return "", false, fmt.Errorf("read the GitHub Connection: %w", err)
	}
	return found.Token, found.Connected, nil
}

func (s *Service) connectedToken(ctx context.Context, owner string) (string, error) {
	token, connected, err := s.token(ctx, owner)
	if err != nil {
		return "", err
	}
	if !connected {
		return "", errNotConnected
	}
	return token, nil
}

// upstream reports a GitHub failure as unavailable, with its message, so the
// gateway shows it.
func upstream(err error) error {
	if _, ok := err.(*contract.Error); ok {
		return err
	}
	return contract.Errorf(contract.StatusUnavailable, "%s", err.Error())
}

func (s *Service) repositories(ctx context.Context, owner contractprojects.Owner) (contractprojects.Repositories, error) {
	token, connected, err := s.token(ctx, owner.OwnerID)
	if err != nil || !connected {
		return contractprojects.Repositories{Repositories: []contractprojects.Repository{}, Connected: connected}, err
	}
	list, err := s.github.repositories(ctx, token)
	if err != nil {
		return contractprojects.Repositories{}, upstream(err)
	}
	return contractprojects.Repositories{Repositories: list, Connected: true}, nil
}

// branches lists a repository's branches, default branch first.
func (s *Service) branches(ctx context.Context, key contractprojects.RepositoryKey) (contractprojects.Branches, error) {
	token, err := s.connectedToken(ctx, key.OwnerID)
	if err != nil {
		return contractprojects.Branches{}, err
	}
	details, err := s.github.repository(ctx, token, key.Repository)
	if err != nil {
		return contractprojects.Branches{}, upstream(err)
	}
	branches, err := s.github.branches(ctx, token, key.Repository)
	if err != nil {
		return contractprojects.Branches{}, upstream(err)
	}
	sort.SliceStable(branches, func(i, j int) bool {
		return branches[i].Name == details.DefaultBranch && branches[j].Name != details.DefaultBranch
	})
	return contractprojects.Branches{Branches: branches}, nil
}

func (s *Service) commits(ctx context.Context, key contractprojects.RepositoryKey) (contractprojects.Commits, error) {
	token, err := s.connectedToken(ctx, key.OwnerID)
	if err != nil {
		return contractprojects.Commits{}, err
	}
	commits, err := s.github.commits(ctx, token, key.Repository, key.Branch)
	if err != nil {
		return contractprojects.Commits{}, upstream(err)
	}
	return contractprojects.Commits{Commits: commits}, nil
}
