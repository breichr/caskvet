package score

import (
	"strings"
	"testing"
	"time"
)

var now = time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)

func daysAgo(d int) time.Time {
	return now.Add(-time.Duration(d) * 24 * time.Hour)
}

func healthyCask() *CaskScan {
	return &CaskScan{
		Token:             "example",
		InstalledVersion:  "1.2.3",
		Homepage:          "https://github.com/acme/example",
		RepoDataAvailable: true,
		RepoArchived:      false,
		LastCommit:        daysAgo(30),
		LastCommitKnown:   true,
		Apps: []AppScan{{
			Name:               "Example.app",
			Path:               "/Applications/Example.app",
			Found:              true,
			Signed:             true,
			TeamID:             "ACME123456",
			Authority:          "Developer ID Application: ACME Inc (ACME123456)",
			Notarized:          true,
			GatekeeperKnown:    true,
			GatekeeperAccepted: true,
			GatekeeperSource:   "Notarized Developer ID",
		}},
		LatestReleaseTag:   "v1.2.3",
		LatestReleaseKnown: true,
	}
}

// dim returns the named dimension of a result.
func dim(t *testing.T, r Result, name string) Dimension {
	t.Helper()
	for _, d := range r.Dimensions {
		if d.Name == name {
			return d
		}
	}
	t.Fatalf("dimension %q missing from result", name)
	return Dimension{}
}

func requireScore(t *testing.T, d Dimension, want int) {
	t.Helper()
	if d.Score == nil {
		t.Fatalf("%s: score unknown, want %d (findings: %v)", d.Name, want, d.Findings)
	}
	if *d.Score != want {
		t.Errorf("%s: score = %d, want %d (findings: %v)", d.Name, *d.Score, want, d.Findings)
	}
}

func requireUnknown(t *testing.T, d Dimension) {
	t.Helper()
	if d.Score != nil {
		t.Errorf("%s: score = %d, want unknown (findings: %v)", d.Name, *d.Score, d.Findings)
	}
}

func findingTexts(d Dimension) string {
	var texts []string
	for _, f := range d.Findings {
		texts = append(texts, f.Text)
	}
	return strings.Join(texts, "\n")
}

func TestHealthyCaskScoresFull(t *testing.T) {
	r := Score(now, healthyCask(), nil)
	for _, name := range []string{DimensionMaintenance, DimensionSigning, DimensionCurrency} {
		requireScore(t, dim(t, r, name), 100)
	}
	if r.Overall == nil || *r.Overall != 100 {
		t.Errorf("overall = %v, want 100", r.Overall)
	}
}

func TestArchivedRepo(t *testing.T) {
	c := healthyCask()
	c.RepoArchived = true
	r := Score(now, c, nil)
	requireScore(t, dim(t, r, DimensionMaintenance), 0)
	if !strings.Contains(findingTexts(dim(t, r, DimensionMaintenance)), "archived") {
		t.Error("archived finding missing")
	}
}

func TestStaleRepoHardDeduction(t *testing.T) {
	c := healthyCask()
	c.LastCommit = daysAgo(400) // > 12 months
	r := Score(now, c, nil)
	requireScore(t, dim(t, r, DimensionMaintenance), StaleCommitScore)

	// Exactly at the threshold is still current.
	fresh := healthyCask()
	fresh.LastCommit = daysAgo(360)
	r2 := Score(now, fresh, nil)
	requireScore(t, dim(t, r2, DimensionMaintenance), 100)
}

func TestAuthorityChangeBetweenSnapshots(t *testing.T) {
	prev := healthyCask()
	current := healthyCask()
	current.Apps[0].TeamID = "NEWT34M1D"
	current.Apps[0].Authority = "Developer ID Application: New Co (NEWT34M1D)"

	r := Score(now, current, prev)
	signing := dim(t, r, DimensionSigning)
	requireScore(t, signing, AuthorityChangeCap)

	text := findingTexts(signing)
	if !strings.Contains(text, "Developer ID Application: ACME Inc (ACME123456) -> Developer ID Application: New Co (NEWT34M1D)") {
		t.Errorf("warning must print old and new authority strings in plain text, got: %s", text)
	}
	if !HasTeamChange(r) {
		t.Error("HasTeamChange must detect the change (hard watch trigger)")
	}
	if HasAuthorityNameChange(r) {
		t.Error("a team change must not also report as a name-only change")
	}

	// Without a previous snapshot there is no change to report.
	r2 := Score(now, current, nil)
	if HasTeamChange(r2) {
		t.Error("first scan must not report an authority change")
	}
	if d := dim(t, r2, DimensionSigning); d.Score == nil || *d.Score != 100 {
		t.Errorf("first scan signing score = %v, want 100", d.Score)
	}
}

