// Package cli wires the collector, localscan, and score packages into
// the caskvet commands: report, why, watch, and the install stub.
//
// Exit codes are defined here, centrally (AGENTS.md): 0 = ok,
// 1 = config/usage/runtime error, 2 = scan completed with at-risk
// findings. cmd/caskvet owns the single os.Exit call.
package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/breichr/caskvet/internal/collector"
	"github.com/breichr/caskvet/internal/localscan"
	"github.com/breichr/caskvet/internal/score"
)

// Exit codes (AGENTS.md). 0 = ok, 1 = config/usage error, 2 = scan
// completed with at-risk findings.
const (
	ExitOK     = 0
	ExitError  = 1
	ExitAtRisk = 2
)

// Options overrides production defaults, mostly for tests. Zero values
// mean "use the real thing".
type Options struct {
	Stdout io.Writer
	Stderr io.Writer

	// Collector overrides.
	HomebrewBaseURL string
	GitHubBaseURL   string
	GitHubToken     string
	CacheDir        string

	// Localscan overrides (runner, dirs).
	LocalscanOptions []localscan.ScannerOption

	// SnapshotDir overrides the snapshot directory.
	SnapshotDir string

	// NotifyRunner executes the osascript notification command.
	// Defaults to localscan.ExecRunner.
	NotifyRunner localscan.Runner

	// Now returns the current time. Defaults to time.Now.
	Now func() time.Time
}

// env carries everything a command needs.
type env struct {
	opts        Options
	out, errOut io.Writer
	collector   *collector.Client
	local       *localscan.Scanner
	snapDir     string
	now         time.Time
	tokenSet    bool
}

func newEnv(o Options) (*env, error) {
	if o.Stdout == nil {
		o.Stdout = os.Stdout
	}
	if o.Stderr == nil {
		o.Stderr = os.Stderr
	}

	colOpts := []collector.Options{}
	if o.HomebrewBaseURL != "" {
		colOpts = append(colOpts, collector.WithHomebrewBaseURL(o.HomebrewBaseURL))
	}
	if o.GitHubBaseURL != "" {
		colOpts = append(colOpts, collector.WithGitHubBaseURL(o.GitHubBaseURL))
	}
	if o.GitHubToken != "" {
		colOpts = append(colOpts, collector.WithToken(o.GitHubToken))
	}
	if o.CacheDir != "" {
		colOpts = append(colOpts, collector.WithCacheDir(o.CacheDir))
	}
	col, err := collector.New(colOpts...)
	if err != nil {
		return nil, fmt.Errorf("initializing collector: %w", err)
	}

	snapDir := o.SnapshotDir
	if snapDir == "" {
		configDir, err := os.UserConfigDir()
		if err != nil {
			return nil, fmt.Errorf("resolving config dir: %w", err)
		}
		snapDir = filepath.Join(configDir, "caskvet", "snapshots")
	}

	now := time.Now()
	if o.Now != nil {
		now = o.Now()
	}

	return &env{
		opts:      o,
		out:       o.Stdout,
		errOut:    o.Stderr,
		collector: col,
		local:     localscan.New(o.LocalscanOptions...),
		snapDir:   snapDir,
		now:       now,
		tokenSet:  o.GitHubToken != "" || os.Getenv("GITHUB_TOKEN") != "",
	}, nil
}

// version is overridden at build time by the release tooling
// (goreleaser injects the tag via -ldflags).
var version = "dev"

// Run parses arguments and executes the command, returning the process
// exit code.
func Run(args []string, o Options) int {
	e, err := newEnv(o)
	if err != nil {
		fmt.Fprintf(o.Stderr, "caskvet: %v\n", err)
		return ExitError
	}

	if len(args) == 0 {
		printUsage(e.errOut)
		return ExitError
	}
	ctx := context.Background()

	switch args[0] {
	case "version", "--version", "-v":
		fmt.Fprintf(e.out, "caskvet %s\n", version)
		return ExitOK
	case "help", "--help", "-h":
		printUsage(e.out)
		return ExitOK
	case "report":
		return e.cmdReport(ctx, args[1:])
	case "why":
		return e.cmdWhy(ctx, args[1:])
	case "watch":
		return e.cmdWatch(ctx, args[1:])
	case "install":
		return e.cmdInstall(ctx, args[1:])
	default:
		fmt.Fprintf(e.errOut, "caskvet: unknown command %q\n", args[0])
		printUsage(e.errOut)
		return ExitError
	}
}

func printUsage(w io.Writer) {
	fmt.Fprint(w, `caskvet — read-only supply-chain health report for installed Homebrew casks

Usage:
  caskvet report [--json]    scan and print the report table
  caskvet why <cask>         detailed scoring justification for one cask
  caskvet watch              compare against the last snapshot, notify on triggers
  caskvet install <cask>     pre-install gate (read-only check)
  caskvet version            print the version
  caskvet help               this help

Exit codes: 0 = ok, 1 = config/usage error, 2 = scan completed with at-risk findings.
`)
}

// atRisk implements the exit-2 definition (AGENTS.md, exact): a
// completed scan is at-risk when any cask has a known overall score
// below 50, a known maintenance or signing score of 0, or a signing
// authority team change fired. Currency and transparency never do.
func atRisk(result score.Result) bool {
	if result.Overall != nil && *result.Overall < 50 {
		return true
	}
	for _, d := range result.Dimensions {
		if d.Score == nil || *d.Score != 0 {
			continue
		}
		if d.Name == score.DimensionMaintenance || d.Name == score.DimensionSigning {
			return true
		}
	}
	return score.HasTeamChange(result)
}
