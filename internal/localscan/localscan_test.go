package localscan

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// fakeRunner replays recorded command output. Keys are "name arg1 arg2".
type fakeRunner struct {
	responses map[string]Output
	errs      map[string]error
	invoked   []string
}

func (r *fakeRunner) Run(name string, args ...string) (Output, error) {
	key := name + " " + strings.Join(args, " ")
	r.invoked = append(r.invoked, key)
	out, ok := r.responses[key]
	if !ok {
		return Output{}, fmt.Errorf("fakeRunner: unexpected command: %s", key)
	}
	return out, r.errs[key]
}

// realExitError produces a genuine *exec.ExitError so scanner exit-code
// handling is tested against the real error type.
func realExitError(t *testing.T, code int) error {
	t.Helper()
	err := exec.Command("/bin/sh", "-c", fmt.Sprintf("exit %d", code)).Run()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("expected exec.ExitError, got %v", err)
	}
	return exitErr
}

func readFixture(t *testing.T, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", rel))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestInstalledCasksParsesBrewList(t *testing.T) {
	body := readFixture(t, filepath.Join("brew", "list-cask-versions.json"))
	runner := &fakeRunner{responses: map[string]Output{
		"brew list --cask --json --versions": {Stdout: body},
	}}
	s := New(WithRunner(runner))

	casks, err := s.InstalledCasks()
	if err != nil {
		t.Fatal(err)
	}
	if len(casks) == 0 {
		t.Fatal("want installed casks from fixture, got none")
	}
	byToken := make(map[string]InstalledCask, len(casks))
	for _, c := range casks {
		byToken[c.Token] = c
	}
	ff, ok := byToken["firefox"]
	if !ok {
		t.Fatalf("firefox missing from %v", casks)
	}
	if ff.Version != "158.0" {
		t.Errorf("firefox version = %q, want 158.0", ff.Version)
	}
	if _, ok := byToken["linearmouse"]; !ok {
		t.Errorf("linearmouse missing from results")
	}
}

func TestInstalledCasksRejectsGarbage(t *testing.T) {
	runner := &fakeRunner{responses: map[string]Output{
		"brew list --cask --json --versions": {Stdout: "not json"},
	}}
	s := New(WithRunner(runner))
	if _, err := s.InstalledCasks(); err == nil {
		t.Fatal("want error for invalid brew JSON")
	}
}

func TestParseCodesignOutput(t *testing.T) {
	t.Run("developer-id", func(t *testing.T) {
		out := readFixture(t, filepath.Join("codesign", "codesign-developer-id.txt"))
		info := ParseCodesignOutput(out)
		if !info.Signed {
			t.Error("Signed = false, want true")
		}
		if info.AdHoc {
			t.Error("AdHoc = true, want false")
		}
		if info.TeamIdentifier != "C5686NKYJ7" {
			t.Errorf("TeamIdentifier = %q, want C5686NKYJ7", info.TeamIdentifier)
		}
		if len(info.Authorities) != 3 {
			t.Errorf("Authorities = %v, want 3 entries", info.Authorities)
		}
		if info.Identifier != "com.lujjjh.LinearMouse" {
			t.Errorf("Identifier = %q", info.Identifier)
		}
	})

	t.Run("adhoc", func(t *testing.T) {
		out := readFixture(t, filepath.Join("codesign", "codesign-adhoc.txt"))
		info := ParseCodesignOutput(out)
		if info.Signed {
			t.Error("Signed = true, want false (ad-hoc is not a signing identity)")
		}
		if !info.AdHoc {
			t.Error("AdHoc = false, want true")
		}
		if info.TeamIdentifier != "" {
			t.Errorf("TeamIdentifier = %q, want empty", info.TeamIdentifier)
		}
	})

	t.Run("unsigned", func(t *testing.T) {
		out := readFixture(t, filepath.Join("codesign", "codesign-unsigned.txt"))
		info := ParseCodesignOutput(out)
		if info.Signed || info.AdHoc {
			t.Errorf("Signed=%v AdHoc=%v, want false/false", info.Signed, info.AdHoc)
		}
		if !info.Unsigned() {
			t.Error("Unsigned() = false, want true")
		}
	})
}

