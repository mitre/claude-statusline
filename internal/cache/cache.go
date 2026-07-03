// Package cache implements the statusline's tiny TTL file cache.
//
// Writes are atomic (temp file + rename): a bare "> file" truncate-then-write
// let same-render readers observe an empty file — the dirty-badge bug fixed in
// the bash reference on 2026-07-03. Rename makes torn reads impossible.
package cache

import (
	"fmt"
	"os"
	"strings"
	"time"
)

// ReadFresh returns the file content when it exists, is non-empty, and is
// younger than ttl.
func ReadFresh(path string, ttl time.Duration) (string, bool) {
	info, err := os.Stat(path)
	if err != nil || info.Size() == 0 {
		return "", false
	}
	if time.Since(info.ModTime()) >= ttl {
		return "", false
	}
	return ReadStale(path)
}

// ReadStale returns the file content regardless of age (stale-truth fallback
// for the usage endpoint when a live fetch fails). Trailing newlines are
// stripped to match bash command-substitution semantics — the reference
// writes some shared caches with echo, so files may end in "\n".
func ReadStale(path string) (string, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	s := strings.TrimRight(string(b), "\n")
	if s == "" {
		return "", false
	}
	return s, true
}

// Write atomically replaces path's content via temp file + rename.
func Write(path, content string) error {
	tmp := fmt.Sprintf("%s.%d", path, os.Getpid())
	if err := os.WriteFile(tmp, []byte(content), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
