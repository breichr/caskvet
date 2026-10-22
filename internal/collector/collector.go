// Package collector provides read-only access to the two external data
// sources caskvet uses: the Homebrew JSON API and the GitHub REST API.
//
// It owns batching, caching, and rate-limit friendliness. It contains no
// scoring logic whatsoever.
package collector

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	// DefaultHomebrewBaseURL is the Homebrew JSON API cask endpoint root.
	DefaultHomebrewBaseURL = "https://formulae.brew.sh/api/cask"
	// DefaultGitHubBaseURL is the GitHub REST API v3 root.
	DefaultGitHubBaseURL = "https://api.github.com"
	// DefaultCacheTTL is how long a cached response is considered fresh.
	DefaultCacheTTL = 24 * time.Hour
	// DefaultMaxWorkers bounds concurrent requests per batch call.
	DefaultMaxWorkers = 6

	userAgent       = "caskvet/0.1 (+https://github.com/breichr/caskvet)"
	maxBodySize     = 16 << 20 // 16 MiB per response
	cacheDirName    = "caskvet"
	cacheSubdirName = "cache"
)

// Errors returned by the collector. Callers map these to "unknown" data,
// never to a fabricated score.
var (
	// ErrNotFound means the resource does not exist upstream.
	ErrNotFound = errors.New("not found")
	// ErrRateLimited means the API refused the request for quota reasons.
	// The caller may retry later or degrade to unknown.
	ErrRateLimited = errors.New("rate limited")
	// ErrInvalidToken means the cask token is not a valid Homebrew token.
	ErrInvalidToken = errors.New("invalid cask token")
	// ErrNotGitHub means the URL does not point at a GitHub repository.
	ErrNotGitHub = errors.New("not a github repository")
)

// Options configures a Client.
type Options func(*Client)

// WithHomebrewBaseURL overrides the Homebrew JSON API root (tests).
func WithHomebrewBaseURL(url string) Options {
	return func(c *Client) { c.homebrewBaseURL = url }
}

// WithGitHubBaseURL overrides the GitHub REST API root (tests).
func WithGitHubBaseURL(url string) Options {
	return func(c *Client) { c.githubBaseURL = url }
}

// WithHTTPClient overrides the HTTP client used for all requests.
func WithHTTPClient(hc *http.Client) Options {
	return func(c *Client) { c.httpClient = hc }
}

// WithToken sets the GitHub API token. It overrides GITHUB_TOKEN.
func WithToken(token string) Options {
	return func(c *Client) { c.token = token }
}

// WithCacheDir overrides the cache directory. An empty string disables
// caching entirely.
func WithCacheDir(dir string) Options {
	return func(c *Client) {
		if dir == "" {
			c.cache = nil
			return
		}
		c.cache = &Cache{dir: dir}
	}
}

// WithCacheTTL overrides how long cached entries stay fresh. Zero means
// entries are always revalidated against the server (conditional requests).
func WithCacheTTL(ttl time.Duration) Options {
	return func(c *Client) {
		if c.cache == nil {
			return
		}
		c.cache.ttl = ttl
		c.cache.ttlSet = true
	}
}

// WithMaxWorkers overrides the per-batch concurrency limit.
func WithMaxWorkers(n int) Options {
	return func(c *Client) {
		if n > 0 {
			c.maxWorkers = n
		}
	}
}

// Client fetches cask and repository metadata from the documented APIs.
// It is safe for concurrent use.
type Client struct {
	homebrewBaseURL string
	githubBaseURL   string
	httpClient      *http.Client
	token           string
	cache           *Cache
	maxWorkers      int
}

// New returns a Client. The cache directory defaults to the user config
// directory (honors XDG_CONFIG_HOME; on macOS that is
// ~/Library/Application Support), the token defaults to the GITHUB_TOKEN
// environment variable, and the TTL to DefaultCacheTTL.
func New(opts ...Options) (*Client, error) {
	c := &Client{
		homebrewBaseURL: DefaultHomebrewBaseURL,
		githubBaseURL:   DefaultGitHubBaseURL,
		httpClient:      &http.Client{Timeout: 30 * time.Second},
		maxWorkers:      DefaultMaxWorkers,
	}
	if dir, err := os.UserConfigDir(); err == nil {
		c.cache = &Cache{dir: filepath.Join(dir, cacheDirName, cacheSubdirName)}
	} else if len(opts) == 0 {
		// Cache is a pure optimization; failing to resolve a config dir is
		// only fatal when nothing else configures the client.
		return nil, fmt.Errorf("collector: resolving config dir: %w", err)
	}
	if tok := os.Getenv("GITHUB_TOKEN"); tok != "" {
		c.token = tok
	}
	for _, opt := range opts {
		opt(c)
	}
	if c.cache != nil && !c.cache.ttlSet {
		c.cache.ttl = DefaultCacheTTL
	}
	return c, nil
}

