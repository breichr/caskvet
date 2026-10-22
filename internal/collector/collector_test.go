package collector

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fixtureServer serves recorded API payloads from testdata/api, mapping
// URL paths to fixture files the same way the real APIs do.
type fixtureServer struct {
	*httptest.Server
	requests atomic.Int64
}

func newFixtureServer(t *testing.T) *fixtureServer {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("testdata", "api"))
	if err != nil {
		t.Fatal(err)
	}
	fs := &fixtureServer{}
	mux := http.NewServeMux()
	mux.HandleFunc("/cask/", func(w http.ResponseWriter, r *http.Request) {
		fs.requests.Add(1)
		http.ServeFile(w, r, filepath.Join(root, "cask", filepath.Base(r.URL.Path)))
	})
	mux.HandleFunc("/repos/", func(w http.ResponseWriter, r *http.Request) {
		fs.requests.Add(1)
		rest := strings.TrimPrefix(r.URL.Path, "/repos/")
		if strings.HasSuffix(rest, "/releases/latest") {
			rest = strings.TrimSuffix(rest, "/releases/latest")
			http.ServeFile(w, r, filepath.Join(root, "repos", filepath.FromSlash(rest), "release.json"))
			return
		}
		http.ServeFile(w, r, filepath.Join(root, "repos", filepath.FromSlash(rest), "repo.json"))
	})
	fs.Server = httptest.NewServer(mux)
	t.Cleanup(fs.Server.Close)
	return fs
}

