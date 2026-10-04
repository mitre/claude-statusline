package input

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func parseFixture(t *testing.T, name string) Session {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "testdata", name))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	s, err := Parse(bytes.NewReader(b))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return s
}

func TestParseFullSession(t *testing.T) {
	s := parseFixture(t, "full.json")
	if s.SessionID != "0a1b2c3d-0000-4000-8000-000000000000" {
		t.Errorf("SessionID = %q", s.SessionID)
	}
	if s.ModelName != "Fable 5" {
		t.Errorf("ModelName = %q", s.ModelName)
	}
	if s.CWD != "/Users/dev/projects/demo-app" {
		t.Errorf("CWD = %q", s.CWD)
	}
	if s.CtxPct != 30 {
		t.Errorf("CtxPct = %d, want 30", s.CtxPct)
	}
	if s.CtxSize != 1000000 {
		t.Errorf("CtxSize = %d, want 1000000", s.CtxSize)
	}
	if s.LinesAdded != 1598 || s.LinesRemoved != 8 {
		t.Errorf("lines = +%d/-%d, want +1598/-8", s.LinesAdded, s.LinesRemoved)
	}
	if s.DurationMS != 62580000 {
		t.Errorf("DurationMS = %d", s.DurationMS)
	}
}

func TestParseDefaults(t *testing.T) {
	s := parseFixture(t, "degraded.json")
	if s.SessionID != "" {
		t.Errorf("SessionID = %q, want empty", s.SessionID)
	}
	if s.CtxSize != 200000 {
		t.Errorf("CtxSize = %d, want default 200000", s.CtxSize)
	}
	if s.CtxPct != 88 {
		t.Errorf("CtxPct = %d, want 88", s.CtxPct)
	}
	if s.LinesAdded != 0 || s.DurationMS != 0 {
		t.Errorf("cost should default to zero, got +%d dur=%d", s.LinesAdded, s.DurationMS)
	}
}

func TestParseCWDFallbackAndModelDefault(t *testing.T) {
	s := parseFixture(t, "cwd-fallback.json")
	if s.CWD != "/tmp/somewhere" {
		t.Errorf("CWD = %q, want fallback to .cwd", s.CWD)
	}
	if s.ModelName != "?" {
		t.Errorf("ModelName = %q, want \"?\" default", s.ModelName)
	}
}

func TestParseMalformedJSONErrors(t *testing.T) {
	in := "not json at all"
	if _, err := Parse(strings.NewReader(in)); err == nil {
		t.Errorf("Parse(%q) = nil error, want decode error", in)
	}
}

