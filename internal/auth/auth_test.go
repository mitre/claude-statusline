package auth

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestAPIKeyEnvWinsAndFlagsMeteredBilling(t *testing.T) {
	badge, apiKeySet := Detect(t.TempDir(), "src", env(map[string]string{"ANTHROPIC_API_KEY": "sk-x"}),
		func() error { return nil })
	if badge != "API" || !apiKeySet {
		t.Errorf("Detect = %q, %v; want API, true", badge, apiKeySet)
	}
}

func TestAuthTokenFlagsBillingButNotBadge(t *testing.T) {
	// Mirrors the reference: ANTHROPIC_AUTH_TOKEN triggers the metered-billing
	// alarm but only ANTHROPIC_API_KEY selects the API badge.
	badge, apiKeySet := Detect(t.TempDir(), "src", env(map[string]string{"ANTHROPIC_AUTH_TOKEN": "tok"}),
		func() error { return nil })
	if badge != "Sub" || !apiKeySet {
		t.Errorf("Detect = %q, %v; want Sub, true", badge, apiKeySet)
	}
}

func TestKeychainGivesSub(t *testing.T) {
	badge, apiKeySet := Detect(t.TempDir(), "src", env(nil), func() error { return nil })
	if badge != "Sub" || apiKeySet {
		t.Errorf("Detect = %q, %v; want Sub, false", badge, apiKeySet)
	}
}

func TestNoCredentialGivesUnknown(t *testing.T) {
	badge, _ := Detect(t.TempDir(), "src", env(nil), func() error { return errors.New("not found") })
	if badge != "?" {
		t.Errorf("Detect = %q, want \"?\"", badge)
	}
}

func TestBadgeIsCached(t *testing.T) {
	dir := t.TempDir()
	if badge, _ := Detect(dir, "src", env(nil), func() error { return nil }); badge != "Sub" {
		t.Fatalf("first Detect = %q", badge)
	}
	// Keychain now failing — fresh cache must still say Sub.
	badge, _ := Detect(dir, "src", env(nil), func() error { return errors.New("locked") })
	if badge != "Sub" {
		t.Errorf("cached badge not served: %q", badge)
	}
	if files := authFiles(t, dir); len(files) != 1 {
		t.Errorf("want one keyed auth cache file, got %v", files)
	}
}

func TestDetectWithNilKeychainOKDegradesToUnknown(t *testing.T) {
	badge, apiKeySet := Detect(t.TempDir(), "src", env(nil), nil)
	if badge != "?" || apiKeySet {
		t.Errorf("Detect with nil keychainOK = %q, %v; want \"?\", false", badge, apiKeySet)
	}
}

// authFiles lists the login-status cache files in dir.
func authFiles(t *testing.T, dir string) []string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(dir, "auth*"))
	if err != nil {
		t.Fatal(err)
	}
	return files
}

var (
	loggedIn  = func() error { return nil }
	loggedOut = func() error { return errors.New("no credential store resolved") }
)

func TestNegativeAnswerForOneSourceDoesNotApplyToAnother(t *testing.T) {
	dir := t.TempDir()
	if badge, _ := Detect(dir, "storage-A", env(nil), loggedOut); badge != "?" {
		t.Fatalf("A = %q, want ?", badge)
	}
	if badge, _ := Detect(dir, "storage-B", env(nil), loggedIn); badge != "Sub" {
		t.Errorf("B took A's cached answer: got %q, want Sub", badge)
	}
}

func TestPositiveAnswerForOneSourceDoesNotApplyToAnother(t *testing.T) {
	dir := t.TempDir()
	if badge, _ := Detect(dir, "storage-A", env(nil), loggedIn); badge != "Sub" {
		t.Fatalf("A = %q, want Sub", badge)
	}
	if badge, _ := Detect(dir, "storage-B", env(nil), loggedOut); badge != "?" {
		t.Errorf("B took A's cached answer: got %q, want ?", badge)
	}
}

func TestLegacyUnkeyedAuthFileIsNeverRead(t *testing.T) {
	// The single shared "auth" file of earlier versions held one storage
	// location's answer for every session; it must not decide any session.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "auth"), []byte("Sub"), 0o600); err != nil {
		t.Fatal(err)
	}
	if badge, _ := Detect(dir, "storage-B", env(nil), loggedOut); badge != "?" {
		t.Errorf("legacy auth file was served: got %q, want ?", badge)
	}
}

func TestCacheFileStaysInsideCacheDirForAnySource(t *testing.T) {
	// The source carries paths and user names; it must never become a path.
	root := t.TempDir()
	dir := filepath.Join(root, "cache")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	Detect(dir, "../escaped/x\nClaude Code-credentials", env(nil), loggedIn)
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "cache" {
		t.Errorf("cache write left cacheDir: %v", entries)
	}
	if files := authFiles(t, dir); len(files) != 1 {
		t.Errorf("want one auth cache file inside cacheDir, got %v", files)
	}
}

func TestUnknownAnswerExpiresQuickly(t *testing.T) {
	// A transient miss (sandboxed shell, locked keychain) must
	// not hide the account row for the full badge TTL. "?" is served while
	// younger than unknownTTL, then re-detected; Sub/API keep badgeTTL.
	dir := t.TempDir()
	if badge, _ := Detect(dir, "storage-A", env(nil), loggedOut); badge != "?" {
		t.Fatalf("first Detect = %q", badge)
	}
	if badge, _ := Detect(dir, "storage-A", env(nil), loggedIn); badge != "?" {
		t.Errorf("fresh unknown answer not served: %q", badge)
	}
	files := authFiles(t, dir)
	if len(files) != 1 {
		t.Fatalf("want one auth cache file, got %v", files)
	}
	old := time.Now().Add(-unknownTTL - time.Second)
	if err := os.Chtimes(files[0], old, old); err != nil {
		t.Fatal(err)
	}
	if badge, _ := Detect(dir, "storage-A", env(nil), loggedIn); badge != "Sub" {
		t.Errorf("expired unknown answer must re-detect: got %q, want Sub", badge)
	}
	// A known answer of the same age is still served.
	if err := os.Chtimes(files[0], old, old); err != nil {
		t.Fatal(err)
	}
	if badge, _ := Detect(dir, "storage-A", env(nil), loggedOut); badge != "Sub" {
		t.Errorf("Sub younger than badgeTTL must be served: got %q", badge)
	}
}
