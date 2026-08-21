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
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"os"
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

	// LockBadge enables the index.lock age check (one os.Stat, both
	// engines); LockBadgeAfter is the strictly-greater-than age threshold
	// below which a present lock stays silent — a lock is often legitimate
	// (any index write; an editor-open commit holds it for minutes).
	LockBadge      bool
	LockBadgeAfter time.Duration

	// inproc is the in-process reader seam for tests; nil means go-git.
	inproc func(cwd string) (string, int, error)
	// now is the clock seam for tests; nil means time.Now.
	now func() time.Time
}

// Get returns the branch label ("?" when unknown, "@<sha>" when detached),
// the dirty-file count, and — when enabled and strictly past the threshold —
// the age of a held .git/index.lock, maintaining the per-directory caches.
func Get(o Options, cwd string) (string, int, time.Duration) {
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
	var branch string
	var dirty int
	if engine == "cli" {
		branch, dirty = cliGet(o.Run, cwd, branchPath, dirtyPath)
	} else {
		branch, dirty = inprocGet(o, cwd, branchPath, dirtyPath, markerPath)
	}
	return branch, dirty, lockAge(o, cwd)
}

// lockAge reports how long .git/index.lock has been held once strictly past
// the threshold; zero otherwise (absent, young, disabled, or non-repo cwd).
// Engine-independent by design: an escalated huge repo is the most likely
// home of a long operation. Pure filesystem — never a subprocess, never a
// lock taken, exactly one os.Stat of index.lock itself.
func lockAge(o Options, cwd string) time.Duration {
	if !o.LockBadge {
		return 0
	}
	gitdir := discoverGitDir(cwd)
	if gitdir == "" {
		return 0
	}
	st, err := os.Stat(filepath.Join(gitdir, "index.lock"))
	if err != nil {
		return 0
	}
	now := time.Now
	if o.now != nil {
		now = o.now
	}
	if age := now().Sub(st.ModTime()); age > o.LockBadgeAfter {
		return age
	}
	return 0
}

// discoverGitDir resolves the git dir governing cwd per
// gitrepository-layout(5): walk ancestors for ".git"; a directory IS the git
// dir, a file names it ("gitdir: <path>", absolute or relative to the file's
// own directory — the linked-worktree and submodule form, whose private git
// dir is where that worktree's index and index.lock live). Empty when cwd is
// outside any repository or the pointer file is malformed.
func discoverGitDir(cwd string) string {
	for dir := cwd; ; dir = filepath.Dir(dir) {
		p := filepath.Join(dir, ".git")
		if st, err := os.Stat(p); err == nil {
			if st.IsDir() {
				return p
			}
			b, err := readPointerFile(p)
			if err != nil {
				return ""
			}
			target, ok := strings.CutPrefix(strings.TrimSpace(string(b)), "gitdir:")
			if !ok {
				return ""
			}
			target = strings.TrimSpace(target)
			if target == "" {
				return ""
			}
			if !filepath.IsAbs(target) {
				target = filepath.Join(dir, target)
			}
			return target
		}
		if filepath.Dir(dir) == dir {
			return ""
		}
	}
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
			if errors.Is(r.err, gogit.ErrRepositoryNotExists) {
				// Genuinely not a repository: degraded values, and no
				// escalation — a non-repo cwd must never fork git per render.
				return staleBranch(branchPath), staleDirty(dirtyPath)
			}
			// A repository go-git cannot handle (unsupported extension such
			// as worktreeConfig, sha256 objectFormat, index features, ...).
			// The CLI is the reference implementation — escalate exactly like
			// the overrun path, but fall back THIS render: the error arrived
			// instantly, so unlike an overrun the budget is still intact.
			_ = cache.Write(markerPath, "1")
			return cliGet(o.Run, cwd, branchPath, dirtyPath)
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
// caller), kept for repos escalated past the in-process budget. A nil runner
// means no CLI is available: serve the same degraded values as a failed run —
// never a panic (reachable via escalation or a stale engine marker).
func cliGet(run RunGit, cwd, branchPath, dirtyPath string) (string, int) {
	if run == nil {
		return staleBranch(branchPath), staleDirty(dirtyPath)
	}
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

// readPointerFile reads a ".git" pointer file, bounded to 8 KiB — one
// "gitdir: <path>" line tops out at PATH_MAX scale, and the workspace path
// is untrusted input: a planted oversized file must not balloon a render.
func readPointerFile(p string) ([]byte, error) {
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	b, err := io.ReadAll(io.LimitReader(f, 8192))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return b, err
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
