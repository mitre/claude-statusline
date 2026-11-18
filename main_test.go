package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mitre/claude-statusline/internal/usage"
)

// fileCreds returns a tokenSource resolving from a real credentials
// file under a sandboxed HOME — the production file path, fixture token.
func fileCreds(t *testing.T, token string) tokenSource {
	t.Helper()
	home := t.TempDir()
	writeCredFile(t, filepath.Join(home, ".claude"), token)
	return tokenSource{
		lookupEnv: lookupMap(map[string]string{"HOME": home}),
		readFile:  os.ReadFile,
	}
}

// pointUsageAt aims fetchUsage at a test server and restores the production
// endpoint afterward. Tests using it must not run in parallel.
func pointUsageAt(t *testing.T, url string) {
	t.Helper()
	prod := usageEndpoint
	usageEndpoint = url
	t.Cleanup(func() { usageEndpoint = prod })
}

func TestFetchUsageServesPayloadWithAuthHeaders(t *testing.T) {
	const payload = `{"five_hour":{"utilization":5.9}}`
	var gotAuth, gotBeta string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotBeta = r.Header.Get("anthropic-beta")
		_, _ = w.Write([]byte(payload))
	}))
	defer srv.Close()
	pointUsageAt(t, srv.URL)

	body, err := fetchUsage(fileCreds(t, "fixture-access-e2e"), fingerprintOf("fixture-access-e2e"))
	if err != nil || string(body) != payload {
		t.Fatalf("fetchUsage = %q, %v; want the payload", body, err)
	}
	if gotAuth != "Bearer fixture-access-e2e" {
		t.Errorf("Authorization = %q; want the bearer token", gotAuth)
	}
	if gotBeta != "oauth-2025-04-20" {
		t.Errorf("anthropic-beta = %q; want the oauth beta header", gotBeta)
	}
}

func TestFetchUsageNoCredentialsSkipsNetwork(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("endpoint hit although no credential source resolves")
	}))
	defer srv.Close()
	pointUsageAt(t, srv.URL)

	creds := tokenSource{
		lookupEnv: lookupMap(map[string]string{"HOME": t.TempDir()}),
		readFile:  os.ReadFile,
	}
	if _, err := fetchUsage(creds, fingerprintOf("fixture-access-e2e")); err == nil {
		t.Fatal("fetchUsage = nil error; want credential-resolution failure")
	}
}

func TestFetchUsageCapsBodyAtOneMiB(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(make([]byte, 1<<20+512))
	}))
	defer srv.Close()
	pointUsageAt(t, srv.URL)

	body, err := fetchUsage(fileCreds(t, "fixture-access-e2e"), fingerprintOf("fixture-access-e2e"))
	if err != nil {
		t.Fatalf("fetchUsage error = %v; want capped body", err)
	}
	if len(body) != 1<<20 {
		t.Errorf("len(body) = %d; want exactly the 1 MiB cap", len(body))
	}
}

func TestFetchUsageTimesOut(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		<-release
	}))
	defer func() { close(release); srv.Close() }()
	pointUsageAt(t, srv.URL)
	prod := usageHTTPTimeout
	usageHTTPTimeout = 50 * time.Millisecond
	t.Cleanup(func() { usageHTTPTimeout = prod })

	if _, err := fetchUsage(fileCreds(t, "fixture-access-e2e"), fingerprintOf("fixture-access-e2e")); err == nil {
		t.Fatal("fetchUsage = nil error; want client timeout")
	}
}

