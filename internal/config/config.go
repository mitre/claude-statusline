// Package config loads the optional TOML config and overlays it onto the
// zero-config defaults (which exactly match the reference bash behavior).
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/mitre/claude-statusline/internal/render"
	"github.com/mitre/claude-statusline/internal/usage"
)

// Config is the full runtime configuration.
type Config struct {
	// Diagnostics are adjustments Load made to a value it could correct
	// without discarding the file (e.g. a TTL raised to the fetch floor);
	// the caller reports them on stderr.
	Diagnostics []string
	Options     render.Options
	Usage       struct {
		Enabled    bool
		TTLSeconds int
	}
	// GitTimeoutMS bounds each git read (Starship's command_timeout
	// pattern) on both engines; 0 disables the bound entirely.
	GitTimeoutMS int
	// GitEngine selects the git reader: "auto" (in-process go-git with
	// measured per-repo escalation to the CLI), "gogit", or "cli".
	GitEngine string
	// LockBadge surfaces a long-held .git/index.lock as a factual age badge
	// on the project row; LockBadgeAfterS is the age threshold in seconds
	// (a present lock younger than this is normal git behavior).
	LockBadge       bool
	LockBadgeAfterS int
	CacheDir        string
}

// fileSchema mirrors the TOML layout. Decoding into a pre-filled value gives
// overlay semantics: keys absent from the file keep their defaults.
type fileSchema struct {
	Rows struct {
		Model    *bool `toml:"model"`
		Project  *bool `toml:"project"`
		Context  *bool `toml:"context"`
		Account  *bool `toml:"account"`
		Limits   *bool `toml:"limits"` // deprecated alias for account
		Activity *bool `toml:"activity"`
	} `toml:"rows"`
	Model struct {
		ShowAuth        *bool    `toml:"show_auth"`
		ShowSession     *bool    `toml:"show_session"`
		ShowContextSize *bool    `toml:"show_context_size"`
		ShowEffort      *bool    `toml:"show_effort"`
		ShowFastMode    *bool    `toml:"show_fast_mode"`
		ShowMeteredCost *bool    `toml:"show_metered_cost"`
		ExtraBudget     *float64 `toml:"extra_budget_dollars"`
	} `toml:"model"`
	Context struct {
		Exceeds200kMarker *bool `toml:"exceeds_200k_marker"`
	} `toml:"context"`
	Project struct {
		ShowBranch     *bool   `toml:"show_branch"`
		ShowDirty      *bool   `toml:"show_dirty"`
		TildeHome      *bool   `toml:"tilde_home"`
		GitTimeoutMS   *int    `toml:"git_timeout_ms"` // 0 = unbounded
		GitEngine      *string `toml:"git_engine"`     // auto | gogit | cli
		LockBadge      *bool   `toml:"lock_badge"`
		LockBadgeAfter *int    `toml:"lock_badge_after_s"`
	} `toml:"project"`
	Account struct {
		ShowResets   *string `toml:"show_resets"` // "always" (default) | "quiet"
		ShowEmail    *bool   `toml:"show_email"`
		EmailStyle   *string `toml:"email_style"` // "normal" (default) | "dim"
		ShowStaleAge *bool   `toml:"show_stale_age"`
		Bars         *bool   `toml:"bars"`
	} `toml:"account"`
	Usage struct {
		Enabled    *bool `toml:"enabled"`
		TTLSeconds *int  `toml:"ttl_seconds"`
	} `toml:"usage"`
	Cache struct {
		Dir *string `toml:"dir"`
	} `toml:"cache"`
	Colors struct {
		Red    *string `toml:"red"`
		Green  *string `toml:"green"`
		Yellow *string `toml:"yellow"`
		Blue   *string `toml:"blue"`
		Cyan   *string `toml:"cyan"`
	} `toml:"colors"`
}

var hexColor = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)

// validColor accepts an ANSI index 0–255 or a "#rrggbb" hex.
func validColor(s string) bool {
	if hexColor.MatchString(s) {
		return true
	}
	n, err := strconv.Atoi(s)
	return err == nil && n >= 0 && n <= 255 && strconv.Itoa(n) == s
}

