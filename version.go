package main

import "fmt"

// Build identity, injected by goreleaser via -ldflags "-X main.version=…".
// The defaults describe an un-injected `go build`/`go install` honestly —
// never a fabricated release version. The plugin's version-sync hook and the
// Homebrew formula test both key off `claude-statusline --version`.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func versionString() string {
	return fmt.Sprintf("claude-statusline %s (%s, %s)", version, commit, date)
}
