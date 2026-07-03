package render

import (
	"strings"
	"testing"
)

// Golden strings in this file ARE the display spec. Provenance: captured
// byte-for-byte from the original bash implementation (git history) on
// 2026-07-03, evolved deliberately since.

const home = "/Users/dev"

func TestModelRowFullState(t *testing.T) {
	got := ModelRow("Fable 5", 1000000, "Sub", "0a1b2c3d-0000-4000-8000-000000000000", "", false, DefaultOptions())
	want := "\x1b[2mmodel    \x1b[0m\x1b[1m\x1b[36mFable 5 1M\x1b[0m\x1b[2m · \x1b[0m\x1b[32mSub\x1b[0m\x1b[2m · session 0a1b2c3d\x1b[0m"
	if got != want {
		t.Errorf("ModelRow full:\n got %q\nwant %q", got, want)
	}
}

func TestModelRowContextSizeLabel(t *testing.T) {
	got := ModelRow("Fable 5", 200000, "Sub", "", "", false, DefaultOptions())
	want := "\x1b[2mmodel    \x1b[0m\x1b[1m\x1b[36mFable 5 200k\x1b[0m\x1b[2m · \x1b[0m\x1b[32mSub\x1b[0m"
	if got != want {
		t.Errorf("ModelRow 200k:\n got %q\nwant %q", got, want)
	}
	// Name already stating context: no size appended (mirrors *ontext* glob).
	got = ModelRow("Opus 4.8 (1M context)", 1000000, "?", "", "", false, DefaultOptions())
	if strings.Contains(got, "context) 1M") {
		t.Errorf("size appended despite name stating context: %q", got)
	}
}

func TestModelRowAPIKeyWarning(t *testing.T) {
	got := ModelRow("Fable 5", 200000, "API", "", "", true, DefaultOptions())
	wantAuth := "\x1b[2m · \x1b[0m\x1b[33mAPI\x1b[0m"
	wantWarn := " \x1b[41m\x1b[37m\x1b[1m ⚠ API KEY SET — METERED BILLING \x1b[0m"
	if !strings.Contains(got, wantAuth) {
		t.Errorf("missing yellow API auth badge: %q", got)
	}
	if !strings.HasSuffix(got, wantWarn) {
		t.Errorf("missing metered-billing warning: %q", got)
	}
}

func TestProjectRow(t *testing.T) {
	got := ProjectRow("/Users/dev/projects/demo-app", home, "main", 2, DefaultOptions())
	want := "\x1b[2mproject  \x1b[0m\x1b[1m~/projects/demo-app\x1b[0m \x1b[2m·\x1b[0m \x1b[34m⎇ main\x1b[0m \x1b[33m~2\x1b[0m"
	if got != want {
		t.Errorf("ProjectRow:\n got %q\nwant %q", got, want)
	}
}

func TestProjectRowCleanTreeHidesDirty(t *testing.T) {
	got := ProjectRow("/Users/dev/x", home, "main", 0, DefaultOptions())
	if strings.Contains(got, "~0") || strings.Contains(got, "\x1b[33m") {
		t.Errorf("dirty badge rendered for clean tree: %q", got)
	}
}

func TestProjectRowOutsideHomeKeepsFullPath(t *testing.T) {
	got := ProjectRow("/opt/work", home, "main", 0, DefaultOptions())
	if !strings.Contains(got, "\x1b[1m/opt/work\x1b[0m") {
		t.Errorf("path outside home was altered: %q", got)
	}
}

func TestContextRowThresholds(t *testing.T) {
	cases := []struct {
		pct  int
		want string
	}{
		{30, "\x1b[2mcontext  \x1b[0m\x1b[32m▓▓▓░░░░░░░\x1b[0m \x1b[32m30%\x1b[0m"},
		{60, "\x1b[2mcontext  \x1b[0m\x1b[33m▓▓▓▓▓▓░░░░\x1b[0m \x1b[33m60%\x1b[0m"},
		{88, "\x1b[2mcontext  \x1b[0m\x1b[31m▓▓▓▓▓▓▓▓░░\x1b[0m \x1b[31m\x1b[1m88%\x1b[0m \x1b[41m\x1b[37m\x1b[1m /compact \x1b[0m"},
	}
	for _, c := range cases {
		if got := ContextRow(c.pct); got != c.want {
			t.Errorf("ContextRow(%d):\n got %q\nwant %q", c.pct, got, c.want)
		}
	}
}

func TestAccountRow(t *testing.T) {
	// No reset labels in the payload: nothing to show in either mode.
	got := AccountRow(Usage{U5: 5, U7: 13}, DefaultOptions())
	want := "\x1b[2maccount  \x1b[0m\x1b[32m5h 5%\x1b[0m \x1b[2m·\x1b[0m \x1b[32mweek 13%\x1b[0m"
	if got != want {
		t.Errorf("AccountRow green:\n got %q\nwant %q", got, want)
	}
}

