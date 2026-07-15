// claude-statusline renders a multi-row status line for Claude Code.
//
// Claude Code invokes it on every render, passing session JSON on stdin;
// whatever is printed to stdout becomes the status line.
//
// Optional config: ~/.claude/statusline.toml (see statusline.example.toml),
// overridable via $CLAUDE_STATUSLINE_CONFIG.
package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"time"
)

func main() {
	// --version prints build identity and exits without touching stdin —
	// the statusline contract (JSON in, ANSI out) applies only to the
	// argless invocation Claude Code performs.
	if len(os.Args) > 1 && os.Args[1] == "--version" {
		fmt.Println(versionString())
		return
	}
	// Subscription credentials resolve through Claude Code's own store
	// precedence (credentials file, then macOS keychain) — see credentials.go.
	creds := credentialSource{
		getenv:   os.Getenv,
		readFile: os.ReadFile,
		keychain: keychainCredentialJSON,
	}
	out, diag := run(deps{
		stdin:      os.Stdin,
		getenv:     os.Getenv,
		runGit:     runGit,
		keychainOK: creds.ok,
		fetchUsage: func() ([]byte, error) { return fetchUsage(creds) },
		readFile:   os.ReadFile,
	})
	if diag != "" {
		fmt.Fprint(os.Stderr, diag)
	}
	// No trailing newline, matching the reference's printf '%b'.
	fmt.Print(out)
}

// runGit executes git bounded by ctx: on expiry CommandContext kills the
// child process — never left running behind an abandoned render.
func runGit(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	// A killed git can leave descendants (hooks, fsmonitor, credential
	// helpers) holding the stdout pipe; without WaitDelay, Output() blocks
	// until every one of them exits. WaitDelay force-closes the pipes
	// shortly after the deadline so the render is actually bounded.
	cmd.WaitDelay = 100 * time.Millisecond
	out, err := cmd.Output()
	return string(out), err
}

// keychainCredentialJSON reads Claude Code's credential JSON from the macOS
// keychain item. On hosts without the `security` binary (Linux, containers)
// the exec fails cleanly and the credential chain treats it as a miss.
func keychainCredentialJSON() (string, error) {
	out, err := exec.Command("security", "find-generic-password",
		"-s", "Claude Code-credentials", "-w").Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// usageEndpoint and usageHTTPTimeout are test seams: main_test points them
// at an httptest server with a short deadline. Production wiring never
// overrides them — injection stays at the var level so fetchUsage keeps a
// single, branch-free production path.
var (
	usageEndpoint    = "https://api.anthropic.com/api/oauth/usage"
	usageHTTPTimeout = 2 * time.Second
)

// fetchUsage pulls the subscription-limits payload from the OAuth usage
// endpoint, resolving the access token through the credential chain per
// fetch — never cached, never logged.
func fetchUsage(creds credentialSource) ([]byte, error) {
	token, err := creds.token()
	if err != nil {
		return nil, err
	}

	client := &http.Client{Timeout: usageHTTPTimeout}
	req, err := http.NewRequest(http.MethodGet, usageEndpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("anthropic-beta", "oauth-2025-04-20")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if cerr := resp.Body.Close(); err == nil {
		err = cerr
	}
	return body, err
}
