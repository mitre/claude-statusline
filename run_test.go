package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	if err := os.WriteFile(cfgPath, []byte("[cache]\ndir = \""+cacheDir+"\"\n"), 0o600); err != nil {
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
		runGit: func(_ string, args ...string) (string, error) {
			switch fmt.Sprintf("%v", args) {
			case "[branch --show-current]":
				return "main\n", nil
			case "[status --porcelain]":
				return "?? SESSION-KICKOFF.md\n?? docs/\n", nil
			}
			return "", errors.New("unexpected git call")
		},
		keychainOK: func() error { return nil },
		fetchUsage: func() ([]byte, error) { return []byte(e2eUsagePayload), nil },
	}
}

func TestRunEndToEndGolden(t *testing.T) {
	out, errOut := run(e2eDeps(t, "full.json"))
	want := strings.Join([]string{
		"\x1b[2mmodel    \x1b[0m\x1b[1m\x1b[36mFable 5 1M\x1b[0m\x1b[2m · \x1b[0m\x1b[32mSub\x1b[0m\x1b[2m · session 0a1b2c3d\x1b[0m",
		"\x1b[2mproject  \x1b[0m\x1b[1m~/github/mitre/ts-inspec-profile-parser\x1b[0m \x1b[2m·\x1b[0m \x1b[34m⎇ main\x1b[0m \x1b[33m~2\x1b[0m",
		"\x1b[2mcontext  \x1b[0m\x1b[32m▓▓▓░░░░░░░\x1b[0m \x1b[32m30%\x1b[0m",
		"\x1b[2mlimits   \x1b[0m\x1b[32m5h 5%\x1b[0m \x1b[2m·\x1b[0m \x1b[32mweek 13%\x1b[0m",
		"\x1b[2mactivity \x1b[0m\x1b[2m17h23m\x1b[0m \x1b[2m·\x1b[0m \x1b[32m+1,598\x1b[0m/\x1b[31m-8\x1b[0m \x1b[2mlines\x1b[0m",
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

func TestRunFetchFailureCollapsesLimitsRow(t *testing.T) {
	d := e2eDeps(t, "full.json")
	d.fetchUsage = func() ([]byte, error) { return nil, errors.New("rate limited") }
	out, _ := run(d)
	if strings.Contains(out, "limits") {
		t.Errorf("limits row rendered without data (fabricated zeros?): %q", out)
	}
}