// shimSecurity places a fake `security` binary first on PATH.
func shimSecurity(t *testing.T, script string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "security"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestKeychainCredentialJSONReadsItem(t *testing.T) {
	shimSecurity(t, "#!/bin/sh\necho '"+fixtureCredJSON("fixture-access-keychain")+"'\n")
	raw, err := keychainCredentialJSON("Claude Code-credentials", "dev")
	if err != nil {
		t.Fatalf("keychainCredentialJSON error = %v", err)
	}
	if strings.TrimSpace(raw) != fixtureCredJSON("fixture-access-keychain") {
		t.Errorf("keychainCredentialJSON = %q; want the shim's credential JSON", raw)
	}
}

func TestKeychainCredentialJSONAsksForServiceAndAccount(t *testing.T) {
	// Claude Code's lookup: security find-generic-password -a <account> -w
	// -s <service>, each value one argv entry.
	argv := filepath.Join(t.TempDir(), "argv")
	shimSecurity(t, "#!/bin/sh\nfor a in \"$@\"; do printf '%s\\n' \"$a\" >> '"+argv+"'; done\necho '"+fixtureCredJSON("fixture-access-keychain")+"'\n")
	if _, err := keychainCredentialJSON("Claude Code-credentials-2e96833a", "dev.user"); err != nil {
		t.Fatalf("keychainCredentialJSON error = %v", err)
	}
	got, err := os.ReadFile(argv)
	if err != nil {
		t.Fatal(err)
	}
	want := "find-generic-password\n-a\ndev.user\n-w\n-s\nClaude Code-credentials-2e96833a\n"
	if string(got) != want {
		t.Errorf("security argv = %q, want %q", got, want)
	}
}

func TestKeychainCredentialJSONRefusesUnvalidatedArguments(t *testing.T) {
	// Derived arguments reach security only in the exact ported shapes.
	ran := filepath.Join(t.TempDir(), "ran")
	shimSecurity(t, "#!/bin/sh\ntouch '"+ran+"'\n")
	for _, tc := range []struct{ service, account string }{
		{"Claude Code-credentials; rm -rf ~", "dev"},
		{"Other-credentials", "dev"},
		{"Claude Code-credentials-2E96833A", "dev"},
		{"Claude Code-credentials-2e96833a0", "dev"},
		{"Claude Code-credentials", "dev user"},
		{"Claude Code-credentials", ""},
	} {
		if _, err := keychainCredentialJSON(tc.service, tc.account); err == nil {
			t.Errorf("keychainCredentialJSON(%q, %q) = nil error; want refusal", tc.service, tc.account)
		}
	}
	if _, err := os.Stat(ran); !os.IsNotExist(err) {
		t.Error("security must not run for an argument outside the ported shapes")
	}
}

func TestKeychainCredentialJSONMissingItem(t *testing.T) {
	// security exits 44 when the item is absent; the chain treats it as a miss.
	shimSecurity(t, "#!/bin/sh\nexit 44\n")
	if _, err := keychainCredentialJSON("Claude Code-credentials", "dev"); err == nil {
		t.Fatal("keychainCredentialJSON = nil error; want exec failure")
	}
}

func TestKeychainCredentialJSONTimeoutBoundsCall(t *testing.T) {
	origTimeout := keychainTimeout
	keychainTimeout = 50 * time.Millisecond
	t.Cleanup(func() { keychainTimeout = origTimeout })

	shimSecurity(t, "#!/bin/sh\nsleep 5\n")
	start := time.Now()
	_, err := keychainCredentialJSON("Claude Code-credentials", "dev")
	if err == nil {
		t.Fatal("keychainCredentialJSON = nil error; want timeout kill")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("keychainCredentialJSON returned after %v; timeout did not bound the call", elapsed)
	}
}

func TestRunGitDeadlineKillsChild(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	dir := t.TempDir()
	shim := t.TempDir()
	if err := os.WriteFile(filepath.Join(shim, "git"), []byte("#!/bin/sh\nsleep 5\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", shim+string(os.PathListSeparator)+os.Getenv("PATH"))

	start := time.Now()
	_, err := runGit(ctx, dir, "status")
	if err == nil {
		t.Fatal("runGit = nil error; want deadline kill")
	}
	// Deadline 50ms + WaitDelay 100ms + slack: must return long before the
	// child's 5s sleep — the bound is real, not cosmetic.
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("runGit returned after %v; deadline did not bound the call", elapsed)
	}
}

func TestConfigPathDefaultsUnderHome(t *testing.T) {
	got := configPath("", envMap(map[string]string{"HOME": "/Users/dev"}), nil)
	if want := "/Users/dev/.claude/statusline.toml"; got != want {
		t.Errorf("configPath = %q; want %q", got, want)
	}
}

func TestConfigPathHonorsOverride(t *testing.T) {
	got := configPath("", envMap(map[string]string{
		"CLAUDE_STATUSLINE_CONFIG": "/tmp/other.toml",
		"HOME":                     "/Users/dev",
	}), nil)
	if got != "/tmp/other.toml" {
		t.Errorf("configPath = %q; want the env override", got)
	}
}

func TestConfigPathHonorsAntigravityOverride(t *testing.T) {
	got := configPath("antigravity", envMap(map[string]string{
		"ANTIGRAVITY_STATUSLINE_CONFIG": "/tmp/ag.toml",
		"CLAUDE_STATUSLINE_CONFIG":      "/tmp/other.toml",
		"HOME":                          "/Users/dev",
	}), nil)
	if got != "/tmp/ag.toml" {
		t.Errorf("configPath = %q; want ANTIGRAVITY_STATUSLINE_CONFIG override", got)
	}
}

func TestConfigPathAntigravityDefaultsUnderGeminiIfPresent(t *testing.T) {
	stat := func(p string) (os.FileInfo, error) {
		if p == "/Users/dev/.gemini/antigravity-cli/statusline.toml" {
			return nil, nil
		}
		return nil, os.ErrNotExist
	}
	got := configPath("antigravity", envMap(map[string]string{"HOME": "/Users/dev"}), stat)
	if want := "/Users/dev/.gemini/antigravity-cli/statusline.toml"; got != want {
		t.Errorf("configPath = %q; want %q", got, want)
	}
}

func TestConfigPathAntigravityFallsBackToClaudeIfAbsent(t *testing.T) {
	stat := func(string) (os.FileInfo, error) {
		return nil, os.ErrNotExist
	}
	got := configPath("antigravity", envMap(map[string]string{"HOME": "/Users/dev"}), stat)
	if want := "/Users/dev/.claude/statusline.toml"; got != want {
		t.Errorf("configPath = %q; want fallback to %q", got, want)
	}
}

func TestConfigPathEmptyHomeReturnsEmpty(t *testing.T) {
	got := configPath("", envMap(map[string]string{"HOME": ""}), nil)
	if got != "" {
		t.Errorf("configPath with empty HOME = %q; want empty string to avoid relative lookup", got)
	}
}

func TestFetchUsageClassifies429WithRetryAfter(t *testing.T) {
	// The usage endpoint answers 429 with a Retry-After in seconds when the
	// statusline polls too fast. That must surface as a typed error carrying
	// the interval so the resolver can hold off, never as a payload.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "199")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"type":"error","error":{"type":"rate_limit_error","message":"Rate limited."}}`))
	}))
	defer srv.Close()
	pointUsageAt(t, srv.URL)

	body, err := fetchUsage(fileCreds(t, "fixture-access-e2e"), fingerprintOf("fixture-access-e2e"))
	var ra *usage.RetryAfterError
	if !errors.As(err, &ra) {
		t.Fatalf("fetchUsage = %q, %v; want *usage.RetryAfterError", body, err)
	}
	if ra.After != 199*time.Second {
		t.Errorf("RetryAfterError.After = %v, want 199s from the header", ra.After)
	}
	if body != nil {
		t.Errorf("body = %q, want nil on a rate-limit response", body)
	}
}

