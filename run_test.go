package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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

func e2eDeps(t *testing.T, stdinFixture string) deps {
	t.Helper()
	cacheDir := t.TempDir()
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
		fetchUsage: func() ([]byte, error) { return []byte(e2eUsagePayload), nil },
		readFile: func(p string) ([]byte, error) {
			if want := "/Users/dev/.claude.json"; p != want {
				return nil, fmt.Errorf("unexpected readFile path %q, want %q", p, want)
			}
			return []byte(`{"oauthAccount":{"emailAddress":"dev@example.com"}}`), nil
		},
	}
}

func TestRunEndToEndGolden(t *testing.T) {
	out, errOut := run(e2eDeps(t, "full.json"))
	want := strings.Join([]string{
		"\x1b[2mmodel    \x1b[m\x1b[1;36mFable 5 1M\x1b[m\x1b[2m · \x1b[m\x1b[32mSub\x1b[m\x1b[2m · session 0a1b2c3d\x1b[m",
		"\x1b[2mproject  \x1b[m\x1b[1m~/projects/demo-app\x1b[m \x1b[2m·\x1b[m \x1b[34m⎇ main\x1b[m \x1b[33m~2\x1b[m",
		"\x1b[2mcontext  \x1b[m\x1b[32m▓▓▓░░░░░░░\x1b[m \x1b[32m30%\x1b[m",
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
	if out, _ := run(seeded); !strings.Contains(out, "gone-stale") || !strings.Contains(out, "~7") {
		t.Fatalf("cache seeding render failed: %q", out)
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
	out, _ := run(blocked)
	elapsed := time.Since(start)

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

	out, _ := run(d)
	if !strings.Contains(out, "⎇ main") || !strings.Contains(out, "~1") {
		t.Errorf("git_timeout_ms=0 must disable the deadline (live ~1 expected): %q", out)
	}
}

func TestRunFetchFailureCollapsesLimitsRow(t *testing.T) {
	d := e2eDeps(t, "full.json")
	d.fetchUsage = func() ([]byte, error) { return nil, errors.New("rate limited") }
	out, _ := run(d)
	if strings.Contains(out, "account") {
		t.Errorf("account row rendered without data (fabricated zeros?): %q", out)
	}
}
