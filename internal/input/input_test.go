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
