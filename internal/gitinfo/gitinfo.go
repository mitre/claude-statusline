// Package gitinfo reports the working tree's branch and dirty-file count,
// caching both per working directory (FNV-1a keyed files under the cache dir).
//
// Two engines: the default reads in-process via go-git — no git binary
// required on the host. Repos whose in-process status walk blows the render
// budget are escalated per-repo to the git CLI: a one-shot statusline
// process cannot carry an abandoned in-process walk across renders, but the
// git binary's fsmonitor daemon and on-disk caches persist between
// invocations, so the CLI stays fast where go-git cannot.
package gitinfo

import (
	"fmt"
	"hash/fnv"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	gogit "github.com/go-git/go-git/v5"

	"github.com/mitre/claude-statusline/internal/cache"
)

// RunGit executes git in dir and returns stdout. Injected for testing.
type RunGit func(dir string, args ...string) (string, error)

const branchTTL = 5 * time.Second

// markerTTL is how long a repo stays escalated to the CLI engine after an
// in-process overrun. Repo size changes slowly, so a day of stickiness
// amortizes the failed probe; the daily re-probe (one stale render) catches
// repos that shrank or gained fsmonitor since.
const markerTTL = 24 * time.Hour

// Options configure Get.
type Options struct {
	CacheDir string
	Run      RunGit        // CLI runner, already deadline-bounded by the caller
	Engine   string        // "auto" (default), "gogit", "cli"
	Budget   time.Duration // in-process read budget; <= 0 disables the bound

	// inproc is the in-process reader seam for tests; nil means go-git.
	inproc func(cwd string) (string, int, error)
}

// Get returns the branch label ("?" when unknown, "@<sha>" when detached)
// and the dirty-file count for cwd, maintaining the per-directory caches.
func Get(o Options, cwd string) (string, int) {
	h := fnv.New64a()
	_, _ = h.Write([]byte(cwd)) // hash.Hash.Write never returns an error
	key := fmt.Sprintf("%x", h.Sum64())
	branchPath := filepath.Join(o.CacheDir, "branch-"+key)
	dirtyPath := filepath.Join(o.CacheDir, "dirty-"+key)
	markerPath := filepath.Join(o.CacheDir, "engine-cli-"+key)

	engine := o.Engine
	if engine == "" || engine == "auto" {
		if _, ok := cache.ReadFresh(markerPath, markerTTL); ok {
			engine = "cli"
		} else {
			engine = "gogit"
		}
	}
	if engine == "cli" {
		return cliGet(o.Run, cwd, branchPath, dirtyPath)
	}
	return inprocGet(o, cwd, branchPath, dirtyPath, markerPath)
}

// inprocGet reads via the in-process engine under the budget. On overrun it
// writes the escalation marker and serves the stale caches: the walk cannot
// finish across renders (one-shot process), so subsequent renders take the
// CLI path instead of failing the same way forever.
func inprocGet(o Options, cwd, branchPath, dirtyPath, markerPath string) (string, int) {
	read := o.inproc
	if read == nil {
		read = gogitRead
	}
	type result struct {
		branch string
		dirty  int
		err    error
	}
	ch := make(chan result, 1)
	go func() {
		b, d, err := read(cwd)
		ch <- result{b, d, err}
	}()

	var overrun <-chan time.Time
	if o.Budget > 0 {
		timer := time.NewTimer(o.Budget)
		defer timer.Stop()
		overrun = timer.C
	}
	select {
	case r := <-ch:
		if r.err != nil {
			// Not a repository / unreadable: same degraded values as the
			// CLI failure path.
			return staleBranch(branchPath), staleDirty(dirtyPath)
		}
		branch := r.branch
		if branch == "" {
			branch = "?"
		} else {
			_ = cache.Write(branchPath, branch)
		}
		_ = cache.Write(dirtyPath, strconv.Itoa(r.dirty))
		return branch, r.dirty
	case <-overrun:
		_ = cache.Write(markerPath, "1")
		return staleBranch(branchPath), staleDirty(dirtyPath)
	}
}

// gogitRead is the real in-process engine: branch from HEAD, dirty count
// from a worktree status walk (the `git status --porcelain` line count).
func gogitRead(cwd string) (string, int, error) {
	repo, err := gogit.PlainOpenWithOptions(cwd, &gogit.PlainOpenOptions{DetectDotGit: true})
	if err != nil {
		return "", 0, err
	}
	branch := ""
	// A Head error means an unborn HEAD (fresh repo, no commits) — normal
	// state, not a failure: branch stays unknown, status still counts.
	if head, herr := repo.Head(); herr == nil {
		if head.Name().IsBranch() {
			branch = head.Name().Short()
		} else {
			branch = "@" + head.Hash().String()[:7]
		}
	}
	wt, err := repo.Worktree()
	if err != nil {
		return branch, 0, err
	}
	st, err := wt.Status()
	if err != nil {
		return branch, 0, err
	}
	dirty := 0
	for _, fs := range st {
		if fs.Staging != gogit.Unmodified || fs.Worktree != gogit.Unmodified {
			dirty++
		}
	}
	return branch, dirty, nil
}

// cliGet is the subprocess engine (deadline-bounded runner injected by the
// caller), kept for repos escalated past the in-process budget.
func cliGet(run RunGit, cwd, branchPath, dirtyPath string) (string, int) {
	branch, ok := cache.ReadFresh(branchPath, branchTTL)
	if !ok {
		branch = liveBranch(cwd, run)
		if branch != "" {
			_ = cache.Write(branchPath, branch)
		} else if stale, sok := cache.ReadStale(branchPath); sok {
			branch = stale
		} else {
			branch = "?"
		}
	}

	dirty := 0
	// --no-optional-locks: a statusline renders every few hundred ms, and a
	// plain `git status` takes the optional index lock to refresh the stat
	// cache — racing the user's interactive git for .git/index.lock.
	if out, err := run(cwd, "--no-optional-locks", "status", "--porcelain"); err == nil {
		dirty = countLines(out)
		_ = cache.Write(dirtyPath, strconv.Itoa(dirty))
	} else {
		dirty = staleDirty(dirtyPath)
	}
	return branch, dirty
}

// liveBranch mirrors `git branch --show-current`, falling back to
// "@<short-sha>" when detached.
func liveBranch(cwd string, run RunGit) string {
	out, err := run(cwd, "branch", "--show-current")
	if err != nil {
		return ""
	}
	b := strings.TrimSpace(out)
	if b != "" {
		return b
	}
	sha, err := run(cwd, "rev-parse", "--short", "HEAD")
	if err != nil || strings.TrimSpace(sha) == "" {
		return ""
	}
	return "@" + strings.TrimSpace(sha)
}

func staleBranch(branchPath string) string {
	if s, ok := cache.ReadStale(branchPath); ok {
		return s
	}
	return "?"
}

func staleDirty(dirtyPath string) int {
	if s, ok := cache.ReadStale(dirtyPath); ok {
		if n, err := strconv.Atoi(strings.TrimSpace(s)); err == nil {
			return n
		}
	}
	return 0
}

func countLines(out string) int {
	trimmed := strings.TrimRight(out, "\n")
	if trimmed == "" {
		return 0
	}
	return strings.Count(trimmed, "\n") + 1
}