// TestCodesignUnsignedAppIsAResultNotAnError pins the contract that an
// unsigned bundle scans successfully with Signed=false: missing data must
// not look like a crash or a risk value.
func TestCodesignUnsignedAppIsAResultNotAnError(t *testing.T) {
	runner := &fakeRunner{
		responses: map[string]Output{
			"codesign -dv --verbose=4 /Applications/Unsigned.app": {
				Stderr: readFixture(t, filepath.Join("codesign", "codesign-unsigned.txt")),
			},
		},
		errs: map[string]error{
			"codesign -dv --verbose=4 /Applications/Unsigned.app": realExitError(t, 1),
		},
	}
	s := New(WithRunner(runner))

	info, err := s.Codesign("/Applications/Unsigned.app")
	if err != nil {
		t.Fatalf("unsigned bundle must scan without error, got %v", err)
	}
	if info.Signed || info.AdHoc {
		t.Errorf("Signed=%v AdHoc=%v, want false/false", info.Signed, info.AdHoc)
	}
}

func TestParseGatekeeperOutput(t *testing.T) {
	t.Run("notarized", func(t *testing.T) {
		out := readFixture(t, filepath.Join("spctl", "spctl-notarized.txt"))
		info := ParseGatekeeperOutput(out)
		if !info.Accepted {
			t.Error("Accepted = false, want true")
		}
		if !info.Notarized() {
			t.Errorf("Notarized() = false, source = %q", info.Source)
		}
		if !strings.Contains(info.Origin, "C5686NKYJ7") {
			t.Errorf("Origin = %q, want team id in origin", info.Origin)
		}
	})

	t.Run("apple-system", func(t *testing.T) {
		out := readFixture(t, filepath.Join("spctl", "spctl-apple-system.txt"))
		info := ParseGatekeeperOutput(out)
		if !info.Accepted {
			t.Error("Accepted = false, want true")
		}
		if info.Notarized() {
			t.Error("Notarized() = true for Apple System software, want false")
		}
	})

	t.Run("rejected", func(t *testing.T) {
		out := readFixture(t, filepath.Join("spctl", "spctl-rejected.txt"))
		info := ParseGatekeeperOutput(out)
		if info.Accepted {
			t.Error("Accepted = true, want false")
		}
		if info.Source != "Unnotarized Developer ID" {
			t.Errorf("Source = %q", info.Source)
		}
	})
}

func TestGatekeeperRejectionIsAResultNotAnError(t *testing.T) {
	runner := &fakeRunner{
		responses: map[string]Output{
			"spctl -a -vv /Applications/Rejected.app": {
				Stderr: readFixture(t, filepath.Join("spctl", "spctl-rejected.txt")),
			},
		},
		errs: map[string]error{
			"spctl -a -vv /Applications/Rejected.app": realExitError(t, 3),
		},
	}
	s := New(WithRunner(runner))

	info, err := s.Gatekeeper("/Applications/Rejected.app")
	if err != nil {
		t.Fatalf("rejected bundle must scan without error, got %v", err)
	}
	if info.Accepted {
		t.Error("Accepted = true, want false")
	}
}

func TestLaunchAgentsDiscovery(t *testing.T) {
	dir := t.TempDir()
	plistBody := readFixture(t, filepath.Join("launchagents", "com.example.helper-agent.plist"))
	if err := os.WriteFile(filepath.Join(dir, "com.example.helper-agent.plist"), []byte(plistBody), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "broken.plist"), []byte("\x00\x01binary junk"), 0o644); err != nil {
		t.Fatal(err)
	}

	s := New(WithLaunchAgentDirs([]string{dir}))
	agents, err := s.LaunchAgents()
	if err != nil {
		t.Fatal(err)
	}
	if len(agents) != 2 {
		t.Fatalf("want 2 agents (incl. unparseable), got %d: %+v", len(agents), agents)
	}

	var found *LaunchAgent
	for i := range agents {
		if strings.HasSuffix(agents[i].Path, "com.example.helper-agent.plist") {
			found = &agents[i]
		}
	}
	if found == nil {
		t.Fatalf("helper agent missing from %+v", agents)
	}
	if found.Label != "com.example.helper-agent" {
		t.Errorf("Label = %q", found.Label)
	}
	if found.Program != "/Applications/Example Tool.app/Contents/MacOS/example-helper" {
		t.Errorf("Program = %q", found.Program)
	}
	if !found.RunAtLoad {
		t.Error("RunAtLoad = false, want true")
	}

	for _, a := range agents {
		if strings.HasSuffix(a.Path, "broken.plist") && a.ParseError == "" {
			t.Error("broken.plist must be reported with ParseError set, not hidden")
		}
	}
}

