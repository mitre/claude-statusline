BINARY := claude-statusline
DIST   := dist

.PHONY: build test vet lint vuln race cover fuzz bench check release snapshot clean \
	tools require-golangci-lint require-govulncheck require-goreleaser

COVER_MIN ?= 90
PKG_COVER_MIN ?= 85
FUZZTIME ?= 30s

# ---- Tool pins: the ONLY place tool versions appear. Workflows install via
# `make tools`; AGENTS.md points here instead of repeating numbers. The
# require-* guards refuse a mismatched local binary — a local/CI version
# split (2.11.3 vs 2.13.2) once turned a release PR red with no local signal.
GOLANGCI_LINT_VERSION := v2.13.2
GOVULNCHECK_VERSION   := v1.7.0
GORELEASER_VERSION    := v2.17.0

# Local builds use exactly go.mod's Go — the same version CI resolves from
# go-version-file — so a newer Homebrew Go can't silently change the gates.
export GOTOOLCHAIN := go$(shell awk '$$1 == "go" {print $$2; exit}' go.mod)

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

test:
	go test ./...

vet:
	go vet ./...

# One artifact pipeline: goreleaser owns cross-compilation, archives, and
# checksums. `release` publishes (tag + GITHUB_TOKEN required — the v* tag
# workflow's job); `snapshot` is the local no-publish proof.
release: require-goreleaser vet test
	goreleaser release --clean

snapshot: require-goreleaser vet test
	goreleaser release --snapshot --clean
	@ls -la $(DIST)

# Renders the formula from this release's checksums and pushes it to
# mitre/homebrew-tap (requires HOMEBREW_TAP_GITHUB_TOKEN and VERSION —
# the release workflow's follow-on step after `release`).
publish-formula:
	sh scripts/publish-formula.sh "$(VERSION)"

clean:
	rm -f $(BINARY)
	rm -rf $(DIST)
