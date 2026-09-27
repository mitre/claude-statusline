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

func TestRenderFormulaMatchesGolden(t *testing.T) {
	out, err := exec.Command("sh", "scripts/render-formula.sh", "9.9.9", "testdata/formula-checksums.txt").Output()
	if err != nil {
		t.Fatalf("render-formula.sh: %v", err)
	}
	golden, err := os.ReadFile("testdata/formula-golden.rb")
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != string(golden) {
		t.Errorf("rendered formula diverges from golden:\n got:\n%s\nwant:\n%s", out, golden)
	}
}

// bareTap creates a local bare repo standing in for mitre/homebrew-tap.
// With reject, a pre-receive hook refuses every push — the "rejected tap
// push" case that once printed "published" and exited 0.
func bareTap(t *testing.T, reject bool) (url, dir string) {
	t.Helper()
	dir = filepath.Join(t.TempDir(), "tap.git")
	// --initial-branch pins the bare repo's HEAD to main like the real tap;
	// otherwise HEAD follows the host's init.defaultBranch and a clone after
	// the first push to main comes back empty (caught on CI, where the
	// default is master).
	if out, err := exec.Command("git", "init", "--quiet", "--bare", "--initial-branch=main", dir).CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %v: %s", err, out)
	}
	if reject {
		hook := "#!/bin/sh\necho \"rejected by test hook\" >&2\nexit 1\n"
		if err := os.WriteFile(filepath.Join(dir, "hooks", "pre-receive"), []byte(hook), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	return "file://" + dir, dir
}

// runPublishFormula runs the script from a scratch cwd holding the golden
// checksums fixture, pointed at tapURL. The token is a dummy: nothing in
// these tests may reach the network.
func runPublishFormula(t *testing.T, tapURL string) (out string, err error) {
	t.Helper()
	script, aerr := filepath.Abs("scripts/publish-formula.sh")
	if aerr != nil {
		t.Fatal(aerr)
	}
	sums, rerr := os.ReadFile("testdata/formula-checksums.txt")
	if rerr != nil {
		t.Fatal(rerr)
	}
	work := t.TempDir()
	if err := os.MkdirAll(filepath.Join(work, "dist"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "dist", "checksums.txt"), sums, 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", script, "9.9.9")
	cmd.Dir = work
	cmd.Env = append(os.Environ(),
		"HOMEBREW_TAP_GITHUB_TOKEN=dummy-test-token",
		"TAP_URL="+tapURL,
	)
	b, err := cmd.CombinedOutput()
	return string(b), err
}

func TestPublishFormulaHonorsTapURLOverride(t *testing.T) {
	url, dir := bareTap(t, false)
	out, err := runPublishFormula(t, url)
	if err != nil {
		t.Fatalf("publish against local tap failed: %v: %s", err, out)
	}
	if !strings.Contains(out, "published") {
		t.Errorf("success run must report the publish, got: %q", out)
	}
	ls, lerr := exec.Command("git", "-C", dir, "ls-tree", "-r", "--name-only", "main").Output()
	if lerr != nil || !strings.Contains(string(ls), "Formula/claude-statusline.rb") {
		t.Errorf("formula must land in the tap repo, ls-tree = %q (%v)", ls, lerr)
	}
}

func TestPublishFormulaFailsWhenPushRejected(t *testing.T) {
	url, _ := bareTap(t, true)
	out, err := runPublishFormula(t, url)
	if err == nil {
		t.Fatalf("a rejected tap push must exit non-zero, output: %s", out)
	}
	if strings.Contains(out, "published Formula") {
		t.Errorf("a rejected push must never claim publication: %q", out)
	}
	if !strings.Contains(out, "push") {
		t.Errorf("failure must say the push failed, got: %q", out)
	}
}

func TestPublishFormulaUnchangedRerunExitsZero(t *testing.T) {
	url, _ := bareTap(t, false)
	if out, err := runPublishFormula(t, url); err != nil {
		t.Fatalf("first publish failed: %v: %s", err, out)
	}
	out, err := runPublishFormula(t, url)
	if err != nil {
		t.Fatalf("unchanged re-run must exit 0: %v: %s", err, out)
	}
	if !strings.Contains(out, "unchanged") {
		t.Errorf("unchanged re-run must say so, got: %q", out)
	}
}

// --- pre-commit gate hook (scripts/githooks/pre-commit + installer) ---

const hookMarker = "# --- BEGIN claude-statusline fast gates ---"

// scratchRepo creates an empty git repo and returns its path.
func scratchRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if out, err := exec.Command("git", "init", "--quiet", "--initial-branch=main", dir).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	return dir
}

func runInstallHooks(t *testing.T, repo string) string {
	t.Helper()
	script, err := filepath.Abs("scripts/install-hooks.sh")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", script)
	cmd.Dir = repo
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("install-hooks.sh: %v: %s", err, out)
	}
	return string(out)
}

