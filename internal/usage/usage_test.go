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
	_, err := Parse([]byte(`{"error":{"type":"rate_limit_error","message":"Rate limited."}}`), now, "")
	if err == nil {
		t.Fatal("error payload parsed as usage data")
	}
}

func TestParseExtractsFields(t *testing.T) {
	u, err := Parse([]byte(goodPayload), now, "")
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
	u, err := Parse([]byte(goodPayload), now, "")
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

const opusPayload = `{
  "five_hour": {"utilization": 28, "resets_at": "2026-07-03T20:30:00Z"},
  "seven_day": {"utilization": 18, "resets_at": "2026-07-06T17:00:00Z"},
  "seven_day_opus": {"utilization": 41.7, "resets_at": "2026-07-07T22:00:00Z"},
  "seven_day_sonnet": {"utilization": 3}
}`

func TestFamilyFromModelName(t *testing.T) {
	cases := []struct {
		name   string
		family string
	}{
		{name: "Opus 4.8", family: "opus"},
		{name: "Claude Sonnet 5", family: "sonnet"},
		{name: "Haiku 4.5", family: "haiku"},
		{name: "Fable 5", family: ""},
		{name: "Fable 5 (1M context)", family: ""},
		{name: "?", family: ""},
	}
	for _, c := range cases {
		if got := FamilyFromModelName(c.name); got != c.family {
			t.Errorf("FamilyFromModelName(%q) = %q, want %q", c.name, got, c.family)
		}
	}
}

func TestParseExtractsModelWindow(t *testing.T) {
	u, err := Parse([]byte(opusPayload), now, "opus")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if u.ModelFamily != "opus" || u.ModelPct != 41 {
		t.Errorf("model window = %q %d, want opus floor(41.7)=41", u.ModelFamily, u.ModelPct)
	}
	// 2026-07-07 is a Tuesday; 22:00Z -> 15:00 PDT.
	if u.ModelReset != "Tue 3:00p" {
		t.Errorf("ModelReset = %q, want \"Tue 3:00p\"", u.ModelReset)
	}
}

func TestParseOmitsModelWindowWhenAbsent(t *testing.T) {
	// Family requested but payload has no matching window: omit (Gate 8).
	u, err := Parse([]byte(goodPayload), now, "opus")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if u.ModelFamily != "" || u.ModelPct != 0 || u.ModelReset != "" {
		t.Errorf("absent window must omit segment, got %q %d %q", u.ModelFamily, u.ModelPct, u.ModelReset)
	}
	// Empty family: never populated even when windows exist.
	u, err = Parse([]byte(opusPayload), now, "")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if u.ModelFamily != "" {
		t.Errorf("empty family must omit segment, got %q", u.ModelFamily)
	}
}

func TestResolveCachesGoodPayload(t *testing.T) {
	dir := t.TempDir()
	got, _, ok := Resolve(dir, time.Minute, now, func() ([]byte, error) { return []byte(goodPayload), nil })
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
	got, _, ok := Resolve(dir, time.Minute, now, func() ([]byte, error) { return errPayload, nil })
	if !ok || string(got) != goodPayload {
		t.Errorf("stale-good fallback failed: ok=%v got=%q", ok, got)
	}
	if b, _ := os.ReadFile(p); string(b) != goodPayload {
		t.Errorf("error payload overwrote good cache: %q", b)
	}

	// Transport failure: same fallback.
	got, _, ok = Resolve(dir, time.Minute, now, func() ([]byte, error) { return nil, errors.New("timeout") })
	if !ok || string(got) != goodPayload {
		t.Errorf("stale-good fallback on transport error failed: ok=%v", ok)
	}
}

func TestResolveFreshCacheSkipsFetch(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "usage"), []byte(goodPayload), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, ok := Resolve(dir, time.Minute, now, func() ([]byte, error) {
		t.Fatal("fetch called despite fresh cache")
		return nil, nil
	})
	if !ok {
		t.Error("fresh cache not served")
	}
}