// testClient returns a Client pointed at the fixture server, with the
// cache disabled so tests exercise the network path deterministically.
func testClient(t *testing.T, fs *fixtureServer, opts ...Options) *Client {
	t.Helper()
	base := opts
	o := append([]Options{
		WithHomebrewBaseURL(fs.URL + "/cask"),
		WithGitHubBaseURL(fs.URL),
		WithCacheDir(""),
	}, base...)
	c, err := New(o...)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestCasksBatchMixedResults(t *testing.T) {
	fs := newFixtureServer(t)
	c := testClient(t, fs)

	results := c.Casks(context.Background(), []string{
		"multi-app", "old-tool", "login-item-cask", "does-not-exist",
		"multi-app", // duplicate must not produce a second result entry
	})

	if len(results) != 4 {
		t.Fatalf("want 4 results, got %d: %v", len(results), results)
	}

	got := results["multi-app"]
	if got.Err != nil {
		t.Fatalf("multi-app: unexpected error: %v", got.Err)
	}
	if len(got.Meta.AppBundles) != 2 {
		t.Errorf("multi-app: want 2 app bundles, got %v", got.Meta.AppBundles)
	}
	if got.Meta.Homepage != "https://github.com/breichr/multi-app-suite" {
		t.Errorf("multi-app: homepage = %q", got.Meta.Homepage)
	}
	if len(got.Meta.LaunchctlPaths) != 1 || got.Meta.LaunchctlPaths[0] != "~/Library/LaunchAgents/com.example.multiapp.plist" {
		t.Errorf("multi-app: launchctl paths = %v", got.Meta.LaunchctlPaths)
	}
	if got.Meta.Version != "1.2.3" {
		t.Errorf("multi-app: version = %q", got.Meta.Version)
	}

	dep := results["old-tool"]
	if dep.Err != nil {
		t.Fatalf("old-tool: unexpected error: %v", dep.Err)
	}
	if !dep.Meta.Deprecated {
		t.Error("old-tool: Deprecated = false, want true")
	}
	if dep.Meta.ReplacementToken != "new-tool" {
		t.Errorf("old-tool: replacement = %q, want new-tool", dep.Meta.ReplacementToken)
	}
	if dep.Meta.DeprecationReason != "discontinued_by_vendor" {
		t.Errorf("old-tool: deprecation reason = %q", dep.Meta.DeprecationReason)
	}
	if dep.Meta.DeprecationDate != "2024-06-01" {
		t.Errorf("old-tool: deprecation date = %q", dep.Meta.DeprecationDate)
	}

	login := results["login-item-cask"]
	if login.Err != nil {
		t.Fatalf("login-item-cask: unexpected error: %v", login.Err)
	}
	if len(login.Meta.LoginItems) != 1 || login.Meta.LoginItems[0] != "Login Item Cask.app" {
		t.Errorf("login-item-cask: login items = %v", login.Meta.LoginItems)
	}
	if len(login.Meta.AppBundles) != 0 {
		t.Errorf("login-item-cask: want no app bundles, got %v", login.Meta.AppBundles)
	}

	missing := results["does-not-exist"]
	if !errors.Is(missing.Err, ErrNotFound) {
		t.Errorf("does-not-exist: want ErrNotFound, got %v", missing.Err)
	}
}

func TestCaskInvalidTokenRejected(t *testing.T) {
	fs := newFixtureServer(t)
	c := testClient(t, fs)

	for _, token := range []string{"", "../etc/passwd", "Foo Bar", "UPPER"} {
		_, err := c.Cask(context.Background(), token)
		if !errors.Is(err, ErrInvalidToken) {
			t.Errorf("token %q: want ErrInvalidToken, got %v", token, err)
		}
	}

	// Versioned tokens with "@" are valid Homebrew tokens.
	fs2 := newFixtureServer(t)
	c2 := testClient(t, fs2)
	if err := newCaskFixture(t, fs2, "1password-cli@1"); err != nil {
		t.Fatal(err)
	}
	meta, err := c2.Cask(context.Background(), "1password-cli@1")
	if err != nil {
		t.Fatalf("versioned token must be accepted: %v", err)
	}
	if meta.Token != "1password-cli@1" {
		t.Errorf("token = %q", meta.Token)
	}
}

// newCaskFixture writes a minimal cask fixture the server can serve.
func newCaskFixture(t *testing.T, fs *fixtureServer, token string) error {
	t.Helper()
	path := filepath.Join("testdata", "api", "cask", token+".json")
	return os.WriteFile(path, []byte(`{"token":"`+token+`","name":["X"],"version":"1.0","artifacts":[]}`), 0o644)
}

func TestCacheServesFreshEntriesWithoutNetwork(t *testing.T) {
	fs := newFixtureServer(t)
	dir := t.TempDir()
	c, err := New(
		WithHomebrewBaseURL(fs.URL+"/cask"),
		WithGitHubBaseURL(fs.URL),
		WithCacheDir(dir),
		WithCacheTTL(time.Hour),
	)
	if err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 3; i++ {
		_, err := c.Cask(context.Background(), "multi-app")
		if err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	if n := fs.requests.Load(); n != 1 {
		t.Errorf("network requests = %d, want 1 (later calls must hit cache)", n)
	}

	// The cache directory holds exactly one JSON entry per distinct URL.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("cache files = %d, want 1", len(entries))
	}
}

func TestCacheRevalidatesWithETag(t *testing.T) {
	var hits atomic.Int64
	body := []byte(`{"token":"etag-cask","name":["Etag Cask"],"version":"1.0","artifacts":[]}`)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("ETag", `"v1"`)
		if r.Header.Get("If-None-Match") == `"v1"` {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Write(body)
	}))
	defer srv.Close()

	// TTL 0: entries are never "fresh", so every call revalidates.
	c, err := New(
		WithHomebrewBaseURL(srv.URL+"/cask"),
		WithGitHubBaseURL(srv.URL),
		WithCacheDir(t.TempDir()),
		WithCacheTTL(0),
	)
	if err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 2; i++ {
		meta, err := c.Cask(context.Background(), "etag-cask")
		if err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
		if meta.Token != "etag-cask" {
			t.Fatalf("call %d: token = %q", i, meta.Token)
		}
	}
	if n := hits.Load(); n != 2 {
		t.Errorf("server hits = %d, want 2 (second call must revalidate via ETag)", n)
	}
}

