// Package config loads the optional TOML config and overlays it onto the
// zero-config defaults (which exactly match the reference bash behavior).
package config

import (
	"errors"
	"io/fs"
	"os"

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
	CacheDir string
}

// fileSchema mirrors the TOML layout. Decoding into a pre-filled value gives
// overlay semantics: keys absent from the file keep their defaults.
type fileSchema struct {
	Rows struct {
		Model    *bool `toml:"model"`
		Project  *bool `toml:"project"`
		Context  *bool `toml:"context"`
		Limits   *bool `toml:"limits"`
		Activity *bool `toml:"activity"`
	} `toml:"rows"`
	Model struct {
		ShowAuth        *bool `toml:"show_auth"`
		ShowSession     *bool `toml:"show_session"`
		ShowContextSize *bool `toml:"show_context_size"`
	} `toml:"model"`
	Project struct {
		ShowBranch *bool `toml:"show_branch"`
		ShowDirty  *bool `toml:"show_dirty"`
		TildeHome  *bool `toml:"tilde_home"`
	} `toml:"project"`
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
	cfg.CacheDir = "/tmp/.claude-statusline-cache"
	return cfg
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
	setB(&cfg.Options.Rows.Limits, f.Rows.Limits)
	setB(&cfg.Options.Rows.Activity, f.Rows.Activity)
	setB(&cfg.Options.Model.ShowAuth, f.Model.ShowAuth)
	setB(&cfg.Options.Model.ShowSession, f.Model.ShowSession)
	setB(&cfg.Options.Model.ShowContextSize, f.Model.ShowContextSize)
	setB(&cfg.Options.Project.ShowBranch, f.Project.ShowBranch)
	setB(&cfg.Options.Project.ShowDirty, f.Project.ShowDirty)
	setB(&cfg.Options.Project.TildeHome, f.Project.TildeHome)
	setB(&cfg.Usage.Enabled, f.Usage.Enabled)
	if f.Usage.TTLSeconds != nil {
		cfg.Usage.TTLSeconds = *f.Usage.TTLSeconds
	}
	if f.Cache.Dir != nil {
		cfg.CacheDir = *f.Cache.Dir
	}
	return cfg, nil
}
