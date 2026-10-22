package score

import (
	"fmt"
	"time"
)

// maintenanceDimension scores upstream maintenance health, weight 0.4.
//
// Rules (AGENTS.md): cask deprecated or disabled in core = 0 (the
// replacement token is surfaced when Homebrew defines one). Repository
// archived = 0. Last commit older than 12 months = hard deduction.
// Unavailable data = unknown.
func maintenanceDimension(now time.Time, c *CaskScan) Dimension {
	dim := Dimension{Name: DimensionMaintenance, Weight: WeightMaintenance}

	if c.Deprecated || c.Disabled {
		state := "deprecated"
		if c.Disabled {
			state = "disabled"
		}
		dim.Score = newScore(0)
		dim.Findings = append(dim.Findings, Finding{
			Severity: SeverityWarning,
			Text:     fmt.Sprintf("cask is %s in homebrew/cask", state),
		})
		if c.ReplacementToken != "" {
			dim.Findings = append(dim.Findings, Finding{
				Severity: SeverityInfo,
				Text:     fmt.Sprintf("homebrew defines a replacement cask: %s", c.ReplacementToken),
			})
		}
		return dim
	}

	if !c.RepoDataAvailable {
		dim.Findings = append(dim.Findings, Finding{
			Severity: SeverityInfo,
			Text: "upstream repository data unavailable " +
				"(non-GitHub homepage or API unreachable): unknown, not scored",
		})
		return dim
	}

	if c.RepoArchived {
		dim.Score = newScore(0)
		dim.Findings = append(dim.Findings, Finding{
			Severity: SeverityWarning,
			Text:     "upstream repository is archived",
		})
		return dim
	}

	if !c.LastCommitKnown {
		dim.Findings = append(dim.Findings, Finding{
			Severity: SeverityInfo,
			Text:     "last commit date unavailable: unknown, not scored",
		})
		return dim
	}

	age := now.Sub(c.LastCommit)
	months := int(age.Hours() / 24 / 30)
	if age > StaleCommit {
		s := StaleCommitScore
		dim.Score = &s
		dim.Findings = append(dim.Findings, Finding{
			Severity: SeverityWarning,
			Text: fmt.Sprintf("last upstream commit was %d months ago (over 12): hard deduction",
				months),
		})
		return dim
	}

	dim.Score = newScore(100)
	dim.Findings = append(dim.Findings, Finding{
		Severity: SeverityInfo,
		Text:     fmt.Sprintf("last upstream commit was %d months ago", months),
	})
	return dim
}

// newScore returns a pointer to a dimension score value.
func newScore(v int) *int {
	s := v
	return &s
}
