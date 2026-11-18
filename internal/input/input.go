// Package input parses the session JSON Claude Code writes to the
// statusline command's stdin.
package input

import (
	"encoding/json"
	"io"
	"math"
	"strings"
	"time"
)

// Session is the subset of the stdin payload the statusline renders.
type Session struct {
	SessionID    string
	Product      string
	PlanTier     string
	Email        string
	ModelName    string
	CWD          string
	CtxPct       int
	CtxSize      int
	LinesAdded   int
	LinesRemoved int
	DurationMS   int64
	// CostUSD is the session's accumulated API cost; rendered only while an
	// API-key override is active (metered billing), 0 renders nothing.
	CostUSD float64
	// Effort is the session's reasoning-effort level ("low"…"xhigh"),
	// "" when the host doesn't send one — segment omitted.
	Effort      string
	FastMode    bool
	Exceeds200k bool
	// RateLimitsOK gates the stdin-sourced meter fallback: true when at
	// least one window is present (R5OK or R7OK), from rate_limits or from
	// the Antigravity quota. Never fabricated.
	RateLimitsOK bool
	// R5OK and R7OK: the five-hour and the weekly window are present in this
	// stdin payload (a window with a used percentage, or a valid quota
	// bucket). Either may be present alone; an absent or null window is
	// absent, and its absence must never read as 0%.
	R5OK, R7OK   bool
	R5Pct, R7Pct int
	// Reset moments as epoch seconds (the stdin encoding; the endpoint
	// serves RFC3339). 0 = absent, no label.
	R5ResetUnix, R7ResetUnix int64
	ScopedMeters             []ScopedMeter
}

// ScopedMeter represents a scoped meter (e.g. secondary quota window).
type ScopedMeter struct {
	Name      string
	Pct       int
	ResetUnix int64
}

// rlWindow is one stdin rate-limit window; pointers detect absence.
type rlWindow struct {
	UsedPercentage *float64 `json:"used_percentage"`
	ResetsAt       int64    `json:"resets_at"`
}

// agyQuotaWindow is one Antigravity stdin quota window.
type agyQuotaWindow struct {
	RemainingFraction *float64 `json:"remaining_fraction"`
	ResetTime         string   `json:"reset_time"`
	ResetInSeconds    int64    `json:"reset_in_seconds"`
}

type payload struct {
	SessionID string `json:"session_id"`
	Product   string `json:"product"`
	PlanTier  string `json:"plan_tier"`
	Email     string `json:"email"`
	CWDTop    string `json:"cwd"`
	Model     struct {
		DisplayName string `json:"display_name"`
		Effort      string `json:"effort"`
	} `json:"model"`
	Workspace struct {
		CurrentDir string `json:"current_dir"`
	} `json:"workspace"`
	ContextWindow struct {
		UsedPercentage    float64 `json:"used_percentage"`
		ContextWindowSize int     `json:"context_window_size"`
	} `json:"context_window"`
	Cost struct {
		TotalLinesAdded   int     `json:"total_lines_added"`
		TotalLinesRemoved int     `json:"total_lines_removed"`
		TotalDurationMS   int64   `json:"total_duration_ms"`
		TotalCostUSD      float64 `json:"total_cost_usd"`
	} `json:"cost"`
	Effort struct {
		Level string `json:"level"`
	} `json:"effort"`
	FastMode    bool `json:"fast_mode"`
	Exceeds200k bool `json:"exceeds_200k_tokens"`
	RateLimits  struct {
		FiveHour *rlWindow `json:"five_hour"`
		SevenDay *rlWindow `json:"seven_day"`
	} `json:"rate_limits"`
	Quota map[string]agyQuotaWindow `json:"quota"`
}

