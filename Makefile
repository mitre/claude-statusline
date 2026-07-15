BINARY := claude-statusline
DIST   := dist

.PHONY: build test vet lint vuln race cover fuzz bench check release snapshot clean

COVER_MIN ?= 90
PKG_COVER_MIN ?= 85
FUZZTIME ?= 30s

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

lint:
	golangci-lint config verify
	golangci-lint run

vuln:
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
release: vet test
	goreleaser release --clean

snapshot: vet test
	goreleaser release --snapshot --clean
	@ls -la $(DIST)

clean:
	rm -f $(BINARY)
	rm -rf $(DIST)
