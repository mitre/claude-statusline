package render

import (
	"strings"
	"testing"
	"time"
)

// Golden strings in this file ARE the display spec. Provenance: captured
// byte-for-byte from the original bash implementation (git history) on
// 2026-07-03, evolved deliberately since.

const home = "/Users/dev"

func TestRenderIsEnvironmentIndependent(t *testing.T) {
	// A statusline is always piped: the host interprets the ANSI, so output
	// must never vary with tty-ness or color env vars (NO_COLOR is
	// deliberately not honored — documented in the README). This guard must
	// survive any styling-engine change.
	baseline := ModelRow(State{Model: "Fable 5", CtxSize: 1000000, Auth: "Sub", SessionID: "0a1b2c3d"}, "", DefaultOptions())
	t.Setenv("NO_COLOR", "1")
	t.Setenv("TERM", "dumb")
	t.Setenv("CLICOLOR_FORCE", "0")
	if got := ModelRow(State{Model: "Fable 5", CtxSize: 1000000, Auth: "Sub", SessionID: "0a1b2c3d"}, "", DefaultOptions()); got != baseline {
		t.Errorf("output varies with environment:\n got %q\nbase %q", got, baseline)
	}
}

func TestModelRowFullState(t *testing.T) {
	got := ModelRow(State{Model: "Fable 5", CtxSize: 1000000, Auth: "Sub", SessionID: "0a1b2c3d-0000-4000-8000-000000000000"}, "", DefaultOptions())
	want := "\x1b[2mmodel    \x1b[m\x1b[1;36mFable 5 1M\x1b[m\x1b[2m · \x1b[m\x1b[32mSub\x1b[m\x1b[2m · session 0a1b2c3d\x1b[m"
	if got != want {
		t.Errorf("ModelRow full:\n got %q\nwant %q", got, want)
	}
}

func TestModelRowContextSizeLabel(t *testing.T) {
	got := ModelRow(State{Model: "Fable 5", CtxSize: 200000, Auth: "Sub"}, "", DefaultOptions())
	want := "\x1b[2mmodel    \x1b[m\x1b[1;36mFable 5 200k\x1b[m\x1b[2m · \x1b[m\x1b[32mSub\x1b[m"
	if got != want {
		t.Errorf("ModelRow 200k:\n got %q\nwant %q", got, want)
	}
	// Name already stating context: no size appended (mirrors *ontext* glob).
	got = ModelRow(State{Model: "Opus 4.8 (1M context)", CtxSize: 1000000, Auth: "?"}, "", DefaultOptions())
	if strings.Contains(got, "context) 1M") {
		t.Errorf("size appended despite name stating context: %q", got)
	}
}

func TestModelRowAPIKeyWarning(t *testing.T) {
	got := ModelRow(State{Model: "Fable 5", CtxSize: 200000, Auth: "API", APIKeySet: true}, "", DefaultOptions())
	wantAuth := "\x1b[2m · \x1b[m\x1b[33mAPI\x1b[m"
	wantWarn := " \x1b[1;37;41m ⚠ API KEY SET — METERED BILLING \x1b[m"
	if !strings.Contains(got, wantAuth) {
		t.Errorf("missing yellow API auth badge: %q", got)
	}
	if !strings.HasSuffix(got, wantWarn) {
		t.Errorf("missing metered-billing warning: %q", got)
	}
}

func TestProjectRow(t *testing.T) {
	got := ProjectRow("/Users/dev/projects/demo-app", home, "main", 2, 0, DefaultOptions())
	want := "\x1b[2mproject  \x1b[m\x1b[1m~/projects/demo-app\x1b[m \x1b[2m·\x1b[m \x1b[34m⎇ main\x1b[m \x1b[33m~2\x1b[m"
	if got != want {
		t.Errorf("ProjectRow:\n got %q\nwant %q", got, want)
	}
}

func TestProjectRowCleanTreeHidesDirty(t *testing.T) {
	got := ProjectRow("/Users/dev/x", home, "main", 0, 0, DefaultOptions())
	if strings.Contains(got, "~0") || strings.Contains(got, "\x1b[33m") {
		t.Errorf("dirty badge rendered for clean tree: %q", got)
	}
}

