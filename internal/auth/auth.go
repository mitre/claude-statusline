// Package auth determines the auth badge (Sub/API/?) and whether an API key
// override is active (metered billing), mirroring the bash reference.
package auth

import (
	"path/filepath"
	"time"

	"github.com/mitre/claude-statusline/internal/cache"
)

const badgeTTL = 300 * time.Second

// unknownTTL negative-caches a "?" badge only briefly: a transient miss
// (sandboxed shell, locked keychain) shares this cache with the live
// statusline and would otherwise hide the account row for the full badgeTTL.
const unknownTTL = 15 * time.Second

// Detect returns the auth badge and the live metered-billing flag. The badge
// is cached (file "auth", 300s; an unknown "?" only 15s); the billing flag is always checked live so
// an accidental export shows up immediately.
func Detect(cacheDir string, getenv func(string) string, keychainOK func() error) (string, bool) {
	apiKeySet := getenv("ANTHROPIC_API_KEY") != "" || getenv("ANTHROPIC_AUTH_TOKEN") != ""

	path := filepath.Join(cacheDir, "auth")
	if badge, ok := cache.ReadFresh(path, badgeTTL); ok {
		if badge != "?" {
			return badge, apiKeySet
		}
		if _, fresh := cache.ReadFresh(path, unknownTTL); fresh {
			return badge, apiKeySet
		}
	}

	badge := "?"
	switch {
	case getenv("ANTHROPIC_API_KEY") != "":
		badge = "API"
	case keychainOK != nil && keychainOK() == nil:
		badge = "Sub"
	}
	_ = cache.Write(path, badge)
	return badge, apiKeySet
}
