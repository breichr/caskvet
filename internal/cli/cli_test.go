package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/breichr/caskvet/internal/localscan"
)

// recordingRunner replays recorded command output and records every
// invocation, including notifications. Unknown commands return empty
// output with no error so notification calls are observable.
type recordingRunner struct {
	mu          sync.Mutex
	responses   map[string]localscan.Output
	invocations []string
}

func (r *recordingRunner) Run(name string, args ...string) (localscan.Output, error) {
	key := name + " " + strings.Join(args, " ")
	r.mu.Lock()
	r.invocations = append(r.invocations, key)
	out := r.responses[key]
	r.mu.Unlock()
	return out, nil
}

func (r *recordingRunner) contains(needle string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, invocation := range r.invocations {
		if strings.Contains(invocation, needle) {
			return true
		}
	}
	return false
}

func (r *recordingRunner) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.invocations)
}

// apiServer serves canned Homebrew and GitHub JSON payloads.
type apiServer struct {
	homebrew *httptest.Server
	github   *httptest.Server
}

func newAPIServer(t *testing.T) *apiServer {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/cask/example.json", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{
  "token": "example",
  "name": ["Example"],
  "homepage": "https://github.com/acme/example",
  "version": "1.2.3",
  "artifacts": [{"app": ["Example.app"]}]
}`)
	})
	gmux := http.NewServeMux()
	gmux.HandleFunc("/repos/acme/example", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"full_name":"acme/example","archived":false,"pushed_at":"2026-10-01T00:00:00Z"}`)
	})
	gmux.HandleFunc("/repos/acme/example/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"tag_name":"1.2.3","published_at":"2026-10-01T00:00:00Z"}`)
	})

	srv := &apiServer{
		homebrew: httptest.NewServer(mux),
		github:   httptest.NewServer(gmux),
	}
	t.Cleanup(srv.homebrew.Close)
	t.Cleanup(srv.github.Close)
	return srv
}

// harness assembles a fully faked environment: local commands via the
// recording runner, APIs via httptest, snapshots in a temp dir.
type harness struct {
	runner       *recordingRunner
	snapDir      string
	api          *apiServer
	appPath      string
	out, errOut  *bytes.Buffer
	buildOptions func() Options
}

func newHarness(t *testing.T) (*harness, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()

	// Fake on-disk layout: /Applications and Caskroom under a temp root.
	root := t.TempDir()
	appDir := filepath.Join(root, "Applications")
	caskroom := filepath.Join(root, "Caskroom")
	if err := os.MkdirAll(filepath.Join(caskroom, "example", "1.2.3"), 0o755); err != nil {
		t.Fatal(err)
	}
	appPath := filepath.Join(appDir, "Example.app")
	if err := os.MkdirAll(appPath, 0o755); err != nil {
		t.Fatal(err)
	}
	launchDir := filepath.Join(root, "LaunchAgents")
	if err := os.MkdirAll(launchDir, 0o755); err != nil {
		t.Fatal(err)
	}

	h := &harness{
		runner:  &recordingRunner{responses: map[string]localscan.Output{}},
		snapDir: t.TempDir(),
		api:     newAPIServer(t),
		appPath: appPath,
		out:     &bytes.Buffer{},
		errOut:  &bytes.Buffer{},
	}

	h.runner.responses["brew list --cask --json --versions"] = localscan.Output{
		Stdout: `{"formulae":[],"casks":[{"token":"example","versions":["1.2.3"],"pinned_version":null}]}`,
	}
	h.runner.responses["brew --prefix"] = localscan.Output{Stdout: caskroom}
	h.runner.responses["brew info --cask --json=v2 example"] = localscan.Output{
		Stdout: `{"formulae":[],"casks":[{"token":"example","tap":"homebrew/cask","name":["Example"],
			"version":"1.2.3","homepage":"https://github.com/acme/example",
			"artifacts":[{"app":["Example.app"]}]}]}`,
	}
	h.runner.responses["codesign -dv --verbose=4 "+appPath] = localscan.Output{
		Stderr: "Executable=/Applications/Example.app/Contents/MacOS/Example\n" +
			"Identifier=com.acme.example\n" +
			"Authority=Developer ID Application: ACME Inc (ACME123456)\n" +
			"TeamIdentifier=ACME123456\n",
	}
	h.runner.responses["spctl -a -vv "+appPath] = localscan.Output{
		Stdout: "/Applications/Example.app: accepted\nsource=Notarized Developer ID\n" +
			"origin=Developer ID Application: ACME Inc (ACME123456)\n",
	}
	h.runner.responses[`osascript -e tell application "System Events" to get {name, path} of every login item`] = localscan.Output{
		Stdout: "Example, " + appPath + "\n",
	}

	h.buildOptions = func() Options {
		return Options{
			Stdout:          h.out,
			Stderr:          h.errOut,
			HomebrewBaseURL: h.api.homebrew.URL + "/cask",
			GitHubBaseURL:   h.api.github.URL,
			CacheDir:        filepath.Join(t.TempDir(), "cache"),
			SnapshotDir:     h.snapDir,
			NotifyRunner:    h.runner,
			Now:             func() time.Time { return time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC) },
			LocalscanOptions: []localscan.ScannerOption{
				localscan.WithRunner(h.runner),
				localscan.WithCaskroomDir(caskroom),
				localscan.WithApplicationsDirs([]string{appDir}),
				localscan.WithLaunchAgentDirs([]string{launchDir}),
				localscan.WithHome(root),
			},
		}
	}
	return h, h.out, h.errOut
}

