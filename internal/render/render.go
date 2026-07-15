// Package render builds the statusline's ANSI rows. The golden tests in this
// package are the display spec of record.
//
// Styling goes through lipgloss v2, which emits ANSI unconditionally — no
// tty detection, no NO_COLOR/TERM sniffing (a statusline is always piped;
// the host interprets the sequences). Downsampling via colorprofile.Writer
// is deliberately not used.
package render

import (
	"fmt"
	"math"
	"strings"
	"time"

	lg "charm.land/lipgloss/v2"
)

// Styles (ANSI 16-color palette, matching the original escape constants).
var (
	dimS   = lg.NewStyle().Faint(true)
	boldS  = lg.NewStyle().Bold(true)
	nameS  = lg.NewStyle().Bold(true).Foreground(lg.Color("6")) // bold cyan
	redS   = lg.NewStyle().Foreground(lg.Color("1"))
	grnS   = lg.NewStyle().Foreground(lg.Color("2"))
	ylwS   = lg.NewStyle().Foreground(lg.Color("3"))
	bluS   = lg.NewStyle().Foreground(lg.Color("4"))
	hotS   = lg.NewStyle().Bold(true).Foreground(lg.Color("1"))                           // loud percentage
	alarmS = lg.NewStyle().Bold(true).Foreground(lg.Color("7")).Background(lg.Color("1")) // white-on-red badge
)

// sepDot is the dim mid-dot row separator with outer spaces.
var sepDot = " " + dimS.Render("·") + " "

// Usage holds the account row's view data: limit windows extracted by the
// usage package, plus the account email resolved by the account package.
type Usage struct {
	Email        string // logged-in account email, "" = segment omitted
	U5, U7       int    // five-hour / seven-day utilization percent
	R5, R7       string // local-time reset labels, "" when absent
	ModelFamily  string // session model's family (opus/sonnet/haiku), "" = no window
	ModelPct     int    // rolling 7-day model-specific pool utilization percent
	ModelReset   string // reset label for the model window, "" when absent
	ExtraEnabled bool
	CreditsMinor int // spend in minor units
	CreditsExp   int // exponent for minor units (2 → cents)
	MaxActive    int // max percent among active plan limits
	// DataAge is the served payload's age when the stale-good fallback is
	// engaged (fetches failing, old data served); zero on the fresh paths.
	// Non-zero renders the trailing dim age marker — the meters keep their
	// true (old) values, the marker qualifies them.
	DataAge time.Duration
}

// State is everything the renderer needs for one frame.
type State struct {
	Model     string
	CtxSize   int
	Auth      string // "Sub", "API", "?"
	SessionID string
	CWD       string
	Home      string
	Branch    string
	Dirty     int
	// LockAge is how long .git/index.lock has been held, already past the
	// configured threshold (gitinfo applies it); zero renders no badge.
	LockAge      time.Duration
	CtxPct       int
	Usage        *Usage // nil → limits row collapses
	DurationMS   int64
	LinesAdded   int
	LinesRemoved int
	APIKeySet    bool
	// Effort is the session's reasoning-effort level ("" = segment omitted).
	Effort string
	// FastMode renders the ⚡ fast badge when true.
	FastMode bool
	// CostUSD is the session's accumulated API cost; rendered only inside
	// the metered-billing alarm (APIKeySet), and only when > 0 — a zero is
	// indistinguishable from an absent field and must not be fabricated.
	CostUSD float64
	// Exceeds200k marks the session crossing the absolute 200k-token
	// threshold (long-context tier on 1M windows) — independent of window
	// pressure, which the /compact badge tracks.
	Exceeds200k bool
}