func TestAccountRowAlwaysShowsResetsBelowThreshold(t *testing.T) {
	in := Usage{U5: 5, R5: "1:30p", U7: 13, R7: "Mon 10a"}
	got := AccountRow(in, DefaultOptions()) // default show_resets = always
	want := "\x1b[2maccount  \x1b[0m\x1b[32m5h 5%\x1b[0m \x1b[2m(resets 1:30p)\x1b[0m \x1b[2m·\x1b[0m \x1b[32mweek 13%\x1b[0m \x1b[2m(resets Mon 10a)\x1b[0m"
	if got != want {
		t.Errorf("AccountRow always/low(%+v):\n got %q\nwant %q", in, got, want)
	}
}

func TestAccountRowQuietModeIsHotOnly(t *testing.T) {
	opts := DefaultOptions()
	opts.Account.AlwaysShowResets = false // "quiet"

	// Below threshold: resets hidden.
	in := Usage{U5: 5, R5: "1:30p", U7: 13, R7: "Mon 10a"}
	got := AccountRow(in, opts)
	want := "\x1b[2maccount  \x1b[0m\x1b[32m5h 5%\x1b[0m \x1b[2m·\x1b[0m \x1b[32mweek 13%\x1b[0m"
	if got != want {
		t.Errorf("AccountRow quiet/low(%+v):\n got %q\nwant %q", in, got, want)
	}

	// Hot window: quiet is NOT never — reset surfaces at >=80%.
	in = Usage{U5: 82, R5: "1:30p", U7: 55}
	got = AccountRow(in, opts)
	want = "\x1b[2maccount  \x1b[0m\x1b[31m5h 82%\x1b[0m \x1b[2m(resets 1:30p)\x1b[0m \x1b[2m·\x1b[0m \x1b[33mweek 55%\x1b[0m"
	if got != want {
		t.Errorf("AccountRow quiet/hot(%+v):\n got %q\nwant %q", in, got, want)
	}
}

func TestAccountRowModelSegment(t *testing.T) {
	in := Usage{
		U5: 28, R5: "1:30p", U7: 18, R7: "Mon 10a",
		ModelFamily: "opus", ModelPct: 41, ModelReset: "Tue 3:00p",
	}
	got := AccountRow(in, DefaultOptions())
	want := "\x1b[2maccount  \x1b[0m\x1b[32m5h 28%\x1b[0m \x1b[2m(resets 1:30p)\x1b[0m \x1b[2m·\x1b[0m \x1b[32mweek 18%\x1b[0m \x1b[2m(resets Mon 10a)\x1b[0m \x1b[2m·\x1b[0m \x1b[32mopus/wk 41%\x1b[0m \x1b[2m(resets Tue 3:00p)\x1b[0m"
	if got != want {
		t.Errorf("AccountRow model segment(%+v):\n got %q\nwant %q", in, got, want)
	}
}

func TestAccountRowOmitsUnknownModelFamily(t *testing.T) {
	// No matching payload window (e.g. fable): segment omitted, never 0%.
	got := AccountRow(Usage{U5: 28, U7: 18, ModelFamily: ""}, DefaultOptions())
	if strings.Contains(got, "/wk") || strings.Contains(got, "0%") && strings.Count(got, "%") > 2 {
		t.Errorf("model segment rendered without data: %q", got)
	}
}

func TestActivityRow(t *testing.T) {
	got := ActivityRow(62580000, 1598, 8)
	want := "\x1b[2mactivity \x1b[0m\x1b[2m17h23m\x1b[0m \x1b[2m·\x1b[0m \x1b[32m+1,598\x1b[0m/\x1b[31m-8\x1b[0m \x1b[2mlines\x1b[0m"
	if got != want {
		t.Errorf("ActivityRow:\n got %q\nwant %q", got, want)
	}
	if got := ActivityRow(0, 0, 0); got != "" {
		t.Errorf("empty activity should collapse, got %q", got)
	}
	// Short session: minutes+seconds form, no churn segment.
	got = ActivityRow(90000, 0, 0)
	want = "\x1b[2mactivity \x1b[0m\x1b[2m1m30s\x1b[0m"
	if got != want {
		t.Errorf("ActivityRow short:\n got %q\nwant %q", got, want)
	}
}