func TestReportTableHealthyCask(t *testing.T) {
	h, out, errOut := newHarness(t)

	code := Run([]string{"report"}, h.buildOptions())
	if code != ExitOK {
		t.Fatalf("exit = %d, want 0 (stderr: %s)", code, errOut.String())
	}
	table := out.String()
	if !strings.Contains(table, "example") {
		t.Errorf("table must list the cask, got: %s", table)
	}
	if !strings.Contains(table, "homebrew/cask") {
		t.Errorf("table must show the tap source, got: %s", table)
	}
	if !strings.Contains(table, "100") {
		t.Errorf("healthy cask must score 100, got: %s", table)
	}
	// Report is read-only apart from the collector cache: no snapshot.
	if _, err := os.ReadDir(h.snapDir); err == nil {
		entries, _ := os.ReadDir(h.snapDir)
		if len(entries) != 0 {
			t.Errorf("report must not write snapshots, found %d files", len(entries))
		}
	}
}

func TestReportJSONSchema(t *testing.T) {
	h, out, _ := newHarness(t)

	code := Run([]string{"report", "--json"}, h.buildOptions())
	if code != ExitOK {
		t.Fatalf("exit = %d", code)
	}
	var report jsonReport
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out.String())
	}
	if report.SchemaVersion != 1 {
		t.Errorf("schema_version = %d, want 1", report.SchemaVersion)
	}
	if len(report.Casks) != 1 {
		t.Fatalf("casks = %d, want 1", len(report.Casks))
	}
	cask := report.Casks[0]
	if cask.Token != "example" {
		t.Errorf("token = %q", cask.Token)
	}
	if cask.Overall == nil || *cask.Overall != 100 {
		t.Errorf("overall = %v, want 100", cask.Overall)
	}
	if len(cask.Dimensions) != 4 {
		t.Errorf("dimensions = %d, want 4", len(cask.Dimensions))
	}
	if report.Scan.GithubTokenHint {
		t.Error("github_token_hint must be false with a small scan")
	}
}

func TestReportExitTwoOnArchivedRepo(t *testing.T) {
	h, out, _ := newHarness(t)
	// The GitHub server reports the upstream repository as archived.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/releases/latest") {
			fmt.Fprint(w, `{"tag_name":"1.2.3","published_at":"2026-10-01T00:00:00Z"}`)
			return
		}
		fmt.Fprint(w, `{"full_name":"acme/example","archived":true,"pushed_at":"2026-10-01T00:00:00Z"}`)
	}))
	defer srv.Close()

	opts := h.buildOptions()
	opts.GitHubBaseURL = srv.URL
	code := Run([]string{"report"}, opts)
	if code != ExitAtRisk {
		t.Fatalf("exit = %d, want 2 (archived repo: known maintenance 0)", code)
	}
	if !strings.Contains(out.String(), "example") {
		t.Errorf("table must still print the cask: %s", out.String())
	}
}

