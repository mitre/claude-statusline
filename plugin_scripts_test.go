package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/json"
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

// --- release preflight (scripts/release-preflight.sh + shared version/notes scripts) ---

// preflightRepo builds a scratch repo shaped like this one where preflight
// looks: plugin manifests at .claude-plugin/, CHANGELOG.md, branch main.
func preflightRepo(t *testing.T, manifestVer, changelogVer string) string {
	t.Helper()
	dir := scratchRepo(t)
	if err := os.MkdirAll(filepath.Join(dir, ".claude-plugin"), 0o700); err != nil {
		t.Fatal(err)
	}
	writePreflightFile(t, dir, ".claude-plugin/plugin.json",
		`{"name":"claude-statusline","version":"`+manifestVer+`"}`)
	writePreflightFile(t, dir, ".claude-plugin/marketplace.json",
		`{"plugins":[{"name":"claude-statusline","version":"`+manifestVer+`"}]}`)
	writePreflightFile(t, dir, "CHANGELOG.md",
		"# Changelog\n\n## [Unreleased]\n\n## ["+changelogVer+"] - 2026-09-27\n\n### Fixed\n\n- a fix entry\n\n## [0.1.0] - 2026-07-15\n\n### Added\n\n- old entry\n")
	gitIn(t, dir, "add", ".")
	gitIn(t, dir, "-c", "user.name=t", "-c", "user.email=t@test.invalid", "commit", "-q", "-m", "seed")
	return dir
}

func writePreflightFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, rel), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

// runPreflight executes the script in dir with MAIN_REF=main (the scratch
// repos have no origin — same seam idea as TAP_URL).
func runPreflight(t *testing.T, dir, tag string) (out string, err error) {
	t.Helper()
	script, aerr := filepath.Abs("scripts/release-preflight.sh")
	if aerr != nil {
		t.Fatal(aerr)
	}
	cmd := exec.Command("sh", script, tag)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "MAIN_REF=main")
	b, err := cmd.CombinedOutput()
	return string(b), err
}

func TestReleasePreflightRejectsVersionMismatch(t *testing.T) {
	dir := preflightRepo(t, "0.2.0", "0.3.0")
	gitIn(t, dir, "tag", "v0.3.0")
	out, err := runPreflight(t, dir, "v0.3.0")
	if err == nil {
		t.Fatalf("plugin.json 0.2.0 vs tag v0.3.0 must fail, output: %s", out)
	}
	if !strings.Contains(out, "plugin.json") {
		t.Errorf("failure must name plugin.json, got: %q", out)
	}
}

func TestReleasePreflightRejectsMarketplaceMismatch(t *testing.T) {
	dir := preflightRepo(t, "0.3.0", "0.3.0")
	writePreflightFile(t, dir, ".claude-plugin/marketplace.json",
		`{"plugins":[{"name":"claude-statusline","version":"0.2.0"}]}`)
	gitIn(t, dir, "tag", "v0.3.0")
	out, err := runPreflight(t, dir, "v0.3.0")
	if err == nil {
		t.Fatalf("marketplace.json 0.2.0 vs tag v0.3.0 must fail, output: %s", out)
	}
	if !strings.Contains(out, "marketplace.json") {
		t.Errorf("failure must name marketplace.json, got: %q", out)
	}
}

func TestReleasePreflightRejectsNonSemverTag(t *testing.T) {
	dir := preflightRepo(t, "0.3.0", "0.3.0")
	gitIn(t, dir, "tag", "v0.3.0-rc1")
	out, err := runPreflight(t, dir, "v0.3.0-rc1")
	if err == nil {
		t.Fatalf("non-semver tag must fail, output: %s", out)
	}
	if !strings.Contains(out, "vMAJOR.MINOR.PATCH") {
		t.Errorf("failure must state the required tag shape, got: %q", out)
	}
}

func TestReleasePreflightRejectsMissingChangelogHeading(t *testing.T) {
	dir := preflightRepo(t, "0.3.0", "0.2.0")
	gitIn(t, dir, "tag", "v0.3.0")
	out, err := runPreflight(t, dir, "v0.3.0")
	if err == nil {
		t.Fatalf("missing CHANGELOG section must fail, output: %s", out)
	}
	if !strings.Contains(out, "CHANGELOG") {
		t.Errorf("failure must name the CHANGELOG, got: %q", out)
	}
}