// Options are the user-configurable display toggles (config maps TOML here).
type Options struct {
	Rows struct {
		Model, Project, Context, Account, Activity bool
	}
	Model struct {
		ShowAuth, ShowSession, ShowContextSize bool
		// ShowEffort/ShowFastMode gate the reasoning-effort and ⚡ fast
		// badges; ShowMeteredCost gates the $ readout inside the
		// metered-billing alarm.
		ShowEffort, ShowFastMode, ShowMeteredCost bool
	}
	Project struct {
		ShowBranch, ShowDirty, TildeHome bool
	}
	Context struct {
		// Exceeds200kMarker gates the dim >200k long-context marker.
		Exceeds200kMarker bool
	}
	Account struct {
		// AlwaysShowResets: true = show (resets …) on every meter (config
		// "always", the default); false = only once a window runs hot >=80%
		// (config "quiet" — quiet is not never).
		AlwaysShowResets bool
		// ShowEmail renders the account email as the row's first segment —
		// it names whose pools the meters describe.
		ShowEmail bool
		// EmailDim: true = dim identity label (config "dim"); false = plain
		// terminal-default weight (config "normal", the default — picked via
		// live A/B, 2026-07-03).
		EmailDim bool
		// ShowStaleAge renders the dim data-age marker when the stale-good
		// fallback is serving (default on — it only appears in a degraded
		// state worth knowing about).
		ShowStaleAge bool
	}
}

// DefaultOptions returns the zero-config behavior: everything on.
func DefaultOptions() Options {
	var o Options
	o.Rows.Model, o.Rows.Project, o.Rows.Context, o.Rows.Account, o.Rows.Activity = true, true, true, true, true
	o.Model.ShowAuth, o.Model.ShowSession, o.Model.ShowContextSize = true, true, true
	o.Model.ShowEffort, o.Model.ShowFastMode, o.Model.ShowMeteredCost = true, true, true
	o.Project.ShowBranch, o.Project.ShowDirty, o.Project.TildeHome = true, true, true
	o.Context.Exceeds200kMarker = true
	o.Account.AlwaysShowResets = true
	o.Account.ShowEmail = true
	o.Account.EmailDim = false
	o.Account.ShowStaleAge = true
	return o
}

// lbl renders the dim, 9-column left label (printf "%-9s" in the reference).
func lbl(name string) string {
	return dimS.Render(fmt.Sprintf("%-9s", name))
}

// ModelRow renders the model name (+context size), auth badge, effort and
// fast-mode badges, session id, extra-usage badge, and the metered-billing
// alarm (carrying the session's accumulated cost when known).
func ModelRow(st State, extraBadge string, o Options) string {
	name := st.Model
	if o.Model.ShowContextSize && !strings.Contains(name, "ontext") {
		if st.CtxSize >= 1000000 {
			name += fmt.Sprintf(" %dM", st.CtxSize/1000000)
		} else {
			name += fmt.Sprintf(" %dk", st.CtxSize/1000)
		}
	}
	row := lbl("model") + nameS.Render(name)
	if o.Model.ShowAuth {
		switch st.Auth {
		case "Sub":
			row += dimS.Render(" · ") + grnS.Render("Sub")
		case "API":
			row += dimS.Render(" · ") + ylwS.Render("API")
		}
	}
	if o.Model.ShowEffort && st.Effort != "" {
		// Furniture tier: effort is standing information, not an alert.
		row += dimS.Render(" · " + st.Effort)
	}
	if o.Model.ShowFastMode && st.FastMode {
		// Attention tier: fast mode changes the session's cost profile.
		row += dimS.Render(" · ") + ylwS.Render("⚡ fast")
	}
	if o.Model.ShowSession && st.SessionID != "" {
		short := st.SessionID
		if len(short) > 8 {
			short = short[:8]
		}
		row += dimS.Render(" · session " + short)
	}
	row += extraBadge
	if st.APIKeySet {
		alarm := " ⚠ API KEY SET — METERED BILLING "
		if o.Model.ShowMeteredCost && st.CostUSD > 0 {
			// The readout exists only inside the alarm: cost is rendered
			// exactly when it is being incurred; zero stays silent (an
			// absent field decodes to 0 — never fabricate $0.00).
			alarm = fmt.Sprintf(" ⚠ API KEY SET — METERED BILLING · $%.2f ", st.CostUSD)
		}
		row += " " + alarmS.Render(alarm)
	}
	return row
}

