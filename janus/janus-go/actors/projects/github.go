package projects

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const githubAPI = "https://api.github.com"

// Repository is GitHub repository metadata available to an authenticated user.
type Repository struct {
	ID            int64  `json:"id"`
	FullName      string `json:"full_name"`
	Description   string `json:"description"`
	Private       bool   `json:"private"`
	DefaultBranch string `json:"default_branch"`
	Language      string `json:"language"`
	Stars         int    `json:"stargazers_count"`
	Forks         int    `json:"forks_count"`
	CloneURL      string `json:"clone_url"`
	HTMLURL       string `json:"html_url"`
	Size          int64  `json:"size"`
}

// Branch identifies a repository branch and its current commit.
type Branch struct {
	Name   string `json:"name"`
	Commit struct {
		SHA string `json:"sha"`
	} `json:"commit"`
}

// Commit contains the display metadata Janus uses for a repository revision.
type Commit struct {
	SHA     string    `json:"sha"`
	Message string    `json:"message"`
	Date    time.Time `json:"date"`
}

// GitHubClient reads repository metadata from the GitHub API using a caller's token.
type GitHubClient struct {
	HTTP    *http.Client
	BaseURL string
}

// NewGitHubClient creates a client with the default GitHub API configuration.
func NewGitHubClient() *GitHubClient {
	return &GitHubClient{HTTP: &http.Client{Timeout: 15 * time.Second}, BaseURL: githubAPI}
}

func (g *GitHubClient) request(ctx context.Context, token, path string, out any) error {
	base := strings.TrimRight(g.BaseURL, "/")
	u, err := url.Parse(base + path)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("Authorization", "Bearer "+token)
	client := g.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
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

func (g *GitHubClient) Repositories(ctx context.Context, token string) ([]Repository, error) {
	all := make([]Repository, 0)
	for page := 1; page <= 10; page++ {
		var batch []Repository
		path := fmt.Sprintf("/user/repos?per_page=100&sort=updated&affiliation=owner,collaborator,organization_member&page=%d", page)
		if err := g.request(ctx, token, path, &batch); err != nil {
			return nil, err
		}
		all = append(all, batch...)
		if len(batch) < 100 {
			break
		}
	}
	return all, nil
}

func (g *GitHubClient) Branches(ctx context.Context, token, fullName string) ([]Branch, error) {
	owner, repo, ok := strings.Cut(fullName, "/")
	if !ok || owner == "" || repo == "" || strings.Contains(repo, "/") {
		return nil, fmt.Errorf("invalid repository")
	}
	var result []Branch
	err := g.request(ctx, token, fmt.Sprintf("/repos/%s/%s/branches?per_page=100", url.PathEscape(owner), url.PathEscape(repo)), &result)
	return result, err
}

func (g *GitHubClient) Commits(ctx context.Context, token, fullName, branch string) ([]Commit, error) {
	owner, repo, ok := strings.Cut(fullName, "/")
	if !ok || owner == "" || repo == "" || strings.Contains(repo, "/") {
		return nil, fmt.Errorf("invalid repository")
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
	err := g.request(ctx, token, path, &result)
	if err != nil {
		return nil, err
	}
	commits := make([]Commit, 0, len(result))
	for _, c := range result {
		commits = append(commits, Commit{SHA: c.SHA, Message: c.Commit.Message, Date: c.Commit.Author.Date})
	}
	return commits, nil
}

func (g *GitHubClient) Repository(ctx context.Context, token, fullName string) (Repository, error) {
	owner, repo, ok := strings.Cut(fullName, "/")
	if !ok || owner == "" || repo == "" || strings.Contains(repo, "/") {
		return Repository{}, fmt.Errorf("invalid repository")
	}
	var result Repository
	err := g.request(ctx, token, fmt.Sprintf("/repos/%s/%s", url.PathEscape(owner), url.PathEscape(repo)), &result)
	return result, err
}