func TestReleasePreflightRejectsTagOffMain(t *testing.T) {
	dir := preflightRepo(t, "0.3.0", "0.3.0")
	gitIn(t, dir, "checkout", "-q", "-b", "side")
	writePreflightFile(t, dir, "extra.txt", "off-main change\n")
	gitIn(t, dir, "add", "extra.txt")
	gitIn(t, dir, "-c", "user.name=t", "-c", "user.email=t@test.invalid", "commit", "-q", "-m", "side commit")
	gitIn(t, dir, "tag", "v0.3.0")
	out, err := runPreflight(t, dir, "v0.3.0")
	if err == nil {
		t.Fatalf("tag off main must fail, output: %s", out)
	}
	if !strings.Contains(out, "not on main") {
		t.Errorf("failure must say the tag is not on main, got: %q", out)
	}
}

func TestReleasePreflightPassesWhenAligned(t *testing.T) {
	dir := preflightRepo(t, "0.3.0", "0.3.0")
	gitIn(t, dir, "tag", "v0.3.0")
	out, err := runPreflight(t, dir, "v0.3.0")
	if err != nil {
		t.Fatalf("aligned release must pass preflight: %v: %s", err, out)
	}
	if !strings.Contains(out, "preflight OK: v0.3.0") {
		t.Errorf("success must report what was verified, got: %q", out)
	}
}

func TestReleasePreflightRejectsInvalidDateFormatInChangelog(t *testing.T) {
	dir := preflightRepo(t, "0.3.0", "0.3.0")
	writePreflightFile(t, dir, "CHANGELOG.md",
		"# Changelog\n\n## [Unreleased]\n\n## [0.3.0] - unreleased\n\n### Fixed\n\n- a fix entry\n")
	gitIn(t, dir, "add", "CHANGELOG.md")
	gitIn(t, dir, "-c", "user.name=t", "-c", "user.email=t@test.invalid", "commit", "-q", "-m", "bad date")
	gitIn(t, dir, "tag", "v0.3.0")
	out, err := runPreflight(t, dir, "v0.3.0")
	if err == nil {
		t.Fatalf("CHANGELOG with non-ISO date must fail preflight, output: %s", out)
	}
	if !strings.Contains(out, "YYYY-MM-DD") {
		t.Errorf("failure must mention YYYY-MM-DD, got: %q", out)
	}
}