func TestProjectRowOutsideHomeKeepsFullPath(t *testing.T) {
	got := ProjectRow("/opt/work", home, "main", 0, 0, DefaultOptions())
	if !strings.Contains(got, "\x1b[1m/opt/work\x1b[m") {
		t.Errorf("path outside home was altered: %q", got)
	}
}

func TestProjectRowIndexLockBadge(t *testing.T) {
	// A long-held .git/index.lock surfaces as a factual yellow age badge —
	// the observable fact ONLY: no "stale" verdict, no removal command (the
	// lock may be legitimately held; a wrong instruction can corrupt a live
	// rebase). gitinfo applies the threshold — render shows any non-zero age.
	got := ProjectRow("/Users/dev/projects/demo-app", home, "main", 2, 14*time.Minute, DefaultOptions())
	want := "\x1b[2mproject  \x1b[m\x1b[1m~/projects/demo-app\x1b[m \x1b[2m·\x1b[m \x1b[34m⎇ main\x1b[m \x1b[33m~2\x1b[m \x1b[33m⚠ index.lock 14m\x1b[m"
	if got != want {
		t.Errorf("ProjectRow lock badge:\n got %q\nwant %q", got, want)
	}

	// Zero age (absent / young / below-threshold lock): byte-identical to
	// today — the badge never renders in the healthy path.
	got = ProjectRow("/Users/dev/projects/demo-app", home, "main", 2, 0, DefaultOptions())
	fresh := "\x1b[2mproject  \x1b[m\x1b[1m~/projects/demo-app\x1b[m \x1b[2m·\x1b[m \x1b[34m⎇ main\x1b[m \x1b[33m~2\x1b[m"
	if got != fresh {
		t.Errorf("no lock must render byte-identical to today:\n got %q\nwant %q", got, fresh)
	}
}

func TestContextRowThresholds(t *testing.T) {
	cases := []struct {
		pct  int
		want string
	}{
		{30, "\x1b[2mcontext  \x1b[m\x1b[32m▓▓▓░░░░░░░\x1b[m \x1b[32m30%\x1b[m"},
		{60, "\x1b[2mcontext  \x1b[m\x1b[33m▓▓▓▓▓▓░░░░\x1b[m \x1b[33m60%\x1b[m"},
		{88, "\x1b[2mcontext  \x1b[m\x1b[31m▓▓▓▓▓▓▓▓░░\x1b[m \x1b[1;31m88%\x1b[m \x1b[1;37;41m /compact \x1b[m"},
	}
	for _, c := range cases {
		if got := ContextRow(c.pct, false, DefaultOptions()); got != c.want {
			t.Errorf("ContextRow(%d):\n got %q\nwant %q", c.pct, got, c.want)
		}
	}
}

func TestAccountRow(t *testing.T) {
	// No reset labels in the payload: nothing to show in either mode.
	got := AccountRow(Usage{U5: 5, U7: 13}, DefaultOptions())
	want := "\x1b[2maccount  \x1b[m\x1b[32m5h 5%\x1b[m \x1b[2m·\x1b[m \x1b[32mweek 13%\x1b[m"
	if got != want {
		t.Errorf("AccountRow green:\n got %q\nwant %q", got, want)
	}
}

