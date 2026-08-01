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
	if !o.Rows.Model || !o.Rows.Project || !o.Rows.Context || !o.Rows.Account || !o.Rows.Activity {
		t.Errorf("default rows not all enabled: %+v", o.Rows)
	}
	if !o.Model.ShowSession || !o.Project.ShowDirty || !o.Project.TildeHome {
		t.Errorf("default segments not all enabled: %+v", o)
	}
	if !cfg.Usage.Enabled || cfg.Usage.TTLSeconds != 180 {
		t.Errorf("usage defaults wrong: %+v", cfg.Usage)
	}
	// Full-Go contract: platform-correct per-user cache location, not the
	// bash era's world-shared /tmp path.
	ucd, err := os.UserCacheDir()
	if err != nil {
		t.Fatalf("UserCacheDir: %v", err)
	}
	if want := filepath.Join(ucd, "claude-statusline"); cfg.CacheDir != want {
		t.Errorf("CacheDir default = %q, want %q", cfg.CacheDir, want)
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

func TestLoadAccountShowResets(t *testing.T) {
	dir := t.TempDir()
	write := func(body string) string {
		p := filepath.Join(dir, "c.toml")
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}

	cfg, err := Load(write("[account]\nshow_resets = \"quiet\"\n"))
	if err != nil {
		t.Fatalf("Load quiet: %v", err)
	}
	if cfg.Options.Account.AlwaysShowResets {
		t.Error("show_resets=quiet not applied")
	}

	cfg, err = Load(write("[account]\nshow_resets = \"always\"\n"))
	if err != nil || !cfg.Options.Account.AlwaysShowResets {
		t.Errorf("show_resets=always: err=%v applied=%v", err, cfg.Options.Account.AlwaysShowResets)
	}

	if _, err = Load(write("[account]\nshow_resets = \"never\"\n")); err == nil {
		t.Error("invalid show_resets value must surface an error")
	}
}

func TestLoadRowsAccountWithLimitsAlias(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}

	cfg, err := Load(write("a.toml", "[rows]\naccount = false\n"))
	if err != nil || cfg.Options.Rows.Account {
		t.Errorf("rows.account=false: err=%v applied=%v", err, !cfg.Options.Rows.Account)
	}
	// Deprecated alias still honored.
	cfg, err = Load(write("b.toml", "[rows]\nlimits = false\n"))
	if err != nil || cfg.Options.Rows.Account {
		t.Errorf("rows.limits alias: err=%v applied=%v", err, !cfg.Options.Rows.Account)
	}
	// Both set: account wins.
	cfg, err = Load(write("c.toml", "[rows]\nlimits = false\naccount = true\n"))
	if err != nil || !cfg.Options.Rows.Account {
		t.Errorf("account precedence over limits alias: err=%v got=%v", err, cfg.Options.Rows.Account)
	}
}

func TestLoadProjectGitTimeout(t *testing.T) {
	dir := t.TempDir()
	write := func(body string) string {
		p := filepath.Join(dir, "c.toml")
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}

	cfg, err := Load(write("[project]\nshow_dirty = true\n"))
	if err != nil || cfg.GitTimeoutMS != 150 {
		t.Errorf("absent git_timeout_ms must default to 150: err=%v got=%d", err, cfg.GitTimeoutMS)
	}

	cfg, err = Load(write("[project]\ngit_timeout_ms = 50\n"))
	if err != nil || cfg.GitTimeoutMS != 50 {
		t.Errorf("git_timeout_ms=50: err=%v got=%d", err, cfg.GitTimeoutMS)
	}

	// 0 is the documented "unbounded" escape hatch, not an error.
	cfg, err = Load(write("[project]\ngit_timeout_ms = 0\n"))
	if err != nil || cfg.GitTimeoutMS != 0 {
		t.Errorf("git_timeout_ms=0 (unbounded): err=%v got=%d", err, cfg.GitTimeoutMS)
	}

	if _, err = Load(write("[project]\ngit_timeout_ms = -1\n")); err == nil {
		t.Error("negative git_timeout_ms must surface an error")
	}
}

