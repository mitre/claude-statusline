package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
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
	// credentialSource names the credential storage location keychainOK
	// and fetchUsage resolve (never the token); the login-status cache is
	// keyed by it. nil means no location is known.
	credentialSource func() string
	fetchUsage       func() ([]byte, error)
	readFile         func(string) ([]byte, error)
	stat             func(string) (os.FileInfo, error)
	// readDir lists a directory for the account resolver's exact-name
	// check on the legacy record; nil means os.ReadDir.
	readDir func(string) ([]os.DirEntry, error)
	// now is the render's clock, handed to the usage resolver and parser
	// (fetch gate, reset-boundary expiry, stale age, reset labels); nil
	// means time.Now. The TTL check inside cache.ReadFresh reads the wall
	// clock itself.
	now func() time.Time
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
	stat := d.stat
	if stat == nil {
		stat = os.Stat
	}
	readDir := d.readDir
	if readDir == nil {
		readDir = os.ReadDir
	}
	clock := d.now
	if clock == nil {
		clock = time.Now
	}
	cfg, err := config.Load(configPath(sess.Product, d.getenv, stat))
	if err != nil {
		cfg = config.Default()
		diag = fmt.Sprintf("claude-statusline: config error: %v\n", err)
	}
	for _, note := range cfg.Diagnostics {
		diag += "claude-statusline: config: " + note + "\n"
	}
	_ = os.MkdirAll(cfg.CacheDir, 0o700)

	source := ""
	if d.credentialSource != nil {
		source = d.credentialSource()
	}
	badge, apiKeySet := auth.Detect(cfg.CacheDir, source, d.getenv, d.keychainOK)
	if sess.Product == "antigravity" {
		apiKeySet = false
		switch {
		case strings.Contains(sess.PlanTier, "Ultra"):
			badge = "Ultra"
		case strings.Contains(sess.PlanTier, "Pro"):
			badge = "Pro"
		case sess.PlanTier != "":
			badge = sess.PlanTier
		default:
			badge = "Sub"
		}
	}

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

	if cfg.Usage.Enabled && badge != "API" && badge != "?" {
		ttl := time.Duration(cfg.Usage.TTLSeconds) * time.Second
		now := clock()
		var u render.Usage
		have := false
		if sess.Product != "antigravity" {
			if raw, staleFor, ok := usage.Resolve(cfg.CacheDir, ttl, now, d.fetchUsage); ok {
				if p, uerr := usage.Parse(raw, now, sess.ModelName); uerr == nil {
					u, have = p, true
					u.DataAge = staleFor
				}
			}
		}
		// The stdin payload carries the all-model meters since v2.1.210.
		// When the endpoint data is stale — or absent entirely (the row used
		// to collapse) — this render's live values replace those meters:
		// fresher by definition. Endpoint-only segments (a weekly reading
		// stdin did not replace, scoped limits, the model window, extra
		// usage) keep their stale-good semantics, and the render layer scopes
		// the dim age marker to the stale account data that remains visible.
		// Fresh endpoint data keeps its readings; a live stdin reading only
		// fills a window the fresh payload reports as not in progress.
		switch {
		case sess.RateLimitsOK && have && u.DataAge == 0:
			if u.FiveHourAbsent {
				u.FiveHourAbsent = false
				u.U5, u.R5 = sess.R5Pct, usage.ResetLabelUnix(sess.R5ResetUnix, now)
			}
			if u.SevenDayAbsent && sess.R7OK {
				u.SevenDayAbsent = false
				u.U7, u.R7 = sess.R7Pct, usage.ResetLabelUnix(sess.R7ResetUnix, now)
			}
		case sess.RateLimitsOK:
			// The host's live readings supersede the endpoint's. The
			// five-hour reading is always present within RateLimitsOK; the
			// weekly one may not be, and without it the endpoint's weekly
			// reading (or its absence) stands — never replaced by 0%.
			u.FiveHourAbsent = false
			u.U5, u.R5 = sess.R5Pct, usage.ResetLabelUnix(sess.R5ResetUnix, now)
			if sess.R7OK {
				u.SevenDayAbsent = false
				u.U7, u.R7 = sess.R7Pct, usage.ResetLabelUnix(sess.R7ResetUnix, now)
			}
			// Taken before the stdin scoped meters are appended: only the
			// endpoint's own scoped limits are stale.
			endpointScoped := len(u.Scoped) > 0
			for _, sc := range sess.ScopedMeters {
				u.Scoped = append(u.Scoped, render.ScopedLimit{
					Name:  sc.Name,
					Pct:   sc.Pct,
					Reset: usage.ResetLabelUnix(sc.ResetUnix, now),
				})
			}
			// Every visible 5h/week/scoped meter is live when the weekly
			// window came from stdin or is absent, and no endpoint scoped
			// limit remains. The model window qualifies separately.
			u.MetersLive = (sess.R7OK || u.SevenDayAbsent) && !endpointScoped
			have = true
		}
		if have {
			if sess.Email != "" {
				u.Email = sess.Email
			} else {
				u.Email = account.Resolve(d.getenv, account.FS{ReadFile: d.readFile, Stat: stat, ReadDir: readDir}).Email
			}
			u.PlanTier = sess.PlanTier
			st.Usage = &u
		}
	}

	return render.Build(st, cfg.Options), diag
}

func configPath(product string, getenv func(string) string, stat func(string) (os.FileInfo, error)) string {
	if p := getenv("ANTIGRAVITY_STATUSLINE_CONFIG"); p != "" {
		return p
	}
	if p := getenv("CLAUDE_STATUSLINE_CONFIG"); p != "" {
		return p
	}
	home := getenv("HOME")
	if home == "" {
		return ""
	}
	if product == "antigravity" {
		agPath := filepath.Join(home, ".gemini", "antigravity-cli", "statusline.toml")
		if stat != nil {
			if _, err := stat(agPath); err == nil {
				return agPath
			}
		}
	}
	return filepath.Join(home, ".claude", "statusline.toml")
}