func TestLocateAppBundles(t *testing.T) {
	root := t.TempDir()
	applications := filepath.Join(root, "Applications")
	caskroom := filepath.Join(root, "Caskroom")
	tokenDir := filepath.Join(caskroom, "multi-app", "1.2.3")
	for _, d := range []string{applications, tokenDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// Multi-app cask: one bundle in /Applications, one still in Caskroom
	// (e.g. an app not in /Applications), one missing entirely.
	for _, bundle := range []string{filepath.Join(applications, "First App.app"), filepath.Join(tokenDir, "Second App.app")} {
		if err := os.MkdirAll(bundle, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	s := New(
		WithCaskroomDir(caskroom),
		WithApplicationsDirs([]string{applications}),
	)
	bundles, err := s.LocateAppBundles("multi-app", "1.2.3", []string{"First App.app", "Second App.app", "Missing.app"})
	if err != nil {
		t.Fatal(err)
	}
	if len(bundles) != 3 {
		t.Fatalf("want 3 bundles, got %d: %+v", len(bundles), bundles)
	}
	byName := make(map[string]AppBundle, len(bundles))
	for _, b := range bundles {
		byName[b.Name] = b
	}
	if b := byName["First App.app"]; !b.Found || b.Path != filepath.Join(applications, "First App.app") {
		t.Errorf("First App.app = %+v, want found in /Applications", b)
	}
	if b := byName["Second App.app"]; !b.Found || b.Path != filepath.Join(tokenDir, "Second App.app") {
		t.Errorf("Second App.app = %+v, want found in Caskroom", b)
	}
	if b := byName["Missing.app"]; b.Found {
		t.Errorf("Missing.app = %+v, want not found", b)
	}
}

func TestLocateAppBundlesWithoutDeclaredArtifacts(t *testing.T) {
	root := t.TempDir()
	applications := filepath.Join(root, "Applications")
	caskroom := filepath.Join(root, "Caskroom", "font-thing", "1.0")
	if err := os.MkdirAll(filepath.Join(caskroom, "SomeFont.ttf"), 0o755); err != nil {
		t.Fatal(err)
	}
	// An app in the applications dir must NOT be attributed to a cask
	// that declares no app artifacts: the glob is Caskroom-only.
	if err := os.MkdirAll(filepath.Join(applications, "Unrelated.app"), 0o755); err != nil {
		t.Fatal(err)
	}
	s := New(WithCaskroomDir(caskroom), WithApplicationsDirs([]string{applications}))

	bundles, err := s.LocateAppBundles("font-thing", "1.0", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(bundles) != 0 {
		t.Errorf("font cask with no declared bundles must not adopt apps from application dirs, got %+v", bundles)
	}
}

func TestLoginItems(t *testing.T) {
	runner := &fakeRunner{responses: map[string]Output{
		`osascript -e tell application "System Events" to get {name, path} of every login item`: {
			Stdout: "Firefox, /Applications/Firefox.app\nObsidian, /Applications/Obsidian.app\n",
		},
	}}
	s := New(WithRunner(runner))

	items, err := s.LoginItems()
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("want 2 login items, got %d: %+v", len(items), items)
	}
	if items[0].Name != "Firefox" || items[0].Path != "/Applications/Firefox.app" {
		t.Errorf("item 0 = %+v", items[0])
	}
	if items[1].Name != "Obsidian" || items[1].Path != "/Applications/Obsidian.app" {
		t.Errorf("item 1 = %+v", items[1])
	}
}

func TestLoginItemsSurfacesPermissionErrors(t *testing.T) {
	runner := &fakeRunner{
		responses: map[string]Output{
			`osascript -e tell application "System Events" to get {name, path} of every login item`: {Stderr: "not authorized"},
		},
		errs: map[string]error{
			`osascript -e tell application "System Events" to get {name, path} of every login item`: realExitError(t, 1),
		},
	}
	s := New(WithRunner(runner))
	if _, err := s.LoginItems(); err == nil {
		t.Fatal("want error when System Events denies access; callers must report unknown, not an empty list")
	}
}

func TestParsePlistDictVariants(t *testing.T) {
	const doc = `<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0"><dict>
<key>Label</key><string>com.example.agent</string>
<key>RunAtLoad</key><false/>
<key>Disabled</key><true/>
<key>Program</key><string>/usr/local/bin/program</string>
</dict></plist>`
	got, err := parsePlistDict([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	if got["Label"] != "com.example.agent" {
		t.Errorf("Label = %v", got["Label"])
	}
	if v, ok := got["RunAtLoad"].(bool); !ok || v {
		t.Errorf("RunAtLoad = %v, want false", got["RunAtLoad"])
	}
	if v, ok := got["Disabled"].(bool); !ok || !v {
		t.Errorf("Disabled = %v, want true", got["Disabled"])
	}
	if got["Program"] != "/usr/local/bin/program" {
		t.Errorf("Program = %v", got["Program"])
	}
}

func TestCaskInfoParsesBrewInfo(t *testing.T) {
	body := readFixture(t, filepath.Join("brew", "info-cask.json"))
	runner := &fakeRunner{responses: map[string]Output{
		"brew info --cask --json=v2 1password-cli@1 example": {Stdout: body},
	}}
	s := New(WithRunner(runner))

	infos, err := s.CaskInfo([]string{"1password-cli@1", "example"})
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 2 {
		t.Fatalf("want 2 casks, got %d", len(infos))
	}

	deprecated := infos["1password-cli@1"]
	if deprecated.Tap != "homebrew/cask" {
		t.Errorf("tap = %q", deprecated.Tap)
	}
	if !deprecated.Deprecated || !deprecated.Disabled {
		t.Errorf("deprecated=%v disabled=%v, want true/true", deprecated.Deprecated, deprecated.Disabled)
	}
	if deprecated.DeprecationReason != "unmaintained" {
		t.Errorf("deprecation reason = %q", deprecated.DeprecationReason)
	}
	if deprecated.Replacement != "" {
		t.Errorf("replacement = %q, want empty (null in JSON)", deprecated.Replacement)
	}
	if len(deprecated.AppBundles) != 0 {
		t.Errorf("pkg cask must report no app bundles, got %v", deprecated.AppBundles)
	}

	tap := infos["example"]
	if tap.Tap != "user/mytap" {
		t.Errorf("tap = %q", tap.Tap)
	}
	if tap.Homepage != "https://github.com/acme/example" {
		t.Errorf("homepage = %q", tap.Homepage)
	}
	if !reflect.DeepEqual(tap.AppBundles, []string{"Example.app"}) {
		t.Errorf("app bundles = %v", tap.AppBundles)
	}
	if len(tap.Launchctl) != 1 {
		t.Errorf("launchctl = %v", tap.Launchctl)
	}
	if len(tap.LoginItems) != 1 || tap.LoginItems[0] != "Example.app" {
		t.Errorf("login items = %v", tap.LoginItems)
	}
	if tap.Replacement != "example-new" {
		t.Errorf("replacement = %q, want example-new", tap.Replacement)
	}

	// Unknown tokens are simply absent from the map.
	if _, ok := infos["mystery"]; ok {
		t.Error("unknown token must be absent from the map")
	}
}

func TestBrewListJSONRoundTrip(t *testing.T) {
	// Sanity-check the recorded fixture is valid JSON of the expected shape.
	var data struct {
		Casks []json.RawMessage `json:"casks"`
	}
	if err := json.Unmarshal([]byte(readFixture(t, filepath.Join("brew", "list-cask-versions.json"))), &data); err != nil {
		t.Fatal(err)
	}
	if len(data.Casks) == 0 {
		t.Fatal("recorded brew fixture has no casks")
	}
}