func TestLoadModelExtraBudget(t *testing.T) {
	dir := t.TempDir()
	write := func(body string) string {
		p := filepath.Join(dir, "c.toml")
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}

	cfg, err := Load(write("[model]\nshow_auth = true\n"))
	if err != nil || cfg.Options.Model.ExtraBudget != 5.0 {
		t.Errorf("absent extra_budget_dollars must default to 5.00: err=%v got=%v",
			err, cfg.Options.Model.ExtraBudget)
	}

	// The owner's case: an allowance somebody already approved.
	cfg, err = Load(write("[model]\nextra_budget_dollars = 50.0\n"))
	if err != nil || cfg.Options.Model.ExtraBudget != 50.0 {
		t.Errorf("extra_budget_dollars=50.0: err=%v got=%v", err, cfg.Options.Model.ExtraBudget)
	}

	// Cents survive the dollars-to-minor-units conversion exactly.
	cfg, err = Load(write("[model]\nextra_budget_dollars = 2.42\n"))
	if err != nil || cfg.Options.Model.ExtraBudget != 2.42 {
		t.Errorf("extra_budget_dollars=2.42 must survive exactly: err=%v got=%v",
			err, cfg.Options.Model.ExtraBudget)
	}

	// 0 is the documented "alarm on any spend" setting, not an error.
	cfg, err = Load(write("[model]\nextra_budget_dollars = 0\n"))
	if err != nil || cfg.Options.Model.ExtraBudget != 0 {
		t.Errorf("extra_budget_dollars=0 (alarm on any spend): err=%v got=%v",
			err, cfg.Options.Model.ExtraBudget)
	}

	if _, err = Load(write("[model]\nextra_budget_dollars = -1.0\n")); err == nil {
		t.Error("negative extra_budget_dollars must surface an error")
	}
}

func TestLoadProjectGitEngine(t *testing.T) {
	dir := t.TempDir()
	write := func(body string) string {
		p := filepath.Join(dir, "c.toml")
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}

	cfg, err := Load(write("[project]\nshow_dirty = true\n"))
	if err != nil || cfg.GitEngine != "auto" {
		t.Errorf("absent git_engine must default to auto: err=%v got=%q", err, cfg.GitEngine)
	}
	for _, v := range []string{"auto", "gogit", "cli"} {
		cfg, err = Load(write("[project]\ngit_engine = \"" + v + "\"\n"))
		if err != nil || cfg.GitEngine != v {
			t.Errorf("git_engine=%s: err=%v got=%q", v, err, cfg.GitEngine)
		}
	}
	if _, err = Load(write("[project]\ngit_engine = \"fast\"\n")); err == nil {
		t.Error("invalid git_engine value must surface an error")
	}
}

func TestLoadAccountShowEmail(t *testing.T) {
	dir := t.TempDir()
	write := func(body string) string {
		p := filepath.Join(dir, "c.toml")
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}

	cfg, err := Load(write("[account]\nshow_resets = \"always\"\n"))
	if err != nil || !cfg.Options.Account.ShowEmail {
		t.Errorf("absent show_email must default to true: err=%v got=%v", err, cfg.Options.Account.ShowEmail)
	}
	cfg, err = Load(write("[account]\nshow_email = false\n"))
	if err != nil || cfg.Options.Account.ShowEmail {
		t.Errorf("show_email=false: err=%v got=%v", err, cfg.Options.Account.ShowEmail)
	}

	// email_style enum, show_resets pattern. Default is "normal" — picked
	// via live A/B comparison (2026-07-03).
	cfg, err = Load(write("[account]\nshow_email = true\n"))
	if err != nil || cfg.Options.Account.EmailDim {
		t.Errorf("absent email_style must default to normal: err=%v got=%v", err, cfg.Options.Account.EmailDim)
	}
	cfg, err = Load(write("[account]\nemail_style = \"normal\"\n"))
	if err != nil || cfg.Options.Account.EmailDim {
		t.Errorf("email_style=normal: err=%v dim=%v", err, cfg.Options.Account.EmailDim)
	}
	cfg, err = Load(write("[account]\nemail_style = \"dim\"\n"))
	if err != nil || !cfg.Options.Account.EmailDim {
		t.Errorf("email_style=dim: err=%v dim=%v", err, cfg.Options.Account.EmailDim)
	}
	if _, err = Load(write("[account]\nemail_style = \"bold\"\n")); err == nil {
		t.Error("invalid email_style value must surface an error")
	}
}