func TestFetchUsage429WithoutRetryAfterStillTyped(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()
	pointUsageAt(t, srv.URL)
	_, err := fetchUsage(fileCreds(t, "fixture-access-e2e"), fingerprintOf("fixture-access-e2e"))
	var ra *usage.RetryAfterError
	if !errors.As(err, &ra) || ra.After != 0 {
		t.Errorf("429 without Retry-After: err=%v; want RetryAfterError with zero After (floor applies)", err)
	}
}

func TestFetchUsageSendsNoRequestForAnotherToken(t *testing.T) {
	// The render's cache entry is bound to one token; if the store now
	// resolves a different one, the request is not sent at all.
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("endpoint hit with a token other than the render's")
	}))
	defer srv.Close()
	pointUsageAt(t, srv.URL)

	if _, err := fetchUsage(fileCreds(t, "fixture-access-e2e"), fingerprintOf("fixture-access-other")); err == nil {
		t.Fatal("fetchUsage = nil error; want a credential-changed failure")
	}
}

// retryNow is a whole-second instant: an HTTP-date carries whole seconds.
var retryNow = time.Date(2026, 11, 18, 9, 0, 0, 0, time.UTC)

func TestRetryAfterParsesHTTPDate(t *testing.T) {
	header := retryNow.Add(90 * time.Second).Format(http.TimeFormat)
	if got := retryAfter(header, retryNow); got != 90*time.Second {
		t.Errorf("retryAfter(%q) = %v, want 90s", header, got)
	}
}

func TestRetryAfterReadsBothFormsAndRejectsTheRest(t *testing.T) {
	// RFC 9110 §10.2.3: Retry-After is an HTTP-date or delay-seconds
	// (1*DIGIT). Anything else, and any value that gives no wait, is 0, which
	// the resolver reads as "apply the floor".
	later := retryNow.Add(150 * time.Second)
	for _, tc := range []struct {
		header string
		want   time.Duration
	}{
		{"120", 120 * time.Second},
		{" 120 ", 120 * time.Second},
		{later.Format(http.TimeFormat), 150 * time.Second},
		{later.Format(time.RFC850), 150 * time.Second},
		{later.Format(time.ANSIC), 150 * time.Second},
		{"99999999", 99999999 * time.Second},
		{"", 0},
		{"soon", 0},
		{"+120", 0},
		{"-120", 0},
		{"1.5", 0},
		{"0", 0},
		{"9223372037", 0},
		{"99999999999999999999", 0},
		{retryNow.Format(http.TimeFormat), 0},
		{retryNow.Add(-time.Minute).Format(http.TimeFormat), 0},
	} {
		if got := retryAfter(tc.header, retryNow); got != tc.want {
			t.Errorf("retryAfter(%q) = %v, want %v", tc.header, got, tc.want)
		}
	}
}

func TestFetchUsage429WithHTTPDateRetryAfter(t *testing.T) {
	// The date form is measured from when the response arrives.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", time.Now().UTC().Add(90*time.Second).Format(http.TimeFormat))
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()
	pointUsageAt(t, srv.URL)
	_, err := fetchUsage(fileCreds(t, "fixture-access-e2e"), fingerprintOf("fixture-access-e2e"))
	var ra *usage.RetryAfterError
	if !errors.As(err, &ra) {
		t.Fatalf("fetchUsage error = %v; want *usage.RetryAfterError", err)
	}
	// The header has whole seconds, so up to a second is lost to rounding,
	// plus the time between the header and the parse.
	if ra.After <= 88*time.Second || ra.After > 90*time.Second {
		t.Errorf("RetryAfterError.After = %v, want just under 90s", ra.After)
	}
}
