package cache

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWriteThenReadFresh(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "branch")
	if err := Write(p, "main"); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got, ok := ReadFresh(p, 5*time.Second)
	if !ok || got != "main" {
		t.Errorf("ReadFresh = %q, %v; want \"main\", true", got, ok)
	}
}

func TestReadFreshMissesOnExpiry(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "auth")
	if err := Write(p, "Sub"); err != nil {
		t.Fatalf("Write: %v", err)
	}
	old := time.Now().Add(-10 * time.Minute)
	if err := os.Chtimes(p, old, old); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}
	if got, ok := ReadFresh(p, 5*time.Minute); ok {
		t.Errorf("expired cache returned %q, want miss", got)
	}
}

func TestReadFreshMissesOnEmptyAndMissing(t *testing.T) {
	dir := t.TempDir()
	empty := filepath.Join(dir, "empty")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := ReadFresh(empty, time.Minute); ok {
		t.Error("empty file should miss (truncated-cache guard)")
	}
	if _, ok := ReadFresh(filepath.Join(dir, "absent"), time.Minute); ok {
		t.Error("missing file should miss")
	}
}

func TestReadPreservesContentExactly(t *testing.T) {
	// Full-Go contract (bash-compat superseded 2026-07-03): the cache returns
	// exactly what was written — no command-substitution newline stripping.
	dir := t.TempDir()
	p := filepath.Join(dir, "auth")
	if err := os.WriteFile(p, []byte("Sub\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, ok := ReadFresh(p, time.Minute)
	if !ok || got != "Sub\n" {
		t.Errorf("ReadFresh = %q, %v; want \"Sub\\n\" byte-exact", got, ok)
	}
}

func TestReadStaleServesExpiredContent(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "usage")
	if err := Write(p, `{"good":true}`); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-1 * time.Hour)
	_ = os.Chtimes(p, old, old)
	got, ok := ReadStale(p)
	if !ok || got != `{"good":true}` {
		t.Errorf("ReadStale = %q, %v; want content, true", got, ok)
	}
}

func TestWriteFailsWhenParentDirMissing(t *testing.T) {
	p := filepath.Join(t.TempDir(), "no-such-subdir", "cachefile")
	if err := Write(p, "x"); err == nil {
		t.Errorf("Write(%q) = nil error, want failure for missing parent dir", p)
	}
}

func TestReadStaleMissesOnUnreadablePath(t *testing.T) {
	dir := t.TempDir() // a directory is not a readable cache file
	if got, ok := ReadStale(dir); ok {
		t.Errorf("ReadStale(%q) = %q, true; want miss for unreadable path", dir, got)
	}
}

func TestWriteLeavesNoTempDebrisAndIsComplete(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "big")
	content := strings.Repeat("x", 64*1024)
	if err := Write(p, content); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "big" {
		t.Errorf("temp debris left in cache dir: %v", entries)
	}
	got, _ := ReadStale(p)
	if len(got) != len(content) {
		t.Errorf("content truncated: %d != %d", len(got), len(content))
	}
}
