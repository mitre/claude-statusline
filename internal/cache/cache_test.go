package cache

import (
	"fmt"
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

// A cache dir another actor can write to may hold a symlink at a name the
// writer can predict. Write must never write through it, and path must end up
// a regular file of its own.
func TestWriteDoesNotFollowAPlantedTempSymlink(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "usage-x")
	victim := filepath.Join(dir, "victim")
	if err := os.WriteFile(victim, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	planted := fmt.Sprintf("%s.%d", p, os.Getpid())
	if err := os.Symlink(victim, planted); err != nil {
		t.Fatal(err)
	}

	if err := Write(p, "new"); err != nil {
		t.Fatalf("Write: %v", err)
	}

	if b, _ := os.ReadFile(victim); string(b) != "original" {
		t.Errorf("symlink target overwritten: victim = %q, want \"original\"", b)
	}
	info, err := os.Lstat(p)
	if err != nil {
		t.Fatalf("Lstat(path): %v", err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		t.Errorf("path mode = %v, want a regular 0600 file", info.Mode())
	}
	if b, _ := os.ReadFile(p); string(b) != "new" {
		t.Errorf("path content = %q, want \"new\"", b)
	}
	if target, err := os.Readlink(planted); err != nil || target != victim {
		t.Errorf("planted symlink disturbed: Readlink = %q, %v", target, err)
	}
}

// When the rename fails after the temp file exists, Write reports the error
// and leaves no temp file behind.
func TestWriteRemovesTempFileWhenRenameFails(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "occupied")
	if err := os.Mkdir(p, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p, "child"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := Write(p, "new"); err == nil {
		t.Fatal("Write over a non-empty directory = nil error, want failure")
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "occupied" {
		t.Errorf("temp debris left after a failed rename: %v", entries)
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