func TestInstallHooksCreatesExecutableHook(t *testing.T) {
	repo := scratchRepo(t)
	runInstallHooks(t, repo)

	hook := filepath.Join(repo, ".git", "hooks", "pre-commit")
	info, err := os.Stat(hook)
	if err != nil {
		t.Fatalf("hook not created: %v", err)
	}
	if info.Mode()&0o100 == 0 {
		t.Errorf("hook must be owner-executable, mode %v", info.Mode())
	}
	content, err := os.ReadFile(hook)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), hookMarker) {
		t.Errorf("hook must contain the managed-block marker, got:\n%s", content)
	}
	if !strings.Contains(string(content), "scripts/githooks/pre-commit") {
		t.Errorf("hook must dispatch to the versioned gate script, got:\n%s", content)
	}
}

func TestInstallHooksIsIdempotent(t *testing.T) {
	repo := scratchRepo(t)
	runInstallHooks(t, repo)
	runInstallHooks(t, repo)

	content, err := os.ReadFile(filepath.Join(repo, ".git", "hooks", "pre-commit"))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(content), hookMarker); got != 1 {
		t.Errorf("double install must leave exactly 1 managed block, got %d:\n%s", got, content)
	}
}

func TestInstallHooksPreservesForeignSections(t *testing.T) {
	repo := scratchRepo(t)
	foreign := "#!/usr/bin/env sh\n# --- BEGIN BEADS INTEGRATION v1.3.0 ---\nbd hooks run pre-commit \"$@\" || exit $?\n# --- END BEADS INTEGRATION v1.3.0 ---\n"
	hook := filepath.Join(repo, ".git", "hooks", "pre-commit")
	if err := os.WriteFile(hook, []byte(foreign), 0o700); err != nil {
		t.Fatal(err)
	}
	runInstallHooks(t, repo)

	content, err := os.ReadFile(hook)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "BEGIN BEADS INTEGRATION v1.3.0") {
		t.Errorf("existing managed sections must survive the install, got:\n%s", content)
	}
	if !strings.Contains(string(content), hookMarker) {
		t.Errorf("our block must be appended alongside, got:\n%s", content)
	}
}

// shimMake fakes `make` first on PATH, recording its argv and exiting rc.
func shimMake(t *testing.T, rc int) (argvFile string) {
	t.Helper()
	dir := t.TempDir()
	argvFile = filepath.Join(dir, "make-argv")
	script := "#!/bin/sh\necho \"$@\" > \"" + argvFile + "\"\nexit " + fmt.Sprint(rc) + "\n"
	if err := os.WriteFile(filepath.Join(dir, "make"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return argvFile
}

func TestPreCommitGateFailsWhenLintFails(t *testing.T) {
	argvFile := shimMake(t, 1)
	out, err := exec.Command("sh", "scripts/githooks/pre-commit").CombinedOutput()
	if err == nil {
		t.Fatalf("gate must exit non-zero when make lint fails, output: %s", out)
	}
	argv, rerr := os.ReadFile(argvFile)
	if rerr != nil || strings.TrimSpace(string(argv)) != "lint" {
		t.Errorf("gate must run exactly 'make lint', argv=%q (%v)", argv, rerr)
	}
}

func TestPreCommitGatePassesWhenLintPasses(t *testing.T) {
	shimMake(t, 0)
	if out, err := exec.Command("sh", "scripts/githooks/pre-commit").CombinedOutput(); err != nil {
		t.Fatalf("gate must exit zero when make lint passes: %v: %s", err, out)
	}
}

func TestRenderFormulaRefusesIncompleteChecksums(t *testing.T) {
	// A checksums file missing any of the four platform assets must fail
	// loudly — a partial formula would break installs for that platform.
	partial := filepath.Join(t.TempDir(), "checksums.txt")
	line := strings.Repeat("1", 64) + "  claude-statusline-9.9.9-darwin-arm64.tar.gz\n"
	if err := os.WriteFile(partial, []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := exec.Command("sh", "scripts/render-formula.sh", "9.9.9", partial).Output(); err == nil {
		t.Fatal("render-formula.sh must fail when a platform asset is missing")
	}
}