func TestAuthorityNameOnlyChangeDoesNotReduceScore(t *testing.T) {
	prev := healthyCask()
	current := healthyCask()
	// Same team, new certificate name (e.g. company renamed).
	current.Apps[0].Authority = "Developer ID Application: ACME Renamed GmbH (ACME123456)"

	r := Score(now, current, prev)
	signing := dim(t, r, DimensionSigning)
	requireScore(t, signing, 100) // reported, no deduction
	text := findingTexts(signing)
	if !strings.Contains(text, "Developer ID Application: ACME Inc (ACME123456) -> Developer ID Application: ACME Renamed GmbH (ACME123456)") {
		t.Errorf("warning must print old and new authority names in plain text, got: %s", text)
	}
	if !strings.Contains(text, "ACME123456 unchanged") {
		t.Errorf("warning must state the team is unchanged, got: %s", text)
	}
	if !HasAuthorityNameChange(r) {
		t.Error("HasAuthorityNameChange must detect the soft warning")
	}
	if HasTeamChange(r) {
		t.Error("a name-only change must not be the hard trigger")
	}
}

func TestAuthorityChangeWithLegacySnapshotWithoutAuthorityField(t *testing.T) {
	// Old snapshots persist no authority string: the team identifier
	// alone must still trigger the hard detection.
	prev := healthyCask()
	prev.Apps[0].Authority = ""
	current := healthyCask()
	current.Apps[0].TeamID = "NEWT34M1D"
	current.Apps[0].Authority = "" // not captured this run either

	r := Score(now, current, prev)
	signing := dim(t, r, DimensionSigning)
	requireScore(t, signing, AuthorityChangeCap)
	text := findingTexts(signing)
	if !strings.Contains(text, "ACME123456 -> NEWT34M1D") {
		t.Errorf("team identifiers must appear in plain text, got: %s", text)
	}
	if !HasTeamChange(r) {
		t.Error("team change must be detected without authority strings")
	}
}

func TestAuthorityChangeDeductionAppliesToWeakestApp(t *testing.T) {
	prev := &CaskScan{
		Token: "dual",
		Apps: []AppScan{
			{Name: "A.app", Found: true, Signed: true, TeamID: "OLDTEAM01"},
			{Name: "B.app", Found: true, Signed: true, TeamID: "OTHERTEAM"},
		},
	}
	current := &CaskScan{
		Token: "dual",
		Apps: []AppScan{
			{Name: "A.app", Found: true, Signed: true, TeamID: "NEWTEAM02"},
			{Name: "B.app", Found: true, Signed: true, TeamID: "OTHERTEAM"},
		},
	}
	r := Score(now, current, prev)
	requireScore(t, dim(t, r, DimensionSigning), AuthorityChangeCap)
	if !HasTeamChange(r) {
		t.Error("team change on one app must be detected")
	}
}

func TestUnsignedApp(t *testing.T) {
	c := healthyCask()
	c.Apps[0] = AppScan{
		Name:  "Example.app",
		Path:  "/Applications/Example.app",
		Found: true,
		// unsigned: no Signed, no TeamID
	}
	r := Score(now, c, nil)
	signing := dim(t, r, DimensionSigning)
	requireScore(t, signing, 0)
	text := findingTexts(signing)
	if !strings.Contains(text, "Gatekeeper") {
		t.Errorf("unsigned finding must include a Gatekeeper explanation, got: %s", text)
	}
}

func TestAdHocApp(t *testing.T) {
	c := healthyCask()
	c.Apps[0] = AppScan{Name: "Example.app", Path: "/Applications/Example.app", Found: true, AdHoc: true}
	r := Score(now, c, nil)
	requireScore(t, dim(t, r, DimensionSigning), 0)
}

func TestCurrencyNeverGatesOverall(t *testing.T) {
	c := healthyCask()
	c.LatestReleaseTag = "v2.0.0"
	c.LatestReleaseKnown = true
	r := Score(now, c, nil)
	requireScore(t, dim(t, r, DimensionSigning), 100)
	requireScore(t, dim(t, r, DimensionCurrency), CurrencyStaleScore)
	// A stale version drags no gate: overall stays at the trust-critical minimum.
	if r.Overall == nil || *r.Overall != 100 {
		t.Errorf("overall = %v, want 100 (currency must never gate)", r.Overall)
	}
}

