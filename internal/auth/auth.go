// Package auth determines the auth badge (Sub/API/?) and whether an API key
// override is active (metered billing), mirroring the bash reference.
package auth

import (
	"path/filepath"
	"time"

	"github.com/mitre/claude-statusline/internal/cache"
)

const badgeTTL = 300 * time.Second

// Detect returns the auth badge and the live metered-billing flag. The badge
// is cached (file "auth", 300s); the billing flag is always checked live so
// an accidental export shows up immediately.
func Detect(cacheDir string, getenv func(string) string, keychainOK func() error) (string, bool) {
	apiKeySet := getenv("ANTHROPIC_API_KEY") != "" || getenv("ANTHROPIC_AUTH_TOKEN") != ""

	path := filepath.Join(cacheDir, "auth")
	if badge, ok := cache.ReadFresh(path, badgeTTL); ok {
		return badge, apiKeySet
	}

	badge := "?"
	switch {
	case getenv("ANTHROPIC_API_KEY") != "":
		badge = "API"
	case keychainOK() == nil:
		badge = "Sub"
	}
	_ = cache.Write(path, badge)
	return badge, apiKeySet
}
