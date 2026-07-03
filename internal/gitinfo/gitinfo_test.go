package gitinfo

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
)

const cwd = "/Users/dev/projects/demo-app"

// initRepo creates a real on-disk repository via go-git (no git binary).
func initRepo(t *testing.T) (string, *gogit.Worktree) {
	t.Helper()
	dir := t.TempDir()
	repo, err := gogit.PlainInit(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	return dir, wt
}

func commitFile(t *testing.T, wt *gogit.Worktree, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := wt.Add(name); err != nil {
		t.Fatal(err)
	}
	if _, err := wt.Commit("add "+name, &gogit.CommitOptions{
		Author: &object.Signature{Name: "dev", Email: "dev@example.com", When: time.Unix(1700000000, 0)},
	}); err != nil {
		t.Fatal(err)
	}
}

func TestDefaultEngineReadsInProcessWithoutSubprocess(t *testing.T) {
	repoDir, wt := initRepo(t)
	commitFile(t, wt, repoDir, "a.txt", "committed")
	if err := os.WriteFile(filepath.Join(repoDir, "b.txt"), []byte("untracked"), 0o600); err != nil {
		t.Fatal(err)
	}

	fatalRun := func(_ string, _ ...string) (string, error) {
		t.Error("CLI git invoked on the default in-process path")
		return "", errors.New("unreachable")
	}
	branch, dirty := Get(Options{CacheDir: t.TempDir(), Run: fatalRun, Budget: time.Second}, repoDir)
	if branch != "master" || dirty != 1 {
		t.Errorf("in-process Get = %q, %d; want master, 1", branch, dirty)
	}
}

func TestInProcessDirtyCountMatchesPorcelainSemantics(t *testing.T) {
	repoDir, wt := initRepo(t)
	// Committed baseline: the .gitignore itself plus the two files that will
	// be dirtied, so none of them count as untracked noise.
	commitFile(t, wt, repoDir, ".gitignore", "ignored.log\n")
	commitFile(t, wt, repoDir, "modified.txt", "v1")
	commitFile(t, wt, repoDir, "staged.txt", "v1")

	// modified, unstaged
	if err := os.WriteFile(filepath.Join(repoDir, "modified.txt"), []byte("v2"), 0o600); err != nil {
		t.Fatal(err)
	}
	// staged
	if err := os.WriteFile(filepath.Join(repoDir, "staged.txt"), []byte("v2"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := wt.Add("staged.txt"); err != nil {
		t.Fatal(err)
	}
	// untracked
	if err := os.WriteFile(filepath.Join(repoDir, "untracked.txt"), []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	// gitignored — must NOT count
	if err := os.WriteFile(filepath.Join(repoDir, "ignored.log"), []byte("noise"), 0o600); err != nil {
		t.Fatal(err)
	}

	fatalRun := func(_ string, _ ...string) (string, error) {
		t.Error("CLI git invoked on the in-process parity test")
		return "", errors.New("unreachable")
	}
	branch, dirty := Get(Options{CacheDir: t.TempDir(), Run: fatalRun, Budget: time.Second}, repoDir)
	// `git status --porcelain` prints exactly three lines here:
	//   " M modified.txt", "M  staged.txt", "?? untracked.txt"
	// — ignored.log is excluded by the committed .gitignore.
	if branch != "master" || dirty != 3 {
		t.Errorf("in-process parity Get = %q, %d; want master, 3", branch, dirty)
	}
}

const cwdKey = "e6531ce5fc09ac4f" // fnv1a-64 of the cwd fixture

func TestOverrunWritesMarkerAndEscalatesNextRender(t *testing.T) {
	cacheDir := t.TempDir()
	slow := func(string) (string, int, error) {
		time.Sleep(300 * time.Millisecond)
		return "too-slow", 0, nil
	}
	cliCalls := 0
	cli := runnerFor(map[string]string{
		"[branch --show-current]":                  "cli-branch\n",
		"[--no-optional-locks status --porcelain]": "?? f\n",
	})
	counted := func(d string, args ...string) (string, error) {
		cliCalls++
		return cli(d, args...)
	}
	opts := Options{CacheDir: cacheDir, Run: counted, Budget: 30 * time.Millisecond, inproc: slow}

	// Render 1: the budget is already burned — no CLI call THIS render,
	// marker written, degraded values served (no stale caches seeded).
	branch, dirty := Get(opts, cwd)
	if cliCalls != 0 {
		t.Errorf("CLI invoked %d times on the overrun render itself", cliCalls)
	}
	if branch != "?" || dirty != 0 {
		t.Errorf("overrun render = %q, %d; want degraded \"?\", 0", branch, dirty)
	}
	if _, err := os.Stat(filepath.Join(cacheDir, "engine-cli-"+cwdKey)); err != nil {
		t.Fatalf("escalation marker not written: %v", err)
	}

	// Render 2: marker fresh — this repo now uses the CLI engine.
	branch, dirty = Get(opts, cwd)
	if cliCalls == 0 || branch != "cli-branch" || dirty != 1 {
		t.Errorf("marked repo render = %q, %d (cli calls %d); want cli-branch, 1 via CLI", branch, dirty, cliCalls)
	}
}

func TestForcedEnginesOverrideAutoSelection(t *testing.T) {
	cacheDir := t.TempDir()
	// Seed a fresh escalation marker: forced "gogit" must ignore it.
	if err := os.WriteFile(filepath.Join(cacheDir, "engine-cli-"+cwdKey), []byte("1"), 0o600); err != nil {
		t.Fatal(err)
	}
	fatalRun := func(_ string, _ ...string) (string, error) {
		t.Error("CLI invoked despite forced gogit engine")
		return "", errors.New("unreachable")
	}
	inproc := func(string) (string, int, error) { return "forced-inproc", 2, nil }
	branch, dirty := Get(Options{CacheDir: cacheDir, Run: fatalRun, Engine: "gogit", Budget: time.Second, inproc: inproc}, cwd)
	if branch != "forced-inproc" || dirty != 2 {
		t.Errorf("forced gogit = %q, %d; want forced-inproc, 2", branch, dirty)
	}

	// Forced "cli": the in-process seam must never run.
	fatalInproc := func(string) (string, int, error) {
		t.Error("in-process engine invoked despite forced cli")
		return "", 0, errors.New("unreachable")
	}
	cli := runnerFor(map[string]string{
		"[branch --show-current]":                  "cli-branch\n",
		"[--no-optional-locks status --porcelain]": "",
	})
	branch, dirty = Get(Options{CacheDir: t.TempDir(), Run: cli, Engine: "cli", inproc: fatalInproc}, cwd)
	if branch != "cli-branch" || dirty != 0 {
		t.Errorf("forced cli = %q, %d; want cli-branch, 0", branch, dirty)
	}
}

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
	branch, dirty := Get(Options{CacheDir: dir, Run: run, Engine: "cli"}, cwd)
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
	branch, _ := Get(Options{CacheDir: dir, Run: run, Engine: "cli"}, cwd)
	if branch != "@abc1234" {
		t.Errorf("branch = %q, want @abc1234", branch)
	}
}

func TestGitFailureFallsBack(t *testing.T) {
	dir := t.TempDir()
	branch, dirty := Get(Options{CacheDir: dir, Run: runnerFor(nil), Engine: "cli"}, cwd)
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
	_, dirty := Get(Options{CacheDir: dir, Run: run, Engine: "cli"}, cwd)
	if dirty != 0 {
		t.Errorf("dirty = %d, want 0 for clean tree", dirty)
	}
}

func TestStaleCachesServedWhenGitFails(t *testing.T) {
	dir := t.TempDir()
	const key = "e6531ce5fc09ac4f" // fnv1a-64 of the cwd fixture
	stale := filepath.Join(dir, "branch-"+key)
	// Byte-exact as Get writes it (Go-only cache dir — no bash-era newlines).
	if err := os.WriteFile(stale, []byte("feature-x"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "dirty-"+key), []byte("4"), 0o600); err != nil {
		t.Fatal(err)
	}

	branch, dirty := Get(Options{CacheDir: dir, Run: runnerFor(nil), Engine: "cli"}, cwd) // every git call fails
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
	Get(Options{CacheDir: dir, Run: run, Engine: "cli"}, cwd)
	if statusArgs != "[--no-optional-locks status --porcelain]" {
		t.Errorf("status args = %s; want [--no-optional-locks status --porcelain]", statusArgs)
	}
}

func TestCacheFilesUseFNVKeys(t *testing.T) {
	// Full-Go contract: cache filenames derive from the FNV-1a 64-bit hex of
	// cwd (stdlib hash/fnv, non-crypto — the md5 bash-interop keys and their
	// gosec exclusions are gone). Literal pinned so derivation drift fails.
	dir := t.TempDir()
	run := runnerFor(map[string]string{
		"[branch --show-current]":                  "main\n",
		"[--no-optional-locks status --porcelain]": "?? x\n",
	})
	Get(Options{CacheDir: dir, Run: run, Engine: "cli"}, cwd)
	const key = "e6531ce5fc09ac4f" // fnv1a-64 of the cwd fixture above
	if _, err := os.Stat(filepath.Join(dir, "branch-"+key)); err != nil {
		t.Errorf("branch cache not at FNV key: %v", err)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "dirty-"+key)); err != nil || string(b) != "1" {
		t.Errorf("dirty cache = %q, %v; want \"1\"", b, err)
	}
}