func TestParseFractionalPercentageFloors(t *testing.T) {
	s, err := Parse(strings.NewReader(`{"context_window":{"used_percentage":42.9}}`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if s.CtxPct != 42 {
		t.Errorf("CtxPct = %d, want floor(42.9)=42", s.CtxPct)
	}
}

func TestParseSegmentFields(t *testing.T) {
	j := `{"model":{"display_name":"Fable 5"},` +
		`"cost":{"total_cost_usd":87.3046},` +
		`"effort":{"level":"xhigh"},"fast_mode":true,"exceeds_200k_tokens":true}`
	s, err := Parse(strings.NewReader(j))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if s.CostUSD != 87.3046 {
		t.Errorf("CostUSD = %v, want 87.3046", s.CostUSD)
	}
	if s.Effort != "xhigh" {
		t.Errorf("Effort = %q, want \"xhigh\"", s.Effort)
	}
	if !s.FastMode {
		t.Error("FastMode = false, want true")
	}
	if !s.Exceeds200k {
		t.Error("Exceeds200k = false, want true")
	}
}

func TestParseSegmentFieldsAbsentStayZero(t *testing.T) {
	s, err := Parse(strings.NewReader(`{"model":{"display_name":"Fable 5"}}`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if s.CostUSD != 0 || s.Effort != "" || s.FastMode || s.Exceeds200k {
		t.Errorf("absent segment fields must stay zero: %+v", s)
	}
}

func TestParseRateLimits(t *testing.T) {
	j := `{"model":{"display_name":"Fable 5"},"rate_limits":{` +
		`"five_hour":{"used_percentage":19.6,"resets_at":1784134800},` +
		`"seven_day":{"used_percentage":45,"resets_at":1784556000}}}`
	s, err := Parse(strings.NewReader(j))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !s.RateLimitsOK {
		t.Fatal("RateLimitsOK = false with a well-formed rate_limits block")
	}
	if s.R5Pct != 19 || s.R7Pct != 45 {
		t.Errorf("percentages = %d/%d, want floor 19/45", s.R5Pct, s.R7Pct)
	}
	if s.R5ResetUnix != 1784134800 || s.R7ResetUnix != 1784556000 {
		t.Errorf("resets = %d/%d, want the epoch values", s.R5ResetUnix, s.R7ResetUnix)
	}
}

func TestParseRateLimitsAbsentOrMalformed(t *testing.T) {
	for _, j := range []string{
		`{"model":{"display_name":"Fable 5"}}`,
		`{"rate_limits":{}}`,
		`{"rate_limits":{"five_hour":{}}}`,
		`{"rate_limits":{"five_hour":null}}`,
	} {
		s, err := Parse(strings.NewReader(j))
		if err != nil {
			t.Fatalf("Parse(%s): %v", j, err)
		}
		if s.RateLimitsOK {
			t.Errorf("RateLimitsOK = true for %s — the shape gate requires five_hour.used_percentage", j)
		}
	}
}

func TestParseAntigravitySession(t *testing.T) {
	j := `{
		"product": "antigravity",
		"plan_tier": "Google AI Ultra",
		"email": "dev@example.com",
		"model": {"display_name": "Gemini 3.8 Flash (High)", "effort": "high"},
		"quota": {
			"gemini-5h": {
				"remaining_fraction": 0.9606924,
				"reset_time": "2026-10-04T18:43:54Z",
				"reset_in_seconds": 13206
			},
			"gemini-weekly": {
				"remaining_fraction": 0.9937261,
				"reset_time": "2026-10-11T01:50:24Z",
				"reset_in_seconds": 557196
			},
			"3p-5h": {
				"remaining_fraction": 1.0,
				"reset_time": "2026-10-04T20:03:09Z",
				"reset_in_seconds": 17961
			}
		}
	}`
	s, err := Parse(strings.NewReader(j))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if s.Product != "antigravity" {
		t.Errorf("Product = %q, want \"antigravity\"", s.Product)
	}
	if s.PlanTier != "Google AI Ultra" {
		t.Errorf("PlanTier = %q, want \"Google AI Ultra\"", s.PlanTier)
	}
	if s.Email != "dev@example.com" {
		t.Errorf("Email = %q, want \"dev@example.com\"", s.Email)
	}
	if !s.RateLimitsOK {
		t.Fatal("RateLimitsOK = false for Antigravity quota")
	}
	if s.R5Pct != 4 {
		t.Errorf("R5Pct = %d, want 4", s.R5Pct)
	}
	if s.R7Pct != 1 {
		t.Errorf("R7Pct = %d, want 1", s.R7Pct)
	}
	if s.R5ResetUnix != 1791139434 {
		t.Errorf("R5ResetUnix = %d, want 1791139434", s.R5ResetUnix)
	}
	if s.R7ResetUnix != 1791683424 {
		t.Errorf("R7ResetUnix = %d, want 1791683424", s.R7ResetUnix)
	}
	// ModelName already contains (High), so Effort stays empty to avoid redundant display
	if s.Effort != "" {
		t.Errorf("Effort = %q, want empty (already in ModelName)", s.Effort)
	}
	// 3p-5h is 100% remaining (0% used), so ScopedMeters should be empty
	if len(s.ScopedMeters) != 0 {
		t.Errorf("ScopedMeters = %+v, want empty", s.ScopedMeters)
	}
}

func TestParseAntigravity3pModel(t *testing.T) {
	j := `{
		"product": "antigravity",
		"model": {"display_name": "Claude 3.7 Sonnet", "effort": "high"},
		"quota": {
			"gemini-5h": {
				"remaining_fraction": 0.90,
				"reset_time": "2026-10-04T18:43:54Z"
			},
			"3p-5h": {
				"remaining_fraction": 0.65,
				"reset_time": "2026-10-04T20:00:00Z"
			},
			"3p-weekly": {
				"remaining_fraction": 0.80,
				"reset_time": "2026-10-11T12:00:00Z"
			}
		}
	}`
	s, err := Parse(strings.NewReader(j))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	// 3p model should select 3p-5h and 3p-weekly
	if s.R5Pct != 35 {
		t.Errorf("R5Pct = %d, want 35", s.R5Pct)
	}
	if s.R7Pct != 20 {
		t.Errorf("R7Pct = %d, want 20", s.R7Pct)
	}
	// Claude 3.7 Sonnet does not contain "high", so Effort should be "high"
	if s.Effort != "high" {
		t.Errorf("Effort = %q, want \"high\"", s.Effort)
	}
	// Gemini secondary has usage (10% used), so ScopedMeters should have it
	if len(s.ScopedMeters) != 1 || s.ScopedMeters[0].Name != "gemini-5h" || s.ScopedMeters[0].Pct != 10 {
		t.Errorf("ScopedMeters = %+v, want [{Name: gemini-5h, Pct: 10}]", s.ScopedMeters)
	}
}
