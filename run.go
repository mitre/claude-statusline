package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/mitre/claude-statusline/internal/account"
	"github.com/mitre/claude-statusline/internal/auth"
	"github.com/mitre/claude-statusline/internal/config"
	"github.com/mitre/claude-statusline/internal/gitinfo"
	"github.com/mitre/claude-statusline/internal/input"
	"github.com/mitre/claude-statusline/internal/render"
	"github.com/mitre/claude-statusline/internal/usage"
)

// deps are run's injectable edges: everything that touches the OS, network,
// or environment. main() wires the real implementations; tests wire fakes.
type deps struct {
	stdin      io.Reader
	getenv     func(string) string
	runGit     runGitCtx
	keychainOK func() error
	fetchUsage func() ([]byte, error)
	readFile   func(string) ([]byte, error)
}

// runGitCtx executes git under a context so a deadline can actually kill the
// child process (exec.CommandContext in the real runner, not fire-and-forget).
type runGitCtx func(ctx context.Context, dir string, args ...string) (string, error)

// withGitDeadline adapts a context-aware git runner to gitinfo's RunGit,
// bounding each call at timeout (Starship's command_timeout pattern);
// timeout 0 disables the bound. On expiry the runner surfaces an error,
// which flows into gitinfo's existing stale-cache fallback.
func withGitDeadline(run runGitCtx, timeout time.Duration) gitinfo.RunGit {
	return func(dir string, args ...string) (string, error) {
		ctx := context.Background()
		if timeout > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, timeout)
			defer cancel()
		}
		return run(ctx, dir, args...)
	}
}

// run renders one statusline frame. It returns the frame for stdout and any
// diagnostic for stderr; it never fails hard — a statusline must not crash
// the host UI.
func run(d deps) (string, string) {
	sess, err := input.Parse(d.stdin)
	if err != nil {
		// Unparseable stdin: render nothing rather than garbage.
		return "", ""
	}

	var diag string
	cfg, err := config.Load(configPath(d.getenv))
	if err != nil {
		cfg = config.Default()
		diag = fmt.Sprintf("claude-statusline: config error: %v\n", err)
	}
	_ = os.MkdirAll(cfg.CacheDir, 0o700)

	badge, apiKeySet := auth.Detect(cfg.CacheDir, d.getenv, d.keychainOK)

	st := render.State{
		Model:        sess.ModelName,
		CtxSize:      sess.CtxSize,
		Auth:         badge,
		SessionID:    sess.SessionID,
		CWD:          sess.CWD,
		Home:         d.getenv("HOME"),
		CtxPct:       sess.CtxPct,
		DurationMS:   sess.DurationMS,
		LinesAdded:   sess.LinesAdded,
		LinesRemoved: sess.LinesRemoved,
		APIKeySet:    apiKeySet,
		Effort:       sess.Effort,
		FastMode:     sess.FastMode,
		CostUSD:      sess.CostUSD,
		Exceeds200k:  sess.Exceeds200k,
	}

	if sess.CWD != "" {
		deadline := time.Duration(cfg.GitTimeoutMS) * time.Millisecond
		st.Branch, st.Dirty, st.LockAge = gitinfo.Get(gitinfo.Options{
			CacheDir:       cfg.CacheDir,
			Run:            withGitDeadline(d.runGit, deadline),
			Engine:         cfg.GitEngine,
			Budget:         deadline,
			LockBadge:      cfg.LockBadge,
			LockBadgeAfter: time.Duration(cfg.LockBadgeAfterS) * time.Second,
		}, sess.CWD)
	} else {
		st.Branch = "?"
	}

	if cfg.Usage.Enabled && badge == "Sub" {
		ttl := time.Duration(cfg.Usage.TTLSeconds) * time.Second
		now := time.Now()
		var u render.Usage
		have := false
		if raw, staleFor, ok := usage.Resolve(cfg.CacheDir, ttl, now, d.fetchUsage); ok {
			family := usage.FamilyFromModelName(sess.ModelName)
			if p, uerr := usage.Parse(raw, now, family); uerr == nil {
				u, have = p, true
				u.DataAge = staleFor
			}
		}
		// The stdin payload carries the two all-model meters since v2.1.210.
		// When the endpoint data is stale — or absent entirely (the row used
		// to collapse) — this render's live values replace those meters:
		// fresher by definition. Endpoint-only segments (model window, extra
		// usage) keep their stale-good semantics, and the render layer scopes
		// the dim age marker to the stale data that remains visible.
		if sess.RateLimitsOK && (!have || u.DataAge > 0) {
			u.U5, u.R5 = sess.R5Pct, usage.ResetLabelUnix(sess.R5ResetUnix, now)
			u.U7, u.R7 = sess.R7Pct, usage.ResetLabelUnix(sess.R7ResetUnix, now)
			u.MetersLive = true
			have = true
		}
		if have {
			u.Email = account.Email(d.getenv("HOME"), d.readFile)
			st.Usage = &u
		}
	}

	return render.Build(st, cfg.Options), diag
}

func configPath(getenv func(string) string) string {
	if p := getenv("CLAUDE_STATUSLINE_CONFIG"); p != "" {
		return p
	}
	return filepath.Join(getenv("HOME"), ".claude", "statusline.toml")
}
