# Release pipeline reference

How a `v*` tag becomes a GitHub Release and a Homebrew formula, as built.
This is the reference implementation for the `mitre/homebrew-tap` publishing
pattern; the tap repo's own docs cover adopting it for other projects and
operating the tap.

## The shape

```
git push origin v0.3.0
        │
        ▼
┌─ preflight ──────────────┐   ┌─ gates ──────────────────┐
│ tag is vMAJOR.MINOR.PATCH │   │ make check on the tagged │
│ plugin.json == tag        │   │ commit: lint + vuln +    │
│ marketplace.json == tag   │   │ race + cover + build     │
│ CHANGELOG has the section │   └──────────────┬───────────┘
│ tagged commit is on main  │                  │
└──────────────┬────────────┘                  │
               └──────────┬────────────────────┘
                          ▼
              ┌─ release ─────────────────────────────┐
              │ goreleaser: darwin/linux × arm64/amd64 │
              │ archives + checksums.txt; release      │
              │ notes = the tag's CHANGELOG section    │
              └──────────────┬─────────────────────────┘
                             ▼
              ┌─ formula ─────────────────────────────┐
              │ downloads the PUBLISHED checksums.txt, │
              │ renders Formula/claude-statusline.rb,  │
              │ pushes it to mitre/homebrew-tap        │
              └────────────────────────────────────────┘
```

Only a tag matching `v[0-9]+.[0-9]+.[0-9]+` triggers the workflow, and a
repository ruleset restricts who can create `v*` tags at all. Publishing
requires every gate: a tag on a red or inconsistent commit stops before any
artifact exists.

## Jobs (`.github/workflows/release.yml`)

Every step is a `make` target or a pinned tool install — no CI-only shell.
Every action is pinned to a full commit SHA (Dependabot proposes bumps).
All checkouts run with `persist-credentials: false`.

**preflight** — `make preflight TAG=<tag>` runs
`scripts/release-preflight.sh`: semver tag shape; `.claude-plugin/plugin.json`
and `.claude-plugin/marketplace.json` versions equal the tag; `CHANGELOG.md`
has a non-empty `## [X.Y.Z] - <date>` section; the tagged commit is an
ancestor of `origin/main`. Runnable locally against any tag.

**gates** — `make tools && make check` on the tagged commit: the same
lint/vuln/race/coverage/build gate as CI and local development.

**release** — `needs: [preflight, gates]`, the only job with
`contents: write`. `make release` extracts the version's CHANGELOG section
(`scripts/release-notes.sh` — goreleaser's git-log changelog is disabled)
and runs goreleaser: four `tar.gz` archives plus `checksums.txt`. The job
runs in the `release` environment (see below) with the shared Go cache off.

**formula** — `needs: release`. Downloads `checksums.txt` from the
*published* release (`make release-checksums`) rather than any build
directory, so "Re-run failed jobs" republishes the formula without ever
rebuilding archives under shipped checksums. Then `make publish-formula`
renders and pushes the formula (details next).

## The Homebrew formula path

goreleaser's own Homebrew publishers are deliberately not used (`brews` is
deprecated; `homebrew_casks` is macOS-only and expects signed binaries), so
the formula is produced by two small tested scripts:

- `scripts/render-formula.sh <version> <checksums>` — renders
  `Formula/claude-statusline.rb` with per-platform URLs and sha256 pins from
  `checksums.txt`, a `test do` stanza asserting `--version`, and a caveats
  block with the `settings.json` snippet. Golden-tested
  (`TestRenderFormulaMatchesGolden`); refuses a checksums file missing any
  platform.
- `scripts/publish-formula.sh <version>` — clones the tap, copies the
  rendered formula in, commits as `mitre-tap-publisher[bot]`, and pushes.
  Every step fails loudly; a re-run with an unchanged formula exits 0
  ("nothing to publish"). `CHECKSUMS_FILE` selects the input (CI passes the
  published copy); `TAP_URL` is overridable so tests publish to a local bare
  repository.

Authentication is a GitHub App, referenced here by role: the tap-publisher
app is installed on `mitre/homebrew-tap` only, with Contents read/write as
its only meaningful permission. The workflow mints a short-lived (1 h)
installation token per release via `actions/create-github-app-token`,
explicitly requesting `permission-contents: write` and nothing else. The
app's client ID and private key live in the `release` environment (variable
and Actions secret respectively) — no long-lived tokens, nothing usable
from a branch workflow.

## Guardrails around the pipeline

- **Tag ruleset** — creating, moving, or deleting `v*` tags is restricted;
  a release tag is never moved or re-pushed.
- **`release` environment** — the publish-path jobs (`release`, `formula`)
  declare `environment: release`, whose deployment policy only admits
  `v[0-9]*.[0-9]*.[0-9]*` tag refs. Branch workflows can never read its
  credentials.
- **Immutable releases** — enabled repo-wide: once published, a release's
  assets cannot be added, replaced, or deleted, and its tag is protected.
- **Default workflow permissions** are read-only; only the release job
  holds `contents: write`.

## Version identity — one source

The version lives in `.claude-plugin/plugin.json` and everything else
derives from it: `scripts/plugin-version.sh` is the single extraction point,
used by the preflight, the SessionStart version-sync hook
(`scripts/sync-binary.sh`), and the plugin setup command. Preflight refuses
any tag that disagrees with the manifests, and `make snapshot` labels local
archives with the manifest version rather than guessing from the last tag.

## Relationship to the plugin

The Claude Code plugin never bundles a binary. Its setup command and its
SessionStart hook both call `scripts/install-binary.sh`, which downloads the
release archive for the host platform, verifies its sha256 against the
release's `checksums.txt`, refuses on mismatch, and installs atomically with
a timestamped backup. The Homebrew formula pins the same checksums — every
install channel resolves to the same published, checksummed artifacts.

## Runbook: cutting a release

1. On a branch: move the `[Unreleased]` CHANGELOG content under a new
   `## [X.Y.Z] - <date>` heading (with compare links at the bottom), and
   bump `version` in `.claude-plugin/plugin.json` and
   `.claude-plugin/marketplace.json`. PR → CI green → merge.
2. Sanity-check locally against the merge commit on `main`:
   `make preflight TAG=vX.Y.Z` after tagging locally, or simply rely on the
   pipeline's preflight.
3. Tag the merge commit and push (tag creation requires ruleset bypass —
   repo admins):
   ```sh
   git tag -a vX.Y.Z -m "claude-statusline vX.Y.Z" <merge-sha>
   git push origin vX.Y.Z
   ```
4. Watch the run: preflight and gates first, then release, then formula.
5. Verify: the release page shows four archives + `checksums.txt` and the
   CHANGELOG-derived notes; `mitre/homebrew-tap` carries the new formula
   whose sha256 lines match `checksums.txt`;
   `brew install mitre/tap/claude-statusline` (or `brew upgrade`) yields a
   binary whose `--version` reports the tag.

A failed `formula` job is safe to re-run in place. A failed `release` job
before anything published is also safe to re-run. Never delete or move a
`v*` tag to "fix" a release — cut the next patch version instead
(releases are immutable).