func TestResolveReportsServedPayloadAgeOnStaleFallback(t *testing.T) {
	// The stale-good fallback must report HOW old the payload it serves is —
	// the account row's dim age marker renders from it. Second-truncated
	// times so the mtime round-trips exactly through the filesystem.
	realNow := time.Now().Truncate(time.Second)
	dir := t.TempDir()
	p := filepath.Join(dir, "usage")
	if err := os.WriteFile(p, []byte(goodPayload), 0o600); err != nil {
		t.Fatal(err)
	}
	fetched := realNow.Add(-10 * time.Minute) // older than the 1m TTL
	if err := os.Chtimes(p, fetched, fetched); err != nil {
		t.Fatal(err)
	}

	_, staleFor, ok := Resolve(dir, time.Minute, realNow, func() ([]byte, error) { return nil, errors.New("network down") })
	if !ok {
		t.Fatal("stale-good fallback not served")
	}
	if staleFor != 10*time.Minute {
		t.Errorf("staleFor = %v, want exactly 10m (now - cache mtime)", staleFor)
	}
}

func TestResolveFreshAndLivePathsReportZeroAge(t *testing.T) {
	// The marker must never appear on healthy data: both fresh-cache and
	// live-fetch serves report a zero age.
	realNow := time.Now()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "usage"), []byte(goodPayload), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, staleFor, ok := Resolve(dir, time.Minute, realNow, func() ([]byte, error) {
		t.Fatal("fetch called despite fresh cache")
		return nil, nil
	}); !ok || staleFor != 0 {
		t.Errorf("fresh cache: ok=%v staleFor=%v, want served with zero age", ok, staleFor)
	}

	dir = t.TempDir()
	if _, staleFor, ok := Resolve(dir, time.Minute, realNow, func() ([]byte, error) { return []byte(goodPayload), nil }); !ok || staleFor != 0 {
		t.Errorf("live fetch: ok=%v staleFor=%v, want served with zero age", ok, staleFor)
	}
}

// boundaryFixture writes a TTL-fresh cache whose payload was fetched BEFORE
// its own reset moment: resets_at is in the past and the file mtime predates
// it. Returns the cache dir and the payload written.
func boundaryFixture(t *testing.T, realNow time.Time, fiveHourReset, sevenDayReset time.Time) (string, string) {
	t.Helper()
	dir := t.TempDir()
	payload := `{
	  "five_hour": {"utilization": 55, "resets_at": "` + fiveHourReset.Format(time.RFC3339) + `"},
	  "seven_day": {"utilization": 23, "resets_at": "` + sevenDayReset.Format(time.RFC3339) + `"}
	}`
	p := filepath.Join(dir, "usage")
	if err := os.WriteFile(p, []byte(payload), 0o600); err != nil {
		t.Fatal(err)
	}
	fetched := realNow.Add(-5 * time.Minute) // within a 10m TTL, before both resets
	if err := os.Chtimes(p, fetched, fetched); err != nil {
		t.Fatal(err)
	}
	return dir, payload
}

func TestResolveRefetchesWhenResetsAtPassed(t *testing.T) {
	realNow := time.Now()
	rolled := `{"five_hour": {"utilization": 0, "resets_at": "` +
		realNow.Add(5*time.Hour).Format(time.RFC3339) + `"}}`

	// five_hour reset passed 2 minutes ago; seven_day still ahead.
	dir, _ := boundaryFixture(t, realNow, realNow.Add(-2*time.Minute), realNow.Add(48*time.Hour))
	calls := 0
	got, _, ok := Resolve(dir, 10*time.Minute, realNow, func() ([]byte, error) { calls++; return []byte(rolled), nil })
	if !ok || string(got) != rolled || calls != 1 {
		t.Errorf("five_hour boundary: ok=%v calls=%d got=%q; want refetched rolled payload", ok, calls, got)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "usage")); string(b) != rolled {
		t.Errorf("rolled payload not cached: %q", b)
	}

	// seven_day reset passed; five_hour still ahead — must also trigger.
	dir, _ = boundaryFixture(t, realNow, realNow.Add(2*time.Hour), realNow.Add(-time.Minute))
	calls = 0
	got, _, ok = Resolve(dir, 10*time.Minute, realNow, func() ([]byte, error) { calls++; return []byte(rolled), nil })
	if !ok || string(got) != rolled || calls != 1 {
		t.Errorf("seven_day boundary: ok=%v calls=%d got=%q; want refetched rolled payload", ok, calls, got)
	}
}