func TestReportRateLimitHint(t *testing.T) {
	h, _, errOut := newHarness(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-RateLimit-Remaining", "0")
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	opts := h.buildOptions()
	opts.GitHubBaseURL = srv.URL
	opts.GitHubToken = ""
	code := Run([]string{"report"}, opts)
	if code == ExitError {
		t.Fatalf("rate-limited scan must complete (unknowns), not fail; stderr: %s", errOut.String())
	}
	if !strings.Contains(errOut.String(), "set GITHUB_TOKEN for full coverage") {
		t.Errorf("token hint must be printed once at the end, got: %s", errOut.String())
	}
}

func TestWhyKnownAndUnknownCask(t *testing.T) {
	h, out, errOut := newHarness(t)

	code := Run([]string{"why", "example"}, h.buildOptions())
	if code != ExitOK {
		t.Fatalf("exit = %d (stderr: %s)", code, errOut.String())
	}
	text := out.String()
	for _, want := range []string{
		"overall: 100",
		"maintenance (weight 0.4)",
		"signing (weight 0.3)",
		"transparency (weight 0.2)",
		"currency (weight 0.1)",
		"ACME123456",
		"TCC permissions are not scanned",
		"login item Example",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("why output missing %q\ngot: %s", want, text)
		}
	}

	var errBuf bytes.Buffer
	opts := h.buildOptions()
	opts.Stdout = &bytes.Buffer{}
	opts.Stderr = &errBuf
	if code := Run([]string{"why", "not-installed"}, opts); code != ExitError {
		t.Errorf("why for a cask that is not installed: exit = %d, want 1", code)
	}
}

func TestBTMItemsAttributedToCask(t *testing.T) {
	h, out, _ := newHarness(t)
	// A BTM app item registered for the cask's app bundle, plus a
	// system-session item that must not be attributed.
	h.runner.responses["sfltool dumpbtm"] = localscan.Output{
		Stdout: `========================
 Records for UID 0 : FFFFEEEE-DDDD-CCCC-BBBB-AAAA00000000
========================

 Items:

 #1:
                 UUID: 11111111-1111-1111-1111-111111111111
                 Name: SysThing
                 Type: app (0x2)
           Identifier: 2.com.example.systhing
                  URL: /Applications/SysThing.app

========================
 Records for UID 501 : FFFFEEEE-DDDD-CCCC-BBBB-AAAA00000501
========================

 Items:

 #2:
                 UUID: 22222222-2222-2222-2222-222222222222
                 Name: Example
       Developer Name: ACME Inc
      Team Identifier: ACME123456
                 Type: app (0x2)
          Disposition: [enabled, allowed, notified] (0xb)
           Identifier: 2.com.acme.example
                  URL: ` + h.appPath + `
`,
	}

	if code := Run([]string{"why", "example"}, h.buildOptions()); code != ExitOK {
		t.Fatalf("exit = %d, output: %s%s", code, h.out.String(), h.errOut.String())
	}
	text := out.String()
	if !strings.Contains(text, "BTM background item Example") {
		t.Errorf("BTM item for the cask's app must be attributed, got: %s", text)
	}
	if !strings.Contains(text, "ACME Inc (ACME123456)") {
		t.Errorf("BTM item must show developer and team, got: %s", text)
	}
	if strings.Contains(text, "SysThing") {
		t.Errorf("BTM items of other apps must not be attributed to this cask, got: %s", text)
	}
	// BTM items are facts: they never gate.
	if !strings.Contains(text, "overall: 100") {
		t.Errorf("overall = changed by BTM facts? got: %s", text)
	}
}

func TestWatchFirstRunWritesBaselineWithoutNotifications(t *testing.T) {
	h, out, errOut := newHarness(t)

	code := Run([]string{"watch"}, h.buildOptions())
	if code != ExitOK {
		t.Fatalf("exit = %d (stderr: %s)", code, errOut.String())
	}
	if !strings.Contains(out.String(), "no previous snapshot") {
		t.Errorf("first watch must state it becomes the baseline, got: %s", out.String())
	}
	if h.runner.contains("display notification") {
		t.Error("first watch must not notify")
	}

	entries, err := os.ReadDir(h.snapDir)
	if err != nil || len(entries) != 2 {
		t.Fatalf("snapshot dir must hold snapshot + latest.json, got %v (err %v)", entries, err)
	}
	if _, err := os.Stat(filepath.Join(h.snapDir, "latest.json")); err != nil {
		t.Error("latest.json pointer missing")
	}
}

func TestWatchDetectsAuthorityChangeAndNotifies(t *testing.T) {
	h, _, _ := newHarness(t)

	// Baseline run.
	if code := Run([]string{"watch"}, h.buildOptions()); code != ExitOK {
		t.Fatalf("baseline watch exit = %d", code)
	}

	// The app is re-signed by a new company: authority and team change.
	h.runner.responses["codesign -dv --verbose=4 "+h.appPath] = localscan.Output{
		Stderr: "Executable=/Applications/Example.app/Contents/MacOS/Example\n" +
			"Identifier=com.acme.example\n" +
			"Authority=Developer ID Application: Aperture Research LLC (APERTURE9)\n" +
			"TeamIdentifier=APERTURE9\n",
	}
	h.runner.responses["spctl -a -vv "+h.appPath] = localscan.Output{
		Stdout: "/Applications/Example.app: accepted\nsource=Notarized Developer ID\n" +
			"origin=Developer ID Application: Aperture Research LLC (APERTURE9)\n",
	}

	var out bytes.Buffer
	opts := h.buildOptions()
	opts.Stdout = &out
	code := Run([]string{"watch"}, opts)
	if code != ExitAtRisk {
		t.Fatalf("exit = %d, want 2 (authority change)", code)
	}
	text := out.String()
	if !strings.Contains(text, "Developer ID Application: ACME Inc (ACME123456) -> Developer ID Application: Aperture Research LLC (APERTURE9)") {
		t.Errorf("diff must print old and new authority in plain text, got: %s", text)
	}
	if !h.runner.contains("display notification") {
		t.Error("authority team change must fire a notification")
	}
	if !h.runner.contains("signing authority changed") {
		t.Error("notification body must describe the authority change")
	}
}