func TestThirdPartyTapHint(t *testing.T) {
	c := healthyCask()
	c.TapSource = "user/mytap"
	c.RepoDataAvailable = false // not in Homebrew core JSON API
	r := Score(now, c, nil)
	maintenance := dim(t, r, DimensionMaintenance)
	requireUnknown(t, maintenance)
	if !strings.Contains(findingTexts(maintenance), "not in Homebrew core API (tap: user/mytap)") {
		t.Errorf("third-party tap must be reported with an explicit hint, got: %s",
			findingTexts(maintenance))
	}
}

func TestTransparencyStatesTCCLimitation(t *testing.T) {
	// With facts...
	c := healthyCask()
	r := Score(now, c, nil)
	text := findingTexts(dim(t, r, DimensionTransparency))
	if !strings.Contains(text, "TCC permissions are not scanned") {
		t.Errorf("TCC limitation must be stated with facts present, got: %s", text)
	}

	// ...and without facts.
	r2 := Score(now, &CaskScan{Token: "bare"}, nil)
	text2 := findingTexts(dim(t, r2, DimensionTransparency))
	if !strings.Contains(text2, "TCC permissions are not scanned") {
		t.Errorf("TCC limitation must be stated without facts too, got: %s", text2)
	}
	if !strings.Contains(text2, "no LaunchAgents or login items detected") {
		t.Errorf("empty fact set must be stated explicitly, got: %s", text2)
	}
}

func TestUnknownSourceCaskIsUnknownNotZero(t *testing.T) {
	c := healthyCask()
	c.Homepage = "https://www.example.com"
	c.RepoDataAvailable = false // non-GitHub homepage
	r := Score(now, c, nil)
	requireUnknown(t, dim(t, r, DimensionMaintenance))
	// Signing and currency still score; the overall ignores maintenance.
	requireScore(t, dim(t, r, DimensionSigning), 100)
	if r.Overall == nil || *r.Overall != 100 {
		t.Errorf("overall = %v, want 100 (unknown dimension must not look like risk)", r.Overall)
	}
}

func TestDeprecatedCaskWithReplacementToken(t *testing.T) {
	c := healthyCask()
	c.Deprecated = true
	c.ReplacementToken = "example-new"
	r := Score(now, c, nil)
	maintenance := dim(t, r, DimensionMaintenance)
	requireScore(t, maintenance, 0)
	if !strings.Contains(findingTexts(maintenance), "example-new") {
		t.Error("replacement token must be surfaced in findings")
	}
}

func TestDisabledCask(t *testing.T) {
	c := healthyCask()
	c.Disabled = true
	r := Score(now, c, nil)
	requireScore(t, dim(t, r, DimensionMaintenance), 0)
}

func TestCaskWithoutAppArtifacts(t *testing.T) {
	c := healthyCask()
	c.Apps = nil // e.g. a font cask
	r := Score(now, c, nil)
	requireUnknown(t, dim(t, r, DimensionSigning))
}

func TestMultipleAppsWeakestDefinesSigning(t *testing.T) {
	c := healthyCask()
	c.Apps = append(c.Apps, AppScan{
		Name:               "Helper.app",
		Path:               "/Applications/Helper.app",
		Found:              true,
		Signed:             true,
		TeamID:             "ACME123456",
		Notarized:          false,
		GatekeeperKnown:    true,
		GatekeeperAccepted: true,
		GatekeeperSource:   "Unnotarized Developer ID",
	})
	r := Score(now, c, nil)
	requireScore(t, dim(t, r, DimensionSigning), SignedNotNotarizedScore)
}

func TestAppNotInApplicationsIsStillFound(t *testing.T) {
	c := healthyCask()
	c.Apps[0].Path = "/opt/homebrew/Caskroom/example/1.2.3/Example.app"
	r := Score(now, c, nil)
	requireScore(t, dim(t, r, DimensionSigning), 100)
}

func TestDeclaredAppMissingFromDisk(t *testing.T) {
	c := healthyCask()
	c.Apps = append(c.Apps, AppScan{Name: "Ghost.app", Found: false})
	r := Score(now, c, nil)
	signing := dim(t, r, DimensionSigning)
	requireScore(t, signing, 100) // the found app still scores
	if !strings.Contains(findingTexts(signing), "Ghost.app") {
		t.Error("missing declared app must be reported as a finding")
	}

	// All apps missing: unknown, not zero.
	c2 := healthyCask()
	c2.Apps = []AppScan{{Name: "Ghost.app", Found: false}}
	r2 := Score(now, c2, nil)
	requireUnknown(t, dim(t, r2, DimensionSigning))
}