func TestPluginVersionScriptExtractsAndFails(t *testing.T) {
	// Parity with the real manifest: the script must extract the exact
	// version declared in .claude-plugin/plugin.json, decoupled from hardcoded strings.
	manifestData, err := os.ReadFile(".claude-plugin/plugin.json")
	if err != nil {
		t.Fatalf("reading .claude-plugin/plugin.json: %v", err)
	}
	var manifest struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		t.Fatalf("unmarshaling .claude-plugin/plugin.json: %v", err)
	}
	if manifest.Version == "" {
		t.Fatal(".claude-plugin/plugin.json has empty version")
	}

	out, err := exec.Command("sh", "scripts/plugin-version.sh", ".claude-plugin/plugin.json").Output()
	if err != nil {
		t.Fatalf("plugin-version.sh on the real manifest: %v", err)
	}
	if got := strings.TrimSpace(string(out)); got != manifest.Version {
		t.Errorf("plugin-version.sh = %q, want %q (manifest version)", got, manifest.Version)
	}

	// Controlled fixture: proves extraction on an arbitrary known version string.
	fixture := filepath.Join(t.TempDir(), "manifest.json")
	if err := os.WriteFile(fixture, []byte(`{"name":"test","version":"9.8.7"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	fOut, err := exec.Command("sh", "scripts/plugin-version.sh", fixture).Output()
	if err != nil {
		t.Fatalf("plugin-version.sh on fixture: %v", err)
	}
	if got := strings.TrimSpace(string(fOut)); got != "9.8.7" {
		t.Errorf("plugin-version.sh on fixture = %q, want %q", got, "9.8.7")
	}

	// Missing version: must fail with non-zero exit code.
	empty := filepath.Join(t.TempDir(), "empty.json")
	if err := os.WriteFile(empty, []byte(`{"name":"x"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := exec.Command("sh", "scripts/plugin-version.sh", empty).Output(); err == nil {
		t.Error("a manifest without a version must fail, not print empty")
	}
}

func TestReleaseNotesExtractsExactSection(t *testing.T) {
	cl := filepath.Join(t.TempDir(), "CHANGELOG.md")
	content := "# Changelog\n\n## [Unreleased]\n\n## [0.3.0] - 2026-09-27\n\n### Fixed\n\n- fix one\n- fix two\n\n## [0.2.0] - 2026-09-24\n\n### Added\n\n- old\n"
	if err := os.WriteFile(cl, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("sh", "scripts/release-notes.sh", "0.3.0", cl).Output()
	if err != nil {
		t.Fatalf("release-notes.sh: %v", err)
	}
	// Trailing blank lines before the next heading are stripped (shell
	// command substitution); one final newline is the canonical shape.
	want := "\n### Fixed\n\n- fix one\n- fix two\n"
	if string(out) != want {
		t.Errorf("extracted section = %q, want %q", out, want)
	}
	if _, err := exec.Command("sh", "scripts/release-notes.sh", "9.9.9", cl).Output(); err == nil {
		t.Error("an absent version's notes must fail, not print empty")
	}
}

func TestReleaseNotesExcludeTrailingLinkDefinitions(t *testing.T) {
	// Keep-a-Changelog link definitions follow the LAST section; they are
	// plumbing, never release notes.
	cl := filepath.Join(t.TempDir(), "CHANGELOG.md")
	content := "# Changelog\n\n## [0.1.0] - 2026-07-15\n\n### Added\n\n- first release\n\n[Unreleased]: https://example.invalid/compare/v0.1.0...HEAD\n[0.1.0]: https://example.invalid/releases/tag/v0.1.0\n"
	if err := os.WriteFile(cl, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("sh", "scripts/release-notes.sh", "0.1.0", cl).Output()
	if err != nil {
		t.Fatalf("release-notes.sh: %v", err)
	}
	want := "\n### Added\n\n- first release\n"
	if string(out) != want {
		t.Errorf("last section must exclude link definitions, got %q, want %q", out, want)
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

// The dev-install script puts a local build where only this repo's sessions
// run it (~/.claude/claude-statusline-dev), never at a release install path.

func devBin(home string) string {
	return filepath.Join(home, ".claude", "claude-statusline-dev")
}

// fakeBuild writes an executable stand-in for a local build reporting tag.
func fakeBuild(t *testing.T, tag string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "claude-statusline")
	if err := os.WriteFile(p, []byte("#!/bin/sh\necho \"claude-statusline dev ("+tag+", today)\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	return p
}

func runDevInstall(t *testing.T, src string) (string, error) {
	t.Helper()
	out, err := exec.Command("sh", "scripts/dev-install.sh", src).CombinedOutput()
	return string(out), err
}

func fileSHA(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("%x", sha256.Sum256(b))
}

// shimPath puts executable stand-ins for the named commands first on PATH.
func shimPath(t *testing.T, scripts map[string]string) {
	t.Helper()
	dir := t.TempDir()
	for name, body := range scripts {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+body), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// shimDate makes the script's backup timestamps deterministic: every call
// returns the same second (fixed) or the next second (increasing).
func shimDate(t *testing.T, fixed bool) {
	t.Helper()
	if fixed {
		shimPath(t, map[string]string{"date": "echo 20261114-120000\n"})
		return
	}
	counter := filepath.Join(t.TempDir(), "n")
	shimPath(t, map[string]string{"date": `n=$(cat "` + counter + `" 2>/dev/null || echo 0)
n=$((n + 1))
echo "$n" > "` + counter + `"
printf '20261114-12%04d\n' "$n"
`})
}

func TestDevInstallSwapsAtomicallyAndKeepsThreeBackups(t *testing.T) {
	for _, tc := range []struct {
		name  string
		fixed bool
	}{
		{"installs in distinct seconds", false},
		{"installs within one second", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			shimDate(t, tc.fixed)
			if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o700); err != nil {
				t.Fatal(err)
			}
			release := []byte("#!/bin/sh\necho claude-statusline 1.0.0 (rel, rel)\n")
			if err := os.WriteFile(installedBin(home), release, 0o700); err != nil {
				t.Fatal(err)
			}

			// Seven installs: builds 0..5 are each replaced in turn, so the
			// newest three backups hold builds 3, 4 and 5.
			var srcs []string
			for i := 0; i < 7; i++ {
				src := fakeBuild(t, fmt.Sprintf("b%d", i))
				srcs = append(srcs, src)
				if out, err := runDevInstall(t, src); err != nil {
					t.Fatalf("dev-install.sh b%d: %v: %s", i, err, out)
				}
			}

			if got, want := fileSHA(t, devBin(home)), fileSHA(t, srcs[6]); got != want {
				t.Errorf("dev copy sha = %s, want the last build's %s", got, want)
			}
			if info, err := os.Lstat(devBin(home)); err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o100 == 0 {
				t.Errorf("dev copy must be an executable regular file: %v %v", info, err)
			}
			if staged, _ := filepath.Glob(devBin(home) + ".new*"); len(staged) != 0 {
				t.Errorf("no staging file may remain after the swap, got %v", staged)
			}
			if _, err := os.Stat(devBin(home) + ".lock"); !os.IsNotExist(err) {
				t.Error("each install must release its lock")
			}

			backups, err := filepath.Glob(devBin(home) + ".backup-*")
			if err != nil {
				t.Fatal(err)
			}
			got := map[string]bool{}
			for _, b := range backups {
				got[fileSHA(t, b)] = true
			}
			if len(backups) != 3 {
				t.Errorf("want exactly the newest 3 backups, got %d: %v", len(backups), backups)
			}
			for _, i := range []int{3, 4, 5} {
				if !got[fileSHA(t, srcs[i])] {
					t.Errorf("backup of build b%d missing; kept %v", i, backups)
				}
			}

			if b, err := os.ReadFile(installedBin(home)); err != nil || !bytes.Equal(b, release) {
				t.Error("dev-install must never touch the release install path")
			}
		})
	}
}

func TestDevInstallIgnoresPlantedStagingSymlink(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o700); err != nil {
		t.Fatal(err)
	}
	release := []byte("#!/bin/sh\necho claude-statusline 1.0.0 (rel, rel)\n")
	if err := os.WriteFile(installedBin(home), release, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(installedBin(home), devBin(home)+".new"); err != nil {
		t.Fatal(err)
	}

	src := fakeBuild(t, "b1")
	if out, err := runDevInstall(t, src); err != nil {
		t.Fatalf("dev-install.sh: %v: %s", err, out)
	}
	if b, err := os.ReadFile(installedBin(home)); err != nil || !bytes.Equal(b, release) {
		t.Error("a symlink at the staging name must never lead the install onto the release copy")
	}
	if info, err := os.Lstat(devBin(home)); err != nil || !info.Mode().IsRegular() {
		t.Fatalf("dev copy must be a regular file, got %v %v", info, err)
	}
	if got, want := fileSHA(t, devBin(home)), fileSHA(t, src); got != want {
		t.Errorf("dev copy sha = %s, want %s", got, want)
	}
}

func TestDevInstallRefusesWhenHashingFails(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o700); err != nil {
		t.Fatal(err)
	}
	prior := []byte("#!/bin/sh\necho claude-statusline dev (prior, today)\n")
	if err := os.WriteFile(devBin(home), prior, 0o700); err != nil {
		t.Fatal(err)
	}
	shimPath(t, map[string]string{"sha256sum": "exit 1\n", "shasum": "exit 1\n"})

	out, err := runDevInstall(t, fakeBuild(t, "b1"))
	if err == nil {
		t.Fatalf("dev-install.sh must refuse when no checksum can be computed, output: %s", out)
	}
	if got, rerr := os.ReadFile(devBin(home)); rerr != nil || !bytes.Equal(got, prior) {
		t.Error("a refused install must leave the existing dev copy untouched")
	}
	if staged, _ := filepath.Glob(devBin(home) + ".new*"); len(staged) != 0 {
		t.Errorf("a refused install must clean up its staging file, got %v", staged)
	}
}

func TestDevInstallRefusesWhileAnotherInstallRuns(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(devBin(home)+".lock", 0o700); err != nil {
		t.Fatal(err)
	}
	prior := []byte("#!/bin/sh\necho claude-statusline dev (prior, today)\n")
	if err := os.WriteFile(devBin(home), prior, 0o700); err != nil {
		t.Fatal(err)
	}

	out, err := runDevInstall(t, fakeBuild(t, "b1"))
	if err == nil {
		t.Fatalf("dev-install.sh must refuse while another install holds the lock, output: %s", out)
	}
	if got, rerr := os.ReadFile(devBin(home)); rerr != nil || !bytes.Equal(got, prior) {
		t.Error("a refused install must leave the existing dev copy untouched")
	}

	if !strings.Contains(out, devBin(home)+".lock") {
		t.Errorf("refusal must name the lock to remove, got: %s", out)
	}

	// A lock left by a killed install is never cleared automatically: two
	// installers reclaiming it at once could both proceed.
	old := time.Now().Add(-24 * time.Hour)
	if err := os.Chtimes(devBin(home)+".lock", old, old); err != nil {
		t.Fatal(err)
	}
	if out, err := runDevInstall(t, fakeBuild(t, "b2")); err == nil {
		t.Fatalf("an old lock must still be refused, output: %s", out)
	}
	if got, rerr := os.ReadFile(devBin(home)); rerr != nil || !bytes.Equal(got, prior) {
		t.Error("a refused install must leave the existing dev copy untouched")
	}
}

func TestDevInstallRefusesPastNinetyNineBackupsInOneSecond(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	shimDate(t, true)
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o700); err != nil {
		t.Fatal(err)
	}
	prior := []byte("#!/bin/sh\necho claude-statusline dev (prior, today)\n")
	if err := os.WriteFile(devBin(home), prior, 0o700); err != nil {
		t.Fatal(err)
	}
	last := devBin(home) + ".backup-20261114-120000-99"
	if err := os.WriteFile(last, prior, 0o700); err != nil {
		t.Fatal(err)
	}

	if out, err := runDevInstall(t, fakeBuild(t, "b1")); err == nil {
		t.Fatalf("a 100th backup name in one second would sort out of order and must be refused, output: %s", out)
	}
	if got, rerr := os.ReadFile(devBin(home)); rerr != nil || !bytes.Equal(got, prior) {
		t.Error("a refused install must leave the existing dev copy untouched")
	}
	if backups, _ := filepath.Glob(devBin(home) + ".backup-*"); len(backups) != 1 {
		t.Errorf("a refused install must not add a backup, got %v", backups)
	}
}

func TestDevInstallFirstInstallMakesNoBackup(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	src := fakeBuild(t, "b1")
	if out, err := runDevInstall(t, src); err != nil {
		t.Fatalf("dev-install.sh: %v: %s", err, out)
	}
	if got, want := fileSHA(t, devBin(home)), fileSHA(t, src); got != want {
		t.Errorf("dev copy sha = %s, want %s", got, want)
	}
	if backups, _ := filepath.Glob(devBin(home) + ".backup-*"); len(backups) != 0 {
		t.Errorf("first install has nothing to back up, got %v", backups)
	}
}

func TestDevInstallRefusesMissingBuild(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o700); err != nil {
		t.Fatal(err)
	}
	prior := []byte("#!/bin/sh\necho claude-statusline dev (prior, today)\n")
	if err := os.WriteFile(devBin(home), prior, 0o700); err != nil {
		t.Fatal(err)
	}

	out, err := runDevInstall(t, filepath.Join(t.TempDir(), "claude-statusline"))
	if err == nil {
		t.Fatalf("dev-install.sh must fail when the build is missing, output: %s", out)
	}
	if !strings.Contains(out, "make build") {
		t.Errorf("refusal should say how to produce the build, got: %s", out)
	}
	if got, rerr := os.ReadFile(devBin(home)); rerr != nil || !bytes.Equal(got, prior) {
		t.Error("a refused install must leave the existing dev copy untouched")
	}
	if backups, _ := filepath.Glob(devBin(home) + ".backup-*"); len(backups) != 0 {
		t.Errorf("a refused install must not create backups, got %v", backups)
	}
}

// releaseFixtureRepo lays out what scripts/release.sh reads from its working
// directory: the plugin manifest (version) and the CHANGELOG, plus a dist/
// holding a stale artifact, as a previous run would leave it.
func releaseFixtureRepo(t *testing.T, changelog string) string {
	t.Helper()
	dir := t.TempDir()
	for rel, content := range map[string]string{
		".claude-plugin/plugin.json": `{"name":"claude-statusline","version":"9.9.9"}`,
		"CHANGELOG.md":               changelog,
		"dist/stale.tar.gz":          "old",
	} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, rel)), 0o700); err != nil {
			t.Fatal(err)
		}
		writePreflightFile(t, dir, rel, content)
	}
	return dir
}

