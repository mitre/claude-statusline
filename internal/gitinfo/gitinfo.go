// Package gitinfo reports the working tree's branch and dirty-file count,
// caching both per working directory (FNV-1a keyed files under the cache dir).
package gitinfo

import (
	"fmt"
	"hash/fnv"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mitre/claude-statusline/internal/cache"
)

// RunGit executes git in dir and returns stdout. Injected for testing.
type RunGit func(dir string, args ...string) (string, error)

const branchTTL = 5 * time.Second

// Get returns the branch label ("?" when unknown, "@<sha>" when detached)
// and the dirty-file count for cwd, maintaining the shared cache files.
// Unlike the bash reference, the dirty count is computed synchronously —
// no one-render lag — while still writing the shared cache for other readers.
func Get(cacheDir, cwd string, run RunGit) (string, int) {
	h := fnv.New64a()
	_, _ = h.Write([]byte(cwd)) // hash.Hash.Write never returns an error
	key := fmt.Sprintf("%x", h.Sum64())
	branchPath := filepath.Join(cacheDir, "branch-"+key)
	dirtyPath := filepath.Join(cacheDir, "dirty-"+key)

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
	} else if s, sok := cache.ReadStale(dirtyPath); sok {
		if n, cerr := strconv.Atoi(strings.TrimSpace(s)); cerr == nil {
			dirty = n
		}
	}
	return branch, dirty
}

// liveBranch mirrors the reference's git_branch(): `git branch --show-current`
// for the clean name, falling back to "@<short-sha>" when detached.
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

func countLines(out string) int {
	trimmed := strings.TrimRight(out, "\n")
	if trimmed == "" {
		return 0
	}
	return strings.Count(trimmed, "\n") + 1
}
