package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadMissingFileGivesDefaults(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "absent.toml"))
	if err != nil {
		t.Fatalf("missing config must not error: %v", err)
	}
	o := cfg.Options
	if !o.Rows.Model || !o.Rows.Project || !o.Rows.Context || !o.Rows.Limits || !o.Rows.Activity {
		t.Errorf("default rows not all enabled: %+v", o.Rows)
	}
	if !o.Model.ShowSession || !o.Project.ShowDirty || !o.Project.TildeHome {
		t.Errorf("default segments not all enabled: %+v", o)
	}
	if !cfg.Usage.Enabled || cfg.Usage.TTLSeconds != 180 {
		t.Errorf("usage defaults wrong: %+v", cfg.Usage)
	}
	if cfg.CacheDir != "/tmp/.claude-statusline-cache" {
		t.Errorf("CacheDir default = %q", cfg.CacheDir)
	}
}

func TestLoadPartialOverlayKeepsOtherDefaults(t *testing.T) {
	p := filepath.Join(t.TempDir(), "statusline.toml")
	body := `
[rows]
activity = false

[model]
show_session = false

[usage]
ttl_seconds = 300
`
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Options.Rows.Activity {
		t.Error("rows.activity=false not applied")
	}
	if cfg.Options.Model.ShowSession {
		t.Error("model.show_session=false not applied")
	}
	if cfg.Usage.TTLSeconds != 300 {
		t.Errorf("usage.ttl_seconds = %d, want 300", cfg.Usage.TTLSeconds)
	}
	// Untouched keys keep defaults.
	if !cfg.Options.Rows.Model || !cfg.Options.Project.ShowDirty || !cfg.Usage.Enabled {
		t.Errorf("unrelated defaults disturbed: %+v", cfg)
	}
}

func TestLoadUnreadablePathSurfacesError(t *testing.T) {
	dir := t.TempDir() // a directory: ReadFile fails with EISDIR, not ErrNotExist
	if _, err := Load(dir); err == nil {
		t.Errorf("Load(%q) = nil error, want unreadable-file error", dir)
	}
}

func TestLoadRejectsMalformedTOML(t *testing.T) {
	p := filepath.Join(t.TempDir(), "bad.toml")
	if err := os.WriteFile(p, []byte("rows = [unclosed"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(p); err == nil {
		t.Error("malformed TOML must surface an error, not silently default")
	}
}