// fetch retrieves a URL through the cache. Fresh cache entries are served
// without a network round trip; stale entries are revalidated with
// If-None-Match so an unchanged resource costs no body transfer.
func (c *Client) fetch(ctx context.Context, url string, github bool) ([]byte, error) {
	if cached, ok := c.cache.get(url); ok && cached.fresh(c.cache.ttlOrDefault()) {
		return []byte(cached.Body), nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("collector: building request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)
	if github {
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
		if c.token != "" {
			req.Header.Set("Authorization", "Bearer "+c.token)
		}
	}
	if cached, ok := c.cache.get(url); ok && cached.ETag != "" {
		req.Header.Set("If-None-Match", cached.ETag)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("collector: GET %s: %w", url, err)
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusNotModified:
		cached, ok := c.cache.get(url)
		if !ok {
			return nil, fmt.Errorf("collector: GET %s: 304 without cached entry", url)
		}
		cached.FetchedAt = time.Now()
		c.cache.put(&cached)
		return []byte(cached.Body), nil

	case resp.StatusCode == http.StatusOK:
		body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodySize))
		if err != nil {
			return nil, fmt.Errorf("collector: GET %s: reading body: %w", url, err)
		}
		c.cache.put(&cacheEntry{
			URL:       url,
			ETag:      resp.Header.Get("ETag"),
			FetchedAt: time.Now(),
			Status:    resp.StatusCode,
			Body:      string(body),
		})
		return body, nil

	case resp.StatusCode == http.StatusNotFound:
		return nil, ErrNotFound

	case resp.StatusCode == http.StatusTooManyRequests,
		resp.StatusCode == http.StatusForbidden && resp.Header.Get("X-RateLimit-Remaining") == "0":
		return nil, fmt.Errorf("collector: GET %s: %w", url, ErrRateLimited)

	default:
		return nil, fmt.Errorf("collector: GET %s: status %d", url, resp.StatusCode)
	}
}

// Cache stores HTTP responses as JSON files under a directory, keyed by
// the SHA-256 of the request URL. A nil *Cache is a valid disabled cache.
type Cache struct {
	dir    string
	ttl    time.Duration
	ttlSet bool
}

func (c *Cache) ttlOrDefault() time.Duration {
	if c.ttlSet {
		return c.ttl
	}
	return DefaultCacheTTL
}

type cacheEntry struct {
	URL       string    `json:"url"`
	ETag      string    `json:"etag,omitempty"`
	FetchedAt time.Time `json:"fetched_at"`
	Status    int       `json:"status"`
	Body      string    `json:"body"`
}

func (e *cacheEntry) fresh(ttl time.Duration) bool {
	return time.Since(e.FetchedAt) < ttl
}

func (c *Cache) key(url string) string {
	sum := sha256.Sum256([]byte(url))
	return hex.EncodeToString(sum[:])
}

// get returns the cached entry for url, if present and intact.
func (c *Cache) get(url string) (cacheEntry, bool) {
	var zero cacheEntry
	if c == nil {
		return zero, false
	}
	data, err := os.ReadFile(filepath.Join(c.dir, c.key(url)+".json"))
	if err != nil {
		return zero, false
	}
	var e cacheEntry
	if err := json.Unmarshal(data, &e); err != nil || e.URL != url {
		return zero, false
	}
	return e, true
}

// put stores an entry. Cache failures are silent: the cache is an
// optimization and must never fail a scan.
func (c *Cache) put(e *cacheEntry) {
	if c == nil {
		return
	}
	if err := os.MkdirAll(c.dir, 0o700); err != nil {
		return
	}
	data, err := json.Marshal(e)
	if err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(c.dir, c.key(e.URL)+".json"), data, 0o600)
}

// merge runs fn for every element of items across a bounded worker pool.
func merge[T any](ctx context.Context, items []T, workers int, fn func(context.Context, T)) {
	if len(items) == 0 {
		return
	}
	if workers <= 0 || workers > len(items) {
		workers = len(items)
	}
	jobs := make(chan T)
	go func() {
		defer close(jobs)
		for _, item := range items {
			select {
			case jobs <- item:
			case <-ctx.Done():
				return
			}
		}
	}()
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for item := range jobs {
				if ctx.Err() != nil {
					return
				}
				fn(ctx, item)
			}
		}()
	}
	wg.Wait()
}
