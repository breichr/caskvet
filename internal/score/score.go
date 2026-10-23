// Package score computes the caskvet trust scores.
//
// It is pure: functions from a scan snapshot to scores, with no I/O of
// any kind. The CLI layer assembles snapshots from the collector and
// localscan packages and hands them here.
//
// Model (AGENTS.md, exact — do not improvise):
//
// Four dimensions, each 0–100, with presentation weights 0.4/0.3/0.2/0.1
// (report ordering and why-emphasis only, never score inputs). Overall
// score = min(maintenance, signing): only the two trust-critical
// dimensions gate it. Transparency lists facts and has no sub-score.
// Currency is displayed and explained but never gates. Never a weighted
// average. A dimension whose data is unavailable reports "unknown" (nil)
// and is excluded from the gate — missing knowledge must not look like
// risk.
//
//   - Maintenance, weight 0.4: repository archived = 0. Last commit > 12
//     months = hard deduction. Cask deprecated/disabled in core = 0,
//     referencing the replacement token if Homebrew defines one.
//   - Signing, weight 0.3: notarized = full. A signing authority change
//     since the previous snapshot is a deduction plus a warning printing
//     the old and new authority strings (team identifier plus name) in
//     plain text. A team-identifier change is the Bartender detection and
//     always triggers the watch warning. A name-only change at the same
//     team is reported but does not reduce the score. Ad-hoc/unsigned = 0
//     with a Gatekeeper explanation.
//   - Transparency, weight 0.2: LaunchAgents, login items, and declared
//     background items are listed as facts. Show, do not judge. TCC
//     permissions are not scanned; the report states that limitation.
//   - Currency, weight 0.1: installed version vs. latest upstream
//     release. Weak signal only; displayed, never gating.
package score

import "time"

// Dimension names, in report order.
const (
	DimensionMaintenance  = "maintenance"
	DimensionSigning      = "signing"
	DimensionTransparency = "transparency"
	DimensionCurrency     = "currency"
)

// Declared presentation weights (AGENTS.md). They order emphasis in the
// report and in `why`; they are not score inputs.
const (
	WeightMaintenance  = 0.4
	WeightSigning      = 0.3
	WeightTransparency = 0.2
	WeightCurrency     = 0.1
)

// Scoring constants. Deduction values are part of the documented model;
// change them only with a README update.
const (
	// Maintenance: a repository with no commit in over StaleCommit gets a
	// hard deduction.
	StaleCommit      = 12 * 30 * 24 * time.Hour
	StaleCommitScore = 30

	// Signing sub-scores.
	NotarizedSignedScore    = 100 // notarized = full
	SignedNotNotarizedScore = 70  // signed, Gatekeeper accepted, not notarized
	SignedRejectedScore     = 20  // signed but Gatekeeper rejected
	AdHocUnsignedScore      = 0   // ad-hoc or unsigned
	AuthorityChangeCap      = 50  // team identifier changed since last snapshot

	// Currency: weak signal only.
	CurrencyCurrentScore = 100
	CurrencyStaleScore   = 70
)

// TCCLimitation is stated in every transparency dimension so the gap is
// visible, never hidden (AGENTS.md).
const TCCLimitation = "TCC permissions are not scanned: reading them requires privacy prompts, which a trust tool must not trigger"

// Severity classifies a finding for output formatting.
type Severity string

const (
	SeverityInfo    Severity = "info"
	SeverityWarning Severity = "warning"
)

// Finding is one human-readable scoring justification.
type Finding struct {
	Severity Severity `json:"severity"`
	Text     string   `json:"text"`
}

// Dimension is one scored axis of a cask.
type Dimension struct {
	Name     string    `json:"name"`
	Weight   float64   `json:"weight"`
	Score    *int      `json:"score"` // nil = unknown
	Findings []Finding `json:"findings,omitempty"`
}

// Result is the score of one cask.
type Result struct {
	Token      string      `json:"token"`
	TapSource  string      `json:"tap_source,omitempty"`
	Overall    *int        `json:"overall"` // nil = unknown (no gate dimension had data)
	Dimensions []Dimension `json:"dimensions"`
}