func TestWatchDetectsOverallDropBelow50(t *testing.T) {
	h, _, _ := newHarness(t)
	if code := Run([]string{"watch"}, h.buildOptions()); code != ExitOK {
		t.Fatalf("baseline watch exit = %d", code)
	}

	// The app bundle is replaced by an unsigned one (Bartender-style
	// takeover, worst case) and the repo is archived.
	h.runner.responses["codesign -dv --verbose=4 "+h.appPath] = localscan.Output{
		Stderr: "code object is not signed at all\n",
	}
	delete(h.runner.responses, "spctl -a -vv "+h.appPath)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/releases/latest") {
			fmt.Fprint(w, `{"tag_name":"1.2.3","published_at":"2026-10-01T00:00:00Z"}`)
			return
		}
		fmt.Fprint(w, `{"full_name":"acme/example","archived":true,"pushed_at":"2026-10-01T00:00:00Z"}`)
	}))
	defer srv.Close()

	opts := h.buildOptions()
	opts.GitHubBaseURL = srv.URL
	var out bytes.Buffer
	opts.Stdout = &out
	code := Run([]string{"watch"}, opts)
	if code != ExitAtRisk {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !h.runner.contains("overall score dropped below 50") {
		t.Error("overall drop below 50 must fire a notification")
	}
}

func TestWatchNoChangeMeansNoNotification(t *testing.T) {
	h, _, _ := newHarness(t)
	Run([]string{"watch"}, h.buildOptions())

	if code := Run([]string{"watch"}, h.buildOptions()); code != ExitOK {
		t.Fatalf("unchanged watch exit = %d", code)
	}
	if h.runner.contains("display notification") {
		t.Error("unchanged state must not notify")
	}
}

func TestThirdPartyTapCaskReportsUnknown(t *testing.T) {
	h, out, _ := newHarness(t)

	// The cask comes from a third-party tap with a non-GitHub
	// homepage: metadata comes from brew info, and maintenance stays
	// unknown with the explicit hint.
	h.runner.responses["brew info --cask --json=v2 example"] = localscan.Output{
		Stdout: `{"formulae":[],"casks":[{"token":"example","tap":"user/mytap","name":["Example"],
			"version":"1.2.3","homepage":"https://vendor.example.com/example","artifacts":[]}]}`,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r) // core API: not found
	}))
	defer srv.Close()

	opts := h.buildOptions()
	opts.HomebrewBaseURL = srv.URL + "/cask"
	if code := Run([]string{"why", "example"}, opts); code != ExitOK {
		t.Fatalf("third-party tap cask must scan (exit %d), not fail; output: %s%s",
			code, h.out.String(), h.errOut.String())
	}
	text := out.String()
	if !strings.Contains(text, "not in Homebrew core API (tap: user/mytap)") {
		t.Errorf("third-party tap hint missing, got: %s", text)
	}
	if !strings.Contains(text, "tap: user/mytap") {
		t.Errorf("tap source must be shown, got: %s", text)
	}
	// No declared app artifacts and no core metadata: local apps can
	// not be attributed to the cask, so signing reports unknown —
	// honest, not zero, not fabricated.
	if !strings.Contains(text, "signing (weight 0.3): unknown") {
		t.Errorf("signing must be unknown without declared artifacts, got: %s", text)
	}
	if !strings.Contains(text, "overall: unknown") {
		t.Errorf("overall must be unknown when no gate dimension has data, got: %s", text)
	}
}

