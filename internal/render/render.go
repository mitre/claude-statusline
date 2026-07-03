// Package render builds the statusline's ANSI rows. Output is byte-for-byte
// compatible with the reference bash implementation (reference/statusline.sh).
package render

import (
	"fmt"
	"math"
	"strings"
)

// ANSI sequences (names match the reference script).
const (
	rst   = "\x1b[0m"
	bold  = "\x1b[1m"
	dim   = "\x1b[2m"
	red   = "\x1b[31m"
	grn   = "\x1b[32m"
	ylw   = "\x1b[33m"
	blu   = "\x1b[34m"
	cyn   = "\x1b[36m"
	wht   = "\x1b[37m"
	bgRed = "\x1b[41m"
)

// Usage holds account-limit data extracted by the usage package.
type Usage struct {
	U5, U7       int    // five-hour / seven-day utilization percent
	R5, R7       string // local-time reset labels, "" when absent
	ExtraEnabled bool
	CreditsMinor int // spend in minor units
	CreditsExp   int // exponent for minor units (2 → cents)
	MaxActive    int // max percent among active plan limits
}

// State is everything the renderer needs for one frame.
type State struct {
	Model        string
	CtxSize      int
	Auth         string // "Sub", "API", "?"
	SessionID    string
	CWD          string
	Home         string
	Branch       string
	Dirty        int
	CtxPct       int
	Usage        *Usage // nil → limits row collapses
	DurationMS   int64
	LinesAdded   int
	LinesRemoved int
	APIKeySet    bool
}

// Options are the user-configurable display toggles (config maps TOML here).
type Options struct {
	Rows struct {
		Model, Project, Context, Limits, Activity bool
	}
	Model struct {
		ShowAuth, ShowSession, ShowContextSize bool
	}
	Project struct {
		ShowBranch, ShowDirty, TildeHome bool
	}
}

// DefaultOptions returns the zero-config behavior: everything on.
func DefaultOptions() Options {
	var o Options
	o.Rows.Model, o.Rows.Project, o.Rows.Context, o.Rows.Limits, o.Rows.Activity = true, true, true, true, true
	o.Model.ShowAuth, o.Model.ShowSession, o.Model.ShowContextSize = true, true, true
	o.Project.ShowBranch, o.Project.ShowDirty, o.Project.TildeHome = true, true, true
	return o
}

// lbl renders the dim, 9-column left label (printf "%-9s" in the reference).
func lbl(name string) string {
	return fmt.Sprintf("%s%-9s%s", dim, name, rst)
}

// ModelRow renders the model name (+context size), auth badge, session id,
// extra-usage badge, and the metered-billing alarm.
func ModelRow(model string, ctxSize int, auth, sessionID, extraBadge string, apiKeySet bool, o Options) string {
	name := model
	if o.Model.ShowContextSize && !strings.Contains(name, "ontext") {
		if ctxSize >= 1000000 {
			name += fmt.Sprintf(" %dM", ctxSize/1000000)
		} else {
			name += fmt.Sprintf(" %dk", ctxSize/1000)
		}
	}
	row := lbl("model") + bold + cyn + name + rst
	if o.Model.ShowAuth {
		switch auth {
		case "Sub":
			row += dim + " · " + rst + grn + "Sub" + rst
		case "API":
			row += dim + " · " + rst + ylw + "API" + rst
		}
	}
	if o.Model.ShowSession && sessionID != "" {
		short := sessionID
		if len(short) > 8 {
			short = short[:8]
		}
		row += dim + " · session " + short + rst
	}
	row += extraBadge
	if apiKeySet {
		row += " " + bgRed + wht + bold + " ⚠ API KEY SET — METERED BILLING " + rst
	}
	return row
}

