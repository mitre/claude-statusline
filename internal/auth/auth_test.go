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

// age backdates the single login-status cache file in dir by d.
func age(t *testing.T, dir string, d time.Duration) {
	t.Helper()
	files := authFiles(t, dir)
	if len(files) != 1 {
		t.Fatalf("want one auth cache file, got %v", files)
	}
	when := time.Now().Add(-d)
	if err := os.Chtimes(files[0], when, when); err != nil {
		t.Fatal(err)
	}
}

func TestCachedAnswerLifetimes(t *testing.T) {
	// An unknown answer lives 15 s, so a transient miss (a sandboxed shell,
	// a locked keychain) recovers quickly; Sub and API live 300 s, the
	// keychain probe being too slow for every render. The probe's answer
	// flips after the first call, so a cache hit and a re-detection differ.
	apiKey := env(map[string]string{"ANTHROPIC_API_KEY": "fixture-key"})
	for _, tc := range []struct {
		name               string
		first, then        func(string) string
		firstOK, thenOK    func() error
		cached, redetected string
		servedAt, expired  time.Duration
	}{
		{"unknown", env(nil), env(nil), loggedOut, loggedIn, "?", "Sub", 14 * time.Second, 16 * time.Second},
		{"Sub", env(nil), env(nil), loggedIn, loggedOut, "Sub", "?", 299 * time.Second, 301 * time.Second},
		{"API", apiKey, env(nil), loggedOut, loggedOut, "API", "?", 299 * time.Second, 301 * time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if badge, _ := Detect(dir, "storage-A", tc.first, tc.firstOK); badge != tc.cached {
				t.Fatalf("first Detect = %q, want %q", badge, tc.cached)
			}
			age(t, dir, tc.servedAt)
			if badge, _ := Detect(dir, "storage-A", tc.then, tc.thenOK); badge != tc.cached {
				t.Errorf("at %v the cached %q must be served, got %q", tc.servedAt, tc.cached, badge)
			}
			age(t, dir, tc.expired)
			if badge, _ := Detect(dir, "storage-A", tc.then, tc.thenOK); badge != tc.redetected {
				t.Errorf("at %v the cached %q must be re-detected as %q, got %q", tc.expired, tc.cached, tc.redetected, badge)
			}
		})
	}
}