// minUsageTTLSeconds is the usage fetch floor (usage.MinFetchInterval) as a
// config value: the default TTL and the lowest one accepted.
var minUsageTTLSeconds = int(usage.MinFetchInterval / time.Second)

// Default returns the zero-config behavior.
func Default() Config {
	cfg := Config{Options: render.DefaultOptions()}
	cfg.Usage.Enabled = true
	cfg.Usage.TTLSeconds = minUsageTTLSeconds
	cfg.GitTimeoutMS = 150
	cfg.GitEngine = "auto"
	cfg.LockBadge = true
	// 300 s: research (2026-07-03) found no established staleness-age
	// convention in other tooling — git itself never expires index.lock —
	// so the threshold just needs to clear legitimate interactive holds
	// (an editor-open `git commit` is minutes-scale). Configurable.
	cfg.LockBadgeAfterS = 300
	cfg.CacheDir = defaultCacheDir()
	return cfg
}

// defaultCacheDir is the platform-correct per-user cache location
// (~/Library/Caches on macOS). Falls back to the system temp dir when the
// user cache dir is undeterminable (e.g. HOME unset).
func defaultCacheDir() string {
	base, err := os.UserCacheDir()
	if err != nil {
		base = os.TempDir()
	}
	return cacheDirIn(base)
}

// cacheDirIn is this program's directory under a per-user cache base.
func cacheDirIn(base string) string {
	return filepath.Join(base, "claude-statusline")
}

// GateDir is the directory of the shared usage fetch gate and its lock:
// this program's directory under the per-user cache base that base
// reports, whatever [cache] dir says, so every session waits on one gate.
// A base that is not resolved (no edge, an error, an empty or relative
// path) leaves the gate in the configured dir.
func GateDir(base func() (string, error), configured string) string {
	if base == nil {
		return configured
	}
	b, err := base()
	if err != nil || !filepath.IsAbs(b) {
		return configured
	}
	return cacheDirIn(b)
}

