# AGENTS.md — caskvet

Project conventions for AI coding agents working on this repository. Follow this file in every change.

## What caskvet is

caskvet is a read-only supply-chain health report for installed Homebrew casks on macOS. It scores upstream maintenance, code-signing state, transparency (background items, LaunchAgents, login items), and version currency, and reports trust-risk changes over time. It is NOT a vulnerability scanner, NOT a cleanup tool, NOT an uninstaller.

Hard product rules:

- Never install, uninstall, upgrade, adopt, delete, or modify anything. Read-only, no exceptions.
- No telemetry, no network calls except the two documented APIs (Homebrew JSON API, GitHub REST API). No cloud, no accounts.
- If a data source is unavailable, report "unknown", never fabricate a score. Missing knowledge must not look like risk.
- Exit codes: 0 = ok, 1 = config/usage error, 2 = scan completed with at-risk findings (see definition below). CI-friendly.

## Language and build

- Go, single static binary. Module path: `github.com/breichr/caskvet`.
- No CGO. No external daemon dependencies.
- Build: `go build ./...`, test: `go test ./...`. Both must pass before any commit.

## Local system interactions (complete list, strictly read-only)

- `brew` — only list, prefix, and info invocations, always read-only.
- `codesign`, `spctl` — verification and assessment parsing.
- `osascript` — System Events queries (read-only) and posting macOS notifications. Notification posting is the only non-read action allowed in the entire codebase.
- File reads: app bundles, Caskroom, LaunchAgent plists, the caskvet cache and snapshot directories.

Anything outside this list requires an AGENTS.md change first, agreed by the maintainers.

## Architecture (do not blur boundaries)

Four packages, strictly separated:

- `internal/collector` — Homebrew JSON API and GitHub REST API access. Batching, caching (XDG config dir, JSON, ETag/304 revalidation), optional GitHub token via `GITHUB_TOKEN` env, rate-limit friendly. No scoring logic here.
- `internal/localscan` — macOS local state: `codesign`/`spctl` output parsing, LaunchAgent and login-item discovery, app bundle location. The only package allowed to touch the filesystem and system tools outside its cache.
- `internal/score` — pure functions from a scan snapshot to scores. No I/O at all. Fully unit-tested.
- `cmd/caskvet` + `internal/cli` — argument parsing, output formatting (table, `--json`), watch/snapshot diffing, macOS notification hook, exit codes defined centrally in one place (never scattered os.Exit calls).

Only `localscan` and `collector` do I/O. If you find yourself importing an I/O package from `score`, the design is broken — stop and restructure.

Collector errors are sentinels (`ErrNotFound`, `ErrRateLimited`, `ErrNotGitHub`, …). The score layer maps any sentinel to "unknown", never to risk.

## Scoring model (exact, do not improvise)

Four dimensions, each 0–100.

Overall score = min(maintenance, signing). Only the two trust-critical dimensions gate the overall score. Transparency has no sub-score (facts are listed, not judged). Currency is displayed and explained but never gates. Weights (0.4/0.3/0.2/0.1) are presentation constants for report ordering and why-emphasis, not score inputs. Never use a weighted average.

- Maintenance, weight 0.4: repository archived = 0. Last commit > 12 months = hard deduction. Cask deprecated/disabled in core = 0, referencing the replacement token if Homebrew defines one.
- Signing, weight 0.3: notarized = full. Signing authority changed since the previous snapshot = deduction plus a warning that prints old and new authority strings (team identifier plus name) in plain text. A team-identifier change is the Bartender detection and always triggers the watch warning. A name-only change at the same team is reported but does not reduce the score. Ad-hoc/unsigned = 0 with a Gatekeeper explanation.
- Transparency, weight 0.2: list LaunchAgents, login items, and declared background items. Show, do not judge. No sub-score here; report the facts. TCC permissions are NOT scanned (reading them requires privacy prompts, which a trust tool must not trigger); the report states this limitation explicitly so the gap is visible.
- Currency, weight 0.1: installed version vs. latest upstream release. Weak signal only; displayed, never gating.

A dimension whose data is unavailable reports "unknown" and is excluded from the gate. Missing knowledge must not look like risk.

## Watch-mode trigger rules

Only two events produce a notification: overall score dropping below 50, and a signing-authority change (team identifier change is the hard trigger; name-only change is a softer, separate warning). Nothing else notifies. Snapshots are stored locally, one JSON file per scan plus a latest.json pointer, versioned and diffable. Keep-all retention in v1; no cleanup logic. Snapshots persist the signing authority per cask so watch diffs need no restructuring later.

## Exit code 2 definition (exact)

Exit 2 when at least one of the following holds after a completed scan:

- any cask has a known overall score < 50;
- any cask has a known maintenance score of 0 or a known signing score of 0;
- a signing-authority change warning fired.

Currency 0 and transparency facts never produce exit 2. Exit 0 otherwise; exit 1 for config/usage/runtime errors before or during the scan.

## CLI contract

```
caskvet report [--json]          # scan and print the table
caskvet why <cask>               # detailed scoring justification
caskvet watch                    # compare against last snapshot, notify on the two triggers
caskvet install <cask>           # pre-install gate (read-only check; caskvet never installs anything)
```

The first run must work with zero configuration and no token. Output is a compact table; `--json` is stable schema, machine-readable, documented. When a scan hits GitHub rate limits (results degraded to unknown) or the cask count visibly exceeds the unauthenticated budget (60 req/h, ~2 calls per cask) with no `GITHUB_TOKEN` set, print a single "set GITHUB_TOKEN for full coverage" hint — once, at the end of the run, never per cask.

## Known limitations (documented in output, not hidden)

- Casks from third-party taps are not in the Homebrew core JSON API. Their metadata (tap, homepage, deprecated/replacement state, declared artifacts) comes from a read-only local `brew info` call instead. When the homepage points at GitHub, maintenance scores normally; otherwise it reports "unknown" with the hint "not in Homebrew core API". The report always shows the tap source.
- TCC permissions are not scanned (see Transparency above).

## Testing requirements

- `internal/score` has 100% deterministic unit tests including: archived repo, stale repo (>12 months), authority change between snapshots (team change and name-only change), unsigned app, unknown-source cask, deprecated cask with replacement token, currency 0 must NOT gate, transparency facts must NOT gate.
- `internal/collector` and `internal/localscan` are tested against recorded fixture data (JSON and command output files under `testdata/`), never against live APIs in CI.
- Edge cases that MUST have tests: casks with multiple apps, casks without app artifacts, apps not in /Applications, non-GitHub homepages (unknown, not 0), offline behavior (last snapshot + date, no crash), manually replaced app bundles, third-party-tap casks (unknown maintenance, tap shown).

## Documentation and release

- README in English. Include the Bartender case study ("score 94 → 41 in three days, authority change visible in the diff").
- Distribution: GitHub Releases first, own Homebrew tap, core PR later. goreleaser or equivalent; releases are signed when the maintainers have a Developer ID.
- FUNDING.yml with GitHub Sponsors. No feature gates, no nagware. A trust tool does not use trust tricks.

## What not to build

No GUI, no auto-update of apps, no formulae support before casks are rock-solid, no community database of owner changes unless it can self-maintain, no vulnerability/CVE features (that field is owned by other tools and sends the wrong signal), no TCC scanning.
