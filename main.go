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
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/user"
	"strconv"
	"strings"
	"time"

	"github.com/mitre/claude-statusline/internal/usage"
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
	// precedence (macOS keychain item, then the plaintext store) — see
	// tokensource.go.
	creds := tokenSource{
		lookupEnv: os.LookupEnv,
		readFile:  os.ReadFile,
		username:  loginName,
		keychain:  keychainCredentialJSON,
	}
	out, diag := run(deps{
		stdin:            os.Stdin,
		getenv:           os.Getenv,
		runGit:           runGit,
		keychainOK:       creds.ok,
		credentialSource: creds.location,
		fetchUsage:       func() ([]byte, error) { return fetchUsage(creds) },
		readFile:         os.ReadFile,
		stat:             os.Stat,
		readDir:          os.ReadDir,
		now:              time.Now,
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

var keychainTimeout = 1 * time.Second

// keychainCredentialJSON reads Claude Code's credential JSON from the macOS
// keychain item for service and account, as Claude Code does:
// security find-generic-password -a <account> -w -s <service>. Both values
// are derived from the environment, so each must match its exact ported
// shape (keychainServicePattern, keychainAccountPattern) before the direct
// exec; anything else is refused without running security. On hosts
// without the security binary (Linux, containers) the exec fails cleanly
// and the token source treats it as a miss. A timeout bounds the exec so
// an unresponsive keychain or prompt never blocks the render.
func keychainCredentialJSON(service, account string) (string, error) {
	if !keychainServicePattern.MatchString(service) || !keychainAccountPattern.MatchString(account) {
		return "", errors.New("keychain lookup refused: service or account outside the expected shape")
	}
	ctx, cancel := context.WithTimeout(context.Background(), keychainTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "security", "find-generic-password",
		"-a", account, "-w", "-s", service)
	cmd.WaitDelay = 100 * time.Millisecond
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// loginName is the OS account name, Claude Code's fallback when USER is
// unset (os.userInfo().username).
func loginName() (string, error) {
	u, err := user.Current()
	if err != nil {
		return "", err
	}
	return u.Username, nil
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
func fetchUsage(creds tokenSource) ([]byte, error) {
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
	if resp.StatusCode == http.StatusTooManyRequests {
		_ = resp.Body.Close()
		return nil, &usage.RetryAfterError{After: retryAfter(resp.Header.Get("Retry-After"))}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if cerr := resp.Body.Close(); err == nil {
		err = cerr
	}
	return body, err
}

// retryAfter parses the delay-seconds form of a Retry-After header. The
// HTTP-date form and anything unparseable yield zero, which the resolver
// treats as "apply the floor".
func retryAfter(header string) time.Duration {
	secs, err := strconv.Atoi(strings.TrimSpace(header))
	if err != nil || secs <= 0 {
		return 0
	}
	return time.Duration(secs) * time.Second
}
