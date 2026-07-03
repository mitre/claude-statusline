package auth

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestAPIKeyEnvWinsAndFlagsMeteredBilling(t *testing.T) {
	badge, apiKeySet := Detect(t.TempDir(), env(map[string]string{"ANTHROPIC_API_KEY": "sk-x"}),
		func() error { return nil })
	if badge != "API" || !apiKeySet {
		t.Errorf("Detect = %q, %v; want API, true", badge, apiKeySet)
	}
}

func TestAuthTokenFlagsBillingButNotBadge(t *testing.T) {
	// Mirrors the reference: ANTHROPIC_AUTH_TOKEN triggers the metered-billing
	// alarm but only ANTHROPIC_API_KEY selects the API badge.
	badge, apiKeySet := Detect(t.TempDir(), env(map[string]string{"ANTHROPIC_AUTH_TOKEN": "tok"}),
		func() error { return nil })
	if badge != "Sub" || !apiKeySet {
		t.Errorf("Detect = %q, %v; want Sub, true", badge, apiKeySet)
	}
}

func TestKeychainGivesSub(t *testing.T) {
	badge, apiKeySet := Detect(t.TempDir(), env(nil), func() error { return nil })
	if badge != "Sub" || apiKeySet {
		t.Errorf("Detect = %q, %v; want Sub, false", badge, apiKeySet)
	}
}

func TestNoCredentialGivesUnknown(t *testing.T) {
	badge, _ := Detect(t.TempDir(), env(nil), func() error { return errors.New("not found") })
	if badge != "?" {
		t.Errorf("Detect = %q, want \"?\"", badge)
	}
}

func TestBadgeIsCached(t *testing.T) {
	dir := t.TempDir()
	if badge, _ := Detect(dir, env(nil), func() error { return nil }); badge != "Sub" {
		t.Fatalf("first Detect = %q", badge)
	}
	// Keychain now failing — fresh cache must still say Sub.
	badge, _ := Detect(dir, env(nil), func() error { return errors.New("locked") })
	if badge != "Sub" {
		t.Errorf("cached badge not served: %q", badge)
	}
	if _, err := os.Stat(filepath.Join(dir, "auth")); err != nil {
		t.Errorf("auth cache file missing (bash-compatible name): %v", err)
	}
}
