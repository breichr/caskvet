package score

import (
	"fmt"
	"sort"
	"strings"
)

// signingDimension scores code-signing state, weight 0.3.
//
// Rules (AGENTS.md): notarized = full. A signing authority change since
// the previous snapshot is a deduction plus a warning printing the old
// and new authority strings (team identifier plus name) in plain text.
// A team-identifier change is the Bartender detection and always
// triggers the watch warning. A name-only change at the same team is
// reported but does not reduce the score. Ad-hoc/unsigned = 0 with a
// Gatekeeper explanation. Multiple app bundles: the weakest bundle
// defines the dimension. No app artifacts or no bundles found on disk:
// unknown.
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
		appScore, finding := appSigningScore(app)
		dim.Findings = append(dim.Findings, finding)
		if appScore < worst {
			worst = appScore
		}
	}
	dim.Score = newScore(worst)

	// The Bartender detection: compare signing authorities against the
	// previous snapshot and print old and new values in plain text.
	for _, change := range authorityChanges(current, previous) {
		if change.teamChanged {
			dim.Findings = append(dim.Findings, Finding{
				Severity: SeverityWarning,
				Text: fmt.Sprintf("signing authority changed for %s: %s -> %s",
					change.app, change.oldAuthority, change.newAuthority),
			})
			if *dim.Score > AuthorityChangeCap {
				dim.Score = newScore(AuthorityChangeCap)
			}
		} else {
			// Name-only change at the same team: reported, no deduction.
			dim.Findings = append(dim.Findings, Finding{
				Severity: SeverityWarning,
				Text: fmt.Sprintf("signing authority name changed for %s (team %s unchanged): %s -> %s",
					change.app, change.newTeam, change.oldAuthority, change.newAuthority),
			})
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
		var parts []string
		if app.GatekeeperSource != "" {
			parts = append(parts, "source: "+app.GatekeeperSource)
		}
		if app.TeamID != "" {
			parts = append(parts, "team: "+app.TeamID)
		}
		text := fmt.Sprintf("Gatekeeper rejects %s", app.Name)
		if len(parts) > 0 {
			text += " (" + strings.Join(parts, ", ") + ")"
		}
		if app.GatekeeperMessage != "" {
			text += ": " + app.GatekeeperMessage
		}
		return SignedRejectedScore, Finding{
			Severity: SeverityWarning,
			Text:     text,
		}
	default:
		return SignedNotNotarizedScore, Finding{
			Severity: SeverityInfo,
			Text: fmt.Sprintf("%s is signed (team %s) but notarization state is unknown",
				app.Name, app.TeamID),
		}
	}
}

// authorityChange records one signing-authority difference between
// snapshots. teamChanged distinguishes the hard trigger (team
// identifier change, the Bartender detection) from a name-only change.
type authorityChange struct {
	app          string
	oldAuthority string
	newAuthority string
	oldTeam      string
	newTeam      string
	teamChanged  bool
}

// authorityChanges compares found, signed app bundles between the
// current and previous snapshot. App bundles missing from either
// snapshot are skipped: a missing authority is unknown, not a change.
// Authority strings fall back to the bare team identifier when the
// authority line was not captured, so old snapshots without the
// authority field still detect team changes.
func authorityChanges(current, previous *CaskScan) []authorityChange {
	if previous == nil {
		return nil
	}
	prevApps := make(map[string]AppScan, len(previous.Apps))
	for _, app := range previous.Apps {
		prevApps[app.Name] = app
	}

	var changes []authorityChange
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
		if app.TeamID == prev.TeamID && app.Authority == prev.Authority {
			continue
		}
		changes = append(changes, authorityChange{
			app:          app.Name,
			oldAuthority: authorityString(prev.Authority, prev.TeamID),
			newAuthority: authorityString(app.Authority, app.TeamID),
			oldTeam:      prev.TeamID,
			newTeam:      app.TeamID,
			teamChanged:  app.TeamID != prev.TeamID,
		})
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].app < changes[j].app })
	return changes
}

// authorityString returns the authority line, falling back to the team
// identifier when the line was not captured.
func authorityString(authority, teamID string) string {
	if authority != "" {
		return authority
	}
	return teamID
}

// HasTeamChange reports whether any signing-authority warning with a
// team-identifier change fired — the hard watch trigger and an
// exit-2 condition.
func HasTeamChange(result Result) bool {
	for _, dim := range result.Dimensions {
		if dim.Name != DimensionSigning {
			continue
		}
		for _, f := range dim.Findings {
			if f.Severity == SeverityWarning && strings.HasPrefix(f.Text, "signing authority changed for") {
				return true
			}
		}
	}
	return false
}

// HasAuthorityNameChange reports whether a name-only authority change
// warning fired: same team, new certificate name. A softer, separate
// warning — it never reduces the score.
func HasAuthorityNameChange(result Result) bool {
	for _, dim := range result.Dimensions {
		if dim.Name != DimensionSigning {
			continue
		}
		for _, f := range dim.Findings {
			if f.Severity == SeverityWarning && strings.HasPrefix(f.Text, "signing authority name changed for") {
				return true
			}
		}
	}
	return false
}
