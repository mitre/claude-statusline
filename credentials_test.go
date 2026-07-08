package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fixtureCredJSON is the documented credential shape — identical in the
// keychain item and .credentials.json (research note on the card, 2026-07-06).
func fixtureCredJSON(token string) string {
	return `{"claudeAiOauth":{"accessToken":"` + token + `","refreshToken":"sk-ant-ort01-fixture","expiresAt":1899999999999,"scopes":["user:inference","user:profile"]}}`
}

// writeCredFile places a credentials file exactly where Claude Code would.
func writeCredFile(t *testing.T, dir, token string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, ".credentials.json")
	if err := os.WriteFile(p, []byte(fixtureCredJSON(token)), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func envMap(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestCredentialTokenFromFile(t *testing.T) {
	// Claude Code's documented Linux/container store: $HOME/.claude/
	// .credentials.json. When present it wins — the keychain must not even
	// be consulted (mirrors Claude Code's own precedence, in which an
	// existing credentials file is read before other sources).
	home := t.TempDir()
	writeCredFile(t, filepath.Join(home, ".claude"), "sk-ant-oat01-from-file")

	src := credentialSource{
		getenv:   envMap(map[string]string{"HOME": home}),
		readFile: os.ReadFile,
		keychain: func() (string, error) {
			t.Error("keychain consulted although a credentials file exists")
			return "", errors.New("unreachable")
		},
	}
	tok, err := src.token()
	if err != nil || tok != "sk-ant-oat01-from-file" {
		t.Errorf("token() = %q, %v; want the file token", tok, err)
	}
	if err := src.ok(); err != nil {
		t.Errorf("ok() = %v; want nil with a resolvable file source", err)
	}
}

func TestCredentialFileHonorsClaudeConfigDir(t *testing.T) {
	// Docs: CLAUDE_CONFIG_DIR relocates .credentials.json. A decoy in HOME
	// proves the override actually takes precedence, not just "also works".
	home := t.TempDir()
	cfgDir := t.TempDir()
	writeCredFile(t, filepath.Join(home, ".claude"), "sk-ant-oat01-decoy-home")
	writeCredFile(t, cfgDir, "sk-ant-oat01-from-config-dir")

	src := credentialSource{
		getenv:   envMap(map[string]string{"HOME": home, "CLAUDE_CONFIG_DIR": cfgDir}),
		readFile: os.ReadFile,
	}
	tok, err := src.token()
	if err != nil || tok != "sk-ant-oat01-from-config-dir" {
		t.Errorf("token() = %q, %v; want the CLAUDE_CONFIG_DIR token", tok, err)
	}
}

func TestCredentialTokenFallsBackToKeychain(t *testing.T) {
	// No credentials file (the macOS default): the keychain serves the same
	// JSON shape.
	src := credentialSource{
		getenv:   envMap(map[string]string{"HOME": t.TempDir()}),
		readFile: os.ReadFile,
		keychain: func() (string, error) { return fixtureCredJSON("sk-ant-oat01-from-keychain"), nil },
	}
	tok, err := src.token()
	if err != nil || tok != "sk-ant-oat01-from-keychain" {
		t.Errorf("token() = %q, %v; want the keychain token", tok, err)
	}
}

func TestCredentialMalformedFileFallsThrough(t *testing.T) {
	// Claude Code itself falls through an unusable credentials file (an
	// empty {} restores the next source — claude-code#68241); the chain
	// mirrors that rather than dying on the first source.
	home := t.TempDir()
	dir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".credentials.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	src := credentialSource{
		getenv:   envMap(map[string]string{"HOME": home}),
		readFile: os.ReadFile,
		keychain: func() (string, error) { return fixtureCredJSON("sk-ant-oat01-rescued"), nil },
	}
	tok, err := src.token()
	if err != nil || tok != "sk-ant-oat01-rescued" {
		t.Errorf("token() = %q, %v; want fall-through to keychain", tok, err)
	}
}

func TestCredentialNoSourceErrors(t *testing.T) {
	// Neither source resolves: error — the badge degrades to "?" and the
	// account row collapses; nothing is fabricated, nothing logged.
	src := credentialSource{
		getenv:   envMap(map[string]string{"HOME": t.TempDir()}),
		readFile: os.ReadFile,
		keychain: func() (string, error) { return "", errors.New("keychain unavailable") },
	}
	tok, err := src.token()
	if err == nil || tok != "" {
		t.Errorf("token() = %q, %v; want an error with no source", tok, err)
	}
	if err := src.ok(); err == nil {
		t.Error("ok() must error when no credential source resolves")
	}
	// The error must never carry secret material (nothing to leak here, but
	// pin the invariant: no token-shaped content in error text).
	if err != nil && strings.Contains(err.Error(), "sk-ant") {
		t.Errorf("error text leaks token material: %q", err)
	}
}

func TestCredentialNilKeychainSeamDegrades(t *testing.T) {
	// A nil keychain seam (no OS keychain mechanism) is a clean miss, not a
	// panic — the file source alone decides.
	src := credentialSource{
		getenv:   envMap(map[string]string{"HOME": t.TempDir()}),
		readFile: os.ReadFile,
		keychain: nil,
	}
	if _, err := src.token(); err == nil {
		t.Error("token() with no file and nil keychain must error")
	}
}