func TestGitHubTokenHeader(t *testing.T) {
	var auth atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth.Store(r.Header.Get("Authorization"))
		w.Write([]byte(`{"full_name":"octocat/example","archived":false,"pushed_at":"2026-01-01T00:00:00Z"}`))
	}))
	defer srv.Close()

	c, err := New(
		WithGitHubBaseURL(srv.URL),
		WithCacheDir(""),
		WithToken("secret-token"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.GitHubRepo(context.Background(), "octocat", "example"); err != nil {
		t.Fatal(err)
	}
	if got, _ := auth.Load().(string); got != "Bearer secret-token" {
		t.Errorf("Authorization header = %q, want Bearer secret-token", got)
	}
}

func TestGitHubReposBatch(t *testing.T) {
	fs := newFixtureServer(t)
	c := testClient(t, fs)

	repos := [][2]string{{"breichr", "multi-app-suite"}, {"breichr", "abandoned"}, {"breichr", "missing"}}
	results := c.GitHubRepos(context.Background(), repos)

	if len(results) != 3 {
		t.Fatalf("want 3 results, got %d", len(results))
	}
	active := results["breichr/multi-app-suite"]
	if active.Err != nil {
		t.Fatalf("multi-app-suite: %v", active.Err)
	}
	if active.RepoData.Archived {
		t.Error("multi-app-suite: Archived = true, want false")
	}
	if active.RepoData.PushedAt.Year() != 2026 {
		t.Errorf("multi-app-suite: pushed_at = %v", active.RepoData.PushedAt)
	}

	archived := results["breichr/abandoned"]
	if archived.Err != nil {
		t.Fatalf("abandoned: %v", archived.Err)
	}
	if !archived.RepoData.Archived {
		t.Error("abandoned: Archived = false, want true")
	}

	if got := results["breichr/missing"].Err; !errors.Is(got, ErrNotFound) {
		t.Errorf("missing: want ErrNotFound, got %v", got)
	}
}

func TestLatestRelease(t *testing.T) {
	fs := newFixtureServer(t)
	c := testClient(t, fs)

	rel, err := c.LatestRelease(context.Background(), "breichr", "multi-app-suite")
	if err != nil {
		t.Fatal(err)
	}
	if rel.TagName != "v1.3.0" {
		t.Errorf("tag = %q, want v1.3.0", rel.TagName)
	}

	_, err = c.LatestRelease(context.Background(), "breichr", "abandoned")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("abandoned release: want ErrNotFound, got %v", err)
	}
}

func TestRateLimitSurfacesAsSentinel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-RateLimit-Remaining", "0")
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	c, err := New(WithGitHubBaseURL(srv.URL), WithCacheDir(""))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.GitHubRepo(context.Background(), "octocat", "example"); !errors.Is(err, ErrRateLimited) {
		t.Errorf("want ErrRateLimited, got %v", err)
	}

	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv2.Close()
	c2, err := New(WithGitHubBaseURL(srv2.URL), WithCacheDir(""))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c2.GitHubRepo(context.Background(), "octocat", "example"); !errors.Is(err, ErrRateLimited) {
		t.Errorf("429: want ErrRateLimited, got %v", err)
	}
}

func TestParseGitHubOwnerRepo(t *testing.T) {
	ok := []struct {
		url, owner, repo string
	}{
		{"https://github.com/breichr/caskvet", "breichr", "caskvet"},
		{"https://www.github.com/breichr/caskvet/", "breichr", "caskvet"},
		{"https://github.com/breichr/caskvet/releases", "breichr", "caskvet"},
		{"http://github.com/foo/bar", "foo", "bar"},
	}
	for _, tc := range ok {
		owner, repo, err := ParseGitHubOwnerRepo(tc.url)
		if err != nil {
			t.Errorf("%s: unexpected error %v", tc.url, err)
			continue
		}
		if owner != tc.owner || repo != tc.repo {
			t.Errorf("%s: got %s/%s, want %s/%s", tc.url, owner, repo, tc.owner, tc.repo)
		}
	}

	notGitHub := []string{
		"",
		"https://gitlab.com/foo/bar",
		"https://example.com/foo/bar",
		"https://github.com/foo",
		"://github.com/foo/bar",
	}
	for _, u := range notGitHub {
		if _, _, err := ParseGitHubOwnerRepo(u); !errors.Is(err, ErrNotGitHub) {
			t.Errorf("%s: want ErrNotGitHub, got %v", u, err)
		}
	}
}
