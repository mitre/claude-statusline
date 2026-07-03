// Package account resolves the logged-in account's email from Claude Code's
// state file (~/.claude.json → oauthAccount.emailAddress) — the only local
// source: the statusline stdin payload carries no identity fields and the
// keychain credential holds tokens only.
//
// Identity is deliberately NOT cached: a stale identity label confused a
// live session for minutes (2026-07-03), and an identity that lags a login
// change defeats the multi-account purpose of the segment. The ~1-3ms parse
// of ~/.claude.json is nothing against the ~90ms render budget.
//
// Upstream watch: if anthropics/claude-code#24679 ships an account field in
// the statusline stdin JSON, read it from stdin instead and delete this
// package (tracked on card claude-statusline-60g.20).
package account

import (
	"encoding/json"
	"path/filepath"
)

// Email returns the account email, or "" when it cannot be determined
// (absent file, missing oauthAccount, unparseable) — the caller omits the
// segment entirely; identity is never fabricated.
func Email(home string, readFile func(string) ([]byte, error)) string {
	raw, err := readFile(filepath.Join(home, ".claude.json"))
	if err != nil {
		return ""
	}
	var f struct {
		OauthAccount struct {
			EmailAddress string `json:"emailAddress"`
		} `json:"oauthAccount"`
	}
	if err := json.Unmarshal(raw, &f); err != nil || f.OauthAccount.EmailAddress == "" {
		return ""
	}
	return f.OauthAccount.EmailAddress
}
