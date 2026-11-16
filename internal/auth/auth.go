// Package auth determines the auth badge (Sub/API/?) and whether an API key
// override is active (metered billing), mirroring the bash reference.
package auth

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"time"

	"github.com/mitre/claude-statusline/internal/cache"
)

// badgeTTL is how long a known answer (Sub/API) is served from the cache:
// the keychain probe is a subprocess, too slow for every render.
const badgeTTL = 300 * time.Second

// unknownTTL is how long an unknown answer ("?") is served. A transient
// miss (a sandboxed shell, a locked keychain) would otherwise hide the
// account row for the full badgeTTL.
const unknownTTL = 15 * time.Second

// Detect returns the auth badge and the live metered-billing flag. The badge
// is cached per credential source: source names the storage location the
// login check resolves (never a token), so one config directory's answer
// never decides another's. A known answer is served for 300 s, an unknown
// one for 15 s. The billing flag is always checked live so an accidental
// export shows up immediately.
func Detect(cacheDir, source string, getenv func(string) string, keychainOK func() error) (string, bool) {
	apiKeySet := getenv("ANTHROPIC_API_KEY") != "" || getenv("ANTHROPIC_AUTH_TOKEN") != ""

	path := filepath.Join(cacheDir, cacheName(source))
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

// cacheName is the login-status cache file for source: "auth-" and the
// first 16 hex digits of its sha256. Hashing keeps the paths and user names
// in source out of the file name; the unkeyed "auth" file of earlier
// versions is never read.
func cacheName(source string) string {
	sum := sha256.Sum256([]byte(source))
	return "auth-" + hex.EncodeToString(sum[:])[:16]
}
