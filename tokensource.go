package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"

	"github.com/mitre/claude-statusline/internal/usage"
)

// tokenSource resolves the subscription OAuth token from Claude Code's
// credential stores the way Claude Code 2.1.325 does:
//
//  1. the macOS keychain item, looked up by service and account name
//     (keychainService, keychainAccount); an item that parses as JSON is
//     final, token or not
//  2. otherwise the plaintext store, <storage dir>/.credentials.json
//     (plaintextStorePath) — the Linux/container store and the macOS
//     fallback
//
// Both stores carry the same claudeAiOauth JSON. It backs the
// deps.keychainOK and deps.fetchUsage seams; the token is read per use,
// never cached, never logged.
type tokenSource struct {
	// lookupEnv distinguishes an unset variable from an empty one, which
	// Claude Code treats differently for CLAUDE_SECURESTORAGE_CONFIG_DIR
	// and CLAUDE_CONFIG_DIR.
	lookupEnv func(string) (string, bool)
	readFile  func(string) ([]byte, error)
	// username is the OS account name, the fallback when USER is unset;
	// nil means none is available.
	username func() (string, error)
	// keychain returns the raw item for service and account; nil means no
	// keychain mechanism.
	keychain func(service, account string) (string, error)
}

// token resolves the OAuth access token from the first store Claude Code
// would use.
func (c tokenSource) token() (string, error) {
	if c.keychain != nil {
		if raw, err := c.keychain(c.keychainService(), c.keychainAccount()); err == nil {
			if item, ok := parsedItem(jsTrim(raw)); ok {
				return usage.TokenFromCredentialJSON(item)
			}
		}
	}
	if p := c.plaintextStorePath(); p != "" {
		if raw, err := c.readFile(p); err == nil {
			if item, ok := parsedItem(string(raw)); ok {
				return usage.TokenFromCredentialJSON(item)
			}
		}
	}
	return "", errors.New("no credential store resolved (no keychain item, no readable plaintext store)")
}

// ok reports whether a store resolves — the auth badge's subscription
// check. The resolved value is discarded immediately.
func (c tokenSource) ok() error {
	_, err := c.token()
	return err
}

// parsedItem reports whether JSON.parse would accept raw with a non-null
// result — what Claude Code counts as a present item; anything else is a
// miss. Syntax only: decoding into Go values would reject valid JSON such
// as 1e400, which JavaScript reads as Infinity. Like JSON.parse, it allows
// JSON whitespace around the value and nothing else.
func parsedItem(raw string) (string, bool) {
	if !json.Valid([]byte(raw)) || strings.Trim(raw, " \t\n\r") == "null" {
		return "", false
	}
	return raw, true
}

// jsTrim is String.prototype.trim, which Claude Code applies to the
// keychain output: it removes ECMAScript white space and line terminators
// (a byte-order mark included) and keeps U+0085, unlike strings.TrimSpace.
func jsTrim(s string) string {
	return strings.TrimFunc(s, func(r rune) bool {
		switch r {
		case '\t', '\n', '\v', '\f', '\r', ' ', '\u00a0', '\ufeff', '\u2028', '\u2029':
			return true
		}
		return unicode.Is(unicode.Zs, r)
	})
}

// keychainServicePattern is every service name keychainService can
// produce; the keychain reader refuses anything else before it execs.
var keychainServicePattern = regexp.MustCompile(`^Claude Code(-custom-oauth)?-credentials(-[0-9a-f]{8})?$`)

// keychainAccountPattern is Claude Code's allowed account-name set.
var keychainAccountPattern = regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)

// keychainService is Claude Code's service name (N3("-credentials")):
// "Claude Code" + OAuth suffix + "-credentials", plus "-" and the first 8
// hex digits of sha256(config dir) when a config dir is in effect: the
// secure-storage dir when CLAUDE_SECURESTORAGE_CONFIG_DIR is set (empty
// means none), else CLAUDE_CONFIG_DIR when non-empty.
func (c tokenSource) keychainService() string {
	name := "Claude Code" + c.oauthSuffix() + "-credentials"
	dir, set := c.lookupEnv("CLAUDE_SECURESTORAGE_CONFIG_DIR")
	if !set {
		dir, _ = c.lookupEnv("CLAUDE_CONFIG_DIR")
	}
	if dir == "" {
		return name
	}
	sum := sha256.Sum256([]byte(norm.NFC.String(dir)))
	return name + "-" + hex.EncodeToString(sum[:])[:8]
}

// oauthSuffix is Claude Code's OAUTH_FILE_SUFFIX for the production
// configuration: "-custom-oauth" under a custom OAuth URL, else "".
func (c tokenSource) oauthSuffix() string {
	if v, _ := c.lookupEnv("CLAUDE_CODE_CUSTOM_OAUTH_URL"); v != "" {
		return "-custom-oauth"
	}
	return ""
}

// keychainAccount is Claude Code's account name (xH): USER, else the OS
// account name, replaced by "claude-code-user" when unavailable or
// outside [a-zA-Z0-9._-].
func (c tokenSource) keychainAccount() string {
	const fallback = "claude-code-user"
	name, _ := c.lookupEnv("USER")
	if name == "" && c.username != nil {
		n, err := c.username()
		if err != nil {
			return fallback
		}
		name = n
	}
	if !keychainAccountPattern.MatchString(name) {
		return fallback
	}
	return name
}

// plaintextStorePath is Claude Code's plaintext store (Ae over H3):
// <storage dir>/.credentials.json, the storage dir being
// CLAUDE_SECURESTORAGE_CONFIG_DIR when set (empty means ~/.claude), else
// CLAUDE_CONFIG_DIR when set, else ~/.claude — NFC-normalized. It returns
// "" when that dir is not absolute: a relative path would resolve against
// the workspace, which is never a credential source.
func (c tokenSource) plaintextStorePath() string {
	home, _ := c.lookupEnv("HOME")
	dir := filepath.Join(home, ".claude")
	if s, set := c.lookupEnv("CLAUDE_SECURESTORAGE_CONFIG_DIR"); set {
		if s != "" {
			dir = s
		}
	} else if d, set := c.lookupEnv("CLAUDE_CONFIG_DIR"); set {
		dir = d
	}
	dir = norm.NFC.String(dir)
	if !filepath.IsAbs(dir) {
		return ""
	}
	return filepath.Join(dir, ".credentials.json")
}

// location names the credential stores token reads, without reading them:
// the keychain service and account and the plaintext store path, NUL
// separated. Two environments share a location exactly when token would
// consult the same stores, so the login-status cache is keyed by it.
func (c tokenSource) location() string {
	return c.keychainService() + "\x00" + c.keychainAccount() + "\x00" + c.plaintextStorePath()
}

// fingerprint is a one-way identifier of the current access token: the
// first 16 hex digits of its sha256. The usage cache stores it to tell
// which token fetched a payload; the token itself is never stored.
func (c tokenSource) fingerprint() (string, error) {
	tok, err := c.token()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(tok))
	return hex.EncodeToString(sum[:8]), nil
}
