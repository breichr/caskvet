package cli

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/breichr/caskvet/internal/collector"
	"github.com/breichr/caskvet/internal/score"
)

// installTokenPattern matches Homebrew cask tokens, including the "@"
// of versioned tokens such as "1password-cli@1".
var installTokenPattern = regexp.MustCompile(`^[a-z0-9@-]+$`)

// cmdInstall is the pre-install gate. It evaluates a cask you are
// CONSIDERING installing, read-only: caskvet never installs anything.
// The verdict gates on the data that exists before an install —
// maintenance and declared facts. Signing is unknown by design until
// the bundle is on disk; `caskvet report` verifies it after install.
func (e *env) cmdInstall(ctx context.Context, args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(e.errOut, "caskvet install: exactly one cask token required (pre-install gate; caskvet never installs anything)")
		return ExitError
	}
	token := args[0]
	if !installTokenPattern.MatchString(token) {
		fmt.Fprintf(e.errOut, "caskvet install: %q is not a valid cask token\n", token)
		return ExitError
	}

	// Metadata from one read-only brew info call: covers every tap,
	// installed or not.
	infos, err := e.local.CaskInfo([]string{token})
	if err != nil {
		fmt.Fprintf(e.errOut, "caskvet install: %v\n", err)
		return ExitError
	}
	info, ok := infos[token]
	if !ok {
		fmt.Fprintf(e.errOut, "caskvet install: no cask named %q found in your taps\n", token)
		return ExitError
	}

	scan := &score.CaskScan{
		Token:                token,
		TapSource:            info.Tap,
		Deprecated:           info.Deprecated,
		Disabled:             info.Disabled,
		ReplacementToken:     info.Replacement,
		Homepage:             info.Homepage,
		CatalogVersion:       info.Version,
		CatalogVersionFrom:   "brew_info",
		DeclaredLaunchAgents: info.Launchctl,
		DeclaredLoginItems:   info.LoginItems,
	}

	// Upstream repository data, same rules as a scan. Currency is
	// Homebrew catalog data (brew info above), never the upstream
	// release.
	outcome := &scanOutcome{}
	if owner, repo, perr := collector.ParseGitHubOwnerRepo(info.Homepage); perr == nil {
		outcome.githubRepos = 1
		if r, err := e.collector.GitHubRepo(ctx, owner, repo); err == nil {
			scan.RepoDataAvailable = true
			scan.RepoArchived = r.Archived
			scan.LastCommit = r.PushedAt
			scan.LastCommitKnown = !r.PushedAt.IsZero()
		} else if isRateLimited(err) {
			outcome.githubLimited = true
		}
	}

	result := score.Score(e.now, scan, nil)

	fmt.Fprintf(e.out, "caskvet install gate: %s", token)
	if info.Tap != "" {
		fmt.Fprintf(e.out, " (tap: %s)", info.Tap)
	}
	if info.Version != "" {
		fmt.Fprintf(e.out, " version %s", info.Version)
	}
	fmt.Fprintln(e.out)

	if len(info.AppBundles) > 0 {
		fmt.Fprintf(e.out, "  would install: %s\n", strings.Join(info.AppBundles, ", "))
	} else {
		fmt.Fprintln(e.out, "  would install: no app artifacts (pkg/font/binary cask)")
	}

	dim := func(name string) (score.Dimension, bool) {
		for _, d := range result.Dimensions {
			if d.Name == name {
				return d, true
			}
		}
		return score.Dimension{}, false
	}

	if m, ok := dim(score.DimensionMaintenance); ok {
		fmt.Fprintf(e.out, "  maintenance: %s\n", dimensionLabel(result, score.DimensionMaintenance))
		for _, f := range m.Findings {
			fmt.Fprintf(e.out, "    [%s] %s\n", f.Severity, f.Text)
		}
	}
	// Signing cannot be verified before the bundle exists on disk.
	fmt.Fprintln(e.out, "  signing: unknown — verified by `caskvet report` after install")
	if t, ok := dim(score.DimensionTransparency); ok {
		for _, f := range t.Findings {
			if strings.HasPrefix(f.Text, "no LaunchAgents") ||
				strings.HasPrefix(f.Text, "cask declares") {
				fmt.Fprintf(e.out, "  %s\n", f.Text)
			}
		}
	}
	if scan.CatalogVersion != "" {
		fmt.Fprintf(e.out, "  current catalog version: %s\n", scan.CatalogVersion)
	}

	if outcome.tokenHintNeeded(e.tokenSet) {
		fmt.Fprintln(e.errOut, tokenHint)
	}

	if atRisk(result) {
		fmt.Fprintf(e.out, "verdict: AT-RISK — review the findings above before installing\n")
		return ExitAtRisk
	}
	fmt.Fprintf(e.out, "verdict: no at-risk findings — proceed with: brew install --cask %s\n", token)
	fmt.Fprintln(e.out, "caskvet never installs anything; it only checks.")
	return ExitOK
}

// isRateLimited reports whether an error carries the collector
// rate-limit sentinel.
func isRateLimited(err error) bool {
	return errors.Is(err, collector.ErrRateLimited)
}
