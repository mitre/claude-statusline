package account

import (
	"errors"
	"path/filepath"
	"testing"
)

const claudeJSON = `{
  "numStartups": 42,
  "oauthAccount": {
    "accountUuid": "00000000-0000-4000-8000-000000000000",
    "emailAddress": "dev@example.com",
    "organizationName": "example org"
  }
}`

func TestEmailFromClaudeJSON(t *testing.T) {
	read := func(p string) ([]byte, error) {
		if want := filepath.Join("/Users/dev", ".claude.json"); p != want {
			t.Errorf("read path = %q, want %q", p, want)
		}
		return []byte(claudeJSON), nil
	}
	if got := Email("/Users/dev", read); got != "dev@example.com" {
		t.Errorf("Email = %q, want dev@example.com", got)
	}
}

func TestEmailReflectsSourceImmediately(t *testing.T) {
	// Identity is NEVER cached: a login change (or any source change) shows
	// on the very next render. Chosen over caching after a stale identity
	// confused a live session for minutes (2026-07-03) — the ~1-3ms parse of
	// ~/.claude.json is nothing against the ~90ms render budget.
	content := claudeJSON
	read := func(string) ([]byte, error) { return []byte(content), nil }
	if got := Email("/Users/dev", read); got != "dev@example.com" {
		t.Fatalf("first Email = %q", got)
	}
	content = `{"oauthAccount":{"emailAddress":"other@example.com"}}`
	if got := Email("/Users/dev", read); got != "other@example.com" {
		t.Errorf("Email after source change = %q, want other@example.com immediately", got)
	}
}

func TestEmailAbsentSourcesYieldEmpty(t *testing.T) {
	unreadable := func(string) ([]byte, error) { return nil, errors.New("no such file") }
	if got := Email("/Users/dev", unreadable); got != "" {
		t.Errorf("unreadable file: Email = %q, want empty (segment omitted)", got)
	}

	malformed := func(string) ([]byte, error) { return []byte("not json"), nil }
	if got := Email("/Users/dev", malformed); got != "" {
		t.Errorf("malformed json: Email = %q, want empty", got)
	}

	noAccount := func(string) ([]byte, error) { return []byte(`{"numStartups": 1}`), nil }
	if got := Email("/Users/dev", noAccount); got != "" {
		t.Errorf("missing oauthAccount: Email = %q, want empty", got)
	}

	emptyEmail := func(string) ([]byte, error) {
		return []byte(`{"oauthAccount":{"emailAddress":""}}`), nil
	}
	if got := Email("/Users/dev", emptyEmail); got != "" {
		t.Errorf("empty emailAddress: Email = %q, want empty", got)
	}
}