// Load reads path if it exists and overlays it onto defaults. A missing file
// is not an error; a malformed file is.
func Load(path string) (Config, error) {
	cfg := Default()
	if path == "" {
		return cfg, nil
	}

	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return cfg, err
	}

	var f fileSchema
	if err := toml.Unmarshal(data, &f); err != nil {
		return cfg, err
	}

	setB := func(dst *bool, src *bool) {
		if src != nil {
			*dst = *src
		}
	}
	setB(&cfg.Options.Rows.Model, f.Rows.Model)
	setB(&cfg.Options.Rows.Project, f.Rows.Project)
	setB(&cfg.Options.Rows.Context, f.Rows.Context)
	// Deprecated alias first, preferred key second: account wins when both set.
	setB(&cfg.Options.Rows.Account, f.Rows.Limits)
	setB(&cfg.Options.Rows.Account, f.Rows.Account)
	setB(&cfg.Options.Rows.Activity, f.Rows.Activity)
	setB(&cfg.Options.Model.ShowAuth, f.Model.ShowAuth)
	setB(&cfg.Options.Model.ShowSession, f.Model.ShowSession)
	setB(&cfg.Options.Model.ShowContextSize, f.Model.ShowContextSize)
	setB(&cfg.Options.Model.ShowEffort, f.Model.ShowEffort)
	setB(&cfg.Options.Model.ShowFastMode, f.Model.ShowFastMode)
	setB(&cfg.Options.Model.ShowMeteredCost, f.Model.ShowMeteredCost)
	if f.Model.ExtraBudget != nil {
		if *f.Model.ExtraBudget < 0 {
			return cfg, fmt.Errorf("model.extra_budget_dollars: %v is not valid (extra-usage spend you accept before the badge alarms; 0 alarms on any spend)", *f.Model.ExtraBudget)
		}
		cfg.Options.Model.ExtraBudget = *f.Model.ExtraBudget
	}
	setB(&cfg.Options.Context.Exceeds200kMarker, f.Context.Exceeds200kMarker)
	setB(&cfg.Options.Project.ShowBranch, f.Project.ShowBranch)
	setB(&cfg.Options.Project.ShowDirty, f.Project.ShowDirty)
	setB(&cfg.Options.Project.TildeHome, f.Project.TildeHome)
	if f.Project.GitTimeoutMS != nil {
		if *f.Project.GitTimeoutMS < 0 {
			return cfg, fmt.Errorf("project.git_timeout_ms: %d is not valid (>= 1 bounds each git call, 0 disables the deadline)", *f.Project.GitTimeoutMS)
		}
		cfg.GitTimeoutMS = *f.Project.GitTimeoutMS
	}
	if f.Project.GitEngine != nil {
		switch *f.Project.GitEngine {
		case "auto", "gogit", "cli":
			cfg.GitEngine = *f.Project.GitEngine
		default:
			return cfg, fmt.Errorf("project.git_engine: %q is not valid (use \"auto\", \"gogit\", or \"cli\")", *f.Project.GitEngine)
		}
	}
	setB(&cfg.LockBadge, f.Project.LockBadge)
	if f.Project.LockBadgeAfter != nil {
		if *f.Project.LockBadgeAfter < 0 {
			return cfg, fmt.Errorf("project.lock_badge_after_s: %d is not valid (seconds a lock must be held before the badge shows; 0 badges any lock)", *f.Project.LockBadgeAfter)
		}
		cfg.LockBadgeAfterS = *f.Project.LockBadgeAfter
	}
	if f.Account.ShowResets != nil {
		switch *f.Account.ShowResets {
		case "always":
			cfg.Options.Account.AlwaysShowResets = true
		case "quiet":
			cfg.Options.Account.AlwaysShowResets = false
		default:
			return cfg, fmt.Errorf("account.show_resets: %q is not valid (use \"always\" or \"quiet\")", *f.Account.ShowResets)
		}
	}
	setB(&cfg.Options.Account.ShowEmail, f.Account.ShowEmail)
	setB(&cfg.Options.Account.ShowStaleAge, f.Account.ShowStaleAge)
	setB(&cfg.Options.Account.Bars, f.Account.Bars)
	if f.Account.EmailStyle != nil {
		switch *f.Account.EmailStyle {
		case "dim":
			cfg.Options.Account.EmailDim = true
		case "normal":
			cfg.Options.Account.EmailDim = false
		default:
			return cfg, fmt.Errorf("account.email_style: %q is not valid (use \"dim\" or \"normal\")", *f.Account.EmailStyle)
		}
	}
	setB(&cfg.Usage.Enabled, f.Usage.Enabled)
	if f.Usage.TTLSeconds != nil {
		cfg.Usage.TTLSeconds = *f.Usage.TTLSeconds
		if *f.Usage.TTLSeconds < minUsageTTLSeconds {
			// Raised, not rejected: the v0.3.0 example shipped 180, and a
			// rejection would discard every other setting in the file.
			cfg.Usage.TTLSeconds = minUsageTTLSeconds
			cfg.Diagnostics = append(cfg.Diagnostics, fmt.Sprintf(
				"usage.ttl_seconds: %d raised to %d (the usage endpoint rate-limits faster polling)",
				*f.Usage.TTLSeconds, minUsageTTLSeconds))
		}
	}
	for _, col := range []struct {
		key string
		dst *string
		src *string
	}{
		{"red", &cfg.Options.Colors.Red, f.Colors.Red},
		{"green", &cfg.Options.Colors.Green, f.Colors.Green},
		{"yellow", &cfg.Options.Colors.Yellow, f.Colors.Yellow},
		{"blue", &cfg.Options.Colors.Blue, f.Colors.Blue},
		{"cyan", &cfg.Options.Colors.Cyan, f.Colors.Cyan},
	} {
		if col.src == nil {
			continue
		}
		if !validColor(*col.src) {
			return cfg, fmt.Errorf("colors.%s: %q is not valid (use an ANSI index 0-255 or a \"#rrggbb\" hex)", col.key, *col.src)
		}
		*col.dst = *col.src
	}
	if f.Cache.Dir != nil {
		cfg.CacheDir = *f.Cache.Dir
	}
	return cfg, nil
}
