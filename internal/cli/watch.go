package cli

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/breichr/caskvet/internal/localscan"
	"github.com/breichr/caskvet/internal/score"
)

// notify posts a macOS notification via osascript — the only non-read
// action in the entire codebase (AGENTS.md).
func (e *env) notify(title, body string) error {
	runner := e.opts.NotifyRunner
	if runner == nil {
		runner = localscan.ExecRunner{}
	}
	script := fmt.Sprintf("display notification %s with title %s",
		appleScriptString(body), appleScriptString(title))
	_, err := runner.Run("osascript", "-e", script)
	return err
}

// appleScriptString quotes a string for an AppleScript literal.
func appleScriptString(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
}

// cmdWatch scans, compares against the last snapshot, notifies on the
// two triggers, and stores the new snapshot. Only two events produce a
// notification (AGENTS.md): an overall score dropping below 50, and a
// signing-authority team-identifier change. A name-only authority
// change is a softer, separate warning in the diff output only.
func (e *env) cmdWatch(ctx context.Context, args []string) int {
	if len(args) != 0 {
		fmt.Fprintln(e.errOut, "caskvet watch: no arguments expected")
		return ExitError
	}

	prev, err := loadLatestSnapshot(e.snapDir)
	switch {
	case err == ErrNoSnapshot:
		prev = nil
		fmt.Fprintln(e.out, "caskvet watch: no previous snapshot, this scan becomes the baseline")
	case err != nil:
		fmt.Fprintf(e.errOut, "caskvet watch: reading last snapshot: %v\n", err)
		return ExitError
	}

	var prevCasks map[string]*score.CaskScan
	if prev != nil {
		prevCasks = prev.Casks
		fmt.Fprintf(e.out, "caskvet watch: comparing against snapshot %s (%d casks)\n",
			prev.Timestamp.Format("2006-01-02 15:04"), len(prevCasks))
	}

	outcome, err := e.scan(ctx, prevCasks)
	if err != nil {
		fmt.Fprintf(e.errOut, "caskvet: %v\n", err)
		return ExitError
	}

	// Diff and triggers.
	tokens := make([]string, 0, len(outcome.scans)+len(prevCasks))
	seen := make(map[string]bool)
	for token := range outcome.scans {
		tokens = append(tokens, token)
		seen[token] = true
	}
	var removed []string
	for token := range prevCasks {
		if !seen[token] {
			removed = append(removed, token)
		}
	}
	sort.Strings(tokens)
	sort.Strings(removed)

	for _, token := range tokens {
		result := outcome.results[token]
		if prevCasks[token] == nil {
			fmt.Fprintf(e.out, "+ %s: new cask (overall %s)\n", token, overallLabel(result.Overall))
			continue
		}
		e.diffCask(token, result, prevCasks[token])
	}
	for _, token := range removed {
		fmt.Fprintf(e.out, "- %s: cask no longer installed\n", token)
	}

	// Store the new snapshot (keep-all: never delete old ones).
	snap := &Snapshot{Timestamp: e.now, Casks: outcome.scans}
	if err := saveSnapshot(e.snapDir, snap); err != nil {
		fmt.Fprintf(e.errOut, "caskvet watch: saving snapshot: %v\n", err)
		return ExitError
	}

	if outcome.tokenHintNeeded(e.tokenSet) {
		fmt.Fprintln(e.errOut, tokenHint)
	}
	return e.exitCode(outcome)
}

// diffCask prints per-cask changes and fires the two notification
// triggers: overall dropping below 50, and a signing-authority team
// change.
func (e *env) diffCask(token string, result score.Result, prevScan *score.CaskScan) {
	prevResult := score.Score(e.now, prevScan, nil)
	prevOverall := prevResult.Overall
	curOverall := result.Overall

	overallChanged := !sameOptional(prevOverall, curOverall)
	if !overallChanged && !score.HasTeamChange(result) && !score.HasAuthorityNameChange(result) {
		return
	}

	if overallChanged {
		fmt.Fprintf(e.out, "~ %s: overall %s -> %s\n",
			token, overallLabel(prevOverall), overallLabel(curOverall))
	}
	for _, d := range result.Dimensions {
		if d.Name != score.DimensionSigning {
			continue
		}
		for _, f := range d.Findings {
			if f.Severity == score.SeverityWarning &&
				(strings.HasPrefix(f.Text, "signing authority changed") ||
					strings.HasPrefix(f.Text, "signing authority name changed")) {
				fmt.Fprintf(e.out, "  %s\n", f.Text)
			}
		}
	}

	// Trigger 1: overall score dropped below 50.
	if curOverall != nil && *curOverall < 50 &&
		(prevOverall == nil || *prevOverall >= 50) {
		fmt.Fprintf(e.out, "  trigger: overall score dropped below 50\n")
		if err := e.notify("caskvet", token+": overall score dropped below 50 ("+overallLabel(curOverall)+")"); err != nil {
			fmt.Fprintf(e.errOut, "caskvet watch: notification failed: %v\n", err)
		}
	}
	// Trigger 2: signing-authority team change (the hard trigger).
	if score.HasTeamChange(result) {
		fmt.Fprintln(e.out, "  trigger: signing authority changed")
		if err := e.notify("caskvet", token+": signing authority changed — check the app vendor"); err != nil {
			fmt.Fprintf(e.errOut, "caskvet watch: notification failed: %v\n", err)
		}
	}
}

// sameOptional compares two optional scores.
func sameOptional(a, b *int) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}
