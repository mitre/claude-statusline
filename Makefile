BINARY := claude-statusline
DIST   := dist

.PHONY: build test vet lint vuln race cover check parity release clean

COVER_MIN ?= 85

# The one gate: everything a card close (and CI) must prove, in one command.
# (race runs the full suite under the race detector; cover re-runs for the
# coverage floor — separate passes because -race skews coverage timing.)
check: lint vuln race cover build parity

race:
	go test -race ./...

cover:
	@go test -coverprofile=coverage.out ./... > /dev/null 2>&1 || { go test ./...; exit 1; }
	@total=$$(go tool cover -func=coverage.out | awk '/^total:/ {gsub(/%/,""); print $$3}'); \
	awk -v t="$$total" -v m="$(COVER_MIN)" 'BEGIN { \
	  if (t+0 < m+0) { printf "FAIL: total coverage %.1f%% is below the %s%% floor\n", t, m; exit 1 } \
	  else           { printf "OK: total coverage %.1f%% meets the %s%% floor\n", t, m } }'

parity: build
	@for f in testdata/*.json; do \
	  bash reference/statusline.sh < $$f > /tmp/parity-ref.out; \
	  ./claude-statusline < $$f > /tmp/parity-go.out; \
	  diff -q /tmp/parity-ref.out /tmp/parity-go.out >/dev/null \
	    && echo "$$f: BYTE-IDENTICAL" \
	    || { echo "$$f: DIFFERS"; exit 1; }; \
	done

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

release: vet test
	@mkdir -p $(DIST)
	GOOS=darwin  GOARCH=arm64 go build -trimpath -ldflags="-s -w" -o $(DIST)/$(BINARY)-darwin-arm64 .
	GOOS=darwin  GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o $(DIST)/$(BINARY)-darwin-amd64 .
	GOOS=linux   GOARCH=arm64 go build -trimpath -ldflags="-s -w" -o $(DIST)/$(BINARY)-linux-arm64 .
	GOOS=linux   GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o $(DIST)/$(BINARY)-linux-amd64 .
	@ls -la $(DIST)

clean:
	rm -f $(BINARY)
	rm -rf $(DIST)
