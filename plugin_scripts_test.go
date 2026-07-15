package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// The plugin's shell layer (scripts/install-binary.sh, scripts/sync-binary.sh)
// is tested through a PATH-shim curl serving local fixtures — the same idiom
// the Go tests use for git and security. The trust boundary under test:
// nothing installs without a verified checksum, dev builds are never touched,
// and the SessionStart hook writes nothing to stdout (it would be injected
// into the session context).

// assetName is the release-asset basename the scripts derive from uname.
func assetName(version string) string {
	arch := runtime.GOARCH
	return fmt.Sprintf("claude-statusline-%s-%s-%s.tar.gz", version, runtime.GOOS, arch)
}

// fakeRelease builds a tar.gz containing an executable fake claude-statusline
// that reports the given version, plus a checksums.txt (tamper flips the
// sum). Pure Go (archive/tar + crypto/sha256) — no subprocesses.
func fakeRelease(t *testing.T, version string, tamper bool) (assets string) {
	t.Helper()
	assets = t.TempDir()
	script := "#!/bin/sh\necho \"claude-statusline " + version + " (test, today)\"\n"

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: "claude-statusline", Mode: 0o755, Size: int64(len(script))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte(script)); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(assets, assetName(version)), buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}

	sum := fmt.Sprintf("%x", sha256.Sum256(buf.Bytes()))
	if tamper {
		sum = strings.Repeat("0", 64)
	}
	line := sum + "  " + assetName(version) + "\n"
	if err := os.WriteFile(filepath.Join(assets, "checksums.txt"), []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	return assets
}

// shimCurl puts a fake curl first on PATH that serves the fixture assets and
// records every invocation; real network is unreachable by construction.
func shimCurl(t *testing.T, assets string) (calledMarker string) {
	t.Helper()
	dir := t.TempDir()
	calledMarker = filepath.Join(dir, "curl-called")
	script := `#!/bin/sh
touch "` + calledMarker + `"
out=""
url=""
prev=""
for a in "$@"; do
  [ "$prev" = "-o" ] && out="$a"
  case "$a" in http*://*) url="$a" ;; esac
  prev="$a"
done
src="` + assets + `/$(basename "$url")"
[ -f "$src" ] || exit 22
if [ -n "$out" ]; then cp "$src" "$out"; else cat "$src"; fi
`
	if err := os.WriteFile(filepath.Join(dir, "curl"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return calledMarker
}

func installedBin(home string) string {
	return filepath.Join(home, ".claude", "claude-statusline")
}

func binVersion(t *testing.T, bin string) string {
	t.Helper()
	out, err := exec.Command(bin, "--version").Output()
	if err != nil {
		return ""
	}
	f := strings.Fields(string(out))
	if len(f) < 2 {
		return ""
	}
	return f[1]
}

func TestInstallScriptInstallsVerifiedBinary(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	shimCurl(t, fakeRelease(t, "9.9.9", false))

	out, err := exec.Command("sh", "scripts/install-binary.sh", "9.9.9").CombinedOutput()
	if err != nil {
		t.Fatalf("install-binary.sh: %v: %s", err, out)
	}
	if got := binVersion(t, installedBin(home)); got != "9.9.9" {
		t.Errorf("installed binary version = %q, want 9.9.9", got)
	}
}

func TestInstallScriptRefusesTamperedChecksum(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	shimCurl(t, fakeRelease(t, "9.9.9", true))

	out, err := exec.Command("sh", "scripts/install-binary.sh", "9.9.9").CombinedOutput()
	if err == nil {
		t.Fatalf("install-binary.sh must fail on checksum mismatch, output: %s", out)
	}
	if _, statErr := os.Stat(installedBin(home)); !os.IsNotExist(statErr) {
		t.Error("a tampered download must never be installed")
	}
}

func TestInstallScriptBacksUpExistingBinary(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(installedBin(home), []byte("#!/bin/sh\necho claude-statusline 1.0.0 (old, old)\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	shimCurl(t, fakeRelease(t, "9.9.9", false))

	if out, err := exec.Command("sh", "scripts/install-binary.sh", "9.9.9").CombinedOutput(); err != nil {
		t.Fatalf("install-binary.sh: %v: %s", err, out)
	}
	backups, err := filepath.Glob(installedBin(home) + ".backup-*")
	if err != nil || len(backups) != 1 {
		t.Errorf("existing binary must be backed up once, got %v (%v)", backups, err)
	}
	if got := binVersion(t, installedBin(home)); got != "9.9.9" {
		t.Errorf("installed binary version = %q, want 9.9.9", got)
	}
}

// pluginRoot builds a fake CLAUDE_PLUGIN_ROOT carrying plugin.json at version.
func pluginRoot(t *testing.T, version string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".claude-plugin"), 0o700); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name":"claude-statusline","version":"` + version + `"}`
	if err := os.WriteFile(filepath.Join(root, ".claude-plugin", "plugin.json"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	return root
}

func runSyncHook(t *testing.T, root string) string {
	t.Helper()
	cmd := exec.Command("sh", "scripts/sync-binary.sh")
	cmd.Env = append(os.Environ(), "CLAUDE_PLUGIN_ROOT="+root)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("sync-binary.sh: %v", err)
	}
	return string(out)
}

func TestSyncHookSkipsDevBuildAndStaysSilent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(installedBin(home), []byte("#!/bin/sh\necho 'claude-statusline dev (none, unknown)'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	marker := shimCurl(t, fakeRelease(t, "9.9.9", false))

	if out := runSyncHook(t, pluginRoot(t, "9.9.9")); out != "" {
		t.Errorf("SessionStart hook stdout must be empty (it is injected into session context): %q", out)
	}
	time.Sleep(300 * time.Millisecond)
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Error("dev build must never trigger a download")
	}
	if got := binVersion(t, installedBin(home)); got != "dev" {
		t.Errorf("dev build must remain untouched, version now %q", got)
	}
}

func TestSyncHookUpdatesOnVersionMismatchSilently(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(installedBin(home), []byte("#!/bin/sh\necho 'claude-statusline 1.0.0 (a, b)'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	shimCurl(t, fakeRelease(t, "9.9.9", false))

	if out := runSyncHook(t, pluginRoot(t, "9.9.9")); out != "" {
		t.Errorf("SessionStart hook stdout must be empty: %q", out)
	}
	// The download runs in the background; poll for the atomic swap.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if binVersion(t, installedBin(home)) == "9.9.9" {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Errorf("hook did not sync the binary: version still %q", binVersion(t, installedBin(home)))
}

func TestSyncHookNoOpWhenVersionsMatch(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(installedBin(home), []byte("#!/bin/sh\necho 'claude-statusline 9.9.9 (x, y)'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	marker := shimCurl(t, fakeRelease(t, "9.9.9", false))

	if out := runSyncHook(t, pluginRoot(t, "9.9.9")); out != "" {
		t.Errorf("SessionStart hook stdout must be empty: %q", out)
	}
	time.Sleep(300 * time.Millisecond)
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Error("matching versions must not trigger any download")
	}
}