// shimGoreleaser stands in for goreleaser: like --clean it deletes dist/
// before anything else, then records its arguments and the release notes it
// can still read.
func shimGoreleaser(t *testing.T) (argsFile, notesFile string) {
	t.Helper()
	dir := t.TempDir()
	argsFile = filepath.Join(dir, "args")
	notesFile = filepath.Join(dir, "notes")
	script := `#!/bin/sh
rm -rf dist
printf '%s\n' "$@" > '` + argsFile + `'
for a in "$@"; do
  case "$a" in --release-notes=*) cat "${a#--release-notes=}" > '` + notesFile + `' ;; esac
done
`
	if err := os.WriteFile(filepath.Join(dir, "goreleaser"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return argsFile, notesFile
}

func runRelease(t *testing.T, dir string) (string, error) {
	t.Helper()
	script, err := filepath.Abs("scripts/release.sh")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", script)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func TestReleaseScriptPassesNotesThatSurviveClean(t *testing.T) {
	dir := releaseFixtureRepo(t, "# Changelog\n\n## [Unreleased]\n\n## [9.9.9] - 2026-11-15\n\n### Fixed\n\n- the fix\n\n## [9.9.8] - 2026-11-01\n\n### Added\n\n- older\n")
	argsFile, notesFile := shimGoreleaser(t)

	if out, err := runRelease(t, dir); err != nil {
		t.Fatalf("release.sh: %v: %s", err, out)
	}
	notes, err := os.ReadFile(notesFile)
	if err != nil {
		t.Fatalf("goreleaser could not read the release notes after cleaning dist/: %v", err)
	}
	if want := "\n### Fixed\n\n- the fix\n"; string(notes) != want {
		t.Errorf("release notes = %q, want the 9.9.9 CHANGELOG section %q", notes, want)
	}
	args, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(args), "release\n--clean\n") {
		t.Errorf("goreleaser must run `release --clean`, got args %q", args)
	}
}

func TestReleaseScriptRefusesWithoutNotes(t *testing.T) {
	for name, changelog := range map[string]string{
		"no section":    "# Changelog\n\n## [Unreleased]\n\n## [9.9.8] - 2026-11-01\n\n### Added\n\n- older\n",
		"empty section": "# Changelog\n\n## [Unreleased]\n\n## [9.9.9] - 2026-11-15\n\n## [9.9.8] - 2026-11-01\n\n### Added\n\n- older\n",
	} {
		dir := releaseFixtureRepo(t, changelog)
		argsFile, _ := shimGoreleaser(t)
		if out, err := runRelease(t, dir); err == nil {
			t.Errorf("%s: release.sh must refuse a version without notes, output: %s", name, out)
		}
		if _, err := os.Stat(argsFile); !os.IsNotExist(err) {
			t.Errorf("%s: goreleaser must not run without release notes", name)
		}
	}
}