func TestAccountRowHotWeekShowsResetInQuietMode(t *testing.T) {
	opts := DefaultOptions()
	opts.Account.AlwaysShowResets = false // quiet: hot week must still surface
	in := Usage{U5: 10, U7: 91, R7: "Mon 10:00a"}
	got := AccountRow(in, opts)
	want := "\x1b[2maccount  \x1b[0m\x1b[32m5h 10%\x1b[0m \x1b[2m·\x1b[0m \x1b[31mweek 91%\x1b[0m \x1b[2m(resets Mon 10:00a)\x1b[0m"
	if got != want {
		t.Errorf("AccountRow quiet/hot-week(%+v):\n got %q\nwant %q", in, got, want)
	}
}

func TestExtraBadgeEnabledNoSpendIsSilent(t *testing.T) {
	in := Usage{ExtraEnabled: true, CreditsMinor: 0, CreditsExp: 2, U5: 20}
	if got := ExtraBadge(in); got != "" {
		t.Errorf("ExtraBadge(%+v) = %q, want empty (enabled, zero spend, within limits)", in, got)
	}
}

func TestContextRowClampsAbove100(t *testing.T) {
	got := ContextRow(120)
	want := "\x1b[2mcontext  \x1b[0m\x1b[31m▓▓▓▓▓▓▓▓▓▓\x1b[0m \x1b[31m\x1b[1m120%\x1b[0m \x1b[41m\x1b[37m\x1b[1m /compact \x1b[0m"
	if got != want {
		t.Errorf("ContextRow(120):\n got %q\nwant %q", got, want)
	}
}

func TestExtraBadge(t *testing.T) {
	// Actively over a limit and burning credits: loud badge.
	got := ExtraBadge(Usage{U5: 100, ExtraEnabled: true, CreditsMinor: 150, CreditsExp: 2})
	want := "  \x1b[41m\x1b[37m\x1b[1m ⚠ EXTRA USAGE $1.50 \x1b[0m"
	if got != want {
		t.Errorf("ExtraBadge loud:\n got %q\nwant %q", got, want)
	}
	// Enabled with spend but within limits: dim tally.
	got = ExtraBadge(Usage{U5: 20, ExtraEnabled: true, CreditsMinor: 150, CreditsExp: 2})
	want = "\x1b[2m · extra $1.50\x1b[0m"
	if got != want {
		t.Errorf("ExtraBadge dim:\n got %q\nwant %q", got, want)
	}
	// Disabled: nothing.
	if got := ExtraBadge(Usage{U5: 100, ExtraEnabled: false, CreditsMinor: 150}); got != "" {
		t.Errorf("ExtraBadge disabled should be empty, got %q", got)
	}
}

func TestBuildAssemblesAndCollapses(t *testing.T) {
	st := State{
		Model: "Fable 5", CtxSize: 1000000, Auth: "Sub",
		SessionID: "0a1b2c3d-0000-4000-8000-000000000000",
		CWD:       "/Users/dev/projects/demo-app",
		Home:      home, Branch: "main", Dirty: 2, CtxPct: 30,
		Usage:      &Usage{U5: 5, U7: 13},
		DurationMS: 62580000, LinesAdded: 1598, LinesRemoved: 8,
	}
	got := Build(st, DefaultOptions())
	lines := strings.Split(got, "\n")
	if len(lines) != 5 {
		t.Fatalf("want 5 rows, got %d:\n%q", len(lines), got)
	}

	// Degraded: no usage, no activity, no session — 3 rows, no empties.
	st.Usage = nil
	st.DurationMS, st.LinesAdded, st.LinesRemoved = 0, 0, 0
	st.SessionID = ""
	got = Build(st, DefaultOptions())
	lines = strings.Split(got, "\n")
	if len(lines) != 3 {
		t.Fatalf("degraded: want 3 rows, got %d:\n%q", len(lines), got)
	}
	for _, l := range lines {
		if strings.TrimSpace(l) == "" {
			t.Errorf("blank row rendered: %q", got)
		}
	}
}

func TestOptionsToggles(t *testing.T) {
	st := State{
		Model: "Fable 5", CtxSize: 1000000, Auth: "Sub", SessionID: "0a1b2c3d-x",
		CWD: "/Users/dev/p", Home: home, Branch: "main", Dirty: 2, CtxPct: 30,
	}
	opts := DefaultOptions()
	opts.Model.ShowSession = false
	opts.Project.ShowDirty = false
	opts.Rows.Activity = false
	got := Build(st, opts)
	if strings.Contains(got, "session") {
		t.Errorf("session shown despite toggle off: %q", got)
	}
	if strings.Contains(got, "~2") {
		t.Errorf("dirty shown despite toggle off: %q", got)
	}

	opts = DefaultOptions()
	opts.Rows.Project = false
	got = Build(st, opts)
	if strings.Contains(got, "⎇") {
		t.Errorf("project row shown despite row toggle off: %q", got)
	}
}
