package main

import (
	"errors"
	"path/filepath"
	"strings"

	"github.com/mitre/claude-statusline/internal/usage"
)

// credentialSource resolves the subscription OAuth token from Claude Code's
// documented credential stores, in Claude Code's own precedence order:
//
//  1. the credentials file — $CLAUDE_CONFIG_DIR/.credentials.json when set,
//     else $HOME/.claude/.credentials.json (the Linux/Windows/container
//     store, and the macOS fallback Claude Code itself honors)
//  2. the macOS keychain item (via the injected keychain reader)
//
// Both stores carry the same claudeAiOauth JSON, so one parser serves both.
// An unusable file falls through to the keychain — mirroring Claude Code,
// which recovers past an empty/expired file rather than dying on it. It
// backs the deps.keychainOK and deps.fetchUsage seams; the token is read
// per use, never cached, never logged.
type credentialSource struct {
	getenv   func(string) string
	readFile func(string) ([]byte, error)
	keychain func() (string, error) // raw credential JSON; nil = no keychain mechanism
}

// token resolves the OAuth access token, trying each source in order.
func (c credentialSource) token() (string, error) {
	if raw, err := c.readFile(credentialsPath(c.getenv)); err == nil {
		if tok, perr := usage.TokenFromCredentialJSON(strings.TrimSpace(string(raw))); perr == nil {
			return tok, nil
		}
	}
	if c.keychain != nil {
		if raw, err := c.keychain(); err == nil {
			return usage.TokenFromCredentialJSON(strings.TrimSpace(raw))
		}
	}
	return "", errors.New("no credential source resolved (no readable credentials file, no keychain item)")
}

// ok reports whether any credential source resolves — the auth badge's
// subscription check. The resolved value is discarded immediately.
func (c credentialSource) ok() error {
	_, err := c.token()
	return err
}

// credentialsPath is Claude Code's documented credentials-file location:
// $CLAUDE_CONFIG_DIR relocates it; otherwise it lives under ~/.claude.
func credentialsPath(getenv func(string) string) string {
	if d := getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return filepath.Join(d, ".credentials.json")
	}
	return filepath.Join(getenv("HOME"), ".claude", ".credentials.json")
}