func TestAccountRowEmailScopeLabel(t *testing.T) {
	// The email names WHOSE pools these are — first segment, dimmed, so the
	// meters keep the visual focus.
	in := Usage{Email: "dev@example.com", U5: 5, U7: 13}
	got := AccountRow(in, DefaultOptions())
	// Default is normal weight (picked via live A/B, 2026-07-03): plain
	// text — brighter than furniture, quieter than the colored meters.
	want := "\x1b[2maccount  \x1b[mdev@example.com \x1b[2m·\x1b[m \x1b[32m5h 5%\x1b[m \x1b[2m·\x1b[m \x1b[32mweek 13%\x1b[m"
	if got != want {
		t.Errorf("AccountRow email(%+v):\n got %q\nwant %q", in, got, want)
	}

	// Toggle off: byte-identical to the email-less row.
	opts := DefaultOptions()
	opts.Account.ShowEmail = false
	noEmail := "\x1b[2maccount  \x1b[m\x1b[32m5h 5%\x1b[m \x1b[2m·\x1b[m \x1b[32mweek 13%\x1b[m"
	if got := AccountRow(in, opts); got != noEmail {
		t.Errorf("show_email=false must omit the segment:\n got %q\nwant %q", got, noEmail)
	}

	// Empty email: omitted entirely — identity is never fabricated.
	if got := AccountRow(Usage{U5: 5, U7: 13}, DefaultOptions()); got != noEmail {
		t.Errorf("empty email must render byte-identical to today:\n got %q\nwant %q", got, noEmail)
	}

	// email_style = "dim": the quiet variant for meter-focused rows.
	dimOpt := DefaultOptions()
	dimOpt.Account.EmailDim = true
	wantDim := "\x1b[2maccount  \x1b[m\x1b[2mdev@example.com\x1b[m \x1b[2m·\x1b[m \x1b[32m5h 5%\x1b[m \x1b[2m·\x1b[m \x1b[32mweek 13%\x1b[m"
	if got := AccountRow(in, dimOpt); got != wantDim {
		t.Errorf("email_style=dim:\n got %q\nwant %q", got, wantDim)
	}
}

