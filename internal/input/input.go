// Package input parses the session JSON Claude Code writes to the
// statusline command's stdin.
package input

import (
	"encoding/json"
	"io"
	"math"
)

// Session is the subset of the stdin payload the statusline renders.
type Session struct {
	SessionID    string
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
	// RateLimitsOK gates the stdin-sourced meter fallback — true only when
	// rate_limits carries five_hour.used_percentage (the same strict shape
	// gate usage.Parse applies to endpoint payloads; never fabricated).
	RateLimitsOK bool
	R5Pct, R7Pct int
	// Reset moments as epoch seconds (the stdin encoding; the endpoint
	// serves RFC3339). 0 = absent, no label.
	R5ResetUnix, R7ResetUnix int64
}

// rlWindow is one stdin rate-limit window; pointers detect absence.
type rlWindow struct {
	UsedPercentage *float64 `json:"used_percentage"`
	ResetsAt       int64    `json:"resets_at"`
}

type payload struct {
	SessionID string `json:"session_id"`
	CWDTop    string `json:"cwd"`
	Model     struct {
		DisplayName string `json:"display_name"`
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
		ModelName:    p.Model.DisplayName,
		CWD:          p.Workspace.CurrentDir,
		CtxPct:       int(math.Floor(p.ContextWindow.UsedPercentage)),
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
	if fh := p.RateLimits.FiveHour; fh != nil && fh.UsedPercentage != nil {
		s.RateLimitsOK = true
		s.R5Pct = int(math.Floor(*fh.UsedPercentage))
		s.R5ResetUnix = fh.ResetsAt
		if sd := p.RateLimits.SevenDay; sd != nil && sd.UsedPercentage != nil {
			s.R7Pct = int(math.Floor(*sd.UsedPercentage))
			s.R7ResetUnix = sd.ResetsAt
		}
	}
	return s, nil
}
