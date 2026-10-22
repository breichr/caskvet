package collector

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"
)

// GitHubRepo is the slice of repository data the maintenance score needs.
type GitHubRepo struct {
	FullName    string    `json:"full_name"`
	HTMLURL     string    `json:"html_url"`
	Description string    `json:"description"`
	Archived    bool      `json:"archived"`
	PushedAt    time.Time `json:"pushed_at"`
}

// GitHubRelease is the latest release of a repository, for the currency
// dimension.
type GitHubRelease struct {
	TagName     string    `json:"tag_name"`
	HTMLURL     string    `json:"html_url"`
	PublishedAt time.Time `json:"published_at"`
}

// GitHubResult pairs a repository (owner/repo) with its data or error.
type GitHubResult struct {
	Repo     string
	RepoData GitHubRepo
	Err      error
}

// GitHubRepo fetches repository metadata for owner/repo.
func (c *Client) GitHubRepo(ctx context.Context, owner, repo string) (GitHubRepo, error) {
	apiURL := fmt.Sprintf("%s/repos/%s/%s", c.githubBaseURL, owner, repo)
	body, err := c.fetch(ctx, apiURL, true)
	if err != nil {
		return GitHubRepo{}, err
	}
	var out GitHubRepo
	if err := json.Unmarshal(body, &out); err != nil {
		return GitHubRepo{}, fmt.Errorf("collector: decoding repo %s/%s: %w", owner, repo, err)
	}
	return out, nil
}

// GitHubRepos fetches repository metadata for many owner/repo pairs with
// bounded concurrency. Results are keyed by "owner/repo".
func (c *Client) GitHubRepos(ctx context.Context, repos [][2]string) map[string]GitHubResult {
	results := make(map[string]GitHubResult, len(repos))
	var mu sync.Mutex
	seen := make(map[string]bool, len(repos))

	type repoJob struct{ owner, repo string }
	unique := make([]repoJob, 0, len(repos))
	for _, pair := range repos {
		key := pair[0] + "/" + pair[1]
		if !seen[key] {
			seen[key] = true
			unique = append(unique, repoJob{pair[0], pair[1]})
		}
	}

	merge(ctx, unique, c.maxWorkers, func(ctx context.Context, job repoJob) {
		data, err := c.GitHubRepo(ctx, job.owner, job.repo)
		mu.Lock()
		results[job.owner+"/"+job.repo] = GitHubResult{Repo: job.owner + "/" + job.repo, RepoData: data, Err: err}
		mu.Unlock()
	})
	return results
}

// ReleaseResult pairs a repository (owner/repo) with its latest release
// or fetch error.
type ReleaseResult struct {
	Repo    string
	Release GitHubRelease
	Err     error
}

// LatestReleases fetches the latest release for many repositories with
// bounded concurrency. Results are keyed by "owner/repo".
func (c *Client) LatestReleases(ctx context.Context, repos [][2]string) map[string]ReleaseResult {
	results := make(map[string]ReleaseResult, len(repos))
	var mu sync.Mutex
	seen := make(map[string]bool, len(repos))

	type repoJob struct{ owner, repo string }
	unique := make([]repoJob, 0, len(repos))
	for _, pair := range repos {
		key := pair[0] + "/" + pair[1]
		if !seen[key] {
			seen[key] = true
			unique = append(unique, repoJob{pair[0], pair[1]})
		}
	}

	merge(ctx, unique, c.maxWorkers, func(ctx context.Context, job repoJob) {
		release, err := c.LatestRelease(ctx, job.owner, job.repo)
		mu.Lock()
		results[job.owner+"/"+job.repo] = ReleaseResult{Repo: job.owner + "/" + job.repo, Release: release, Err: err}
		mu.Unlock()
	})
	return results
}

// LatestRelease fetches the latest non-draft, non-prerelease release.
// A repository without releases yields ErrNotFound; callers report
// "unknown", never a fabricated value.
func (c *Client) LatestRelease(ctx context.Context, owner, repo string) (GitHubRelease, error) {
	apiURL := fmt.Sprintf("%s/repos/%s/%s/releases/latest", c.githubBaseURL, owner, repo)
	body, err := c.fetch(ctx, apiURL, true)
	if err != nil {
		return GitHubRelease{}, err
	}
	var out GitHubRelease
	if err := json.Unmarshal(body, &out); err != nil {
		return GitHubRelease{}, fmt.Errorf("collector: decoding release %s/%s: %w", owner, repo, err)
	}
	return out, nil
}

// ParseGitHubOwnerRepo extracts owner/repo from a repository URL such as a
// cask homepage. It returns ErrNotGitHub for any other host or malformed
// input; the caller must treat that as "unknown", never as risk.
func ParseGitHubOwnerRepo(rawURL string) (owner, repo string, err error) {
	if rawURL == "" {
		return "", "", ErrNotGitHub
	}
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || u.Hostname() != "github.com" && u.Hostname() != "www.github.com" {
		return "", "", ErrNotGitHub
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return "", "", ErrNotGitHub
	}
	return parts[0], parts[1], nil
}
