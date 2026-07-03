package usage

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

const goodPayload = `{
  "five_hour": {"utilization": 5.9, "resets_at": "2026-07-03T20:30:00Z"},
  "seven_day": {"utilization": 13, "resets_at": "2026-07-06T17:00:00Z"},
  "extra_usage": {"is_enabled": true},
  "spend": {"used": {"amount_minor": 150, "exponent": 2}},
  "limits": [
    {"is_active": true, "percent": 40},
    {"is_active": false, "percent": 90},
    {"is_active": true, "percent": 12}
  ]
}`

// PDT: UTC-7. 2026-07-03T20:30:00Z → 13:30 local (same day as `now`).
var now = time.Date(2026, 7, 3, 9, 0, 0, 0, time.FixedZone("PDT", -7*3600))

func TestParseRejectsErrorPayload(t *testing.T) {
	// The exact payload that poisoned the bash cache on 2026-07-03: valid
	// JSON, but not usage data. Must be an error, never zeros.
	_, err := Parse([]byte(`{"error":{"type":"rate_limit_error","message":"Rate limited."}}`), now)
	if err == nil {
		t.Fatal("error payload parsed as usage data")
	}
}

func TestParseExtractsFields(t *testing.T) {
	u, err := Parse([]byte(goodPayload), now)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if u.U5 != 5 {
		t.Errorf("U5 = %d, want floor(5.9)=5", u.U5)
	}
	if u.U7 != 13 {
		t.Errorf("U7 = %d, want 13", u.U7)
	}
	if !u.ExtraEnabled || u.CreditsMinor != 150 || u.CreditsExp != 2 {
		t.Errorf("extra usage fields: %+v", u)
	}
	if u.MaxActive != 40 {
		t.Errorf("MaxActive = %d, want 40 (max over active limits only)", u.MaxActive)
	}
}

func TestParseResetLabels(t *testing.T) {
	u, err := Parse([]byte(goodPayload), now)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if u.R5 != "1:30p" {
		t.Errorf("R5 = %q, want \"1:30p\" (same-day: bare time)", u.R5)
	}
	// 2026-07-06 is a Monday; 17:00Z → 10:00 PDT.
	if u.R7 != "Mon 10:00a" {
		t.Errorf("R7 = %q, want \"Mon 10:00a\" (other day: weekday prefix)", u.R7)
	}
}

func TestResolveCachesGoodPayload(t *testing.T) {
	dir := t.TempDir()
	got, ok := Resolve(dir, time.Minute, func() ([]byte, error) { return []byte(goodPayload), nil })
	if !ok {
		t.Fatal("Resolve returned no payload")
	}
	if string(got) != goodPayload {
		t.Errorf("payload altered")
	}
	if b, err := os.ReadFile(filepath.Join(dir, "usage")); err != nil || string(b) != goodPayload {
		t.Errorf("good payload not cached: %v %q", err, b)
	}
}

func TestResolveFallsBackToStaleOnBadFetch(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "usage")
	if err := os.WriteFile(p, []byte(goodPayload), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	_ = os.Chtimes(p, old, old) // expired

	errPayload := []byte(`{"error":{"type":"rate_limit_error"}}`)
	got, ok := Resolve(dir, time.Minute, func() ([]byte, error) { return errPayload, nil })
	if !ok || string(got) != goodPayload {
		t.Errorf("stale-good fallback failed: ok=%v got=%q", ok, got)
	}
	if b, _ := os.ReadFile(p); string(b) != goodPayload {
		t.Errorf("error payload overwrote good cache: %q", b)
	}

	// Transport failure: same fallback.
	got, ok = Resolve(dir, time.Minute, func() ([]byte, error) { return nil, errors.New("timeout") })
	if !ok || string(got) != goodPayload {
		t.Errorf("stale-good fallback on transport error failed: ok=%v", ok)
	}
}

func TestResolveFreshCacheSkipsFetch(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "usage"), []byte(goodPayload), 0o600); err != nil {
		t.Fatal(err)
	}
	_, ok := Resolve(dir, time.Minute, func() ([]byte, error) {
		t.Fatal("fetch called despite fresh cache")
		return nil, nil
	})
	if !ok {
		t.Error("fresh cache not served")
	}
}

func TestParseMalformedResetTimestampYieldsEmptyLabel(t *testing.T) {
	payload := `{"five_hour":{"utilization":50,"resets_at":"not-a-timestamp"}}`
	u, err := Parse([]byte(payload), now)
	if err != nil {
		t.Fatalf("Parse(%q): %v", payload, err)
	}
	if u.R5 != "" {
		t.Errorf("Parse(%q).R5 = %q, want empty label for malformed resets_at", payload, u.R5)
	}
}

func TestTokenFromKeychainRejectsNonJSON(t *testing.T) {
	in := "keychain: locked"
	if tok, err := TokenFromKeychain(in); err == nil {
		t.Errorf("TokenFromKeychain(%q) = %q, nil; want error for non-JSON", in, tok)
	}
}

func TestTokenFromKeychain(t *testing.T) {
	tok, err := TokenFromKeychain(`{"claudeAiOauth":{"accessToken":"tok-123"}}`)
	if err != nil || tok != "tok-123" {
		t.Errorf("TokenFromKeychain = %q, %v", tok, err)
	}
	if _, err := TokenFromKeychain(`{}`); err == nil {
		t.Error("missing token must error")
	}
}