// CaskScan is the scan snapshot for one cask: everything the score needs.
// The CLI layer fills it from collector and localscan outputs. Zero
// values mean "no data", which scores as unknown — never as risk.
type CaskScan struct {
	Token            string `json:"token"`
	InstalledVersion string `json:"installed_version"`

	// TapSource is the tap the cask came from ("homebrew/cask" for
	// core). Third-party taps are not in the Homebrew core JSON API and
	// report maintenance unknown with an explicit hint.
	TapSource string `json:"tap_source,omitempty"`

	// Upstream cask state (Homebrew JSON API).
	Deprecated       bool   `json:"deprecated"`
	Disabled         bool   `json:"disabled"`
	ReplacementToken string `json:"replacement_token,omitempty"`

	// Upstream repository state (GitHub REST API).
	Homepage          string    `json:"homepage,omitempty"`
	RepoDataAvailable bool      `json:"repo_data_available"`
	RepoArchived      bool      `json:"repo_archived"`
	LastCommit        time.Time `json:"last_commit"`
	LastCommitKnown   bool      `json:"last_commit_known"`

	// Local signing state, one entry per app bundle declared by the cask.
	Apps []AppScan `json:"apps"`

	// Transparency facts: discovered and declared background items.
	LaunchAgents         []LaunchAgentFact `json:"launch_agents,omitempty"`
	LoginItems           []LoginItemFact   `json:"login_items,omitempty"`
	DeclaredLaunchAgents []string          `json:"declared_launch_agents,omitempty"`
	DeclaredLoginItems   []string          `json:"declared_login_items,omitempty"`

	// TapChangedAt is when the cask file was last updated in its tap
	// (Homebrew API `changed_at`, core casks). Display-only: a
	// transparency fact about the packaging, not the vendor.
	TapChangedAt     string `json:"tap_changed_at,omitempty"`
	TapChangedAtFrom string `json:"tap_changed_at_from,omitempty"` // "homebrew_api" when known

	// Currency: the current version of the cask in the Homebrew
	// catalog. This is Homebrew data, NOT the upstream release.
	CatalogVersion     string `json:"catalog_version,omitempty"`
	CatalogVersionFrom string `json:"catalog_version_from,omitempty"` // "homebrew_api" | "brew_info"
}

// AppScan is the local signing state of one app bundle.
type AppScan struct {
	Name string `json:"name"`
	Path string `json:"path"`
	// Found is false when a declared bundle is missing from disk.
	Found bool `json:"found"`

	Signed bool   `json:"signed"`
	AdHoc  bool   `json:"ad_hoc"`
	TeamID string `json:"team_id"`
	// Authority is the full signing authority string, e.g.
	// "Developer ID Application: ACME Inc (ACME123456)". It is compared
	// between snapshots to detect name-only authority changes.
	Authority          string `json:"authority,omitempty"`
	Notarized          bool   `json:"notarized"`
	GatekeeperKnown    bool   `json:"gatekeeper_known"`
	GatekeeperAccepted bool   `json:"gatekeeper_accepted"`
	GatekeeperSource   string `json:"gatekeeper_source"`
}

// LaunchAgentFact is one LaunchAgent associated with the cask's apps.
type LaunchAgentFact struct {
	Label     string `json:"label"`
	Path      string `json:"path"`
	Program   string `json:"program"`
	RunAtLoad bool   `json:"run_at_load"`
}

// LoginItemFact is one login item associated with the cask's apps.
type LoginItemFact struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// Score computes the result for one cask. previous may be nil (first
// run): authority-change detection simply does not fire. now is
// injected so scoring is fully deterministic.
func Score(now time.Time, current, previous *CaskScan) Result {
	if current == nil {
		current = &CaskScan{}
	}
	result := Result{
		Token:      current.Token,
		TapSource:  current.TapSource,
		Dimensions: make([]Dimension, 0, 4),
	}

	maintenance := maintenanceDimension(now, current)
	result.Dimensions = append(result.Dimensions, maintenance)
	result.Dimensions = append(result.Dimensions, signingDimension(current, previous))
	result.Dimensions = append(result.Dimensions, transparencyDimension(current))
	result.Dimensions = append(result.Dimensions, currencyDimension(current))

	// Overall = min(maintenance, signing). Only the two trust-critical
	// dimensions gate the overall score. Currency is displayed and
	// explained but never gates; transparency reports facts only.
	var overall *int
	for _, dim := range result.Dimensions {
		if dim.Score == nil {
			continue
		}
		if dim.Name != DimensionMaintenance && dim.Name != DimensionSigning {
			continue
		}
		if overall == nil || *dim.Score < *overall {
			s := *dim.Score
			overall = &s
		}
	}
	result.Overall = overall
	return result
}

// ScoreSnapshot scores every cask in a snapshot against an optional
// previous snapshot, keyed by token.
func ScoreSnapshot(now time.Time, current map[string]*CaskScan, previous map[string]*CaskScan) map[string]Result {
	results := make(map[string]Result, len(current))
	for token, cask := range current {
		results[token] = Score(now, cask, previous[token])
	}
	return results
}
