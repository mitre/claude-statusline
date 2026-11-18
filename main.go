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
	"math"
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
		tokenFingerprint: creds.fingerprint,
		fetchUsage:       func(fingerprint string) ([]byte, error) { return fetchUsage(creds, fingerprint) },
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
func fetchUsage(creds tokenSource, fingerprint string) ([]byte, error) {
	token, err := creds.tokenFor(fingerprint)
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
		return nil, &usage.RetryAfterError{After: retryAfter(resp.Header.Get("Retry-After"), time.Now())}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if cerr := resp.Body.Close(); err == nil {
		err = cerr
	}
	return body, err
}

// retryAfter reads a Retry-After header (RFC 9110 §10.2.3) received at now:
// delay-seconds (digits only) or an HTTP-date in any of the three forms
// net/http parses. A value that gives no wait, does not fit a
// time.Duration, or is not one of those forms yields zero, which the
// resolver treats as "apply the floor". The upper bound is the resolver's
// (usage.MaxRetryAfter), not this parser's.
func retryAfter(header string, now time.Time) time.Duration {
	h := strings.TrimSpace(header)
	if h == "" {
		return 0
	}
	if strings.Trim(h, "0123456789") == "" {
		secs, err := strconv.ParseInt(h, 10, 64)
		if err != nil || secs <= 0 || secs > math.MaxInt64/int64(time.Second) {
			return 0
		}
		return time.Duration(secs) * time.Second
	}
	at, err := http.ParseTime(h)
	if err != nil {
		return 0
	}
	if wait := at.Sub(now); wait > 0 {
		return wait
	}
	return 0
}
