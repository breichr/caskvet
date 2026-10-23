package score

import (
	"fmt"
	"strings"
)

// transparencyDimension lists the cask's transparency facts, weight 0.2.
// Show, do not judge: there is no sub-score here, so the dimension is
// always unknown (nil) and never part of the overall minimum. The TCC
// limitation is stated in every report so the gap is visible, never
// hidden.
func transparencyDimension(c *CaskScan) Dimension {
	dim := Dimension{Name: DimensionTransparency, Weight: WeightTransparency}

	factCount := 0
	for _, agent := range c.LaunchAgents {
		factCount++
		text := fmt.Sprintf("LaunchAgent %s (%s)", agent.Label, agent.Path)
		if agent.RunAtLoad {
			text += " runs at login"
		}
		if agent.Program != "" {
			text += fmt.Sprintf(", program: %s", agent.Program)
		}
		dim.Findings = append(dim.Findings, Finding{Severity: SeverityInfo, Text: text})
	}
	for _, item := range c.LoginItems {
		factCount++
		dim.Findings = append(dim.Findings, Finding{
			Severity: SeverityInfo,
			Text:     fmt.Sprintf("login item %s (%s)", item.Name, item.Path),
		})
	}
	for _, item := range c.BTMItems {
		factCount++
		text := fmt.Sprintf("BTM background item %s", item.Name)
		if item.Type != "" {
			text += fmt.Sprintf(" (%s)", item.Type)
		}
		switch {
		case item.ParseError != "":
			text += fmt.Sprintf(" [%s]", item.ParseError)
		case item.URL != "":
			text += fmt.Sprintf(" at %s", item.URL)
		case item.ExecutablePath != "":
			text += fmt.Sprintf(" executable %s", item.ExecutablePath)
		}
		if item.Session != "" {
			text += ", " + item.Session + " session"
		}
		if item.Disposition != "" {
			text += fmt.Sprintf(", disposition: %s", item.Disposition)
		}
		if item.DeveloperName != "" || item.TeamID != "" {
			text += fmt.Sprintf(", developer: %s (%s)", item.DeveloperName, item.TeamID)
		}
		dim.Findings = append(dim.Findings, Finding{Severity: SeverityInfo, Text: text})
	}
	if len(c.DeclaredLaunchAgents) > 0 {
		dim.Findings = append(dim.Findings, Finding{
			Severity: SeverityInfo,
			Text: fmt.Sprintf("cask declares LaunchAgents: %s",
				strings.Join(c.DeclaredLaunchAgents, ", ")),
		})
	}
	if len(c.DeclaredLoginItems) > 0 {
		dim.Findings = append(dim.Findings, Finding{
			Severity: SeverityInfo,
			Text: fmt.Sprintf("cask declares login items: %s",
				strings.Join(c.DeclaredLoginItems, ", ")),
		})
	}

	// Last-cask-update: a display-only fact about the packaging, never
	// about the upstream vendor, never part of any score.
	if c.TapChangedAt != "" {
		text := fmt.Sprintf("cask updated in tap on %s", c.TapChangedAt)
		if c.TapChangedAtFrom != "" {
			text += fmt.Sprintf(" (source: %s)", c.TapChangedAtFrom)
		}
		dim.Findings = append(dim.Findings, Finding{Severity: SeverityInfo, Text: text})
	} else {
		dim.Findings = append(dim.Findings, Finding{Severity: SeverityInfo,
			Text: "cask last tap update unknown: the Homebrew API does not provide it"})
	}

	// State an empty fact set explicitly rather than showing nothing,
	// then always state the TCC limitation so the gap is visible.
	if factCount == 0 {
		dim.Findings = append(dim.Findings, Finding{
			Severity: SeverityInfo,
			Text:     "no LaunchAgents or login items detected for this cask",
		})
	}
	dim.Findings = append(dim.Findings, Finding{
		Severity: SeverityInfo,
		Text:     TCCLimitation,
	})
	return dim
}

// currencyDimension scores the installed version against the current
// version of the cask in the Homebrew catalog — Homebrew data, not the
// upstream release (AGENTS.md). Weak signal only, by design, displayed
// and explained but never gating. Unavailable data (installed version
// or catalog version unknown) = unknown.
func currencyDimension(c *CaskScan) Dimension {
	dim := Dimension{Name: DimensionCurrency, Weight: WeightCurrency}

	if c.InstalledVersion == "" {
		dim.Findings = append(dim.Findings, Finding{
			Severity: SeverityInfo,
			Text:     "installed version unknown: currency not scored",
		})
		return dim
	}
	if c.CatalogVersion == "" {
		dim.Findings = append(dim.Findings, Finding{
			Severity: SeverityInfo,
			Text:     "current catalog version unknown: currency not scored",
		})
		return dim
	}

	installed := normalizeVersion(c.InstalledVersion)
	catalog := normalizeVersion(c.CatalogVersion)
	if installed == catalog {
		dim.Score = newScore(CurrencyCurrentScore)
		dim.Findings = append(dim.Findings, Finding{
			Severity: SeverityInfo,
			Text: fmt.Sprintf("installed version %s matches the current catalog version %s",
				c.InstalledVersion, c.CatalogVersion),
		})
		return dim
	}

	dim.Score = newScore(CurrencyStaleScore)
	dim.Findings = append(dim.Findings, Finding{
		Severity: SeverityWarning,
		Text: fmt.Sprintf("installed version %s is behind the current catalog version %s",
			c.InstalledVersion, c.CatalogVersion),
	})
	return dim
}

// normalizeVersion strips a leading "v" for tag comparison. It is a
// deliberate, documented weak comparison: exact version arithmetic is
// out of scope for a weak signal.
func normalizeVersion(v string) string {
	return strings.TrimPrefix(strings.TrimSpace(v), "v")
}
