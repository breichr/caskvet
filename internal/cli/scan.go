package cli

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/breichr/caskvet/internal/collector"
	"github.com/breichr/caskvet/internal/localscan"
	"github.com/breichr/caskvet/internal/score"
)

// coreTap is the only tap served by the Homebrew JSON API.
const coreTap = "homebrew/cask"

// scanOutcome is everything one full scan produced.
type scanOutcome struct {
	scans   map[string]*score.CaskScan
	results map[string]score.Result
	// GitHub access diagnostics for the token hint (AGENTS.md).
	githubRepos   int // distinct GitHub repositories queried
	githubLimited bool
}

// scan assembles the snapshot for every installed cask and scores it.
// previous may be nil (first run): authority-change detection does not
// fire. Individual source failures degrade to unknown — they never
// fail the scan. Only infrastructure failures (brew unavailable) do.
func (e *env) scan(ctx context.Context, previous map[string]*score.CaskScan) (*scanOutcome, error) {
	installed, err := e.local.InstalledCasks()
	if err != nil {
		return nil, fmt.Errorf("listing installed casks: %w", err)
	}
	if len(installed) == 0 {
		return &scanOutcome{
			scans:   map[string]*score.CaskScan{},
			results: map[string]score.Result{},
		}, nil
	}

	tokens := make([]string, 0, len(installed))
	for _, c := range installed {
		tokens = append(tokens, c.Token)
	}

	// Tap sources: third-party taps are not in the core JSON API and
	// report maintenance unknown with a hint. Failure is tolerated.
	taps, err := e.local.CaskTaps(tokens)
	if err != nil {
		taps = map[string]string{}
	}

	// Upstream metadata for core casks only.
	coreTokens := make([]string, 0, len(tokens))
	for _, token := range tokens {
		if taps[token] == "" || taps[token] == coreTap {
			coreTokens = append(coreTokens, token)
		}
	}
	metas := e.collector.Casks(ctx, coreTokens)

	// GitHub repository and release data, batched.
	var repoPairs [][2]string
	seenRepo := make(map[string]bool)
	for _, token := range tokens {
		meta := metas[token].Meta
		owner, repo, perr := collector.ParseGitHubOwnerRepo(meta.Homepage)
		if perr != nil {
			continue
		}
		key := owner + "/" + repo
		if !seenRepo[key] {
			seenRepo[key] = true
			repoPairs = append(repoPairs, [2]string{owner, repo})
		}
	}
	repos := e.collector.GitHubRepos(ctx, repoPairs)
	releases := e.collector.LatestReleases(ctx, repoPairs)

	agents, _ := e.local.LaunchAgents()
	loginItems, loginErr := e.local.LoginItems()
	if loginErr != nil {
		loginItems = nil
	}

	outcome := &scanOutcome{scans: make(map[string]*score.CaskScan, len(installed))}
	outcome.githubRepos = len(repoPairs)

	for _, c := range installed {
		token := c.Token
		scan := &score.CaskScan{
			Token:            token,
			InstalledVersion: c.Version,
			TapSource:        taps[token],
		}
		meta, metaOK := metas[token].Meta, metas[token].Err == nil
		if metaOK {
			scan.Deprecated = meta.Deprecated
			scan.Disabled = meta.Disabled
			scan.ReplacementToken = meta.ReplacementToken
			scan.Homepage = meta.Homepage
			scan.DeclaredLaunchAgents = meta.LaunchctlPaths
			scan.DeclaredLoginItems = meta.LoginItems
		}

		if owner, repo, perr := collector.ParseGitHubOwnerRepo(scan.Homepage); perr == nil {
			key := owner + "/" + repo
			if r, ok := repos[key]; ok {
				if r.Err == nil {
					scan.RepoDataAvailable = true
					scan.RepoArchived = r.RepoData.Archived
					scan.LastCommit = r.RepoData.PushedAt
					scan.LastCommitKnown = !r.RepoData.PushedAt.IsZero()
				} else if errors.Is(r.Err, collector.ErrRateLimited) {
					outcome.githubLimited = true
				}
			}
			if r, ok := releases[key]; ok {
				if r.Err == nil {
					scan.LatestReleaseTag = r.Release.TagName
					scan.LatestReleaseKnown = true
				} else if errors.Is(r.Err, collector.ErrRateLimited) {
					outcome.githubLimited = true
				}
			}
		}

		// Local app bundles and signing state.
		var declared []string
		if metaOK {
			declared = meta.AppBundles
		}
		bundles, err := e.local.LocateAppBundles(token, c.Version, declared)
		if err != nil {
			bundles = nil
		}
		var appPaths []string
		for _, bundle := range bundles {
			app := score.AppScan{Name: bundle.Name, Path: bundle.Path, Found: bundle.Found}
			if bundle.Found {
				appPaths = append(appPaths, bundle.Path)
				cs, csErr := e.local.Codesign(bundle.Path)
				app.Signed = cs.Signed
				app.AdHoc = cs.AdHoc
				app.TeamID = cs.TeamIdentifier
				if len(cs.Authorities) > 0 {
					app.Authority = cs.Authorities[0]
				}
				if csErr != nil && localscan.ExitCode(csErr) == -1 {
					// codesign itself failed (not merely "unsigned"):
					// signing facts stay unknown, not zero.
					app.Signed = false
					app.AdHoc = false
					app.TeamID = ""
					app.Authority = ""
				}
				gk, gkErr := e.local.Gatekeeper(bundle.Path)
				if localscan.ExitCode(gkErr) >= 0 {
					app.GatekeeperKnown = true
					app.GatekeeperAccepted = gk.Accepted
					app.GatekeeperSource = gk.Source
					app.Notarized = gk.Notarized()
				}
			}
			scan.Apps = append(scan.Apps, app)
		}

		// Transparency facts: LaunchAgents and login items that belong
		// to this cask's app bundles or declared paths.
		scan.LaunchAgents = matchLaunchAgents(agents, appPaths, scan.DeclaredLaunchAgents, e.local.Home)
		for _, item := range loginItems {
			if anyPathPrefix(item.Path, appPaths) {
				scan.LoginItems = append(scan.LoginItems, score.LoginItemFact{Name: item.Name, Path: item.Path})
			}
		}

		outcome.scans[token] = scan
	}

	outcome.results = score.ScoreSnapshot(e.now, outcome.scans, previous)
	return outcome, nil
}