func TestAccountRowAlwaysShowsResetsBelowThreshold(t *testing.T) {
	in := Usage{U5: 5, R5: "1:30p", U7: 13, R7: "Mon 10a"}
	got := AccountRow(in, DefaultOptions()) // default show_resets = always
	want := "\x1b[2maccount  \x1b[m\x1b[32m5h 5%\x1b[m \x1b[2m(resets 1:30p)\x1b[m \x1b[2m·\x1b[m \x1b[32mweek 13%\x1b[m \x1b[2m(resets Mon 10a)\x1b[m"
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
	want := "\x1b[2maccount  \x1b[m\x1b[32m5h 5%\x1b[m \x1b[2m·\x1b[m \x1b[32mweek 13%\x1b[m"
	if got != want {
		t.Errorf("AccountRow quiet/low(%+v):\n got %q\nwant %q", in, got, want)
	}

	// Hot window: quiet is NOT never — reset surfaces at >=80%.
	in = Usage{U5: 82, R5: "1:30p", U7: 55}
	got = AccountRow(in, opts)
	want = "\x1b[2maccount  \x1b[m\x1b[31m5h 82%\x1b[m \x1b[2m(resets 1:30p)\x1b[m \x1b[2m·\x1b[m \x1b[33mweek 55%\x1b[m"
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
	want := "\x1b[2maccount  \x1b[m\x1b[32m5h 28%\x1b[m \x1b[2m(resets 1:30p)\x1b[m \x1b[2m·\x1b[m \x1b[32mweek 18%\x1b[m \x1b[2m(resets Mon 10a)\x1b[m \x1b[2m·\x1b[m \x1b[32mopus/wk 41%\x1b[m \x1b[2m(resets Tue 3:00p)\x1b[m"
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

func TestAccountRowStaleDataAgeMarker(t *testing.T) {
	// Stale-good fallback engaged (fetches failing, old payload served): a
	// trailing dim, factual age qualifies the meters. The meters keep their
	// true (old) values — the marker informs, it never censors.
	in := Usage{U5: 55, R5: "3:00p", U7: 18, DataAge: 6 * time.Minute}
	got := AccountRow(in, DefaultOptions())
	want := "\x1b[2maccount  \x1b[m\x1b[33m5h 55%\x1b[m \x1b[2m(resets 3:00p)\x1b[m \x1b[2m·\x1b[m \x1b[32mweek 18%\x1b[m \x1b[2m·\x1b[m \x1b[2m(data 6m old)\x1b[m"
	if got != want {
		t.Errorf("AccountRow stale(%+v):\n got %q\nwant %q", in, got, want)
	}

	// Fresh payload (DataAge zero): byte-identical to today — zero visual
	// change in the healthy path.
	in.DataAge = 0
	fresh := "\x1b[2maccount  \x1b[m\x1b[33m5h 55%\x1b[m \x1b[2m(resets 3:00p)\x1b[m \x1b[2m·\x1b[m \x1b[32mweek 18%\x1b[m"
	if got := AccountRow(in, DefaultOptions()); got != fresh {
		t.Errorf("fresh payload must render byte-identical to today:\n got %q\nwant %q", got, fresh)
	}

	// show_stale_age = false: marker suppressed even while stale — the row
	// renders byte-identical to the fresh form (owner-requested toggle,
	// 2026-07-03; default stays on).
	off := DefaultOptions()
	off.Account.ShowStaleAge = false
	in.DataAge = 6 * time.Minute
	if got := AccountRow(in, off); got != fresh {
		t.Errorf("show_stale_age=false must suppress the marker:\n got %q\nwant %q", got, fresh)
	}
}

func TestAgeLabel(t *testing.T) {
	// Minute granularity in the activity row's compact unit style — an age
	// marker must not tick seconds across renders; sub-minute ages stay
	// factual instead of a false "0m".
	cases := []struct {
		age  time.Duration
		want string
	}{
		{45 * time.Second, "45s"},
		{6 * time.Minute, "6m"},
		{6*time.Minute + 32*time.Second, "6m"},
		{66 * time.Minute, "1h6m"},
		{3 * time.Hour, "3h0m"},
	}
	for _, c := range cases {
		if got := ageLabel(c.age); got != c.want {
			t.Errorf("ageLabel(%v) = %q, want %q", c.age, got, c.want)
		}
	}
}

func TestActivityRow(t *testing.T) {
	got := ActivityRow(62580000, 1598, 8)
	want := "\x1b[2mactivity \x1b[m\x1b[2m17h23m\x1b[m \x1b[2m·\x1b[m \x1b[32m+1,598\x1b[m/\x1b[31m-8\x1b[m \x1b[2mlines\x1b[m"
	if got != want {
		t.Errorf("ActivityRow:\n got %q\nwant %q", got, want)
	}
	if got := ActivityRow(0, 0, 0); got != "" {
		t.Errorf("empty activity should collapse, got %q", got)
	}
	// Short session: minutes+seconds form, no churn segment.
	got = ActivityRow(90000, 0, 0)
	want = "\x1b[2mactivity \x1b[m\x1b[2m1m30s\x1b[m"
	if got != want {
		t.Errorf("ActivityRow short:\n got %q\nwant %q", got, want)
	}
}

func TestAccountRowHotWeekShowsResetInQuietMode(t *testing.T) {
	opts := DefaultOptions()
	opts.Account.AlwaysShowResets = false // quiet: hot week must still surface
	in := Usage{U5: 10, U7: 91, R7: "Mon 10:00a"}
	got := AccountRow(in, opts)
	want := "\x1b[2maccount  \x1b[m\x1b[32m5h 10%\x1b[m \x1b[2m·\x1b[m \x1b[31mweek 91%\x1b[m \x1b[2m(resets Mon 10:00a)\x1b[m"
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
	got := ContextRow(120, false, DefaultOptions())
	want := "\x1b[2mcontext  \x1b[m\x1b[31m▓▓▓▓▓▓▓▓▓▓\x1b[m \x1b[1;31m120%\x1b[m \x1b[1;37;41m /compact \x1b[m"
	if got != want {
		t.Errorf("ContextRow(120, false, DefaultOptions()):\n got %q\nwant %q", got, want)
	}
}

func TestExtraBadge(t *testing.T) {
	// Actively over a limit and burning credits: loud badge.
	got := ExtraBadge(Usage{U5: 100, ExtraEnabled: true, CreditsMinor: 150, CreditsExp: 2})
	want := "  \x1b[1;37;41m ⚠ EXTRA USAGE $1.50 \x1b[m"
	if got != want {
		t.Errorf("ExtraBadge loud:\n got %q\nwant %q", got, want)
	}
	// Enabled with spend but within limits: dim tally.
	got = ExtraBadge(Usage{U5: 20, ExtraEnabled: true, CreditsMinor: 150, CreditsExp: 2})
	want = "\x1b[2m · extra $1.50\x1b[m"
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

func TestModelRowEffortAndFastBadges(t *testing.T) {
	st := State{Model: "Fable 5", CtxSize: 1000000, Auth: "Sub", Effort: "xhigh", FastMode: true}
	got := ModelRow(st, "", DefaultOptions())
	if !strings.Contains(got, "\x1b[2m · xhigh\x1b[m") {
		t.Errorf("effort badge missing (dim furniture tier): %q", got)
	}
	if !strings.Contains(got, "\x1b[33m⚡ fast\x1b[m") {
		t.Errorf("fast badge missing (yellow attention tier): %q", got)
	}

	o := DefaultOptions()
	o.Model.ShowEffort, o.Model.ShowFastMode = false, false
	got = ModelRow(st, "", o)
	if strings.Contains(got, "xhigh") || strings.Contains(got, "⚡") {
		t.Errorf("toggles off must suppress both badges: %q", got)
	}

	got = ModelRow(State{Model: "Fable 5", CtxSize: 1000000, Auth: "Sub"}, "", DefaultOptions())
	if strings.Contains(got, "xhigh") || strings.Contains(got, "⚡") {
		t.Errorf("absent effort/fast must render nothing: %q", got)
	}
}

func TestModelRowMeteredCostReadout(t *testing.T) {
	st := State{Model: "Fable 5", CtxSize: 1000000, Auth: "API", APIKeySet: true, CostUSD: 87.3046}
	got := ModelRow(st, "", DefaultOptions())
	if !strings.Contains(got, " ⚠ API KEY SET — METERED BILLING · $87.30 ") {
		t.Errorf("alarm must carry the session cost: %q", got)
	}

	st.APIKeySet = false
	if got := ModelRow(st, "", DefaultOptions()); strings.Contains(got, "$") {
		t.Errorf("cost must render ONLY under the metered alarm: %q", got)
	}

	st.APIKeySet, st.CostUSD = true, 0
	got = ModelRow(st, "", DefaultOptions())
	if !strings.Contains(got, " ⚠ API KEY SET — METERED BILLING ") || strings.Contains(got, "$") {
		t.Errorf("zero/absent cost must leave the alarm plain (no fabricated $0.00): %q", got)
	}

	o := DefaultOptions()
	o.Model.ShowMeteredCost = false
	st.CostUSD = 87.3046
	if got := ModelRow(st, "", o); strings.Contains(got, "$") {
		t.Errorf("show_metered_cost=false must suppress the readout: %q", got)
	}
}

func TestContextRow200kMarker(t *testing.T) {
	if got := ContextRow(30, true, DefaultOptions()); !strings.Contains(got, "\x1b[2m>200k\x1b[m") {
		t.Errorf("dim >200k marker missing: %q", got)
	}
	if got := ContextRow(30, false, DefaultOptions()); strings.Contains(got, ">200k") {
		t.Errorf(">200k marker must not render when flag is false: %q", got)
	}
	o := DefaultOptions()
	o.Context.Exceeds200kMarker = false
	if got := ContextRow(30, true, o); strings.Contains(got, ">200k") {
		t.Errorf("exceeds_200k_marker=false must suppress the marker: %q", got)
	}
	got := ContextRow(87, true, DefaultOptions())
	if !strings.Contains(got, ">200k") || !strings.Contains(got, "/compact") {
		t.Errorf("marker and /compact badge must coexist at 87%%: %q", got)
	}
}

func TestAccountRowMixedSourceStaleMarker(t *testing.T) {
	// Mixed mode: live stdin meters + stale endpoint extras. The dim age
	// marker qualifies only visible stale data — with live meters it renders
	// exactly when an endpoint-sourced segment (the model window) shows.
	u := Usage{Email: "dev@example.com", U5: 19, U7: 45, DataAge: 6 * time.Minute, MetersLive: true}
	if got := AccountRow(u, DefaultOptions()); strings.Contains(got, "old)") {
		t.Errorf("live meters with no visible stale segment must carry no marker: %q", got)
	}

	u.ModelFamily, u.ModelPct = "opus", 41
	if got := AccountRow(u, DefaultOptions()); !strings.Contains(got, "\x1b[2m(data 6m old)\x1b[m") {
		t.Errorf("visible stale model window must keep the marker: %q", got)
	}

	// Endpoint-sourced meters (MetersLive false): today's behavior unchanged.
	u = Usage{Email: "dev@example.com", U5: 5, U7: 13, DataAge: 6 * time.Minute}
	if got := AccountRow(u, DefaultOptions()); !strings.Contains(got, "(data 6m old)") {
		t.Errorf("stale endpoint meters must keep the marker: %q", got)
	}
}