// ProjectRow renders the tilde-shortened cwd, branch glyph, dirty count, and
// the long-held index.lock badge.
func ProjectRow(cwd, home, branch string, dirty int, lockAge time.Duration, o Options) string {
	path := cwd
	if o.Project.TildeHome && home != "" && strings.HasPrefix(path, home) {
		path = "~" + strings.TrimPrefix(path, home)
	}
	row := lbl("project") + boldS.Render(path)
	if o.Project.ShowBranch && branch != "" {
		row += sepDot + bluS.Render("⎇ "+branch)
	}
	if o.Project.ShowDirty && dirty > 0 {
		row += " " + ylwS.Render(fmt.Sprintf("~%d", dirty))
	}
	if lockAge > 0 {
		// Factual age of a long-held .git/index.lock — information, never an
		// instruction: the lock may be legitimately held (editor-open commit,
		// live rebase), so no "stale" verdict and no removal command, and
		// yellow rather than the alarm tier.
		row += " " + ylwS.Render("⚠ index.lock "+ageLabel(lockAge))
	}
	return row
}

// ContextRow renders the 10-segment context bar with green/yellow/red
// thresholds, the dim >200k long-context marker, and the /compact alarm at
// >= 85%. The marker and the alarm answer different questions: absolute
// tokens past the 200k tier boundary vs pressure on the window.
func ContextRow(pct int, exceeds200k bool, o Options) string {
	filled := min(pct/10, 10)
	bar := strings.Repeat("▓", filled) + strings.Repeat("░", 10-filled)

	barS, labelS := grnS, grnS
	switch {
	case pct >= 80:
		barS, labelS = redS, hotS
	case pct >= 50:
		barS, labelS = ylwS, ylwS
	}

	row := lbl("context") + barS.Render(bar) + " " + labelS.Render(fmt.Sprintf("%d%%", pct))
	if exceeds200k && o.Context.Exceeds200kMarker {
		row += " " + dimS.Render(">200k")
	}
	if pct >= 85 {
		row += " " + alarmS.Render(" /compact ")
	}
	return row
}

func limitStyle(pct int) lg.Style {
	switch {
	case pct >= 80:
		return redS
	case pct >= 50:
		return ylwS
	default:
		return grnS
	}
}

// meter renders one account meter with its optional reset label.
func meter(text string, pct int, reset string, alwaysShowReset bool) string {
	s := limitStyle(pct).Render(text)
	if reset != "" && (alwaysShowReset || pct >= 80) {
		s += " " + dimS.Render("(resets "+reset+")")
	}
	return s
}

// AccountRow renders the ACCOUNT-scope meters: the 5-hour and 7-day
// all-models pools, plus the rolling 7-day pool for this session's model
// family when the payload carries one (omitted otherwise — never a
// fabricated 0%). All values are account-wide percent-of-plan-allotment
// as reported by the usage API. The model window is a PARALLEL weekly cap,
// not a subset of the week meter.
func AccountRow(u Usage, o Options) string {
	always := o.Account.AlwaysShowResets
	var parts []string
	if o.Account.ShowEmail && u.Email != "" {
		e := u.Email
		if o.Account.EmailDim {
			e = dimS.Render(e)
		}
		parts = append(parts, e)
	}
	parts = append(parts,
		meter(fmt.Sprintf("5h %d%%", u.U5), u.U5, u.R5, always),
		meter(fmt.Sprintf("week %d%%", u.U7), u.U7, u.R7, always),
	)
	if u.ModelFamily != "" {
		parts = append(parts, meter(fmt.Sprintf("%s/wk %d%%", u.ModelFamily, u.ModelPct), u.ModelPct, u.ModelReset, always))
	}
	if u.DataAge > 0 && o.Account.ShowStaleAge {
		// Stale-good fallback engaged: qualify the meters with a factual,
		// dim age — informational tier, never alarm styling (red stays
		// reserved for billing/API alarms).
		parts = append(parts, dimS.Render("(data "+ageLabel(u.DataAge)+" old)"))
	}
	return lbl("account") + strings.Join(parts, sepDot)
}

