// Package account resolves the logged-in account (oauthAccount.accountUuid
// and oauthAccount.emailAddress) from Claude Code's account record — the
// only local source: the statusline stdin payload carries no account ID,
// and the credential stores hold tokens only.
//
// The record is found the way Claude Code finds it (ported from its own
// path functions, verified against the 2.1.325 binary): the current record
// is .claude<suffix>.json in CLAUDE_CONFIG_DIR, else the home directory,
// and a usable legacy .config.json in Claude Code's config home takes
// precedence over it.
//
// Identity is deliberately NOT cached: a stale identity label confused a
// live session for minutes (2026-07-03), and an identity that lags a login
// change defeats the multi-account purpose of the segment. One small JSON
// parse per render is nothing against the ~90ms render budget.
//
// Upstream watch: if anthropics/claude-code#24679 ships an account field in
// the statusline stdin JSON, read it from stdin instead.
package account

import (
	"encoding/json"
	"errors"
	"io/fs"
	"path/filepath"

	"golang.org/x/text/unicode/norm"
)

// Identity is the logged-in account; a field is empty when the record
// cannot be read or does not carry it — the caller omits what is missing,
// never fabricates it.
type Identity struct {
	// AccountUUID is oauthAccount.accountUuid. Other accountUuid fields in
	// the record are caches of other state and are never used.
	AccountUUID string
	Email       string
}

// FS is the file access the resolver needs, injected from run's deps.
type FS struct {
	ReadFile func(string) ([]byte, error)
	Stat     func(string) (fs.FileInfo, error)
	ReadDir  func(string) ([]fs.DirEntry, error)
}

// Resolve reads the account record Claude Code would use and returns its
// identity from that single read.
func Resolve(getenv func(string) string, fsys FS) Identity {
	path := recordPath(getenv, fsys)
	if path == "" {
		return Identity{}
	}
	raw, err := fsys.ReadFile(path)
	if err != nil {
		return Identity{}
	}
	var f struct {
		OauthAccount struct {
			AccountUUID  string `json:"accountUuid"`
			EmailAddress string `json:"emailAddress"`
		} `json:"oauthAccount"`
	}
	if err := json.Unmarshal(raw, &f); err != nil {
		return Identity{}
	}
	return Identity{AccountUUID: f.OauthAccount.AccountUUID, Email: f.OauthAccount.EmailAddress}
}

// recordPath returns the record to read, or "" when neither
// CLAUDE_CONFIG_DIR nor HOME gives an absolute directory: a relative path
// would resolve against the workspace, which is never an identity source.
func recordPath(getenv func(string) string, fsys FS) string {
	configDir := getenv("CLAUDE_CONFIG_DIR")
	home := getenv("HOME")

	// Current record (Claude Code's aYr/E6): CLAUDE_CONFIG_DIR || homedir,
	// .claude.json, or .claude-custom-oauth.json under a custom OAuth URL.
	base := configDir
	if base == "" {
		base = home
	}
	if !filepath.IsAbs(base) {
		return ""
	}
	suffix := ""
	if getenv("CLAUDE_CODE_CUSTOM_OAUTH_URL") != "" {
		suffix = "-custom-oauth"
	}
	current := filepath.Join(base, ".claude"+suffix+".json")

	// Config home (Claude Code's Ge): CLAUDE_CONFIG_DIR, else
	// homedir/.claude, NFC-normalized. An empty CLAUDE_CONFIG_DIR is
	// indistinguishable from an unset one here and is treated as unset.
	configHome := configDir
	if configHome == "" {
		configHome = filepath.Join(home, ".claude")
	}
	configHome = norm.NFC.String(configHome)
	return selectRecord(filepath.Join(configHome, ".config.json"), current, fsys)
}

// selectRecord ports Claude Code's legacy selector (z_): a legacy file
// that is a regular file, non-empty or writable, is used instead of the
// current record unless the current record is plainly the live one. Every
// unexpected error falls back to the current record, as z_ does.
func selectRecord(legacy, current string, fsys FS) string {
	l, err := fsys.Stat(legacy)
	if err != nil || !l.Mode().IsRegular() || (l.Size() == 0 && l.Mode().Perm()&0o222 == 0) {
		return current
	}
	c, err := fsys.Stat(current)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) || l.Size() == 0 {
			return legacy
		}
		c = nil
	}
	if l.Size() == 0 {
		if c != nil && linuxPlaceholder(c) {
			return legacy
		}
		return current
	}
	if c != nil && (!c.Mode().IsRegular() || c.Size() == 0) {
		return legacy
	}
	// The stat above may have matched another spelling on a
	// case-insensitive volume; the legacy file counts only when its exact
	// name is listed. A listing error other than not-found is ignored.
	entries, err := fsys.ReadDir(filepath.Dir(legacy))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return current
		}
	} else if !listed(entries, filepath.Base(legacy)) {
		return current
	}
	return legacy
}

func listed(entries []fs.DirEntry, name string) bool {
	for _, e := range entries {
		if e.Name() == name {
			return true
		}
	}
	return false
}