// Parse reads the stdin JSON and applies the reference defaults
// (model "?", context window 200000, workspace.current_dir over .cwd).
func Parse(r io.Reader) (Session, error) {
	var p payload
	if err := json.NewDecoder(r).Decode(&p); err != nil {
		return Session{}, err
	}

	s := Session{
		SessionID:    p.SessionID,
		Product:      p.Product,
		PlanTier:     p.PlanTier,
		Email:        p.Email,
		ModelName:    p.Model.DisplayName,
		CWD:          p.Workspace.CurrentDir,
		CtxPct:       int(math.Floor(max(0.0, min(100.0, p.ContextWindow.UsedPercentage)))),
		CtxSize:      p.ContextWindow.ContextWindowSize,
		LinesAdded:   p.Cost.TotalLinesAdded,
		LinesRemoved: p.Cost.TotalLinesRemoved,
		DurationMS:   p.Cost.TotalDurationMS,
		CostUSD:      p.Cost.TotalCostUSD,
		Effort:       p.Effort.Level,
		FastMode:     p.FastMode,
		Exceeds200k:  p.Exceeds200k,
	}
	if s.ModelName == "" {
		s.ModelName = "?"
	}
	if s.CWD == "" {
		s.CWD = p.CWDTop
	}
	if s.CtxSize == 0 {
		s.CtxSize = 200000
	}
	if s.Effort == "" && p.Model.Effort != "" {
		if !strings.Contains(strings.ToLower(s.ModelName), strings.ToLower(p.Model.Effort)) {
			s.Effort = p.Model.Effort
		}
	}
	if fh := p.RateLimits.FiveHour; fh != nil && fh.UsedPercentage != nil {
		s.R5OK = true
		s.R5Pct = int(math.Floor(max(0.0, min(100.0, *fh.UsedPercentage))))
		s.R5ResetUnix = fh.ResetsAt
	}
	if sd := p.RateLimits.SevenDay; sd != nil && sd.UsedPercentage != nil {
		s.R7OK = true
		s.R7Pct = int(math.Floor(max(0.0, min(100.0, *sd.UsedPercentage))))
		s.R7ResetUnix = sd.ResetsAt
	}
	s.RateLimitsOK = s.R5OK || s.R7OK
	if !s.RateLimitsOK && len(p.Quota) > 0 {
		parseAgyQuota(&s, p.Quota)
	}
	return s, nil
}

func parseAgyWindow(w agyQuotaWindow) (pct int, resetUnix int64, ok bool) {
	if w.RemainingFraction == nil {
		return 0, 0, false
	}
	rem := *w.RemainingFraction
	if rem < 0 {
		rem = 0
	} else if rem > 1 {
		rem = 1
	}
	usedPct := (1.0 - rem) * 100.0
	pct = int(math.Round(usedPct))
	if pct < 0 {
		pct = 0
	} else if pct > 100 {
		pct = 100
	}
	if w.ResetTime != "" {
		if t, err := time.Parse(time.RFC3339, w.ResetTime); err == nil {
			resetUnix = t.Unix()
		}
	}
	if resetUnix == 0 && w.ResetInSeconds > 0 {
		resetUnix = time.Now().Unix() + w.ResetInSeconds
	}
	return pct, resetUnix, true
}

// parseAgyQuota reads the Antigravity quota: the five-hour and weekly
// buckets of the family the model draws from, each present on its own, and
// the other family's five-hour bucket as a scoped meter while it is in use.
func parseAgyQuota(s *Session, quota map[string]agyQuotaWindow) {
	family, other := selectAgyQuotaFamily(s.ModelName, quota)
	if family == "" {
		return
	}
	if pct, rUnix, ok := agyBucket(quota, family+"-5h"); ok {
		s.R5OK, s.R5Pct, s.R5ResetUnix = true, pct, rUnix
	}
	if pct, rUnix, ok := agyBucket(quota, family+"-weekly"); ok {
		s.R7OK, s.R7Pct, s.R7ResetUnix = true, pct, rUnix
	}
	s.RateLimitsOK = s.R5OK || s.R7OK
	if !s.RateLimitsOK || other == "" {
		return
	}
	sKey := other + "-5h"
	if spct, srUnix, ok := agyBucket(quota, sKey); ok && spct > 0 {
		s.ScopedMeters = append(s.ScopedMeters, ScopedMeter{
			Name:      sKey,
			Pct:       spct,
			ResetUnix: srUnix,
		})
	}
}

// agyBucket parses the quota bucket named key, if present and valid.
func agyBucket(quota map[string]agyQuotaWindow, key string) (pct int, resetUnix int64, ok bool) {
	w, found := quota[key]
	if !found {
		return 0, 0, false
	}
	return parseAgyWindow(w)
}

// selectAgyQuotaFamily picks the quota family ("3p" or "gemini") the model
// draws from among the families with a usable bucket (one agyBucket accepts;
// an empty or null bucket does not count), and the other family whose
// five-hour bucket is shown as a scoped meter ("" for none).
func selectAgyQuotaFamily(modelName string, quota map[string]agyQuotaWindow) (family, other string) {
	has := func(f string) bool {
		_, _, h := agyBucket(quota, f+"-5h")
		_, _, w := agyBucket(quota, f+"-weekly")
		return h || w
	}
	modelLower := strings.ToLower(modelName)
	is3p := strings.Contains(modelLower, "claude") || strings.Contains(modelLower, "gpt") || strings.Contains(modelLower, "3p")
	switch {
	case is3p && has("3p"):
		return "3p", "gemini"
	case has("gemini"):
		return "gemini", "3p"
	case has("3p"):
		return "3p", ""
	default:
		return "", ""
	}
}
