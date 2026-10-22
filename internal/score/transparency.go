package score

import (
	"fmt"
	"strings"
)

// transparencyDimension lists the cask's transparency facts, weight 0.2.
// Show, do not judge: there is no sub-score here, so the dimension is
// always unknown (nil) and never part of the overall minimum.
func transparencyDimension(c *CaskScan) Dimension {
	dim := Dimension{Name: DimensionTransparency, Weight: WeightTransparency}

	for _, agent := range c.LaunchAgents {
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
		dim.Findings = append(dim.Findings, Finding{
			Severity: SeverityInfo,
			Text:     fmt.Sprintf("login item %s (%s)", item.Name, item.Path),
		})
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

	if len(dim.Findings) == 0 {
		dim.Findings = append(dim.Findings, Finding{
			Severity: SeverityInfo,
			Text:     "no LaunchAgents or login items detected for this cask",
		})
	}
	return dim
}

// currencyDimension scores installed version against the latest upstream
// release, weight 0.1. Weak signal only, by design. Unavailable data
// (installed version or release unknown) = unknown.
func currencyDimension(c *CaskScan) Dimension {
	dim := Dimension{Name: DimensionCurrency, Weight: WeightCurrency}

	if c.InstalledVersion == "" {
		dim.Findings = append(dim.Findings, Finding{
			Severity: SeverityInfo,
			Text:     "installed version unknown: currency not scored",
		})
		return dim
	}
	if !c.LatestReleaseKnown || c.LatestReleaseTag == "" {
		dim.Findings = append(dim.Findings, Finding{
			Severity: SeverityInfo,
			Text:     "latest upstream release unknown: currency not scored",
		})
		return dim
	}

	installed := normalizeVersion(c.InstalledVersion)
	latest := normalizeVersion(c.LatestReleaseTag)
	if installed == latest {
		dim.Score = newScore(CurrencyCurrentScore)
		dim.Findings = append(dim.Findings, Finding{
			Severity: SeverityInfo,
			Text: fmt.Sprintf("installed version %s matches the latest release %s",
				c.InstalledVersion, c.LatestReleaseTag),
		})
		return dim
	}

	dim.Score = newScore(CurrencyStaleScore)
	dim.Findings = append(dim.Findings, Finding{
		Severity: SeverityWarning,
		Text: fmt.Sprintf("installed version %s is behind the latest release %s",
			c.InstalledVersion, c.LatestReleaseTag),
	})
	return dim
}

// normalizeVersion strips a leading "v" for tag comparison. It is a
// deliberate, documented weak comparison: exact version arithmetic is
// out of scope for a weak signal.
func normalizeVersion(v string) string {
	return strings.TrimPrefix(strings.TrimSpace(v), "v")
}
