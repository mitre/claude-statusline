// Package config loads the optional TOML config and overlays it onto the
// zero-config defaults (which exactly match the reference bash behavior).
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
	"github.com/mitre/claude-statusline/internal/render"
)

// Config is the full runtime configuration.
type Config struct {
	Options render.Options
	Usage   struct {
		Enabled    bool
		TTLSeconds int
	}
	// GitTimeoutMS bounds each git read (Starship's command_timeout
	// pattern) on both engines; 0 disables the bound entirely.
	GitTimeoutMS int
	// GitEngine selects the git reader: "auto" (in-process go-git with
	// measured per-repo escalation to the CLI), "gogit", or "cli".
	GitEngine string
	CacheDir  string
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
		ShowAuth        *bool `toml:"show_auth"`
		ShowSession     *bool `toml:"show_session"`
		ShowContextSize *bool `toml:"show_context_size"`
	} `toml:"model"`
	Project struct {
		ShowBranch   *bool   `toml:"show_branch"`
		ShowDirty    *bool   `toml:"show_dirty"`
		TildeHome    *bool   `toml:"tilde_home"`
		GitTimeoutMS *int    `toml:"git_timeout_ms"` // 0 = unbounded
		GitEngine    *string `toml:"git_engine"`     // auto | gogit | cli
	} `toml:"project"`
	Account struct {
		ShowResets *string `toml:"show_resets"` // "always" (default) | "quiet"
	} `toml:"account"`
	Usage struct {
		Enabled    *bool `toml:"enabled"`
		TTLSeconds *int  `toml:"ttl_seconds"`
	} `toml:"usage"`
	Cache struct {
		Dir *string `toml:"dir"`
	} `toml:"cache"`
}

// Default returns the zero-config behavior.
func Default() Config {
	cfg := Config{Options: render.DefaultOptions()}
	cfg.Usage.Enabled = true
	cfg.Usage.TTLSeconds = 180
	cfg.GitTimeoutMS = 150
	cfg.GitEngine = "auto"
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
	return filepath.Join(base, "claude-statusline")
}

// Load reads path if it exists and overlays it onto defaults. A missing file
// is not an error; a malformed file is.
func Load(path string) (Config, error) {
	cfg := Default()

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
	setB(&cfg.Usage.Enabled, f.Usage.Enabled)
	if f.Usage.TTLSeconds != nil {
		cfg.Usage.TTLSeconds = *f.Usage.TTLSeconds
	}
	if f.Cache.Dir != nil {
		cfg.CacheDir = *f.Cache.Dir
	}
	return cfg, nil
}