func TestLoadProjectLockBadge(t *testing.T) {
	dir := t.TempDir()
	write := func(body string) string {
		p := filepath.Join(dir, "c.toml")
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}

	// Defaults: badge on, threshold 300 s (research 2026-07-03: no
	// established age convention exists in other tooling — git never
	// auto-expires index.lock — so the conservative proposal stands).
	cfg, err := Load(write("[project]\ntilde_home = true\n"))
	if err != nil || !cfg.LockBadge || cfg.LockBadgeAfterS != 300 {
		t.Errorf("defaults: err=%v badge=%v after=%d; want true, 300", err, cfg.LockBadge, cfg.LockBadgeAfterS)
	}
	cfg, err = Load(write("[project]\nlock_badge = false\n"))
	if err != nil || cfg.LockBadge {
		t.Errorf("lock_badge=false: err=%v got=%v", err, cfg.LockBadge)
	}
	cfg, err = Load(write("[project]\nlock_badge_after_s = 60\n"))
	if err != nil || cfg.LockBadgeAfterS != 60 {
		t.Errorf("lock_badge_after_s=60: err=%v got=%d", err, cfg.LockBadgeAfterS)
	}
	if _, err = Load(write("[project]\nlock_badge_after_s = -1\n")); err == nil {
		t.Error("negative lock_badge_after_s must surface an error")
	}
}

func TestLoadAccountShowStaleAge(t *testing.T) {
	dir := t.TempDir()
	write := func(body string) string {
		p := filepath.Join(dir, "c.toml")
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}

	cfg, err := Load(write("[account]\nshow_resets = \"always\"\n"))
	if err != nil || !cfg.Options.Account.ShowStaleAge {
		t.Errorf("absent show_stale_age must default to true: err=%v got=%v", err, cfg.Options.Account.ShowStaleAge)
	}
	cfg, err = Load(write("[account]\nshow_stale_age = false\n"))
	if err != nil || cfg.Options.Account.ShowStaleAge {
		t.Errorf("show_stale_age=false: err=%v got=%v", err, cfg.Options.Account.ShowStaleAge)
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

func TestSegmentTogglesOverlay(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	p := filepath.Join(t.TempDir(), "s.toml")
	body := "[model]\nshow_effort = false\nshow_fast_mode = false\nshow_metered_cost = false\n" +
		"[context]\nexceeds_200k_marker = false\n"
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Options.Model.ShowEffort || cfg.Options.Model.ShowFastMode || cfg.Options.Model.ShowMeteredCost {
		t.Errorf("model segment toggles not overlaid: %+v", cfg.Options.Model)
	}
	if cfg.Options.Context.Exceeds200kMarker {
		t.Errorf("context.exceeds_200k_marker not overlaid: %+v", cfg.Options.Context)
	}
}

func TestSegmentToggleDefaultsOn(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg := Default()
	if !cfg.Options.Model.ShowEffort || !cfg.Options.Model.ShowFastMode ||
		!cfg.Options.Model.ShowMeteredCost || !cfg.Options.Context.Exceeds200kMarker {
		t.Errorf("segment toggles must default on: %+v", cfg.Options)
	}
}

func TestSegmentToggleInvalidTypeErrors(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	p := filepath.Join(t.TempDir(), "s.toml")
	if err := os.WriteFile(p, []byte("[model]\nshow_effort = \"loud\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(p); err == nil {
		t.Error("a non-bool show_effort must surface a config error, not fall through silently")
	}
}
