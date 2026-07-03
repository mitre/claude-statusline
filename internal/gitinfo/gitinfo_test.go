package gitinfo

import (
	"crypto/md5"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const cwd = "/Users/dev/github/mitre/ts-inspec-profile-parser"

func runnerFor(m map[string]string) RunGit {
	return func(_ string, args ...string) (string, error) {
		key := fmt.Sprintf("%v", args)
		out, ok := m[key]
		if !ok {
			return "", errors.New("git failed")
		}
		return out, nil
	}
}

func TestBranchAndDirty(t *testing.T) {
	dir := t.TempDir()
	run := runnerFor(map[string]string{
		"[branch --show-current]":                  "main\n",
		"[--no-optional-locks status --porcelain]": "?? SESSION-KICKOFF.md\n?? docs/\n",
	})
	branch, dirty := Get(dir, cwd, run)
	if branch != "main" || dirty != 2 {
		t.Errorf("Get = %q, %d; want main, 2", branch, dirty)
	}
}

func TestDetachedHeadShowsShortSHA(t *testing.T) {
	dir := t.TempDir()
	run := runnerFor(map[string]string{
		"[branch --show-current]":                  "\n",
		"[rev-parse --short HEAD]":                 "abc1234\n",
		"[--no-optional-locks status --porcelain]": "",
	})
	branch, _ := Get(dir, cwd, run)
	if branch != "@abc1234" {
		t.Errorf("branch = %q, want @abc1234", branch)
	}
}

func TestGitFailureFallsBack(t *testing.T) {
	dir := t.TempDir()
	branch, dirty := Get(dir, cwd, runnerFor(nil))
	if branch != "?" || dirty != 0 {
		t.Errorf("Get on failure = %q, %d; want \"?\", 0", branch, dirty)
	}
}

func TestCleanTreeDirtyZero(t *testing.T) {
	dir := t.TempDir()
	run := runnerFor(map[string]string{
		"[branch --show-current]":                  "main\n",
		"[--no-optional-locks status --porcelain]": "",
	})
	_, dirty := Get(dir, cwd, run)
	if dirty != 0 {
		t.Errorf("dirty = %d, want 0 for clean tree", dirty)
	}
}

func TestStaleCachesServedWhenGitFails(t *testing.T) {
	dir := t.TempDir()
	key := fmt.Sprintf("%x", md5.Sum([]byte(cwd)))
	stale := filepath.Join(dir, "branch-"+key)
	if err := os.WriteFile(stale, []byte("feature-x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "dirty-"+key), []byte("4"), 0o600); err != nil {
		t.Fatal(err)
	}

	branch, dirty := Get(dir, cwd, runnerFor(nil)) // every git call fails
	if branch != "feature-x" || dirty != 4 {
		t.Errorf("Get(git down, stale caches present) = %q, %d; want \"feature-x\", 4", branch, dirty)
	}
}

func TestStatusUsesNoOptionalLocks(t *testing.T) {
	// Plain `git status` takes the optional index lock to refresh the stat
	// cache, so a ~300ms render cadence races the user's interactive git
	// for .git/index.lock (observed live 2026-07-03: a commit failed while
	// our own installed binary held the lock). --no-optional-locks is git's
	// documented mechanism for background tooling.
	dir := t.TempDir()
	var statusArgs string
	run := func(_ string, args ...string) (string, error) {
		joined := fmt.Sprintf("%v", args)
		if strings.Contains(joined, "status") {
			statusArgs = joined
			return "?? x\n", nil
		}
		return "main\n", nil
	}
	Get(dir, cwd, run)
	if statusArgs != "[--no-optional-locks status --porcelain]" {
		t.Errorf("status args = %s; want [--no-optional-locks status --porcelain]", statusArgs)
	}
}

func TestCacheFilesUseBashCompatibleMD5Keys(t *testing.T) {
	// Drop-in requirement: share cache files with the bash reference, which
	// keys by `md5 -qs "$CWD"`.
	dir := t.TempDir()
	run := runnerFor(map[string]string{
		"[branch --show-current]":                  "main\n",
		"[--no-optional-locks status --porcelain]": "?? x\n",
	})
	Get(dir, cwd, run)
	key := fmt.Sprintf("%x", md5.Sum([]byte(cwd)))
	if _, err := os.Stat(filepath.Join(dir, "branch-"+key)); err != nil {
		t.Errorf("branch cache not at bash-compatible key: %v", err)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "dirty-"+key)); err != nil || string(b) != "1" {
		t.Errorf("dirty cache = %q, %v; want \"1\"", b, err)
	}
}
