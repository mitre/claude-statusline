package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

const e2eUsagePayload = `{
  "five_hour": {"utilization": 5.9},
  "seven_day": {"utilization": 13},
  "extra_usage": {"is_enabled": false},
  "spend": {"used": {"amount_minor": 0, "exponent": 2}},
  "limits": []
}`

// e2eAccountUUID and e2eFingerprint are the neutral account and token
// fingerprint every end-to-end render runs as.
const (
	e2eAccountUUID = "0a1b2c3d-1111-4000-8000-000000000000"
	e2eFingerprint = "0123456789abcdef"
)

func e2eDeps(t testing.TB, stdinFixture string) deps {
	t.Helper()
	// config.Load and config.Default resolve the default cache dir from the
	// process environment: point it away from the developer's real cache.
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	cacheDir := t.TempDir()
	// The per-user cache base the shared fetch gate lives under.
	gateBase := t.TempDir()
	cfgPath := filepath.Join(t.TempDir(), "statusline.toml")
	// The fake runners below are CLI runners — pin the CLI engine. This also
	// proves the config→engine wiring: if cfg.GitEngine never reached
	// gitinfo, these renders would take the go-git path, the fakes would go
	// unused, and every golden would fail.
	cfg := "[cache]\ndir = \"" + cacheDir + "\"\n[project]\ngit_engine = \"cli\"\n"
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join("testdata", stdinFixture))
	if err != nil {
		t.Fatal(err)
	}

	env := map[string]string{
		"CLAUDE_STATUSLINE_CONFIG": cfgPath,
		"HOME":                     "/Users/dev",
	}
	return deps{
		stdin:  bytes.NewReader(b),
		getenv: func(k string) string { return env[k] },
		runGit: func(_ context.Context, _ string, args ...string) (string, error) {
			switch fmt.Sprintf("%v", args) {
			case "[branch --show-current]":
				return "main\n", nil
			case "[--no-optional-locks status --porcelain]":
				return "?? SESSION-KICKOFF.md\n?? docs/\n", nil
			}
			return "", errors.New("unexpected git call")
		},
		keychainOK: func() error { return nil },
		fetchUsage: func(string) ([]byte, error) { return []byte(e2eUsagePayload), nil },
		readFile: func(p string) ([]byte, error) {
			if want := "/Users/dev/.claude.json"; p != want {
				return nil, fmt.Errorf("unexpected readFile path %q, want %q", p, want)
			}
			return []byte(`{"oauthAccount":{"accountUuid":"` + e2eAccountUUID + `","emailAddress":"dev@example.com"}}`), nil
		},
		tokenFingerprint: func() (string, error) { return e2eFingerprint, nil },
		// Hermetic: no legacy account record and no Antigravity config exist.
		stat:         func(string) (os.FileInfo, error) { return nil, os.ErrNotExist },
		readDir:      func(string) ([]os.DirEntry, error) { return nil, os.ErrNotExist },
		userCacheDir: func() (string, error) { return gateBase, nil },
	}
}

func TestRunEndToEndGolden(t *testing.T) {
	out, errOut := run(e2eDeps(t, "full.json"))
	want := strings.Join([]string{
		"\x1b[2mmodel    \x1b[m\x1b[1;36mFable 5 1M\x1b[m\x1b[2m · \x1b[m\x1b[32mSub\x1b[m\x1b[2m · xhigh\x1b[m\x1b[2m · \x1b[m\x1b[33m⚡ fast\x1b[m\x1b[2m · session 0a1b2c3d\x1b[m",
		"\x1b[2mproject  \x1b[m\x1b[1m~/projects/demo-app\x1b[m \x1b[2m·\x1b[m \x1b[34m⎇ main\x1b[m \x1b[33m~2\x1b[m",
		"\x1b[2mcontext  \x1b[m\x1b[32m▓▓▓░░░░░░░\x1b[m \x1b[32m30%\x1b[m \x1b[2m>200k\x1b[m",
		"\x1b[2maccount  \x1b[mdev@example.com \x1b[2m·\x1b[m \x1b[32m5h 5%\x1b[m \x1b[2m·\x1b[m \x1b[32mweek 13%\x1b[m",
		"\x1b[2mactivity \x1b[m\x1b[2m17h23m\x1b[m \x1b[2m·\x1b[m \x1b[32m+1,598\x1b[m/\x1b[31m-8\x1b[m \x1b[2mlines\x1b[m",
	}, "\n")
	if out != want {
		t.Errorf("run() output:\n got %q\nwant %q", out, want)
	}
	if errOut != "" {
		t.Errorf("unexpected stderr: %q", errOut)
	}
}

func TestRunUnparseableStdinRendersNothing(t *testing.T) {
	d := e2eDeps(t, "full.json")
	d.stdin = strings.NewReader("this is not json")
	out, _ := run(d)
	if out != "" {
		t.Errorf("bad stdin must render nothing (never crash the host UI), got %q", out)
	}
}

func TestRunMalformedConfigFallsBackToDefaults(t *testing.T) {
	// The default-config fallback resolves CacheDir via the PROCESS env
	// (os.UserCacheDir) — sandbox HOME or this test writes fixture data into
	// the developer's real ~/Library/Caches/claude-statusline (caught live
	// 2026-07-03: the fixture email surfaced in the real statusline).
	t.Setenv("HOME", t.TempDir())
	d := e2eDeps(t, "full.json")
	bad := filepath.Join(t.TempDir(), "bad.toml")
	if err := os.WriteFile(bad, []byte("rows = [unclosed"), 0o600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"CLAUDE_STATUSLINE_CONFIG": bad, "HOME": "/Users/dev"}
	d.getenv = func(k string) string { return env[k] }

	out, errOut := run(d)
	if !strings.Contains(out, "Fable 5 1M") {
		t.Errorf("defaults not rendered on config error: %q", out)
	}
	if !strings.Contains(errOut, "config error") {
		t.Errorf("config error not surfaced on stderr: %q", errOut)
	}
}

