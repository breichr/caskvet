# AGENTS.md — caskvet

Project conventions for AI coding agents working on this repository. Follow this file in every change.

## What caskvet is

caskvet is a read-only supply-chain health report for installed Homebrew casks on macOS. It scores upstream maintenance, code-signing state, transparency (permissions, LaunchAgents, login items), and version currency, and reports trust-risk changes over time. It is NOT a vulnerability scanner, NOT a cleanup tool, NOT an uninstaller.

Hard product rules:

- Never install, uninstall, upgrade, adopt, delete, or modify anything. Read-only, no exceptions.
- No telemetry, no network calls except the two documented APIs (Homebrew JSON API, GitHub REST API). No cloud, no accounts.
- If a data source is unavailable, report "unknown", never fabricate a score. Missing knowledge must not look like risk.
- Exit codes: 0 = ok, 1 = config/usage error, 2 = scan completed with at-risk findings. CI-friendly.

## Language and build

- Go, single static binary. Module path: `github.com/breichr/caskvet` (adjust owner if the org differs).
- No CGO. No external daemon dependencies. `codesign`, `spctl` and file reads are the only local system interactions.
- Build: `go build ./...`, test: `go test ./...`. Both must pass before any commit.

## Architecture (do not blur boundaries)

Four packages, strictly separated:

- `internal/collector` — Homebrew JSON API and GitHub REST API access. Batching, caching (XDG config dir, JSON), optional GitHub token via `GITHUB_TOKEN` env, rate-limit friendly. No scoring logic here.
- `internal/localscan` — macOS local state: `codesign`/`spctl` output parsing, LaunchAgent and login-item discovery under `/Library/LaunchAgents`, `~/Library/LaunchAgents`, relevant `Application Support` paths. The only package allowed to touch the filesystem outside its cache.
- `internal/score` — pure functions from a scan snapshot to scores. No I/O at all. Fully unit-tested.
- `cmd/caskvet` + `internal/cli` — argument parsing, output formatting (table, `--json`), watch/snapshot diffing, macOS notification hook.

Only `localscan` and `collector` do I/O. If you find yourself importing an I/O package from `score`, the design is broken — stop and restructure.

## Scoring model (exact, do not improvise)

Four dimensions, each 0–100. Overall score = weighted MINIMUM, never an average:

- Maintenance, weight 0.4: repository archived = 0. Last commit > 12 months = hard deduction. Cask deprecated/disabled in core = 0, referencing the replacement token if Homebrew defines one.
- Signing, weight 0.3: notarized = full. Signing certificate changed since the previous snapshot = deduction plus a warning that prints old and new team identifiers in plain text (the Bartender detection). Ad-hoc/unsigned = 0 with a Gatekeeper explanation.
- Transparency, weight 0.2: list permissions, LaunchAgents, and login items. Show, do not judge. No sub-score here; report the facts.
- Currency, weight 0.1: installed version vs. latest upstream release. Weak signal only.

A dimension whose data is unavailable reports "unknown" and is excluded from the weighted minimum.

## Watch-mode trigger rules

Only two events produce a notification: overall score dropping below 50, and a signing-certificate hash change. Nothing else notifies. Snapshots are stored locally, versioned, and diffable.

## CLI contract

```
caskvet report [--json]          # scan and print the table
caskvet why <cask>               # detailed scoring justification
caskvet watch                    # compare against last snapshot, notify on the two triggers
caskvet install <cask>           # pre-install gate (later roadmap stage)
```

The first run must work with zero configuration and no token. Output is a compact table; `--json` is stable schema, machine-readable, documented.

## Testing requirements

- `internal/score` has 100% deterministic unit tests including: archived repo, stale repo (>12 months), cert change between snapshots, unsigned app, unknown-source cask, deprecated cask with replacement token.
- `internal/collector` and `internal/localscan` are tested against recorded fixture data (JSON and command output files under `testdata/`), never against live APIs in CI.
- Edge cases that MUST have tests: casks with multiple apps, casks without app artifacts, apps not in /Applications, non-GitHub homepages (unknown, not 0), offline behavior (last snapshot + date, no crash), manually replaced app bundles.

## Documentation and release

- README in English. Include the Bartender case study ("score 94 → 41 in three days, certificate change visible in the diff").
- Distribution: GitHub Releases first, own Homebrew tap (`homebrew-caskvet`), core PR later. goreleaser or equivalent; releases are signed when the maintainers have a Developer ID.
- FUNDING.yml with GitHub Sponsors. No feature gates, no nagware. A trust tool does not use trust tricks.

## What not to build

No GUI, no auto-update of apps, no formulae support before casks are rock-solid, no community database of owner changes unless it can self-maintain, no vulnerability/CVE features (that field is owned by other tools and sends the wrong signal).
