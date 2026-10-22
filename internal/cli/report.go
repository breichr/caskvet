package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/breichr/caskvet/internal/score"
)

// reportSchema is the stable --json schema version.
const reportSchema = 1

// jsonReport is the machine-readable report root (documented schema).
type jsonReport struct {
	SchemaVersion int                 `json:"schema_version"`
	GeneratedAt   time.Time           `json:"generated_at"`
	Casks         []score.Result      `json:"casks"`
	Scan          jsonScanDiagnostics `json:"scan"`
}

// jsonScanDiagnostics summarizes data-source health for the run.
type jsonScanDiagnostics struct {
	CasksScanned      int  `json:"casks_scanned"`
	GithubRateLimited bool `json:"github_rate_limited"`
	GithubTokenHint   bool `json:"github_token_hint"`
}

const tokenHint = "set GITHUB_TOKEN for full coverage"

// cmdReport scans and prints the table (or stable JSON).
func (e *env) cmdReport(ctx context.Context, args []string) int {
	jsonOut := false
	for _, arg := range args {
		switch arg {
		case "--json":
			jsonOut = true
		default:
			fmt.Fprintf(e.errOut, "caskvet report: unknown flag %q\n", arg)
			printUsage(e.errOut)
			return ExitError
		}
	}

	prev, _ := loadLatestSnapshot(e.snapDir) // best effort; no failure mode
	var prevCasks map[string]*score.CaskScan
	if prev != nil {
		prevCasks = prev.Casks
	}
	outcome, err := e.scan(ctx, prevCasks)
	if err != nil {
		fmt.Fprintf(e.errOut, "caskvet: %v\n", err)
		return ExitError
	}

	results := sortedResults(outcome.results)
	hint := outcome.tokenHintNeeded(e.tokenSet)

	if jsonOut {
		return e.printJSON(results, outcome, hint)
	}
	e.printTable(results)
	if hint {
		fmt.Fprintln(e.errOut, tokenHint)
	}
	return e.exitCode(outcome)
}

// exitCode maps a completed scan to the process exit code.
func (e *env) exitCode(outcome *scanOutcome) int {
	for _, r := range outcome.results {
		if atRisk(r) {
			return ExitAtRisk
		}
	}
	return ExitOK
}

// sortedResults returns results worst-first: known overalls ascending,
// unknown last, then by token.
func sortedResults(results map[string]score.Result) []score.Result {
	list := make([]score.Result, 0, len(results))
	for _, r := range results {
		list = append(list, r)
	}
	rank := func(r score.Result) int {
		if r.Overall == nil {
			return 101
		}
		return *r.Overall
	}
	sort.Slice(list, func(i, j int) bool {
		if rank(list[i]) != rank(list[j]) {
			return rank(list[i]) < rank(list[j])
		}
		return list[i].Token < list[j].Token
	})
	return list
}

// printTable writes the compact report table.
func (e *env) printTable(results []score.Result) {
	w := tabwriter.NewWriter(e.out, 2, 4, 2, ' ', 0)
	fmt.Fprintln(w, "CASK\tTAP\tOVERALL\tMAINT\tSIGN\tCURR\tBG")
	for _, r := range results {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%d\n",
			r.Token, tapLabel(r.TapSource),
			overallLabel(r.Overall),
			dimensionLabel(r, score.DimensionMaintenance),
			dimensionLabel(r, score.DimensionSigning),
			dimensionLabel(r, score.DimensionCurrency),
			backgroundItemCount(r),
		)
	}
	w.Flush()
}

// dimensionLabel renders a dimension score, unknown included.
func dimensionLabel(r score.Result, name string) string {
	for _, d := range r.Dimensions {
		if d.Name != name {
			continue
		}
		if d.Score == nil {
			return "unknown"
		}
		return fmt.Sprintf("%d", *d.Score)
	}
	return "unknown"
}

func overallLabel(v *int) string {
	if v == nil {
		return "unknown"
	}
	return fmt.Sprintf("%d", *v)
}

// tapLabel shortens the tap for the table; the empty string (unknown
// tap source) is shown explicitly, never hidden.
func tapLabel(tap string) string {
	if tap == "" {
		return "?"
	}
	return tap
}

// backgroundItemCount counts transparency facts (LaunchAgents and
// login items) reported for the cask.
func backgroundItemCount(r score.Result) int {
	count := 0
	for _, d := range r.Dimensions {
		if d.Name != score.DimensionTransparency {
			continue
		}
		for _, f := range d.Findings {
			if strings.HasPrefix(f.Text, "LaunchAgent ") || strings.HasPrefix(f.Text, "login item ") {
				count++
			}
		}
	}
	return count
}

// printJSON writes the stable machine-readable schema.
func (e *env) printJSON(results []score.Result, outcome *scanOutcome, hint bool) int {
	report := jsonReport{
		SchemaVersion: reportSchema,
		GeneratedAt:   e.now,
		Casks:         results,
		Scan: jsonScanDiagnostics{
			CasksScanned:      len(outcome.scans),
			GithubRateLimited: outcome.githubLimited,
			GithubTokenHint:   hint,
		},
	}
	enc := json.NewEncoder(e.out)
	enc.SetIndent("", "  ")
	if err := enc.Encode(report); err != nil {
		fmt.Fprintf(e.errOut, "caskvet: encoding JSON report: %v\n", err)
		return ExitError
	}
	return e.exitCode(outcome)
}

// cmdWhy prints the detailed scoring justification for one installed
// cask.
func (e *env) cmdWhy(ctx context.Context, args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(e.errOut, "caskvet why: exactly one cask token required")
		return ExitError
	}
	token := args[0]

	prev, _ := loadLatestSnapshot(e.snapDir)
	var prevCasks map[string]*score.CaskScan
	if prev != nil {
		prevCasks = prev.Casks
	}
	outcome, err := e.scan(ctx, prevCasks)
	if err != nil {
		fmt.Fprintf(e.errOut, "caskvet: %v\n", err)
		return ExitError
	}
	result, ok := outcome.results[token]
	if !ok {
		fmt.Fprintf(e.errOut, "caskvet why: %q is not an installed cask\n", token)
		return ExitError
	}
	scan := outcome.scans[token]

	fmt.Fprintf(e.out, "caskvet: %s", result.Token)
	if result.TapSource != "" {
		fmt.Fprintf(e.out, " (tap: %s)", result.TapSource)
	}
	fmt.Fprintf(e.out, "\n")
	fmt.Fprintf(e.out, "overall: %s (min of maintenance and signing)\n", overallLabel(result.Overall))
	if scan.InstalledVersion != "" {
		fmt.Fprintf(e.out, "installed version: %s\n", scan.InstalledVersion)
	}
	for _, d := range result.Dimensions {
		fmt.Fprintf(e.out, "\n%s (weight %.1f): %s\n", d.Name, d.Weight, dimensionLabel(result, d.Name))
		for _, f := range d.Findings {
			fmt.Fprintf(e.out, "  [%s] %s\n", f.Severity, f.Text)
		}
	}

	if atRisk(result) {
		return ExitAtRisk
	}
	return ExitOK
}