// ageLabel formats a data age at minute granularity in the activity row's
// compact unit style ("1h6m", "6m"; sub-minute ages stay factual as "45s").
// Seconds are deliberately dropped above a minute — an age marker must not
// tick on every render, and "how stale" is a minutes-scale question.
func ageLabel(age time.Duration) string {
	ts := int64(age.Seconds())
	switch {
	case ts >= 3600:
		return fmt.Sprintf("%dh%dm", ts/3600, (ts%3600)/60)
	case ts >= 60:
		return fmt.Sprintf("%dm", ts/60)
	default:
		return fmt.Sprintf("%ds", ts)
	}
}

// ActivityRow renders session duration and code churn; collapses when idle.
func ActivityRow(durationMS int64, added, removed int) string {
	var parts []string
	if durationMS > 60000 {
		ts := durationMS / 1000
		if ts >= 3600 {
			parts = append(parts, dimS.Render(fmt.Sprintf("%dh%dm", ts/3600, (ts%3600)/60)))
		} else {
			parts = append(parts, dimS.Render(fmt.Sprintf("%dm%ds", ts/60, ts%60)))
		}
	}
	if added > 0 || removed > 0 {
		parts = append(parts, grnS.Render("+"+comma(added))+"/"+redS.Render("-"+comma(removed))+" "+dimS.Render("lines"))
	}
	if len(parts) == 0 {
		return ""
	}
	return lbl("activity") + strings.Join(parts, sepDot)
}

// ExtraBadge renders the extra-usage indicator for the model row: a loud
// alarm while actively billing extra usage, a dim tally when merely enabled
// with spend, nothing otherwise.
func ExtraBadge(u Usage) string {
	if !u.ExtraEnabled {
		return ""
	}
	cred := fmt.Sprintf("%.2f", float64(u.CreditsMinor)/math.Pow10(u.CreditsExp))
	switch {
	case u.U5 >= 100 || u.U7 >= 100 || u.MaxActive >= 100:
		return "  " + alarmS.Render(" ⚠ EXTRA USAGE $"+cred+" ")
	case u.CreditsMinor > 0:
		return dimS.Render(" · extra $" + cred)
	}
	return ""
}

// comma formats a non-negative integer with thousands separators
// (printf "%'d" in the reference).
func comma(n int) string {
	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 {
		return s
	}
	var b strings.Builder
	lead := len(s) % 3
	if lead > 0 {
		b.WriteString(s[:lead])
	}
	for i := lead; i < len(s); i += 3 {
		if b.Len() > 0 {
			b.WriteString(",")
		}
		b.WriteString(s[i : i+3])
	}
	return b.String()
}

// Build assembles all rows in reference order, collapsing empty ones and
// honoring the per-row toggles.
func Build(st State, o Options) string {
	var rows []string
	add := func(enabled bool, row string) {
		if enabled && row != "" {
			rows = append(rows, row)
		}
	}

	extra := ""
	if st.Usage != nil {
		extra = ExtraBadge(*st.Usage)
	}
	add(o.Rows.Model, ModelRow(st, extra, o))
	add(o.Rows.Project, ProjectRow(st.CWD, st.Home, st.Branch, st.Dirty, st.LockAge, o))
	add(o.Rows.Context, ContextRow(st.CtxPct, st.Exceeds200k, o))
	if st.Usage != nil {
		add(o.Rows.Account, AccountRow(*st.Usage, o))
	}
	add(o.Rows.Activity, ActivityRow(st.DurationMS, st.LinesAdded, st.LinesRemoved))
	return strings.Join(rows, "\n")
}
