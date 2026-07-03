// claude-statusline renders a multi-row status line for Claude Code.
//
// Claude Code invokes it on every render, passing session JSON on stdin;
// whatever is printed to stdout becomes the status line. Drop-in replacement
// for the bash reference in reference/statusline.sh, sharing its cache files.
//
// Optional config: ~/.claude/statusline.toml (see statusline.example.toml),
// overridable via $CLAUDE_STATUSLINE_CONFIG.
package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/mitre/claude-statusline/internal/usage"
)

func main() {
	out, diag := run(deps{
		stdin:      os.Stdin,
		getenv:     os.Getenv,
		runGit:     runGit,
		keychainOK: keychainCheck,
		fetchUsage: fetchUsage,
	})
	if diag != "" {
		fmt.Fprint(os.Stderr, diag)
	}
	// No trailing newline, matching the reference's printf '%b'.
	fmt.Print(out)
}

func runGit(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.Output()
	return string(out), err
}

func keychainCheck() error {
	return exec.Command("security", "find-generic-password",
		"-s", "Claude Code-credentials").Run()
}

// fetchUsage pulls the subscription-limits payload from the OAuth usage
// endpoint using the keychain access token.
func fetchUsage() ([]byte, error) {
	credRaw, err := exec.Command("security", "find-generic-password",
		"-s", "Claude Code-credentials", "-w").Output()
	if err != nil {
		return nil, err
	}
	token, err := usage.TokenFromKeychain(strings.TrimSpace(string(credRaw)))
	if err != nil {
		return nil, err
	}

	client := &http.Client{Timeout: 2 * time.Second}
	req, err := http.NewRequest(http.MethodGet, "https://api.anthropic.com/api/oauth/usage", nil)
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