// appendConfig grows the config file e2eDeps wrote (it already holds the
// [cache] dir section) with extra TOML.
func appendConfig(t *testing.T, d deps, body string) {
	t.Helper()
	f, err := os.OpenFile(d.getenv("CLAUDE_STATUSLINE_CONFIG"), os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(body); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestRunGitDeadlineServesStaleCacheWithinBudget(t *testing.T) {
	d := e2eDeps(t, "full.json")
	appendConfig(t, d, "git_timeout_ms = 50\n")
	fix, err := os.ReadFile(filepath.Join("testdata", "full.json"))
	if err != nil {
		t.Fatal(err)
	}

	// Seed the caches through one healthy render: branch + 7 dirty files.
	seeded := d
	seeded.runGit = func(_ context.Context, _ string, args ...string) (string, error) {
		switch fmt.Sprintf("%v", args) {
		case "[branch --show-current]":
			return "gone-stale\n", nil
		case "[--no-optional-locks status --porcelain]":
			return strings.Repeat("?? f\n", 7), nil
		}
		return "", errors.New("unexpected git call")
	}
	if out, errOut := run(seeded); errOut != "" || !strings.Contains(out, "gone-stale") || !strings.Contains(out, "~7") {
		t.Fatalf("cache seeding render failed: out %q stderr %q", out, errOut)
	}

	// A pathological worktree: git blocks far past the deadline. A correct
	// runner is cancelled at 50ms and the stale cache is served; a broken
	// one returns only after the full sleep and fails both assertions.
	blocked := d
	blocked.stdin = bytes.NewReader(fix)
	blocked.runGit = func(ctx context.Context, _ string, _ ...string) (string, error) {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(500 * time.Millisecond):
			return "", nil
		}
	}

	start := time.Now()
	out, errOut := run(blocked)
	elapsed := time.Since(start)
	if errOut != "" {
		t.Fatalf("config must parse cleanly: %q", errOut)
	}

	if !strings.Contains(out, "gone-stale") || !strings.Contains(out, "~7") {
		t.Errorf("stale cached branch/dirty not served on git deadline: %q", out)
	}
	if elapsed > 250*time.Millisecond {
		t.Errorf("render blocked %v; the 50ms deadline must bound git latency", elapsed)
	}
}

// The fakes above model a well-behaved runner; this pins the REAL runner.
// The shim's sh is killed at the deadline, but its orphaned `sleep` child
// inherits the stdout pipe — without Cmd.WaitDelay, Output() blocks until
// every descendant exits (the 10.5s live-test hang, real git can do the
// same via hooks/fsmonitor/credential helpers).
func TestRunGitRealRunnerBoundedByDeadline(t *testing.T) {
	shim := t.TempDir()
	script := "#!/bin/sh\nsleep 2\nexec /usr/bin/git \"$@\"\n"
	if err := os.WriteFile(filepath.Join(shim, "git"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", shim+string(os.PathListSeparator)+os.Getenv("PATH"))

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := runGit(ctx, t.TempDir(), "status", "--porcelain")
	elapsed := time.Since(start)

	if err == nil {
		t.Error("a deadline-killed git call must surface an error")
	}
	if elapsed > 500*time.Millisecond {
		t.Errorf("runGit blocked %v; the deadline must bound the real runner even when a descendant holds the stdout pipe", elapsed)
	}
}

func TestRunGitTimeoutZeroDisablesDeadline(t *testing.T) {
	d := e2eDeps(t, "full.json")
	appendConfig(t, d, "git_timeout_ms = 0\n")

	// The status call outlasts the 150ms default deadline. With 0 wired
	// through (unbounded), the live value must arrive; if the default were
	// applied instead, the call would be cancelled and ~1 never rendered.
	d.runGit = func(ctx context.Context, _ string, args ...string) (string, error) {
		if fmt.Sprintf("%v", args) == "[branch --show-current]" {
			return "main\n", nil
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(300 * time.Millisecond):
			return "?? f\n", nil
		}
	}

	out, errOut := run(d)
	if errOut != "" {
		t.Fatalf("config must parse cleanly: %q", errOut)
	}
	if !strings.Contains(out, "⎇ main") || !strings.Contains(out, "~1") {
		t.Errorf("git_timeout_ms=0 must disable the deadline (live ~1 expected): %q", out)
	}
}

func TestRunFetchFailureCollapsesLimitsRow(t *testing.T) {
	// No endpoint data AND no stdin rate_limits: nothing truthful to show,
	// so the row collapses — never fabricated zeros. (With stdin
	// rate_limits present, the live-meter fallback renders instead — see
	// TestRunNoCacheStdinMetersStillRender.)
	d := e2eDeps(t, "full.json")
	d.fetchUsage = func(string) ([]byte, error) { return nil, errors.New("rate limited") }
	d.stdin = strings.NewReader(e2eStdinNoRateLimits)
	out, _ := run(d)
	if strings.Contains(out, "account") {
		t.Errorf("account row rendered without data (fabricated zeros?): %q", out)
	}
}

// e2eCacheDir reads the [cache] dir back out of the config file e2eDeps
// wrote — the same file run() resolves, so a test can seed the exact cache
// the render will read.
func e2eCacheDir(t *testing.T, d deps) string {
	t.Helper()
	b, err := os.ReadFile(d.getenv("CLAUDE_STATUSLINE_CONFIG"))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(b), "\n") {
		if rest, ok := strings.CutPrefix(line, `dir = "`); ok {
			return strings.TrimSuffix(rest, `"`)
		}
	}
	t.Fatal("no cache dir in e2e config")
	return ""
}

func TestRunStaleUsageDataShowsDimAgeMarker(t *testing.T) {
	// Fetches failing + an expired good payload in the cache: the stale-good
	// fallback serves it, and the account row must say so — a trailing dim,
	// factual age. The meters keep their true (old) values. Before this
	// marker, this state was indistinguishable from fresh data without
	// reading cache mtimes (the 2:00p diagnosis, 2026-07-03).
	d := e2eDeps(t, "full.json")
	d.fetchUsage = func(string) ([]byte, error) { return nil, errors.New("network down") }
	// Endpoint-stale semantics require a stdin WITHOUT rate_limits — with
	// them, the live-meter fallback takes over (its own tests below).
	d.stdin = strings.NewReader(e2eStdinNoRateLimits)

	seedStaleUsage(t, d, e2eUsagePayload) // six minutes: past the default 200 s TTL

	out, _ := run(d)
	want := "\x1b[2maccount  \x1b[mdev@example.com \x1b[2m·\x1b[m \x1b[32m5h 5%\x1b[m \x1b[2m·\x1b[m \x1b[32mweek 13%\x1b[m \x1b[2m·\x1b[m \x1b[2m(data 6m old)\x1b[m"
	if !strings.Contains(out, want) {
		t.Errorf("stale-good serve must carry the dim age marker:\n got %q\nwant row %q", out, want)
	}
}

func TestRunIndexLockBadgeWiring(t *testing.T) {
	// A repo cwd holding a 14-minute index.lock (past the 300 s default)
	// must surface the yellow factual badge on the project row — proves the
	// config default → gitinfo threshold → render wiring end-to-end.
	d := e2eDeps(t, "full.json")
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	lock := filepath.Join(repo, ".git", "index.lock")
	if err := os.WriteFile(lock, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	held := time.Now().Add(-14 * time.Minute)
	if err := os.Chtimes(lock, held, held); err != nil {
		t.Fatal(err)
	}
	d.stdin = strings.NewReader(`{"session_id":"0a1b2c3d-0000-4000-8000-000000000000",` +
		`"model":{"display_name":"Fable 5"},"workspace":{"current_dir":"` + repo + `"},` +
		`"context_window":{"used_percentage":30,"context_window_size":1000000},` +
		`"cost":{"total_lines_added":0,"total_lines_removed":0,"total_duration_ms":0}}`)

	out, errOut := run(d)
	if errOut != "" {
		t.Fatalf("config must parse cleanly (a fallback would silently test defaults against the REAL cache): %q", errOut)
	}
	badge := " \x1b[33m⚠ index.lock 14m\x1b[m"
	if !strings.Contains(out, badge) {
		t.Errorf("project row must carry the lock badge:\n got %q\nwant fragment %q", out, badge)
	}

	// lock_badge = false: same lock, badge gone — the toggle reaches gitinfo.
	// The e2e config already holds an open [project] table — append the bare
	// key (a second [project] header is a TOML duplicate-table error).
	d2 := e2eDeps(t, "full.json")
	appendConfig(t, d2, "lock_badge = false\n")
	d2.stdin = strings.NewReader(`{"session_id":"0a1b2c3d-0000-4000-8000-000000000000",` +
		`"model":{"display_name":"Fable 5"},"workspace":{"current_dir":"` + repo + `"},` +
		`"context_window":{"used_percentage":30,"context_window_size":1000000},` +
		`"cost":{"total_lines_added":0,"total_lines_removed":0,"total_duration_ms":0}}`)
	out, errOut = run(d2)
	if errOut != "" {
		t.Fatalf("config must parse cleanly: %q", errOut)
	}
	if strings.Contains(out, "index.lock") {
		t.Errorf("lock_badge=false must suppress the badge: %q", out)
	}
}

func TestRunStaleAgeMarkerToggleOff(t *testing.T) {
	// [account] show_stale_age = false: same stale-good serve, no marker —
	// proves the config→render wiring, not just the Options field.
	d := e2eDeps(t, "full.json")
	d.fetchUsage = func(string) ([]byte, error) { return nil, errors.New("network down") }
	d.stdin = strings.NewReader(e2eStdinNoRateLimits) // endpoint-stale semantics, no fallback
	appendConfig(t, d, "[account]\nshow_stale_age = false\n")

	seedStaleUsage(t, d, e2eUsagePayload)

	out, errOut := run(d)
	if errOut != "" {
		t.Fatalf("config must parse cleanly: %q", errOut)
	}
	want := "\x1b[2maccount  \x1b[mdev@example.com \x1b[2m·\x1b[m \x1b[32m5h 5%\x1b[m \x1b[2m·\x1b[m \x1b[32mweek 13%\x1b[m"
	if !strings.Contains(out, want) || strings.Contains(out, "old)") {
		t.Errorf("show_stale_age=false must serve stale meters without the marker:\n got %q\nwant row %q", out, want)
	}
}

// e2eStdinNoRateLimits mirrors full.json WITHOUT the rate_limits block —
// the pre-fallback stdin shape, for tests pinning pure endpoint semantics.
const e2eStdinNoRateLimits = `{"session_id":"0a1b2c3d-0000-4000-8000-000000000000",` +
	`"model":{"display_name":"Fable 5"},` +
	`"workspace":{"current_dir":"/Users/dev/projects/demo-app"},` +
	`"context_window":{"used_percentage":30,"context_window_size":1000000},` +
	`"cost":{"total_lines_added":1598,"total_lines_removed":8,"total_duration_ms":62580000}}`

func TestRunStaleFallbackUsesStdinMeters(t *testing.T) {
	// Fetch failing + stale cache + stdin rate_limits: the live stdin
	// meters (42/77) replace the stale endpoint values (5/13), and the age
	// marker disappears — nothing stale remains visible (no model window).
	d := e2eDeps(t, "full.json")
	d.fetchUsage = func(string) ([]byte, error) { return nil, errors.New("network down") }
	seedStaleUsage(t, d, e2eUsagePayload)

	out, _ := run(d)
	if !strings.Contains(out, "5h 42%") || !strings.Contains(out, "week 77%") {
		t.Errorf("stdin meters must replace stale endpoint values: %q", out)
	}
	if strings.Contains(out, "5h 5%") || strings.Contains(out, "old)") {
		t.Errorf("stale values/marker must not render with live stdin meters: %q", out)
	}
}

func TestRunNoCacheStdinMetersStillRender(t *testing.T) {
	// Fetch failing + NO cache at all: before the fallback the account row
	// collapsed; with stdin rate_limits it renders live meters + email.
	d := e2eDeps(t, "full.json")
	d.fetchUsage = func(string) ([]byte, error) { return nil, errors.New("network down") }

	out, _ := run(d)
	if !strings.Contains(out, "account") || !strings.Contains(out, "5h 42%") ||
		!strings.Contains(out, "week 77%") || !strings.Contains(out, "dev@example.com") {
		t.Errorf("stdin-only account row must render when no cache exists: %q", out)
	}
}

func TestRunAntigravityEndToEnd(t *testing.T) {
	d := e2eDeps(t, "full.json")
	// Antigravity does not use keychain or fetchUsage
	d.keychainOK = func() error { return errors.New("no claude credentials") }
	d.fetchUsage = func(string) ([]byte, error) { return nil, errors.New("no anthropic endpoint") }
	d.readFile = func(_ string) ([]byte, error) { return nil, os.ErrNotExist }
	d.stdin = strings.NewReader(`{
		"session_id": "9233d019-88e0-4ce2-a28c-6de1295fad0a",
		"product": "antigravity",
		"plan_tier": "Google AI Ultra",
		"email": "lippold@gmail.com",
		"cwd": "/Users/dev/projects/demo-app",
		"workspace": {"current_dir": "/Users/dev/projects/demo-app"},
		"model": {"display_name": "Gemini 3.8 Flash (High)", "effort": "high"},
		"context_window": {"used_percentage": 22.09, "context_window_size": 1048576},
		"exceeds_200k_tokens": true,
		"quota": {
			"gemini-5h": {"remaining_fraction": 0.9606924, "reset_time": "2026-10-04T18:43:54Z"},
			"gemini-weekly": {"remaining_fraction": 0.9937261, "reset_time": "2026-10-11T01:50:24Z"},
			"3p-5h": {"remaining_fraction": 1.0, "reset_time": "2026-10-04T20:03:09Z"}
		}
	}`)

	out, errOut := run(d)
	if errOut != "" {
		t.Errorf("unexpected stderr: %q", errOut)
	}
	if !strings.Contains(out, "Gemini 3.8 Flash (High) 1M") {
		t.Errorf("missing model name: %q", out)
	}
	if !strings.Contains(out, "Ultra") {
		t.Errorf("missing Ultra auth badge: %q", out)
	}
	if !strings.Contains(out, "22%") || !strings.Contains(out, ">200k") {
		t.Errorf("missing context bar: %q", out)
	}
	if !strings.Contains(out, "account") || !strings.Contains(out, "lippold@gmail.com") {
		t.Errorf("missing account email: %q", out)
	}
	if !strings.Contains(out, "Google AI Ultra") {
		t.Errorf("missing plan tier in account row: %q", out)
	}
	if !strings.Contains(out, "5h 4%") || !strings.Contains(out, "week 1%") {
		t.Errorf("missing 5h/week meters: %q", out)
	}
}

func TestRunAntigravityConfigLoaded(t *testing.T) {
	d := e2eDeps(t, "full.json")
	d.keychainOK = func() error { return errors.New("no claude credentials") }
	d.fetchUsage = func(string) ([]byte, error) { return nil, errors.New("no anthropic endpoint") }
	d.readFile = func(_ string) ([]byte, error) { return nil, os.ErrNotExist }

	home := t.TempDir()
	// The Antigravity config sets no cache dir, so config.Default resolves
	// it from the PROCESS HOME: sandbox it, or the renders write the
	// developer's real user cache.
	t.Setenv("HOME", home)
	agDir := filepath.Join(home, ".gemini", "antigravity-cli")
	if err := os.MkdirAll(agDir, 0o700); err != nil {
		t.Fatal(err)
	}
	d.getenv = func(k string) string {
		if k == "HOME" {
			return home
		}
		return ""
	}
	// The config file lives on the real filesystem under a temp HOME.
	d.stat = os.Stat
	const stdin = `{
		"session_id": "9233d019-88e0-4ce2-a28c-6de1295fad0a",
		"product": "antigravity",
		"plan_tier": "Google AI Ultra",
		"email": "lippold@gmail.com",
		"cwd": "/Users/dev/projects/demo-app",
		"cost": {"total_lines_added": 12, "total_lines_removed": 5},
		"quota": {
			"gemini-5h": {"remaining_fraction": 0.95, "reset_time": "2026-10-04T18:43:54Z"}
		}
	}`

	// Control: without the Antigravity config the activity row renders.
	d.stdin = strings.NewReader(stdin)
	if out, errOut := run(d); !strings.Contains(out, "activity") || errOut != "" {
		t.Fatalf("without the Antigravity config the activity row must render: out %q stderr %q", out, errOut)
	}

	// Hide activity row via Antigravity config file
	agConfig := "[rows]\nactivity = false\n"
	if err := os.WriteFile(filepath.Join(agDir, "statusline.toml"), []byte(agConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	d.stdin = strings.NewReader(stdin)
	out, errOut := run(d)
	if errOut != "" {
		t.Errorf("unexpected stderr: %q", errOut)
	}
	if strings.Contains(out, "activity") || strings.Contains(out, "+12/-5") {
		t.Errorf("activity row should have been disabled by ~/.gemini/antigravity-cli/statusline.toml: %q", out)
	}
	if !strings.Contains(out, "Google AI Ultra") {
		t.Errorf("missing plan tier in account row: %q", out)
	}
}

func TestRunAntigravityIgnoresAnthropicAPIKeyBadge(t *testing.T) {
	d := e2eDeps(t, "full.json")
	d.keychainOK = func() error { return errors.New("no claude credentials") }
	d.fetchUsage = func(string) ([]byte, error) { return nil, errors.New("no anthropic endpoint") }
	d.readFile = func(_ string) ([]byte, error) { return nil, os.ErrNotExist }
	origGetenv := d.getenv
	d.getenv = func(k string) string {
		if k == "ANTHROPIC_API_KEY" {
			return "sk-ant-test"
		}
		return origGetenv(k)
	}
	// Antigravity payload with NO plan_tier specified
	d.stdin = strings.NewReader(`{
		"session_id": "9233d019-88e0-4ce2-a28c-6de1295fad0a",
		"product": "antigravity",
		"email": "lippold@gmail.com",
		"cwd": "/Users/dev/projects/demo-app",
		"quota": {
			"gemini-5h": {"remaining_fraction": 0.95, "reset_time": "2026-10-04T18:43:54Z"}
		}
	}`)

	out, _ := run(d)
	// Must NOT show API key alarm or yellow API badge
	if strings.Contains(out, "METERED BILLING") || strings.Contains(out, "· API") {
		t.Errorf("Antigravity session must not inherit API badge or alarm from ANTHROPIC_API_KEY: %q", out)
	}
	// Must show Sub badge
	if !strings.Contains(out, "Sub") {
		t.Errorf("Antigravity session with empty plan_tier must default to Sub: %q", out)
	}
	// Account meters must render!
	if !strings.Contains(out, "account") || !strings.Contains(out, "5h 5%") {
		t.Errorf("account meters must render despite ANTHROPIC_API_KEY in env: %q", out)
	}
}

// e2eNullFiveHourPayload is the endpoint's shape when no five-hour window is
// in progress: five_hour null, the weekly window and plan limits present.
const e2eNullFiveHourPayload = `{
  "five_hour": null,
  "seven_day": {"utilization": 43, "resets_at": "2026-11-06T22:00:00Z"},
  "extra_usage": {"is_enabled": false},
  "spend": {"used": {"amount_minor": 0, "exponent": 2}},
  "limits": [{"kind": "weekly_all", "percent": 43, "is_active": false, "scope": null}]
}`

func TestRunNullFiveHourPayloadOmitsTheMeter(t *testing.T) {
	// A fresh payload with no five-hour window and no live stdin reading
	// renders the week meter alone: no fabricated "5h 0%", no stale marker.
	d := e2eDeps(t, "full.json")
	d.stdin = strings.NewReader(e2eStdinNoRateLimits)
	d.fetchUsage = func(string) ([]byte, error) { return []byte(e2eNullFiveHourPayload), nil }

	out, _ := run(d)
	if !strings.Contains(out, "week 43%") || !strings.Contains(out, "dev@example.com") {
		t.Errorf("account row must render the weekly window from a null-five_hour payload: %q", out)
	}
	if strings.Contains(out, "5h") || strings.Contains(out, "old)") {
		t.Errorf("no 5h meter or age marker may render for a fresh null five_hour: %q", out)
	}
}

func TestRunStdinFiveHourSupersedesAbsentWindow(t *testing.T) {
	// Stale null-five_hour cache + failing fetch + stdin rate_limits: the
	// host's live reading brings the 5h meter back (42%) beside week 77%.
	d := e2eDeps(t, "full.json")
	d.fetchUsage = func(string) ([]byte, error) { return nil, errors.New("network down") }
	seedStaleUsage(t, d, e2eNullFiveHourPayload)

	out, _ := run(d)
	if !strings.Contains(out, "5h 42%") || !strings.Contains(out, "week 77%") {
		t.Errorf("stdin five-hour reading must supersede the absent endpoint window: %q", out)
	}
}

func TestRunTTLBelowFloorKeepsTheRestOfTheConfig(t *testing.T) {
	// The v0.3.0 example config shipped ttl_seconds = 180. Upgrading must
	// raise it to the floor and keep every other setting in the file, with
	// one diagnostic — not fall back to the defaults wholesale.
	d := e2eDeps(t, "full.json")
	appendConfig(t, d, "[rows]\nproject = false\n[usage]\nttl_seconds = 180\n")

	out, errOut := run(d)
	if strings.Contains(out, "project") {
		t.Errorf("rows.project = false from the same file must still apply: %q", out)
	}
	if !strings.Contains(out, "account") {
		t.Errorf("the account row must still render: %q", out)
	}
	if strings.Contains(errOut, "config error") || !strings.Contains(errOut, "usage.ttl_seconds") ||
		!strings.Contains(errOut, "180") || !strings.Contains(errOut, "200") {
		t.Errorf("stderr must carry one adjustment note, not a config error: %q", errOut)
	}
}

// e2eLimitsOnlyPayload passes the shape gate on its plan limits alone: no
// five-hour and no weekly window is in progress.
const e2eLimitsOnlyPayload = `{
  "five_hour": null,
  "seven_day": null,
  "extra_usage": {"is_enabled": false},
  "spend": {"used": {"amount_minor": 0, "exponent": 2}},
  "limits": [{"kind": "weekly_scoped", "percent": 40, "is_active": true,
    "resets_at": "2026-11-06T22:00:00Z", "scope": {"model": {"display_name": "Fable"}}}]
}`

// e2eStdinFiveHourOnly is a stdin payload whose rate_limits carry the
// five-hour window (42%) and no weekly window.
const e2eStdinFiveHourOnly = `{"session_id":"0a1b2c3d-0000-4000-8000-000000000000",` +
	`"model":{"display_name":"Fable 5"},` +
	`"workspace":{"current_dir":"/Users/dev/projects/demo-app"},` +
	`"rate_limits":{"five_hour":{"used_percentage":42,"resets_at":0}}}`

// seedStaleUsage writes payload as the usage cache, six minutes old, so the
// render serves it through the stale fallback.
func seedStaleUsage(t *testing.T, d deps, payload string) {
	t.Helper()
	seed := d
	seed.stdin = strings.NewReader(e2eStdinNoRateLimits)
	seed.fetchUsage = func(string) ([]byte, error) { return []byte(payload), nil }
	if _, errOut := run(seed); errOut != "" {
		t.Fatalf("seeding render: %q", errOut)
	}
	ageUsage(t, d, time.Now().Add(-6*time.Minute))
}

// ageUsage backdates the cached usage payload to when; the seeding render
// also shut the shared fetch gate, so the next render serves the cache.
func ageUsage(t *testing.T, d deps, when time.Time) {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(e2eCacheDir(t, d), "usage-*"))
	if err != nil || len(files) != 1 {
		t.Fatalf("want one cached usage payload, got %v (%v)", files, err)
	}
	if err := os.Chtimes(files[0], when, when); err != nil {
		t.Fatal(err)
	}
}

func TestRunLimitsOnlyPayloadOmitsTheWeekMeter(t *testing.T) {
	// A fresh payload with neither window and no live stdin reading renders
	// the scoped limit alone: no fabricated "week 0%" and no "5h 0%".
	d := e2eDeps(t, "full.json")
	d.stdin = strings.NewReader(e2eStdinNoRateLimits)
	d.fetchUsage = func(string) ([]byte, error) { return []byte(e2eLimitsOnlyPayload), nil }

	out, _ := run(d)
	if !strings.Contains(out, "Fable 40%") {
		t.Errorf("account row must render the scoped limit from a limits-only payload: %q", out)
	}
	if strings.Contains(out, "week") || strings.Contains(out, "5h") {
		t.Errorf("no week or 5h meter may render when the payload carries neither window: %q", out)
	}
}

func TestRunLiveStdinFillsWindowsAFreshPayloadMarksAbsent(t *testing.T) {
	// A fresh endpoint payload that reports a window as not in progress
	// loses to a live stdin reading of that window; windows the payload
	// does report keep the fresh endpoint value. Nothing is stale: no marker.
	for _, c := range []struct {
		name, payload, stdin string
		want, absent         []string
	}{
		{
			"null five_hour, stdin both windows", e2eNullFiveHourPayload, "",
			[]string{"5h 42%", "week 43%"},
			[]string{"week 77%", "old)"},
		},
		{
			"limits only, stdin both windows", e2eLimitsOnlyPayload, "",
			[]string{"5h 42%", "week 77%", "Fable 40%"},
			[]string{"old)"},
		},
		{
			"limits only, stdin five-hour only", e2eLimitsOnlyPayload, e2eStdinFiveHourOnly,
			[]string{"5h 42%", "Fable 40%"},
			[]string{"week", "old)"},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			d := e2eDeps(t, "full.json")
			if c.stdin != "" {
				d.stdin = strings.NewReader(c.stdin)
			}
			payload := c.payload
			d.fetchUsage = func(string) ([]byte, error) { return []byte(payload), nil }

			out, _ := run(d)
			for _, w := range c.want {
				if !strings.Contains(out, w) {
					t.Errorf("want %q in the account row: %q", w, out)
				}
			}
			for _, a := range c.absent {
				if strings.Contains(out, a) {
					t.Errorf("%q must not render: %q", a, out)
				}
			}
		})
	}
}

func TestRunStdinWeeklySupersedesAbsentWindow(t *testing.T) {
	// Stale limits-only cache + failing fetch + stdin rate_limits carrying
	// both windows: the host's live readings bring back 5h 42% and week 77%.
	d := e2eDeps(t, "full.json")
	d.fetchUsage = func(string) ([]byte, error) { return nil, errors.New("network down") }
	seedStaleUsage(t, d, e2eLimitsOnlyPayload)

	out, _ := run(d)
	if !strings.Contains(out, "5h 42%") || !strings.Contains(out, "week 77%") {
		t.Errorf("stdin readings must supersede the absent endpoint windows: %q", out)
	}
	if !strings.Contains(out, "Fable 40%") || !strings.Contains(out, "(data 6m old)") {
		t.Errorf("the stale endpoint scoped limit must stay visible with the age marker: %q", out)
	}
}

func TestRunStdinWithoutWeeklyKeepsTheWeekMeterAbsent(t *testing.T) {
	// Stale limits-only cache + failing fetch + stdin carrying only the
	// five-hour window: the live 5h reading renders; the weekly window is
	// absent on both sides, so no "week 0%".
	d := e2eDeps(t, "full.json")
	d.stdin = strings.NewReader(e2eStdinFiveHourOnly)
	d.fetchUsage = func(string) ([]byte, error) { return nil, errors.New("network down") }
	seedStaleUsage(t, d, e2eLimitsOnlyPayload)

	out, _ := run(d)
	if !strings.Contains(out, "5h 42%") {
		t.Errorf("stdin five-hour reading must render: %q", out)
	}
	if strings.Contains(out, "week") {
		t.Errorf("no week meter may render when neither side reports a weekly window: %q", out)
	}
	if !strings.Contains(out, "Fable 40%") || !strings.Contains(out, "(data 6m old)") {
		t.Errorf("the stale scoped limit must stay visible with the age marker: %q", out)
	}
}

// e2eNoWeeklyPayload carries a five-hour window, no weekly window and no
// scoped limit.
const e2eNoWeeklyPayload = `{"five_hour": {"utilization": 5.9}, "seven_day": null,
  "extra_usage": {"is_enabled": false}, "spend": {"used": {"amount_minor": 0, "exponent": 2}},
  "limits": []}`

func TestRunLiveMetersOverAbsentEndpointWeekHaveNoAgeMarker(t *testing.T) {
	// Stale cache with no weekly window and no scoped limit + failing fetch
	// + stdin carrying only the five-hour window: the only visible meter is
	// the live 5h reading, so nothing stale is shown and no marker renders.
	d := e2eDeps(t, "full.json")
	d.stdin = strings.NewReader(e2eStdinFiveHourOnly)
	d.fetchUsage = func(string) ([]byte, error) { return nil, errors.New("network down") }
	seedStaleUsage(t, d, e2eNoWeeklyPayload)

	out, _ := run(d)
	if !strings.Contains(out, "5h 42%") {
		t.Errorf("the live 5h reading must render: %q", out)
	}
	if strings.Contains(out, "week") || strings.Contains(out, "old)") {
		t.Errorf("no week meter and no age marker may render over only-live meters: %q", out)
	}
}

// e2eUnscopedLimitsOnlyPayload passes the shape gate on an unscoped plan
// limit alone: it carries nothing the account row can show.
const e2eUnscopedLimitsOnlyPayload = `{"five_hour": null, "seven_day": null,
  "extra_usage": {"is_enabled": false}, "spend": {"used": {"amount_minor": 0, "exponent": 2}},
  "limits": [{"kind": "weekly_all", "percent": 40, "is_active": true, "scope": null}]}`

func TestRunPayloadWithoutAccountMetersRendersNoAccountRow(t *testing.T) {
	// No renderable meter, fresh or stale, and no stdin windows: no account
	// row — never the identity alone, never an age marker on nothing.
	for _, stale := range []bool{false, true} {
		d := e2eDeps(t, "full.json")
		d.stdin = strings.NewReader(e2eStdinNoRateLimits)
		if stale {
			d.fetchUsage = func(string) ([]byte, error) { return nil, errors.New("network down") }
			seedStaleUsage(t, d, e2eUnscopedLimitsOnlyPayload)
		} else {
			d.fetchUsage = func(string) ([]byte, error) { return []byte(e2eUnscopedLimitsOnlyPayload), nil }
		}

		out, _ := run(d)
		if strings.Contains(out, "account") {
			t.Errorf("stale=%v: no account row may render without a meter: %q", stale, out)
		}
	}
}

var ansiSeq = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func TestRunLiveStdinResetTimesBelongToTheirOwnWindows(t *testing.T) {
	// Fixed clock: Monday 2026-11-02 12:00 UTC. The five-hour window resets
	// the same day at 15:00 ("3:00p"); the weekly window resets Thursday at
	// 22:00 ("Thu 10:00p"). Each live meter shows its own window's reset.
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	five := time.Date(2026, 11, 2, 15, 0, 0, 0, time.UTC).Unix()
	week := time.Date(2026, 11, 5, 22, 0, 0, 0, time.UTC).Unix()
	stdin := fmt.Sprintf(`{"session_id":"0a1b2c3d-0000-4000-8000-000000000000",`+
		`"model":{"display_name":"Fable 5"},`+
		`"workspace":{"current_dir":"/Users/dev/projects/demo-app"},`+
		`"rate_limits":{"five_hour":{"used_percentage":42,"resets_at":%d},`+
		`"seven_day":{"used_percentage":77,"resets_at":%d}}}`, five, week)
	for _, c := range []struct {
		name  string
		setup func(t *testing.T, d *deps)
	}{
		{"fresh payload with both windows absent", func(_ *testing.T, d *deps) {
			d.fetchUsage = func(string) ([]byte, error) { return []byte(e2eLimitsOnlyPayload), nil }
		}},
		{"stale payload replaced by stdin", func(t *testing.T, d *deps) {
			d.fetchUsage = func(string) ([]byte, error) { return nil, errors.New("network down") }
			seedStaleUsage(t, *d, e2eUsagePayload)
			// Six minutes old on the render's clock, not the wall clock.
			old := now.Add(-6 * time.Minute)
			ageUsage(t, *d, old)
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			d := e2eDeps(t, "full.json")
			d.stdin = strings.NewReader(stdin)
			d.now = func() time.Time { return now }
			c.setup(t, &d)

			out, _ := run(d)
			plain := ansiSeq.ReplaceAllString(out, "")
			for _, want := range []string{"5h 42% (resets 3:00p)", "week 77% (resets Thu 10:00p)"} {
				if !strings.Contains(plain, want) {
					t.Errorf("want %q in the account row: %q", want, plain)
				}
			}
		})
	}
}

// e2eStdinQuota is a stdin payload without a product whose quota yields
// live 5h 42%, week 77% and a scoped "3p-5h 25%" meter.
const e2eStdinQuota = `{"session_id":"0a1b2c3d-0000-4000-8000-000000000000",` +
	`"model":{"display_name":"Fable 5"},` +
	`"workspace":{"current_dir":"/Users/dev/projects/demo-app"},` +
	`"quota":{"gemini-5h":{"remaining_fraction":0.58},` +
	`"gemini-weekly":{"remaining_fraction":0.23},"3p-5h":{"remaining_fraction":0.75}}}`

// e2eGlobalsOnlyPayload carries both windows and no scoped limit.
const e2eGlobalsOnlyPayload = `{"five_hour": {"utilization": 5.9}, "seven_day": {"utilization": 13},
  "extra_usage": {"is_enabled": false}, "spend": {"used": {"amount_minor": 0, "exponent": 2}},
  "limits": []}`

func TestRunLiveStdinScopedMetersAreNotStale(t *testing.T) {
	// Live stdin scoped meters merged over a stale endpoint payload never
	// count as stale data; the endpoint's own scoped limits still do.
	for _, c := range []struct {
		name, payload string
		wantScoped    string // stale endpoint scoped meter that must stay; "" = none
		wantMarker    bool
	}{
		{"endpoint without scoped limits", e2eGlobalsOnlyPayload, "", false},
		{"endpoint with a scoped limit", e2eLimitsOnlyPayload, "Fable 40%", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			d := e2eDeps(t, "full.json")
			d.stdin = strings.NewReader(e2eStdinQuota)
			d.fetchUsage = func(string) ([]byte, error) { return nil, errors.New("network down") }
			seedStaleUsage(t, d, c.payload)

			out, _ := run(d)
			for _, want := range []string{"5h 42%", "week 77%", "3p-5h 25%"} {
				if !strings.Contains(out, want) {
					t.Errorf("live stdin meter %q must render: %q", want, out)
				}
			}
			if c.wantScoped != "" && !strings.Contains(out, c.wantScoped) {
				t.Errorf("stale endpoint scoped meter %q must stay visible: %q", c.wantScoped, out)
			}
			if got := strings.Contains(out, "(data 6m old)"); got != c.wantMarker {
				t.Errorf("age marker present = %v, want %v: %q", got, c.wantMarker, out)
			}
		})
	}
}

func TestRunStdinWithoutWeeklyKeepsTheStaleWeeklyReading(t *testing.T) {
	// Stale cache with a real weekly reading (13%) + failing fetch + stdin
	// carrying only the five-hour window: the live 5h reading replaces the
	// stale one, and the stale weekly reading stays — never "week 0%".
	d := e2eDeps(t, "full.json")
	d.stdin = strings.NewReader(e2eStdinFiveHourOnly)
	d.fetchUsage = func(string) ([]byte, error) { return nil, errors.New("network down") }
	seedStaleUsage(t, d, e2eUsagePayload)

	out, _ := run(d)
	if !strings.Contains(out, "5h 42%") || !strings.Contains(out, "week 13%") {
		t.Errorf("live 5h and the stale weekly reading must both render: %q", out)
	}
	if strings.Contains(out, "week 0%") {
		t.Errorf("stdin without a weekly window must not overwrite the stale reading with 0%%: %q", out)
	}
	if !strings.Contains(out, "(data 6m old)") {
		t.Errorf("the stale weekly reading must carry the age marker: %q", out)
	}
}

func TestRunAccountRowUsesTheConfigDirRecord(t *testing.T) {
	// A second account run through CLAUDE_CONFIG_DIR keeps its record in
	// that directory; the row must name that account, not the default one.
	d := e2eDeps(t, "full.json")
	base := d.getenv
	d.getenv = func(k string) string {
		if k == "CLAUDE_CONFIG_DIR" {
			return "/Users/dev/second"
		}
		return base(k)
	}
	d.readFile = func(p string) ([]byte, error) {
		switch p {
		case "/Users/dev/.claude.json":
			return []byte(`{"oauthAccount":{"emailAddress":"dev@example.com"}}`), nil
		case "/Users/dev/second/.claude.json":
			return []byte(`{"oauthAccount":{"emailAddress":"second@example.com"}}`), nil
		}
		return nil, os.ErrNotExist
	}

	out, errOut := run(d)
	if !strings.Contains(out, "second@example.com") || strings.Contains(out, "dev@example.com") {
		t.Errorf("account row must name the CLAUDE_CONFIG_DIR account only: %q", out)
	}
	if errOut != "" {
		t.Errorf("unexpected stderr: %q", errOut)
	}
}

func TestRunLoginStatusIsPerCredentialSource(t *testing.T) {
	// Two sessions share one cache dir but resolve different credential
	// stores (CLAUDE_CONFIG_DIR A without a login, B with one): each gets
	// its own answer, in either order.
	t.Setenv("HOME", t.TempDir())
	stdin, err := os.ReadFile(filepath.Join("testdata", "full.json"))
	if err != nil {
		t.Fatal(err)
	}
	session := func(d *deps, source string, loggedIn bool) string {
		d.stdin = bytes.NewReader(stdin)
		d.credentialSource = func() string { return source }
		d.keychainOK = func() error {
			if loggedIn {
				return nil
			}
			return errors.New("no credential store resolved")
		}
		out, errOut := run(*d)
		if errOut != "" {
			t.Fatalf("unexpected stderr (config fallback would hide the test cache dir): %q", errOut)
		}
		return out
	}
	subBadge := "\x1b[32mSub\x1b[m"

	d := e2eDeps(t, "full.json")
	if out := session(&d, "config-dir-A", false); strings.Contains(out, subBadge) {
		t.Fatalf("A has no login but rendered Sub: %q", out)
	}
	if out := session(&d, "config-dir-B", true); !strings.Contains(out, subBadge) {
		t.Errorf("B took A's negative answer: %q", out)
	}

	d = e2eDeps(t, "full.json")
	if out := session(&d, "config-dir-A", true); !strings.Contains(out, subBadge) {
		t.Fatalf("A has a login but rendered no Sub: %q", out)
	}
	if out := session(&d, "config-dir-B", false); strings.Contains(out, subBadge) {
		t.Errorf("B took A's positive answer: %q", out)
	}
}

// asAccount makes d's account record and token belong to the account
// named by uuid and fingerprint.
func asAccount(d *deps, uuid, fingerprint, email string) {
	d.readFile = func(p string) ([]byte, error) {
		if p != "/Users/dev/.claude.json" {
			return nil, fmt.Errorf("unexpected readFile path %q", p)
		}
		return []byte(`{"oauthAccount":{"accountUuid":"` + uuid + `","emailAddress":"` + email + `"}}`), nil
	}
	d.tokenFingerprint = func() (string, error) { return fingerprint, nil }
}

func TestRunFetchCarriesTheRendersTokenFingerprint(t *testing.T) {
	// The request must be bound to the token the render's cache entry is
	// keyed by.
	t.Setenv("HOME", t.TempDir())
	d := e2eDeps(t, "full.json")
	var got string
	fetch := d.fetchUsage
	d.fetchUsage = func(fp string) ([]byte, error) { got = fp; return fetch(fp) }
	if _, errOut := run(d); errOut != "" {
		t.Fatalf("unexpected stderr: %q", errOut)
	}
	if got != e2eFingerprint {
		t.Errorf("fetch carried fingerprint %q, want the render's %q", got, e2eFingerprint)
	}
}

func TestRunUsageIsPerAccount(t *testing.T) {
	// /login from A to B in the same cache: B never shows A's meters, and A
	// still has its own afterwards.
	t.Setenv("HOME", t.TempDir())
	render := func(d deps) string {
		d.stdin = strings.NewReader(e2eStdinNoRateLimits)
		out, errOut := run(d)
		if errOut != "" {
			t.Fatalf("unexpected stderr: %q", errOut)
		}
		return out
	}
	aMeters := "week 13%"
	d := e2eDeps(t, "full.json")
	asAccount(&d, "11111111-aaaa-4000-8000-000000000001", "aaaaaaaaaaaaaaaa", "a@example.com")
	if out := render(d); !strings.Contains(out, aMeters) {
		t.Fatalf("A's own payload not rendered: %q", out)
	}
	asAccount(&d, "22222222-bbbb-4000-8000-000000000002", "bbbbbbbbbbbbbbbb", "b@example.com")
	d.fetchUsage = func(string) ([]byte, error) { return nil, errors.New("network down") }
	if out := render(d); strings.Contains(out, aMeters) || strings.Contains(out, "a@example.com") {
		t.Errorf("B rendered A's usage: %q", out)
	}
	asAccount(&d, "11111111-aaaa-4000-8000-000000000001", "aaaaaaaaaaaaaaaa", "a@example.com")
	if out := render(d); !strings.Contains(out, aMeters) {
		t.Errorf("A lost its own payload after B: %q", out)
	}
}

func TestRunLoginDuringTheFetchRendersNoEndpointData(t *testing.T) {
	// The record changes to another account while the request is out: the
	// response belongs to neither for certain, so it is not shown or kept.
	t.Setenv("HOME", t.TempDir())
	d := e2eDeps(t, "full.json")
	d.stdin = strings.NewReader(e2eStdinNoRateLimits)
	// run takes deps by value, so the switch goes through state the
	// callbacks read rather than by replacing them.
	var a, b deps
	asAccount(&a, "11111111-aaaa-4000-8000-000000000001", "aaaaaaaaaaaaaaaa", "a@example.com")
	asAccount(&b, "22222222-bbbb-4000-8000-000000000002", "bbbbbbbbbbbbbbbb", "b@example.com")
	who := &a
	d.readFile = func(p string) ([]byte, error) { return who.readFile(p) }
	d.tokenFingerprint = func() (string, error) { return who.tokenFingerprint() }
	fetch := d.fetchUsage
	d.fetchUsage = func(fp string) ([]byte, error) {
		who = &b // /login completes while the request is out
		return fetch(fp)
	}
	if out, _ := run(d); strings.Contains(out, "week 13%") {
		t.Errorf("a response fetched across a /login was rendered: %q", out)
	}
	if files, _ := filepath.Glob(filepath.Join(e2eCacheDir(t, d), "usage-*")); len(files) != 0 {
		t.Errorf("a response fetched across a /login was cached: %v", files)
	}
}

func TestRunTokenSessionUsesStdinOnly(t *testing.T) {
	// CLAUDE_CODE_OAUTH_TOKEN: the saved login does not describe this
	// session's token, so neither the endpoint nor the saved email is used.
	t.Setenv("HOME", t.TempDir())
	d := e2eDeps(t, "full.json")
	getenv := d.getenv
	d.getenv = func(k string) string {
		if k == "CLAUDE_CODE_OAUTH_TOKEN" {
			return "fixture-oauth-token"
		}
		return getenv(k)
	}
	d.fetchUsage = func(string) ([]byte, error) {
		t.Fatal("token session must not fetch")
		return nil, nil
	}
	d.readFile = func(p string) ([]byte, error) {
		t.Fatalf("token session read the saved login: %s", p)
		return nil, nil
	}
	out, errOut := run(d)
	if errOut != "" {
		t.Fatalf("unexpected stderr: %q", errOut)
	}
	if !strings.Contains(out, "account") || !strings.Contains(out, "5h 42%") {
		t.Errorf("token session must show the stdin windows: %q", out)
	}
	if strings.Contains(out, "dev@example.com") {
		t.Errorf("token session showed the saved email: %q", out)
	}
}

func TestRunTokenSessionWithoutASavedLoginShowsStdinWindows(t *testing.T) {
	// A machine that authenticates only with CLAUDE_CODE_OAUTH_TOKEN has no
	// saved login, so the login check says "?"; the session's own windows
	// still render.
	t.Setenv("HOME", t.TempDir())
	d := e2eDeps(t, "full.json")
	getenv := d.getenv
	d.getenv = func(k string) string {
		if k == "CLAUDE_CODE_OAUTH_TOKEN" {
			return "fixture-oauth-token"
		}
		return getenv(k)
	}
	d.keychainOK = func() error { return errors.New("no saved login") }
	d.fetchUsage = func(string) ([]byte, error) {
		t.Fatal("token session must not fetch")
		return nil, nil
	}
	out, errOut := run(d)
	if errOut != "" {
		t.Fatalf("unexpected stderr: %q", errOut)
	}
	if !strings.Contains(out, "account") || !strings.Contains(out, "5h 42%") {
		t.Errorf("token session without a saved login must show its stdin windows: %q", out)
	}
}

func TestRunOverriddenCacheDirSharesTheFetchGate(t *testing.T) {
	// A session with its own [cache] dir still waits on the gate every
	// session shares: once a render has fetched, a render with another
	// [cache] dir does not fetch before the gate opens, and shows the
	// session's own numbers.
	first := e2eDeps(t, "full.json")
	firstFetches := 0
	first.fetchUsage = func(string) ([]byte, error) { firstFetches++; return []byte(e2eUsagePayload), nil }
	if _, errOut := run(first); errOut != "" {
		t.Fatalf("unexpected stderr: %q", errOut)
	}
	if firstFetches != 1 {
		t.Fatalf("the first render fetched %d times through an open gate, want 1", firstFetches)
	}

	other := e2eDeps(t, "full.json")
	other.userCacheDir = first.userCacheDir
	otherFetches := 0
	other.fetchUsage = func(string) ([]byte, error) { otherFetches++; return []byte(e2eUsagePayload), nil }
	out, errOut := run(other)
	if errOut != "" {
		t.Fatalf("unexpected stderr: %q", errOut)
	}
	if otherFetches != 0 {
		t.Errorf("a render with another [cache] dir fetched %d times while the shared gate was shut", otherFetches)
	}
	if !strings.Contains(out, "5h 42%") || !strings.Contains(out, "week 77%") {
		t.Errorf("the session's own numbers must render: %q", out)
	}
	for _, name := range []string{"usage.next-attempt", "usage.lock"} {
		if _, err := os.Lstat(filepath.Join(e2eCacheDir(t, other), name)); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s in the configured [cache] dir: %v", name, err)
		}
	}
}

func TestRunGateDirThatCannotBeCreatedFetchesNothing(t *testing.T) {
	// The per-user cache base resolves, but this program's directory under
	// it cannot be created (a file sits at the base): no fetch, and the
	// session's own numbers still render.
	d := e2eDeps(t, "full.json")
	base := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(base, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	d.userCacheDir = func() (string, error) { return base, nil }
	fetches := 0
	d.fetchUsage = func(string) ([]byte, error) { fetches++; return []byte(e2eUsagePayload), nil }
	out, errOut := run(d)
	if errOut != "" {
		t.Fatalf("unexpected stderr: %q", errOut)
	}
	if fetches != 0 {
		t.Errorf("fetched %d times without the gate directory", fetches)
	}
	if !strings.Contains(out, "5h 42%") || !strings.Contains(out, "week 77%") {
		t.Errorf("the session's own numbers must render: %q", out)
	}
}

// e2eExtraSpendPayload has extra usage enabled with $12.34 spent, over the
// default $5.00 budget, and both windows with no scoped limit.
const e2eExtraSpendPayload = `{"five_hour": {"utilization": 5.9}, "seven_day": {"utilization": 13},
  "extra_usage": {"is_enabled": true}, "spend": {"used": {"amount_minor": 1234, "exponent": 2}},
  "limits": []}`

func TestRunStaleExtraUsageCarriesItsAge(t *testing.T) {
	// The extra-usage badge on the model row comes only from the endpoint
	// payload. Served stale, it carries its own age: with the account row
	// hidden, and with every account meter live (no account-row marker).
	for _, c := range []struct {
		name, stdin, config string
		wantAccountMarker   bool
	}{
		{"account row hidden", e2eStdinNoRateLimits, "[rows]\naccount = false\n", false},
		{"account meters live", "", "", false}, // full.json: both windows live
	} {
		t.Run(c.name, func(t *testing.T) {
			d := e2eDeps(t, "full.json")
			if c.config != "" {
				appendConfig(t, d, c.config)
			}
			d.fetchUsage = func(string) ([]byte, error) { return nil, errors.New("network down") }
			seedStaleUsage(t, d, e2eExtraSpendPayload)
			if c.stdin != "" {
				d.stdin = strings.NewReader(c.stdin)
			}

			out, errOut := run(d)
			if errOut != "" {
				t.Fatalf("config must parse cleanly: %q", errOut)
			}
			model, _, _ := strings.Cut(out, "\n")
			if want := "⚠ EXTRA USAGE $12.34 \x1b[m \x1b[2m(6m old)\x1b[m"; !strings.Contains(model, want) {
				t.Errorf("stale extra usage must carry its age on the model row:\n got %q\nwant %q", model, want)
			}
			if got := strings.Contains(out, "(data 6m old)"); got != c.wantAccountMarker {
				t.Errorf("account-row marker present = %v, want %v: %q", got, c.wantAccountMarker, out)
			}
		})
	}
}

// accountRow is the account row of a rendered frame, ANSI removed; "" when
// the frame has none.
func accountRow(out string) string {
	for _, line := range strings.Split(out, "\n") {
		plain := regexp.MustCompile(`\x1b\[[0-9;]*m`).ReplaceAllString(line, "")
		if strings.HasPrefix(plain, "account") {
			return plain
		}
	}
	return ""
}

// stdinWith is a stdin payload carrying the given rate-limit or quota JSON.
func stdinWith(limits string) string {
	return `{"session_id":"0a1b2c3d-0000-4000-8000-000000000000","model":{"display_name":"Fable 5"},` +
		`"workspace":{"current_dir":"/Users/dev/projects/demo-app"},` + limits + `}`
}

func TestRunStdinWindowsRenderOnlyWhatIsPresent(t *testing.T) {
	// No endpoint data: the row shows exactly the windows stdin reports. An
	// absent window is omitted, never "0%", and either window alone renders.
	for _, c := range []struct {
		name, limits, product string
		meter                 string // the one meter the row must show
	}{
		{"five-hour only", `"rate_limits":{"five_hour":{"used_percentage":42}}`, "", "5h 42%"},
		{"weekly only", `"rate_limits":{"seven_day":{"used_percentage":77}}`, "", "week 77%"},
		{"null five-hour, weekly", `"rate_limits":{"five_hour":null,"seven_day":{"used_percentage":77}}`, "", "week 77%"},
		{"antigravity five-hour bucket only", `"product":"antigravity","quota":{"gemini-5h":{"remaining_fraction":0.95}}`, "antigravity", "5h 5%"},
		{"antigravity weekly bucket only", `"product":"antigravity","quota":{"gemini-weekly":{"remaining_fraction":0.25}}`, "antigravity", "week 75%"},
	} {
		t.Run(c.name, func(t *testing.T) {
			d := e2eDeps(t, "full.json")
			getenv := d.getenv
			d.getenv = func(k string) string {
				if k == "CLAUDE_CODE_OAUTH_TOKEN" && c.product == "" {
					return "fixture-oauth-token" // stdin windows only, no endpoint
				}
				return getenv(k)
			}
			d.fetchUsage = func(string) ([]byte, error) {
				t.Fatal("no endpoint fetch expected")
				return nil, nil
			}
			d.stdin = strings.NewReader(stdinWith(c.limits))
			out, errOut := run(d)
			if errOut != "" {
				t.Fatalf("unexpected stderr: %q", errOut)
			}
			if got := accountRow(out); !strings.HasSuffix(got, c.meter) || strings.Count(got, "%") != 1 {
				t.Errorf("account row = %q, want the one meter %q", got, c.meter)
			}
		})
	}
}

func TestRunWeeklyOnlyStdinKeepsTheStaleFiveHourReading(t *testing.T) {
	// A stale payload with a five-hour reading (5%) and stdin carrying only
	// the weekly window: the live weekly reading replaces the stale one, the
	// stale five-hour reading stays with the age marker, and nothing reads
	// "5h 0%".
	d := e2eDeps(t, "full.json")
	d.fetchUsage = func(string) ([]byte, error) { return nil, errors.New("network down") }
	seedStaleUsage(t, d, e2eUsagePayload)
	d.stdin = strings.NewReader(stdinWith(`"rate_limits":{"seven_day":{"used_percentage":77}}`))
	out, errOut := run(d)
	if errOut != "" {
		t.Fatalf("unexpected stderr: %q", errOut)
	}
	if got, want := accountRow(out), "account  dev@example.com · 5h 5% · week 77% · (data 6m old)"; got != want {
		t.Errorf("account row:\n got %q\nwant %q", got, want)
	}
}

func TestRunWeeklyOnlyStdinOverAFreshPayloadWithoutFiveHour(t *testing.T) {
	// A fresh payload reporting no five-hour window in progress, and stdin
	// carrying only the weekly window: no five-hour meter appears.
	d := e2eDeps(t, "full.json")
	d.fetchUsage = func(string) ([]byte, error) {
		return []byte(`{"five_hour": null, "seven_day": {"utilization": 13},
  "extra_usage": {"is_enabled": false}, "spend": {"used": {"amount_minor": 0, "exponent": 2}}, "limits": []}`), nil
	}
	d.stdin = strings.NewReader(stdinWith(`"rate_limits":{"seven_day":{"used_percentage":77}}`))
	out, errOut := run(d)
	if errOut != "" {
		t.Fatalf("unexpected stderr: %q", errOut)
	}
	if got, want := accountRow(out), "account  dev@example.com · week 13%"; got != want {
		t.Errorf("account row:\n got %q\nwant %q", got, want)
	}
}

func TestRunAntigravityShowsAnotherFamilysQuotaOnlyUnderItsName(t *testing.T) {
	// A Claude model in Antigravity draws from the 3p quota. With only
	// Gemini buckets in the quota, the Gemini five-hour bucket shows under
	// its own name, never as this session's "5h", and a Gemini weekly
	// bucket does not show at all.
	for _, c := range []struct {
		name, quota string
		want        string // the account row's meters, "" for no account row
	}{
		{"gemini five-hour only", `"gemini-5h":{"remaining_fraction":0.9}`, "gemini-5h 10%"},
		{"gemini weekly only", `"gemini-weekly":{"remaining_fraction":0.9}`, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			d := e2eDeps(t, "full.json")
			d.fetchUsage = func(string) ([]byte, error) {
				t.Fatal("an Antigravity session does not fetch the endpoint")
				return nil, nil
			}
			d.stdin = strings.NewReader(`{"session_id":"0a1b2c3d-0000-4000-8000-000000000000","product":"antigravity",` +
				`"model":{"display_name":"Claude Opus 5.5"},"workspace":{"current_dir":"/Users/dev/projects/demo-app"},` +
				`"quota":{` + c.quota + `}}`)
			out, errOut := run(d)
			if errOut != "" {
				t.Fatalf("unexpected stderr: %q", errOut)
			}
			row := accountRow(out)
			if c.want == "" {
				if row != "" {
					t.Errorf("want no account row, got %q", row)
				}
				return
			}
			if !strings.HasSuffix(row, c.want) || strings.Count(row, "%") != 1 {
				t.Errorf("account row = %q, want the one meter %q", row, c.want)
			}
		})
	}
}