// ProjectRow renders the tilde-shortened cwd, branch glyph, and dirty count.
func ProjectRow(cwd, home, branch string, dirty int, o Options) string {
	path := cwd
	if o.Project.TildeHome && home != "" && strings.HasPrefix(path, home) {
		path = "~" + strings.TrimPrefix(path, home)
	}
	row := lbl("project") + bold + path + rst
	if o.Project.ShowBranch && branch != "" {
		row += " " + dim + "·" + rst + " " + blu + "⎇ " + branch + rst
	}
	if o.Project.ShowDirty && dirty > 0 {
		row += fmt.Sprintf(" %s~%d%s", ylw, dirty, rst)
	}
	return row
}

// ContextRow renders the 10-segment context bar with green/yellow/red
// thresholds and the /compact alarm at >= 85%.
func ContextRow(pct int) string {
	filled := pct / 10
	if filled > 10 {
		filled = 10
	}
	bar := strings.Repeat("▓", filled) + strings.Repeat("░", 10-filled)

	var color, label string
	switch {
	case pct >= 80:
		color, label = red, red+bold+fmt.Sprintf("%d%%", pct)+rst
	case pct >= 50:
		color, label = ylw, ylw+fmt.Sprintf("%d%%", pct)+rst
	default:
		color, label = grn, grn+fmt.Sprintf("%d%%", pct)+rst
	}

	row := lbl("context") + color + bar + rst + " " + label
	if pct >= 85 {
		row += " " + bgRed + wht + bold + " /compact " + rst
	}
	return row
}

func limitColor(pct int) string {
	switch {
	case pct >= 80:
		return red
	case pct >= 50:
		return ylw
	default:
		return grn
	}
}

// LimitsRow renders 5h and week utilization with reset labels once hot (>= 80%).
func LimitsRow(u Usage) string {
	five := limitColor(u.U5) + fmt.Sprintf("5h %d%%", u.U5) + rst
	if u.U5 >= 80 && u.R5 != "" {
		five += " " + dim + "(resets " + u.R5 + ")" + rst
	}
	week := limitColor(u.U7) + fmt.Sprintf("week %d%%", u.U7) + rst
	if u.U7 >= 80 && u.R7 != "" {
		week += " " + dim + "(resets " + u.R7 + ")" + rst
	}
	return lbl("limits") + five + " " + dim + "·" + rst + " " + week
}

// ActivityRow renders session duration and code churn; collapses when idle.
func ActivityRow(durationMS int64, added, removed int) string {
	var parts []string
	if durationMS > 60000 {
		ts := durationMS / 1000
		if ts >= 3600 {
			parts = append(parts, fmt.Sprintf("%s%dh%dm%s", dim, ts/3600, (ts%3600)/60, rst))
		} else {
			parts = append(parts, fmt.Sprintf("%s%dm%ds%s", dim, ts/60, ts%60, rst))
		}
	}
	if added > 0 || removed > 0 {
		parts = append(parts, fmt.Sprintf("%s+%s%s/%s-%s%s %slines%s",
			grn, comma(added), rst, red, comma(removed), rst, dim, rst))
	}
	if len(parts) == 0 {
		return ""
	}
	return lbl("activity") + strings.Join(parts, " "+dim+"·"+rst+" ")
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
		return "  " + bgRed + wht + bold + " ⚠ EXTRA USAGE $" + cred + " " + rst
	case u.CreditsMinor > 0:
		return dim + " · extra $" + cred + rst
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
	add(o.Rows.Model, ModelRow(st.Model, st.CtxSize, st.Auth, st.SessionID, extra, st.APIKeySet, o))
	add(o.Rows.Project, ProjectRow(st.CWD, st.Home, st.Branch, st.Dirty, o))
	add(o.Rows.Context, ContextRow(st.CtxPct))
	if st.Usage != nil {
		add(o.Rows.Limits, LimitsRow(*st.Usage))
	}
	add(o.Rows.Activity, ActivityRow(st.DurationMS, st.LinesAdded, st.LinesRemoved))
	return strings.Join(rows, "\n")
}