func TestResolveBoundaryRefetchIsBounded(t *testing.T) {
	// A payload REfetched after the boundary that still reports a past
	// resets_at (API lag / clock skew): mtime >= resets_at, so the boundary
	// check must NOT fire — TTL cadence, no per-render fetch storm.
	realNow := time.Now()
	dir := t.TempDir()
	stillPast := `{"five_hour": {"utilization": 55, "resets_at": "` +
		realNow.Add(-10*time.Minute).Format(time.RFC3339) + `"}}`
	p := filepath.Join(dir, "usage")
	if err := os.WriteFile(p, []byte(stillPast), 0o600); err != nil {
		t.Fatal(err)
	}
	after := realNow.Add(-2 * time.Minute) // fetched AFTER the reset moment
	if err := os.Chtimes(p, after, after); err != nil {
		t.Fatal(err)
	}

	got, _, ok := Resolve(dir, 10*time.Minute, realNow, func() ([]byte, error) {
		t.Fatal("fetch called for a post-boundary payload — storm guard broken")
		return nil, nil
	})
	if !ok || string(got) != stillPast {
		t.Errorf("post-boundary payload must serve from cache: ok=%v got=%q", ok, got)
	}
}

func TestResolveBoundaryFetchFailureServesStaleGood(t *testing.T) {
	realNow := time.Now().Truncate(time.Second)
	dir, cached := boundaryFixture(t, realNow, realNow.Add(-2*time.Minute), realNow.Add(48*time.Hour))
	got, staleFor, ok := Resolve(dir, 10*time.Minute, realNow, func() ([]byte, error) { return nil, errors.New("api down") })
	if !ok || string(got) != cached {
		t.Errorf("boundary + failed fetch must serve stale-good payload: ok=%v got=%q", ok, got)
	}
	// Boundary-stale is stale even though the file is younger than the TTL:
	// the fallback engaged, so the age (fixture writes mtime 5m back) must
	// surface for the marker.
	if staleFor != 5*time.Minute {
		t.Errorf("staleFor = %v, want exactly 5m (boundary-stale payload age)", staleFor)
	}
}

func TestResolveMissingResetsAtKeepsTTLBehavior(t *testing.T) {
	realNow := time.Now()
	dir := t.TempDir()
	noResets := `{"five_hour": {"utilization": 5.9}, "seven_day": {"utilization": 13}}`
	if err := os.WriteFile(filepath.Join(dir, "usage"), []byte(noResets), 0o600); err != nil {
		t.Fatal(err)
	}
	got, _, ok := Resolve(dir, time.Minute, realNow, func() ([]byte, error) {
		t.Fatal("fetch called despite fresh cache without resets_at")
		return nil, nil
	})
	if !ok || string(got) != noResets {
		t.Errorf("fresh cache without resets_at must serve: ok=%v", ok)
	}

	// Unparseable resets_at: same — plain TTL, no crash, no fetch.
	dir = t.TempDir()
	badReset := `{"five_hour": {"utilization": 5.9, "resets_at": "not-a-timestamp"}}`
	if err := os.WriteFile(filepath.Join(dir, "usage"), []byte(badReset), 0o600); err != nil {
		t.Fatal(err)
	}
	got, _, ok = Resolve(dir, time.Minute, realNow, func() ([]byte, error) {
		t.Fatal("fetch called despite fresh cache with unparseable resets_at")
		return nil, nil
	})
	if !ok || string(got) != badReset {
		t.Errorf("fresh cache with unparseable resets_at must serve: ok=%v", ok)
	}
}

func TestParseMalformedResetTimestampYieldsEmptyLabel(t *testing.T) {
	payload := `{"five_hour":{"utilization":50,"resets_at":"not-a-timestamp"}}`
	u, err := Parse([]byte(payload), now, "")
	if err != nil {
		t.Fatalf("Parse(%q): %v", payload, err)
	}
	if u.R5 != "" {
		t.Errorf("Parse(%q).R5 = %q, want empty label for malformed resets_at", payload, u.R5)
	}
}

func TestTokenFromCredentialJSONRejectsNonJSON(t *testing.T) {
	in := "keychain: locked"
	if tok, err := TokenFromCredentialJSON(in); err == nil {
		t.Errorf("TokenFromCredentialJSON(%q) = %q, nil; want error for non-JSON", in, tok)
	}
}

func TestTokenFromCredentialJSON(t *testing.T) {
	tok, err := TokenFromCredentialJSON(`{"claudeAiOauth":{"accessToken":"tok-123"}}`)
	if err != nil || tok != "tok-123" {
		t.Errorf("TokenFromCredentialJSON = %q, %v", tok, err)
	}
	if _, err := TokenFromCredentialJSON(`{}`); err == nil {
		t.Error("missing token must error")
	}
}
