package score

import (
	"fmt"
	"sort"
	"strings"
)

// signingDimension scores code-signing state, weight 0.3.
//
// Rules (AGENTS.md): notarized = full. The signing certificate changing
// since the previous snapshot is a deduction plus a warning that prints
// the old and new team identifiers in plain text (the Bartender
// detection). Ad-hoc/unsigned = 0 with a Gatekeeper explanation.
// Multiple app bundles: the weakest bundle defines the dimension.
// No app artifacts or no bundles found on disk: unknown.
func signingDimension(current, previous *CaskScan) Dimension {
	dim := Dimension{Name: DimensionSigning, Weight: WeightSigning}

	if len(current.Apps) == 0 {
		dim.Findings = append(dim.Findings, Finding{
			Severity: SeverityInfo,
			Text:     "cask declares no app artifacts: signing state unknown, not scored",
		})
		return dim
	}

	found := make([]AppScan, 0, len(current.Apps))
	for _, app := range current.Apps {
		if app.Found {
			found = append(found, app)
			continue
		}
		dim.Findings = append(dim.Findings, Finding{
			Severity: SeverityWarning,
			Text:     fmt.Sprintf("declared app bundle %s was not found on disk", app.Name),
		})
	}
	if len(found) == 0 {
		dim.Findings = append(dim.Findings, Finding{
			Severity: SeverityInfo,
			Text:     "no declared app bundles found on disk: signing state unknown, not scored",
		})
		return dim
	}

	worst := 100
	for _, app := range found {
		score, finding := appSigningScore(app)
		dim.Findings = append(dim.Findings, finding)
		if score < worst {
			worst = score
		}
	}
	dim.Score = newScore(worst)

	// The Bartender detection: compare team identifiers against the
	// previous snapshot and print old and new values in plain text.
	for _, change := range certificateChanges(current, previous) {
		dim.Findings = append(dim.Findings, Finding{
			Severity: SeverityWarning,
			Text: fmt.Sprintf("signing certificate changed for %s: %s -> %s",
				change.app, change.oldTeam, change.newTeam),
		})
		if *dim.Score > CertChangeCap {
			dim.Score = newScore(CertChangeCap)
		}
	}
	return dim
}

// appSigningScore scores one app bundle and explains the verdict.
func appSigningScore(app AppScan) (int, Finding) {
	switch {
	case app.AdHoc || !app.Signed:
		return AdHocUnsignedScore, Finding{
			Severity: SeverityWarning,
			Text: fmt.Sprintf("%s is not signed (ad-hoc or unsigned): Gatekeeper blocks first-run "+
				"unless the user overrides it explicitly", app.Name),
		}
	case app.GatekeeperKnown && app.GatekeeperAccepted && app.Notarized:
		return NotarizedSignedScore, Finding{
			Severity: SeverityInfo,
			Text:     fmt.Sprintf("%s is signed and notarized (team %s)", app.Name, app.TeamID),
		}
	case app.GatekeeperKnown && app.GatekeeperAccepted:
		return SignedNotNotarizedScore, Finding{
			Severity: SeverityWarning,
			Text: fmt.Sprintf("%s is signed but not notarized (source: %s, team %s)",
				app.Name, app.GatekeeperSource, app.TeamID),
		}
	case app.GatekeeperKnown && !app.GatekeeperAccepted:
		return SignedRejectedScore, Finding{
			Severity: SeverityWarning,
			Text: fmt.Sprintf("Gatekeeper rejects %s (source: %s, team %s)",
				app.Name, app.GatekeeperSource, app.TeamID),
		}
	default:
		return SignedNotNotarizedScore, Finding{
			Severity: SeverityInfo,
			Text: fmt.Sprintf("%s is signed (team %s) but notarization state is unknown",
				app.Name, app.TeamID),
		}
	}
}

// certChange records one team-identifier change between snapshots.
type certChange struct {
	app              string
	oldTeam, newTeam string
}

// certificateChanges compares found, signed app bundles between the
// current and previous snapshot and returns the team-identifier changes.
// App bundles missing from either snapshot are skipped: a missing
// certificate is unknown, not a change.
func certificateChanges(current, previous *CaskScan) []certChange {
	if previous == nil {
		return nil
	}
	prevApps := make(map[string]AppScan, len(previous.Apps))
	for _, app := range previous.Apps {
		prevApps[app.Name] = app
	}

	var changes []certChange
	seen := make(map[string]bool)
	for _, app := range current.Apps {
		if !app.Found || !app.Signed || app.TeamID == "" || seen[app.Name] {
			continue
		}
		seen[app.Name] = true
		prev, ok := prevApps[app.Name]
		if !ok || !prev.Found || !prev.Signed || prev.TeamID == "" {
			continue
		}
		if app.TeamID != prev.TeamID {
			changes = append(changes, certChange{
				app:     app.Name,
				oldTeam: prev.TeamID,
				newTeam: app.TeamID,
			})
		}
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].app < changes[j].app })
	return changes
}

// HasCertificateChange reports whether the signing dimension produced a
// certificate-change warning. Used by watch mode as one of the two
// notification triggers.
func HasCertificateChange(result Result) bool {
	for _, dim := range result.Dimensions {
		if dim.Name != DimensionSigning {
			continue
		}
		for _, f := range dim.Findings {
			if f.Severity == SeverityWarning && strings.HasPrefix(f.Text, "signing certificate changed") {
				return true
			}
		}
	}
	return false
}
