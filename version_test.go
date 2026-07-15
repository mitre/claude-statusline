package main

import "testing"

func TestVersionStringDefaults(t *testing.T) {
	// Un-injected dev build: the defaults must say so honestly — never a
	// fabricated release version.
	if got, want := versionString(), "claude-statusline dev (none, unknown)"; got != want {
		t.Errorf("versionString() = %q, want %q", got, want)
	}
}

func TestVersionStringInjected(t *testing.T) {
	// The goreleaser ldflags seam: -X main.version/commit/date.
	origV, origC, origD := version, commit, date
	t.Cleanup(func() { version, commit, date = origV, origC, origD })
	version, commit, date = "0.1.0", "9430009", "2026-07-15"
	if got, want := versionString(), "claude-statusline 0.1.0 (9430009, 2026-07-15)"; got != want {
		t.Errorf("versionString() = %q, want %q", got, want)
	}
}
