BINARY := claude-statusline
DIST   := dist

.PHONY: build test vet lint vuln race cover fuzz bench check release snapshot clean \
	tools hooks preflight release-checksums publish-formula dev-install \
	require-golangci-lint require-govulncheck require-goreleaser \
	endpoint-lock require-uv endpoint-up endpoint-down endpoint-status endpoint-pull

COVER_MIN ?= 90
PKG_COVER_MIN ?= 85
FUZZTIME ?= 30s

# ---- Tool pins: the ONLY place tool versions appear. Workflows install via
# `make tools`; AGENTS.md points here instead of repeating numbers. The
# require-* guards refuse a mismatched local binary — a local/CI version
# split (2.11.3 vs 2.13.2) once turned a release PR red with no local signal.
GOLANGCI_LINT_VERSION := v2.14.0
GOVULNCHECK_VERSION   := v1.7.0
GORELEASER_VERSION    := v2.17.0

# LiteLLM for the local test endpoint (developer-only; never part of check or
# CI). Run through uv from the hash-locked file make endpoint-lock writes;
# nothing is installed globally. A new pin waits 7 days after its release
# unless the owner approves an exception.
LITELLM_VERSION  := 1.110.2
LITELLM_PYTHON   := 3.12
LITELLM_LOCK     := scripts/local-endpoint/requirements.txt
LITELLM_EXCLUDES := scripts/local-endpoint/excludes.txt

# Local builds use exactly go.mod's Go — the same version CI resolves from
# go-version-file — so a newer Homebrew Go can't silently change the gates.
export GOTOOLCHAIN := go$(shell awk '$$1 == "go" {print $$2; exit}' go.mod)

# Installs the fast-gate pre-commit hook (lint only; idempotent; appends
# alongside other tools' managed hook sections — see scripts/install-hooks.sh).
hooks:
	sh scripts/install-hooks.sh

tools:
	go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
	go install golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION)
	go install github.com/goreleaser/goreleaser/v2@$(GORELEASER_VERSION)

# Each guard parses the binary's own version report; "missing" and
# "mismatched" both fail with the same remedy. Kept as three explicit
# targets: each tool reports its version in a different shape.
require-golangci-lint:
	@have=$$(golangci-lint --version 2>/dev/null | awk '{print $$4}'); \
	[ "v$$have" = "$(GOLANGCI_LINT_VERSION)" ] || { \
	  echo "FAIL: local golangci-lint is '$${have:-missing}', pinned $(GOLANGCI_LINT_VERSION) — run: make tools" >&2; exit 1; }

require-govulncheck:
	@have=$$(govulncheck -version 2>/dev/null | awk '/^Scanner:/ {sub(/.*@/, "", $$2); print $$2}'); \
	[ "$$have" = "$(GOVULNCHECK_VERSION)" ] || { \
	  echo "FAIL: local govulncheck is '$${have:-missing}', pinned $(GOVULNCHECK_VERSION) — run: make tools" >&2; exit 1; }

require-goreleaser:
	@have=$$(goreleaser --version 2>/dev/null | awk '/^GitVersion:/ {print $$2}'); \
	[ "$$have" = "$(GORELEASER_VERSION)" ] || { \
	  echo "FAIL: local goreleaser is '$${have:-missing}', pinned $(GORELEASER_VERSION) — run: make tools" >&2; exit 1; }

# uv resolves and runs the pinned LiteLLM; any uv release can read the lock,
# so only its presence is checked.
require-uv:
	@command -v uv >/dev/null 2>&1 || { \
	  echo "FAIL: uv is missing — install uv: https://docs.astral.sh/uv/getting-started/installation/" >&2; exit 1; }

# Regenerates the LiteLLM lock from LITELLM_VERSION alone: every package
# pinned with hashes, resolved for all platforms at LITELLM_PYTHON, minus the
# packages listed in LITELLM_EXCLUDES.
endpoint-lock: require-uv
	printf 'litellm[proxy]==%s\n' '$(LITELLM_VERSION)' | uv pip compile - \
	  --universal --python-version $(LITELLM_PYTHON) --generate-hashes \
	  --excludes $(LITELLM_EXCLUDES) \
	  --custom-compile-command 'make endpoint-lock' --output-file $(LITELLM_LOCK)

# The local test endpoint (scripts/local-endpoint.sh): LiteLLM on 127.0.0.1
# with the routes in scripts/local-endpoint/litellm.yaml. Developer-only.
endpoint-up: require-uv
	LITELLM_VERSION='$(LITELLM_VERSION)' LITELLM_PYTHON='$(LITELLM_PYTHON)' sh scripts/local-endpoint.sh start

endpoint-down:
	sh scripts/local-endpoint.sh stop

endpoint-status:
	sh scripts/local-endpoint.sh status

# Pulls the Ollama model the "local" route names. The model is named only in
# the config, so changing it there is the whole change. The name comes from
# the running proxy (local-endpoint.sh model), which has parsed the YAML.
endpoint-pull: endpoint-up
	@command -v ollama >/dev/null 2>&1 || { \
	  echo "FAIL: ollama is missing — install Ollama: https://ollama.com/download" >&2; exit 1; }
	@model=$$(sh scripts/local-endpoint.sh model) || exit 1; \
	echo "ollama pull $$model"; ollama pull "$$model"

