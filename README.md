# caskvet

[![CI](https://github.com/breichr/caskvet/actions/workflows/ci.yml/badge.svg)](https://github.com/breichr/caskvet/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/breichr/caskvet?color=blue)](https://github.com/breichr/caskvet/releases/latest)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
[![Go Version](https://img.shields.io/github/go-mod/go-version/breichr/caskvet?label=go)](https://go.dev)

A read-only supply-chain health report for the Homebrew casks installed
on your Mac.

caskvet scores upstream maintenance, code-signing state, transparency
(background items, LaunchAgents, login items), and version currency —
and reports trust-risk changes over time. It answers one question:
*do I still trust the software that installed itself on my machine, and
did anything change since I last looked?*

It is **not** a vulnerability scanner, **not** a cleanup tool, and
**not** an uninstaller. It never installs, upgrades, or deletes
anything. Read-only, no exceptions.

## Why

In 2024, the popular Mac menu bar app Bartender was quietly sold to a
new company. The app kept working, but the entity behind it changed —
and users found out from a changed code-signing certificate, not from
the vendor.

That event is exactly what caskvet detects: a **signing-authority
change**, i.e. a new code-signing team identifier on an app you already
installed. When it happens, watch mode prints the old and new authority
strings in plain text and posts a notification — the score deduction
follows.

> **The Bartender case study:** an app's caskvet score dropped from
> **94 to 41 in three days**. The signing authority change — old and
> new certificate names and team identifiers — was visible in the
> snapshot diff, in plain text, on the day it happened.

A score is never a verdict. It is a reason to look closer.

## Install

With Homebrew:

```sh
brew install breichr/caskvet/caskvet
```

Or download the release for your Mac — [v0.1.0](https://github.com/breichr/caskvet/releases/tag/v0.1.0):

- [caskvet_v0.1.0_darwin_arm64.zip](https://github.com/breichr/caskvet/releases/download/v0.1.0/caskvet_v0.1.0_darwin_arm64.zip) (Apple Silicon)
- [caskvet_v0.1.0_darwin_amd64.zip](https://github.com/breichr/caskvet/releases/download/v0.1.0/caskvet_v0.1.0_darwin_amd64.zip) (Intel)

Unzip and put the binary on your PATH. Binaries are Developer ID signed
and notarized with Apple. Verify with `shasum -a 256 -c checksums.txt`
using the [checksums](https://github.com/breichr/caskvet/releases/download/v0.1.0/checksums.txt).

From source:

```sh
git clone https://github.com/breichr/caskvet
cd caskvet
go build ./cmd/caskvet
```

The first run works with zero configuration and no token.

## Usage

```sh
caskvet report [--json]     # scan and print the report table
caskvet why <cask>          # detailed scoring justification
caskvet watch               # compare against the last snapshot
caskvet install <cask>      # pre-install gate (read-only check)
```

Example:

```console
$ caskvet report
CASK                 TAP            OVERALL  MAINT    SIGN     CURR  BG
anybar               homebrew/cask  0        100      0        100   0
freecad              homebrew/cask  20       unknown  20       100   0
androidtool          homebrew/cask  30       30       100      100   0
firefox              homebrew/cask  100      unknown  100      70    0
linearmouse          homebrew/cask  100      unknown  100      100   1
font-jetbrains-mono  homebrew/cask  unknown  unknown  unknown  100   0
```

The table lists the worst scores first, always shows the tap source,
and prints `unknown` wherever data was unavailable — never a guessed
number. The `BG` column counts background items (LaunchAgents and
login items) associated with the cask. Every line above is a real
captured scan; the table below decodes each row.

`caskvet why <cask>` prints the full justification: every dimension's
score and the facts behind it. A real scan — the upstream project is
actively maintained (maintenance from real GitHub data) but ships an
unsigned app, so the signing gate floors the overall score:

```console
$ caskvet why anybar
caskvet: anybar (tap: homebrew/cask)
overall: 0 (min of maintenance and signing)
installed version: 0.2.3

maintenance (weight 0.4): 100
  [info] last upstream commit was 7 months ago

signing (weight 0.3): 0
  [warning] AnyBar.app is not signed (ad-hoc or unsigned): Gatekeeper blocks first-run unless the user overrides it explicitly

transparency (weight 0.2): unknown
  [info] cask last tap update unknown: the Homebrew API does not provide it
  [info] no LaunchAgents or login items detected for this cask
  [info] TCC permissions are not scanned: reading them requires privacy prompts, which a trust tool must not trigger

currency (weight 0.1): 100
  [info] installed version 0.2.3 matches the current catalog version 0.2.3
```

### Pre-install gate

`caskvet install <cask>` checks a cask you are **considering**
installing — read-only; caskvet never installs anything. It gates on
what can be known before an install: maintenance (repository archived,
stale, deprecated — including the replacement token when Homebrew
defines one), declared background items, and the latest release.
Signing is unknown by design until the bundle is on disk; run
`caskvet report` after installing to verify it.

```console
$ caskvet install linearmouse
caskvet install gate: linearmouse (tap: homebrew/cask) version 0.12.0
  would install: LinearMouse.app
  maintenance: 100
    [info] last upstream commit was 1 months ago
  signing: unknown — verified by `caskvet report` after install
  latest release: v0.12.0
verdict: no at-risk findings — proceed with: brew install --cask linearmouse
caskvet never installs anything; it only checks.
```

### What the scores look like

Real caskvet v0.1.1 scans and pre-install gates of popular software —
one row per lesson. Transparency facts are the `BG` count
(LaunchAgents/login items); currency never gates the overall score.

| Cask | Maint | Sign | Curr | BG | Overall | What it shows |
|---|---|---|---|---|---|---|
| anybar | 100 | 0 | 100 | 0 | 0 | actively maintained, but the app is unsigned — Gatekeeper blocks first-run |
| androidtool | 30 | 100 | 100 | 0 | 30 | the app itself is fine; the upstream code is 43 months old |
| freecad | unknown | 20 | 100 | 0 | 20 | the app writes files into its own bundle, so the signed seal no longer matches |
| firefox | unknown | 100 | 70 | 0 | 100 | a version behind the catalog is displayed, never gated |
| linearmouse | unknown | 100 | 100 | 1 | 100 | background items are listed as facts (its login item) |
| font-jetbrains-mono | unknown | unknown | 100 | 0 | unknown | no app artifacts: nothing guessed, nothing scored |
| alacritty *(pre-install gate)* | 0 | unknown | 100 | 0 | 0 | disabled in homebrew/cask — flagged before you install |

Maintenance is `unknown` for several popular apps because their
homepages are vendor domains, not GitHub repos — caskvet never guesses
a repository mapping. The one signing verdict not shown (70: signed
and Gatekeeper-accepted but not notarized) is rare for third-party
apps today.

### Exit codes

| Code | Meaning                                              |
|------|------------------------------------------------------|
| 0    | ok                                                    |
| 1    | config/usage error                                    |
| 2    | scan completed with at-risk findings (CI-friendly)    |

A completed scan is at-risk when any cask has a known overall score
below 50, a known maintenance or signing score of 0, or a
signing-authority change fired. Currency and transparency facts never
produce exit 2.

## The scoring model

Four dimensions, each 0–100. Weights (0.4/0.3/0.2/0.1) are presentation
constants for report ordering and `why` emphasis — not score inputs.
Never a weighted average.

**Overall score = min(maintenance, signing).** Only the two
trust-critical dimensions gate the overall score.

- **Maintenance (0.4)** — repository archived = 0. Last commit more
  than 12 months ago = hard deduction. Cask deprecated or disabled in
  Homebrew core = 0, with the replacement token shown when Homebrew
  defines one.
- **Signing (0.3)** — notarized = full. A signing-authority change
  since the previous snapshot is a deduction plus a warning that prints
  the old and new authority strings (certificate name plus team
  identifier) in plain text. A team-identifier change is the hard
  trigger — the Bartender detection. A name-only change at the same
  team is reported but does not reduce the score. Ad-hoc/unsigned = 0
  with a Gatekeeper explanation. For multiple app bundles, the
  weakest bundle defines the dimension.
- **Transparency (0.2)** — LaunchAgents, login items, and declared
  background items are listed as facts. Show, do not judge: there is
  no sub-score. The cask's last update in its tap is displayed too
  (when the Homebrew API provides the date) — a packaging fact, not a
  vendor fact.
- **Currency (0.1)** — installed version vs. the current version of
  the cask in the Homebrew catalog. This is Homebrew data, not the
  upstream release. A weak signal by design: displayed and explained,
  never gating.

**Unknown is not risk.** A dimension whose data is unavailable reports
`unknown` and is excluded from the gate. A cask with a non-GitHub
homepage scores maintenance *unknown* — never 0. Missing knowledge must
never look like risk.

## Watch mode

`caskvet watch` scans, compares against the last stored snapshot,
prints the diff, and posts a macOS notification on exactly two events:

1. an overall score **dropping below 50**, and
2. a **signing-authority team change** (a name-only change at the same
   team is a softer, separate warning in the diff output).

Nothing else notifies.

Snapshots are stored locally under your user config directory
(`~/Library/Application Support/caskvet/snapshots/` on macOS): one JSON
file per scan plus a `latest.json` pointer, versioned, diffable, and
never cleaned up. Each snapshot persists the signing authority per
cask, so diffs explain exactly what changed.

```console
$ caskvet watch
caskvet watch: comparing against snapshot 2026-10-21 20:14 (10 casks)
~ linearmouse: overall 100 -> 50
  signing authority changed for LinearMouse.app: Developer ID Application: Jiahao Lu (C5686NKYJ7) -> Developer ID Application: Aperture Research LLC (APERTURE9)
  trigger: signing authority changed
```

## JSON output

`caskvet report --json` prints a stable, machine-readable, versioned
schema:

```jsonc
{
  "schema_version": 1,
  "generated_at": "2026-10-22T18:58:24Z",
  "casks": [
    {
      "token": "linearmouse",
      "tap_source": "homebrew/cask",
      "overall": 100,                    // null = unknown
      "dimensions": [
        {
          "name": "maintenance",         // maintenance | signing | transparency | currency
          "weight": 0.4,                 // presentation constant
          "score": 100,                  // null = unknown
          "findings": [                  // severity: info | warning
            { "severity": "info", "text": "last upstream commit was 0 months ago" }
          ]
        }
        // ... signing, transparency, currency
      ]
    }
  ],
  "scan": {
    "casks_scanned": 10,
    "github_rate_limited": false,        // GitHub degraded to unknown during the scan
    "github_token_hint": false           // set GITHUB_TOKEN for full coverage
  }
}
```

## Privacy and data sources

- **No telemetry.** caskvet talks to exactly two APIs: the Homebrew
  JSON API and the GitHub REST API. No cloud, no accounts.
- **Read-only.** The only local system interactions are `brew` (list,
  prefix, info — always read-only), `codesign`, `spctl`, `osascript`
  (System Events queries and posting notifications), and file reads of
  app bundles, the Caskroom, LaunchAgent plists, and caskvet's own
  cache and snapshot directories. Posting a notification is the only
  non-read action in the entire codebase.
- **Optional GitHub token.** Set `GITHUB_TOKEN` for better rate limits.
  Without it, GitHub allows 60 requests per hour and a scan needs about
  one call per cask (the upstream repository); when the scan hits that
  limit, or when the cask count visibly exceeds the budget, the
  affected dimensions degrade to `unknown` and a single hint —
  `set GITHUB_TOKEN for full coverage` — prints once at the end of the
  run, never per cask. Degraded data is reported as unknown, never as
  risk.

## Known limitations

Documented in the output, not hidden:

- **Third-party taps.** Casks from taps other than `homebrew/cask`
  are not in the Homebrew core JSON API; their metadata comes from a
  read-only local `brew info` call instead. When the cask's homepage
  points at GitHub, maintenance scores normally; otherwise maintenance
  reports `unknown` with the hint "not in Homebrew core API" and the
  tap shown.
- **Non-GitHub homepages.** Maintenance is scored from the upstream
  GitHub repository, matched **only** by parsing the cask's homepage
  URL. Many modern casks point at a vendor domain
  (`ghostty.org`, `linearmouse.org`, …) rather than GitHub; those casks
  report maintenance `unknown` — never 0. Repositories are never
  auto-attributed via GitHub search: a wrong mapping would create
  false trust.
- **TCC permissions are not scanned.** Reading them requires privacy
  prompts, which a trust tool must not trigger. Every report states
  this limitation explicitly.
- **BTM items are not scanned.** `sfltool dumpbtm` requires Touch ID or
  an admin password on the maintainers' machine; a trust tool must not
  trigger authentication prompts, so the feature is dropped rather
  than gated behind a flag. The report states this gap so it is
  visible.
- **Last-cask-update date.** The Homebrew API currently does not
  populate the cask `changed_at` field; when it is absent, the report
  states "cask last tap update unknown" instead of guessing.

## Development

```sh
go build ./...
go test ./...
```

Architecture: `internal/collector` (the two APIs, batching, caching,
rate-limit friendliness), `internal/localscan` (macOS local state),
`internal/score` (pure functions, zero I/O, fully unit-tested),
`internal/cli` + `cmd/caskvet` (output, snapshots, watch, notifications,
exit codes). Only `collector` and `localscan` perform I/O.

## Roadmap

1. `--version` flag and formula test conventions.
2. Formulae support, once casks are rock-solid.
3. Homebrew core PR, once the tap is established.

The agreed model changes from AGENTS.md — the currency source switch
to the Homebrew catalog version and the last-cask-update display — are
implemented. The BTM background-item scan was dropped: `sfltool
dumpbtm` requires Touch ID or an admin password, and a trust tool does
not trigger authentication prompts.

## Funding

If caskvet saves you a surprise, consider buying the maintainer a
coffee. Entirely optional — the tool has no feature gates and no
nagware, and it never will.

<a href="https://www.buymeacoffee.com/cbreiteneder" target="_blank"><img src="https://cdn.buymeacoffee.com/buttons/v2/default-yellow.png" alt="Buy Me a Coffee" style="height: 60px !important;width: 217px !important;" ></a>

## License

MIT — see [LICENSE](LICENSE).