// matchLaunchAgents returns the agents whose program runs from inside
// one of the cask's app bundles, or whose plist file matches a path the
// cask declares. Facts only — no judgment here.
func matchLaunchAgents(agents []localscan.LaunchAgent, appPaths, declaredPaths []string, home string) []score.LaunchAgentFact {
	var facts []score.LaunchAgentFact
	for _, agent := range agents {
		belongs := anyPathPrefix(agent.Program, appPaths)
		if !belongs && len(declaredPaths) > 0 {
			expanded := expandHome(agent.Path, home)
			for _, declared := range declaredPaths {
				if expanded == expandHome(declared, home) {
					belongs = true
					break
				}
			}
		}
		if belongs {
			facts = append(facts, score.LaunchAgentFact{
				Label:     agent.Label,
				Path:      agent.Path,
				Program:   agent.Program,
				RunAtLoad: agent.RunAtLoad,
			})
		}
	}
	return facts
}

// anyPathPrefix reports whether path lies inside one of the prefixes.
func anyPathPrefix(path string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if prefix != "" && strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

// expandHome replaces a leading ~ with home.
func expandHome(path, home string) string {
	if path == "~" {
		return home
	}
	if strings.HasPrefix(path, "~/") {
		return filepath.Join(home, strings.TrimPrefix(path, "~/"))
	}
	return path
}

// tokenHintNeeded implements the GITHUB_TOKEN hint rule (AGENTS.md):
// once per run when the scan hit GitHub rate limits, or when the cask
// count visibly exceeds the unauthenticated budget (60 req/h, ~2 calls
// per cask) and no token is set.
func (o *scanOutcome) tokenHintNeeded(tokenSet bool) bool {
	if tokenSet {
		return false
	}
	return o.githubLimited || o.githubRepos*2 > 60
}