func TestThirdPartyTapGitHubHomepageScoresMaintenance(t *testing.T) {
	h, out, _ := newHarness(t)

	// Roadmap item: third-party tap metadata via local brew info. The
	// tap cask declares its app bundle and a GitHub homepage, so both
	// gate dimensions score like a core cask.
	h.runner.responses["brew info --cask --json=v2 example"] = localscan.Output{
		Stdout: `{"formulae":[],"casks":[{"token":"example","tap":"user/mytap","name":["Example"],
			"version":"1.2.3","homepage":"https://github.com/acme/example",
			"artifacts":[{"app":["Example.app"]}]}]}`,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r) // not in the core JSON API
	}))
	defer srv.Close()

	opts := h.buildOptions()
	opts.HomebrewBaseURL = srv.URL + "/cask"
	if code := Run([]string{"why", "example"}, opts); code != ExitOK {
		t.Fatalf("third-party tap cask with GitHub homepage must scan (exit %d), output: %s%s",
			code, h.out.String(), h.errOut.String())
	}
	text := out.String()
	if !strings.Contains(text, "maintenance (weight 0.4): 100") {
		t.Errorf("maintenance must score from the GitHub homepage via brew info, got: %s", text)
	}
	if !strings.Contains(text, "signing (weight 0.3): 100") {
		t.Errorf("signing must score from the declared app bundle found on disk, got: %s", text)
	}
	if !strings.Contains(text, "overall: 100") {
		t.Errorf("overall must be 100, got: %s", text)
	}
}

func TestUsageErrors(t *testing.T) {
	h, _, errOut := newHarness(t)

	if code := Run(nil, h.buildOptions()); code != ExitError {
		t.Errorf("no args: exit = %d, want 1", code)
	}
	if code := Run([]string{"bogus"}, h.buildOptions()); code != ExitError {
		t.Errorf("unknown command: exit = %d, want 1", code)
	}
	if code := Run([]string{"report", "--xml"}, h.buildOptions()); code != ExitError {
		t.Errorf("unknown flag: exit = %d, want 1", code)
	}
	_ = errOut
}

func TestInstallGateHealthyCask(t *testing.T) {
	h, out, errOut := newHarness(t)

	code := Run([]string{"install", "example"}, h.buildOptions())
	if code != ExitOK {
		t.Fatalf("exit = %d (stderr: %s)", code, errOut.String())
	}
	text := out.String()
	for _, want := range []string{
		"install gate: example (tap: homebrew/cask)",
		"would install: Example.app",
		"maintenance: 100",
		"signing: unknown — verified by `caskvet report` after install",
		"current catalog version: 1.2.3",
		"verdict: no at-risk findings — proceed with: brew install --cask example",
		"caskvet never installs anything",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("install gate output missing %q\ngot: %s", want, text)
		}
	}
}

func TestInstallGateAtRiskOnDeprecatedCask(t *testing.T) {
	h, out, _ := newHarness(t)
	// The cask lives in a third-party tap and is deprecated there with
	// a replacement token — all known from brew info alone.
	h.runner.responses["brew info --cask --json=v2 old-tool"] = localscan.Output{
		Stdout: `{"formulae":[],"casks":[{"token":"old-tool","tap":"user/mytap","name":["Old Tool"],
			"version":"0.9","homepage":"https://github.com/acme/example",
			"deprecated":true,"replacement":"new-tool","artifacts":[{"app":["Old Tool.app"]}]}]}`,
	}

	code := Run([]string{"install", "old-tool"}, h.buildOptions())
	if code != ExitAtRisk {
		t.Fatalf("exit = %d, want 2 (deprecated cask)", code)
	}
	text := out.String()
	if !strings.Contains(text, "AT-RISK") {
		t.Errorf("verdict must be AT-RISK, got: %s", text)
	}
	if !strings.Contains(text, "replacement cask: new-tool") {
		t.Errorf("replacement token must be surfaced, got: %s", text)
	}
}

func TestInstallGateUsageErrors(t *testing.T) {
	h, _, errOut := newHarness(t)

	// Invalid token characters.
	if code := Run([]string{"install", "Not Valid"}, h.buildOptions()); code != ExitError {
		t.Errorf("invalid token: exit = %d, want 1", code)
	}
	// Valid token that no tap knows.
	h.runner.responses["brew info --cask --json=v2 mystery"] = localscan.Output{
		Stdout: `{"formulae":[],"casks":[]}`,
	}
	if code := Run([]string{"install", "mystery"}, h.buildOptions()); code != ExitError {
		t.Errorf("unknown cask: exit = %d, want 1", code)
	}
	if !strings.Contains(errOut.String(), "mystery") {
		t.Errorf("error must name the missing cask, got: %s", errOut.String())
	}
}

// lookupResponse finds the recorded command key starting with prefix.
func lookupResponse(r *recordingRunner, prefix string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	for key := range r.responses {
		if strings.HasPrefix(key, prefix) {
			return key
		}
	}
	return prefix
}