# The one gate: everything a card close (and CI) must prove, in one command.
# (race runs the full suite under the race detector; cover re-runs for the
# coverage floor — separate passes because -race skews coverage timing.)
# The Go golden tests are the display spec of record (bash-parity era ended
# 2026-07-03; the original script lives in git history).
check: lint vuln race cover build

race:
	go test -race ./...

# Two floors: PKG_COVER_MIN guards every package (a weak package must not
# hide behind the average — root sat at 63% unnoticed under a total-only
# floor), COVER_MIN guards the total. A package without test files fails
# the per-package floor outright.
cover:
	@out=$$(go test -coverprofile=coverage.out ./... 2>&1) || { echo "$$out"; exit 1; }; \
	echo "$$out" | awk -v m="$(PKG_COVER_MIN)" ' \
	  /\[no test files\]/ { printf "FAIL: package %s has no test files (below the %s%% per-package floor)\n", $$2, m; bad=1 } \
	  /coverage:/ { pct=""; for (i=1;i<=NF;i++) if ($$i=="coverage:") pct=$$(i+1); gsub(/%/,"",pct); \
	    if (pct+0 < m+0) { printf "FAIL: package %s coverage %s%% is below the %s%% per-package floor\n", $$2, pct, m; bad=1 } } \
	  END { exit bad }'
	@total=$$(go tool cover -func=coverage.out | awk '/^total:/ {gsub(/%/,""); print $$3}'); \
	awk -v t="$$total" -v m="$(COVER_MIN)" -v pm="$(PKG_COVER_MIN)" 'BEGIN { \
	  if (t+0 < m+0) { printf "FAIL: total coverage %.1f%% is below the %s%% floor\n", t, m; exit 1 } \
	  else           { printf "OK: total coverage %.1f%% meets the %s%% floor; every package meets the %s%% floor\n", t, m, pm } }'

# Extended fuzzing of the untrusted-JSON parsers (stdin session payload,
# usage-endpoint bodies). Plain `go test ./...` already replays the seed
# corpora as regression tests on every run — this target explores beyond them.
fuzz:
	go test -run='^$$' -fuzz=FuzzParse -fuzztime=$(FUZZTIME) ./internal/input
	go test -run='^$$' -fuzz=FuzzParse -fuzztime=$(FUZZTIME) ./internal/usage

# Observe-only compute-path benchmarks (render.Build + full run() frame over
# fakes). No thresholds — shared runners flake; baselines live in the
# CHANGELOG/card notes and regressions surface in review.
bench:
	go test -run='^$$' -bench=. -benchmem ./...

lint: require-golangci-lint
	golangci-lint config verify
	golangci-lint run

vuln: require-govulncheck
	govulncheck ./...

build: vet
	go build -trimpath -ldflags="-s -w" -o $(BINARY) .

# Builds HEAD and installs it as the dev copy, ~/.claude/claude-statusline-dev
# (backup, staged swap, sha256 check, newest 3 backups kept); a failed build
# stops here and leaves the dev copy alone. Run `make check` first so only a
# gated build reaches the live line. Release copies (Homebrew, the plugin's
# ~/.claude/claude-statusline) are never touched.
dev-install: build
	sh scripts/dev-install.sh $(BINARY)

test:
	go test ./...

vet:
	go vet ./...

# Release preflight: tag shape, manifest/tag version parity, CHANGELOG
# section, tagged-commit-on-main — the release workflow's first job.
preflight:
	sh scripts/release-preflight.sh "$(TAG)"

# One artifact pipeline: goreleaser owns cross-compilation, archives, and
# checksums. `release` publishes (tag + GITHUB_TOKEN required — the v* tag
# workflow's job); `snapshot` is the local no-publish proof. Release notes
# come from CHANGELOG.md's section for the version, passed via
# --release-notes, which bypasses goreleaser's git-log changelog;
# scripts/release.sh keeps the notes file outside dist/, which --clean
# deletes before reading it.
release: require-goreleaser vet test
	sh scripts/release.sh

snapshot: require-goreleaser vet test
	@VER=$$(sh scripts/plugin-version.sh .claude-plugin/plugin.json) && \
	  SNAPSHOT_VERSION="$$VER" goreleaser release --snapshot --clean
	@ls -la $(DIST)

# VERSION reaches these recipes as an ENVIRONMENT value ($$VERSION), never
# make-text expansion — tag-derived text must not be spliced into command
# lines. `export` covers both `VERSION=v0.2.0 make …` and `make … VERSION=…`.
export VERSION

# Fetches the PUBLISHED release's checksums.txt (requires VERSION, e.g.
# v0.2.0, and gh auth) into published/ — the formula job's input, so a
# job re-run can never rebuild archives under shipped checksums.
release-checksums:
	@mkdir -p published
	gh release download "$$VERSION" --pattern checksums.txt --dir published --clobber

# Renders the formula from a checksums file and pushes it to
# mitre/homebrew-tap (requires HOMEBREW_TAP_GITHUB_TOKEN and VERSION).
# CI passes CHECKSUMS_FILE=published/checksums.txt from release-checksums;
# locally it defaults to dist/checksums.txt.
publish-formula:
	CHECKSUMS_FILE="$(CHECKSUMS_FILE)" sh scripts/publish-formula.sh "$$VERSION"

clean:
	rm -f $(BINARY)
	rm -rf $(DIST)
