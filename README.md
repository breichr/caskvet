# caskvet

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
new company. Users found out from a changed code-signing certificate,
not from the vendor. The maintenance signs were all there in
hindsight: the app kept working, but the entity behind it had changed.

caskvet exists to make that visible on your machine:

> **The Bartender case study:** an app's caskvet score dropped from
> **94 to 41 in three days**. The signing authority change — old and
> new certificate names and team identifiers — was visible in the
> snapshot diff, in plain text, on the day it happened.

A score is never a verdict. It is a reason to look closer.

## Install

From source (a binary release, an own Homebrew tap, and a core PR
follow later on the roadmap):

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
caskvet install <cask>      # pre-install gate (later roadmap stage)
```

Example:

```console
$ caskvet report
CASK                 TAP            OVERALL  MAINT    SIGN     CURR     BG
freecad              homebrew/cask  20       unknown  20       unknown  0
firefox              homebrew/cask  100      unknown  100      unknown  0
ghostty              homebrew/cask  100      unknown  100      unknown  0
...
```

The table lists the worst scores first, always shows the tap source,
and prints `unknown` wherever data was unavailable — never a guessed
number. The `BG` column counts background items (LaunchAgents and
login items) associated with the cask.

`caskvet why linearmouse` prints the full justification: every
dimension's score and the facts behind it, including signing team
identifiers and every background item found.

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
  no sub-score.
- **Currency (0.1)** — installed version vs. latest upstream release.
  A weak signal by design: displayed and explained, never gating.

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
- **Optional GitHub token.** For better rate limits set
  `GITHUB_TOKEN`; without it, a scan of many casks may degrade some
  dimensions to `unknown` (a single hint is printed at the end of the
  run when that happens).

## Known limitations

Documented in the output, not hidden:

- **Third-party taps.** Casks from taps other than `homebrew/cask` are
  not in the Homebrew core JSON API; they report maintenance `unknown`
  with the hint "not in Homebrew core API" and their tap shown.
  Pulling metadata from local `brew info` instead is a later roadmap
  item.
- **TCC permissions are not scanned.** Reading them requires privacy
  prompts, which a trust tool must not trigger. Every report states
  this limitation explicitly.

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

1. `caskvet install <cask>` — a pre-install trust gate (casks are
   rock-solid first).
2. Local `brew info` metadata for third-party tap casks.
3. Formulae support, once casks are rock-solid.

## License

MIT — see [LICENSE](LICENSE).