func TestCurrencyUnknownWhenReleaseMissing(t *testing.T) {
	c := healthyCask()
	c.LatestReleaseKnown = false
	r := Score(now, c, nil)
	requireUnknown(t, dim(t, r, DimensionCurrency))

	c2 := healthyCask()
	c2.InstalledVersion = ""
	r2 := Score(now, c2, nil)
	requireUnknown(t, dim(t, r2, DimensionCurrency))
	// Unknown currency must not gate or zero the overall either way.
	if r2.Overall == nil || *r2.Overall != 100 {
		t.Errorf("overall = %v, want 100 (unknown currency excluded from gates)", r2.Overall)
	}
}

func TestAllDimensionsUnknown(t *testing.T) {
	c := &CaskScan{Token: "mystery"} // offline: no data at all
	r := Score(now, c, nil)
	for _, name := range []string{DimensionMaintenance, DimensionSigning, DimensionCurrency} {
		requireUnknown(t, dim(t, r, name))
	}
	if r.Overall != nil {
		t.Errorf("overall = %d, want unknown (nil): no data must not become a score", *r.Overall)
	}
	if len(r.Dimensions) == 0 {
		t.Fatal("result must still contain all four dimensions")
	}
}

func TestTransparencyShowsFactsNeverScores(t *testing.T) {
	c := healthyCask()
	c.LaunchAgents = []LaunchAgentFact{{
		Label: "com.example.helper", Path: "/Library/LaunchAgents/com.example.helper.plist",
		Program: "/Applications/Example.app/Contents/MacOS/helper", RunAtLoad: true,
	}}
	c.LoginItems = []LoginItemFact{{Name: "Example", Path: "/Applications/Example.app"}}
	c.DeclaredLaunchAgents = []string{"~/Library/LaunchAgents/com.example.helper.plist"}

	r := Score(now, c, nil)
	transparent := dim(t, r, DimensionTransparency)
	requireUnknown(t, transparent) // show, do not judge
	text := findingTexts(transparent)
	for _, want := range []string{"com.example.helper", "runs at login", "login item Example"} {
		if !strings.Contains(text, want) {
			t.Errorf("transparency finding %q missing, got: %s", want, text)
		}
	}
	// Transparency must not drag the overall down.
	if r.Overall == nil || *r.Overall != 100 {
		t.Errorf("overall = %v, want 100: facts must not be scored", r.Overall)
	}
}

func TestSignedRejectedByGatekeeper(t *testing.T) {
	c := healthyCask()
	c.Apps[0].Notarized = false
	c.Apps[0].GatekeeperAccepted = false
	c.Apps[0].GatekeeperSource = "Unnotarized Developer ID"
	r := Score(now, c, nil)
	requireScore(t, dim(t, r, DimensionSigning), SignedRejectedScore)
}

func TestGatekeeperUnknownIsNotARiskVerdict(t *testing.T) {
	c := healthyCask()
	c.Apps[0].GatekeeperKnown = false
	c.Apps[0].Notarized = false
	c.Apps[0].GatekeeperAccepted = false
	r := Score(now, c, nil)
	requireScore(t, dim(t, r, DimensionSigning), SignedNotNotarizedScore)
	if !strings.Contains(findingTexts(dim(t, r, DimensionSigning)), "unknown") {
		t.Error("unknown notarization must be stated, not guessed")
	}
}

func TestScoreSnapshotByKey(t *testing.T) {
	current := map[string]*CaskScan{
		"a": healthyCask(),
		"b": func() *CaskScan { c := healthyCask(); c.Token = "b"; c.Deprecated = true; return c }(),
	}
	results := ScoreSnapshot(now, current, nil)
	if len(results) != 2 {
		t.Fatalf("want 2 results, got %d", len(results))
	}
	if r := results["a"]; r.Overall == nil || *r.Overall != 100 {
		t.Errorf("a overall = %v", r.Overall)
	}
	if r := results["b"]; r.Overall == nil || *r.Overall != 0 {
		t.Errorf("b overall = %v, want 0 (deprecated)", r.Overall)
	}
}

func TestOverallIsMinimumNotAverage(t *testing.T) {
	// maintenance 100, signing 0, currency 100 -> min 0, not average 70.
	c := healthyCask()
	c.Apps[0] = AppScan{Name: "Example.app", Found: true}
	r := Score(now, c, nil)
	if r.Overall == nil || *r.Overall != 0 {
		t.Errorf("overall = %v, want 0 (minimum, never average)", r.Overall)
	}
}
