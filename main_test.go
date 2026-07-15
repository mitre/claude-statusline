package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fileCreds returns a credentialSource resolving from a real credentials
// file under a sandboxed HOME — the production file path, fixture token.
func fileCreds(t *testing.T, token string) credentialSource {
	t.Helper()
	home := t.TempDir()
	writeCredFile(t, filepath.Join(home, ".claude"), token)
	return credentialSource{
		getenv:   envMap(map[string]string{"HOME": home}),
		readFile: os.ReadFile,
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

	body, err := fetchUsage(fileCreds(t, "sk-ant-oat01-e2e"))
	if err != nil || string(body) != payload {
		t.Fatalf("fetchUsage = %q, %v; want the payload", body, err)
	}
	if gotAuth != "Bearer sk-ant-oat01-e2e" {
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

	creds := credentialSource{
		getenv:   envMap(map[string]string{"HOME": t.TempDir()}),
		readFile: os.ReadFile,
	}
	if _, err := fetchUsage(creds); err == nil {
		t.Fatal("fetchUsage = nil error; want credential-resolution failure")
	}
}

func TestFetchUsageCapsBodyAtOneMiB(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(make([]byte, 1<<20+512))
	}))
	defer srv.Close()
	pointUsageAt(t, srv.URL)

	body, err := fetchUsage(fileCreds(t, "sk-ant-oat01-e2e"))
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

	if _, err := fetchUsage(fileCreds(t, "sk-ant-oat01-e2e")); err == nil {
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
	shimSecurity(t, "#!/bin/sh\necho '"+fixtureCredJSON("sk-ant-oat01-keychain")+"'\n")
	raw, err := keychainCredentialJSON()
	if err != nil {
		t.Fatalf("keychainCredentialJSON error = %v", err)
	}
	if strings.TrimSpace(raw) != fixtureCredJSON("sk-ant-oat01-keychain") {
		t.Errorf("keychainCredentialJSON = %q; want the shim's credential JSON", raw)
	}
}

func TestKeychainCredentialJSONMissingItem(t *testing.T) {
	// security exits 44 when the item is absent; the chain treats it as a miss.
	shimSecurity(t, "#!/bin/sh\nexit 44\n")
	if _, err := keychainCredentialJSON(); err == nil {
		t.Fatal("keychainCredentialJSON = nil error; want exec failure")
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
	t.Setenv("PATH", shim)

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
	got := configPath(envMap(map[string]string{"HOME": "/Users/dev"}))
	if want := "/Users/dev/.claude/statusline.toml"; got != want {
		t.Errorf("configPath = %q; want %q", got, want)
	}
}

func TestConfigPathHonorsOverride(t *testing.T) {
	got := configPath(envMap(map[string]string{
		"CLAUDE_STATUSLINE_CONFIG": "/tmp/other.toml",
		"HOME":                     "/Users/dev",
	}))
	if got != "/tmp/other.toml" {
		t.Errorf("configPath = %q; want the env override", got)
	}
}
