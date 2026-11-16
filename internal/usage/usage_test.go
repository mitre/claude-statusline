package usage

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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

// scopedPayload carries one plan limit narrowed to a model scope, alongside
// the two unscoped ones. The model name is synthetic on purpose: the label
// is read from the payload, so the vendor can rename the scoped model with
// no change here.
const scopedPayload = `{
  "five_hour": {"utilization": 24, "resets_at": "2026-07-03T20:30:00Z"},
  "seven_day": {"utilization": 71, "resets_at": "2026-07-06T17:00:00Z"},
  "extra_usage": {"is_enabled": true},
  "spend": {"used": {"amount_minor": 0, "exponent": 2}},
  "limits": [
    {"kind": "session", "percent": 24, "is_active": false},
    {"kind": "weekly_all", "percent": 71, "is_active": false},
    {"kind": "weekly_scoped", "percent": 100, "is_active": true,
     "resets_at": "2026-07-06T17:00:00Z",
     "scope": {"model": {"id": null, "display_name": "Zephyr"}}}
  ]
}`

func TestParseExtractsScopedLimits(t *testing.T) {
	u, err := Parse([]byte(scopedPayload), now, "")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(u.Scoped) != 1 {
		t.Fatalf("Scoped = %+v, want only the scoped limit (unscoped ones stay out)", u.Scoped)
	}
	got := u.Scoped[0]
	if got.Name != "Zephyr" {
		t.Errorf("Name = %q, want the payload's scope display name", got.Name)
	}
	if got.Pct != 100 {
		t.Errorf("Pct = %d, want 100", got.Pct)
	}
	if got.Reset != "Mon 10:00a" {
		t.Errorf("Reset = %q, want \"Mon 10:00a\"", got.Reset)
	}
}

func TestParseKeepsScopedLimitTheVendorCannotName(t *testing.T) {
	// A scope with no usable name still surfaces: a binding limit is never
	// dropped, and the meter never renders a blank label.
	raw := `{
  "five_hour": {"utilization": 24, "resets_at": "2026-07-03T20:30:00Z"},
  "limits": [
    {"kind": "weekly_scoped", "percent": 90, "is_active": true,
     "resets_at": "2026-07-06T17:00:00Z",
     "scope": {"model": {"id": null, "display_name": ""}, "surface": null}}
  ]
}`
	u, err := Parse([]byte(raw), now, "")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(u.Scoped) != 1 {
		t.Fatalf("Scoped = %+v, want the nameless scoped limit kept", u.Scoped)
	}
	if u.Scoped[0].Name != "scoped" {
		t.Errorf("Name = %q, want the generic %q label", u.Scoped[0].Name, "scoped")
	}
	if u.Scoped[0].Pct != 90 {
		t.Errorf("Pct = %d, want 90", u.Scoped[0].Pct)
	}
}

const opusPayload = `{
  "five_hour": {"utilization": 28, "resets_at": "2026-07-03T20:30:00Z"},
  "seven_day": {"utilization": 18, "resets_at": "2026-07-06T17:00:00Z"},
  "seven_day_opus": {"utilization": 41.7, "resets_at": "2026-07-07T22:00:00Z"},
  "seven_day_sonnet": {"utilization": 3}
}`

// unlistedModelPayload names a weekly window for a model that exists in no
// source-level list, alongside a non-model scope real payloads carry. Both
// the key and the model name below are invented: the only way these tests
// pass is by reading the payload's own vocabulary.
const unlistedModelPayload = `{
  "five_hour": {"utilization": 24, "resets_at": "2026-07-03T20:30:00Z"},
  "seven_day": {"utilization": 71, "resets_at": "2026-07-06T17:00:00Z"},
  "seven_day_oauth_apps": {"utilization": 5},
  "seven_day_zephyr": {"utilization": 62.4, "resets_at": "2026-07-07T22:00:00Z"}
}`

func TestParseResolvesWindowForModelInNoList(t *testing.T) {
	u, err := Parse([]byte(unlistedModelPayload), now, "Zephyr 5 (1M context)")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if u.ModelFamily != "zephyr" {
		t.Errorf("ModelFamily = %q, want the payload's own key suffix", u.ModelFamily)
	}
	if u.ModelPct != 62 {
		t.Errorf("ModelPct = %d, want floor(62.4)=62", u.ModelPct)
	}
	// 2026-07-07 is a Tuesday; 22:00Z → 15:00 PDT.
	if u.ModelReset != "Tue 3:00p" {
		t.Errorf("ModelReset = %q, want \"Tue 3:00p\"", u.ModelReset)
	}
}

func TestParseOmitsWindowWhenNoKeyMatchesTheModel(t *testing.T) {
	u, err := Parse([]byte(unlistedModelPayload), now, "Mythos 2")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if u.ModelFamily != "" || u.ModelPct != 0 || u.ModelReset != "" {
		t.Errorf("unmatched model fabricated a window: %+v", u)
	}
}

func TestParseDoesNotMatchNonModelScopes(t *testing.T) {
	// seven_day_oauth_apps is a surface scope, not a model window: no model
	// name implies it, so it must never be picked as one.
	u, err := Parse([]byte(unlistedModelPayload), now, "OAuth 1")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if u.ModelFamily != "" {
		t.Errorf("ModelFamily = %q, want none — a surface scope is not a model window", u.ModelFamily)
	}
}

func TestParsePrefersTheMoreSpecificWindowKey(t *testing.T) {
	// Two keys match: selection must be deterministic and pick the more
	// specific one, never whichever the map happened to yield first. The
	// underscored key must line up with the spaced display name.
	raw := `{
  "five_hour": {"utilization": 24, "resets_at": "2026-07-03T20:30:00Z"},
  "seven_day_zephyr": {"utilization": 10},
  "seven_day_zephyr_compact": {"utilization": 77, "resets_at": "2026-07-07T22:00:00Z"}
}`
	for i := 0; i < 20; i++ {
		u, err := Parse([]byte(raw), now, "Zephyr Compact 5")
		if err != nil {
			t.Fatalf("Parse: %v", err)
		}
		if u.ModelFamily != "zephyr_compact" || u.ModelPct != 77 {
			t.Fatalf("run %d: got %q %d, want zephyr_compact 77", i, u.ModelFamily, u.ModelPct)
		}
	}
}

func TestParseExtractsModelWindow(t *testing.T) {
	u, err := Parse([]byte(opusPayload), now, "Opus 4.8")
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
	got, _, ok := resolveAs(dir, time.Minute, now, func() ([]byte, error) { return []byte(goodPayload), nil })
	if !ok {
		t.Fatal("Resolve returned no payload")
	}
	if string(got) != goodPayload {
		t.Errorf("payload altered")
	}
	if b, err := readOwned(payloadPath(dir, testOwner)); err != nil || string(b) != goodPayload {
		t.Errorf("good payload not cached: %v %q", err, b)
	}
}

func TestResolveFallsBackToStaleOnBadFetch(t *testing.T) {
	dir := t.TempDir()
	p := payloadPath(dir, testOwner)
	if err := writeOwned(p, []byte(goodPayload), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	_ = os.Chtimes(p, old, old) // expired

	errPayload := []byte(`{"error":{"type":"rate_limit_error"}}`)
	got, _, ok := resolveAs(dir, time.Minute, now, func() ([]byte, error) { return errPayload, nil })
	if !ok || string(got) != goodPayload {
		t.Errorf("stale-good fallback failed: ok=%v got=%q", ok, got)
	}
	if b, _ := readOwned(p); string(b) != goodPayload {
		t.Errorf("error payload overwrote good cache: %q", b)
	}

	// Transport failure: same fallback.
	got, _, ok = resolveAs(dir, time.Minute, now, func() ([]byte, error) { return nil, errors.New("timeout") })
	if !ok || string(got) != goodPayload {
		t.Errorf("stale-good fallback on transport error failed: ok=%v", ok)
	}
}

func TestResolveFreshCacheSkipsFetch(t *testing.T) {
	dir := t.TempDir()
	if err := writeOwned(payloadPath(dir, testOwner), []byte(goodPayload), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, ok := resolveAs(dir, time.Minute, now, func() ([]byte, error) {
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
	p := payloadPath(dir, testOwner)
	if err := writeOwned(p, []byte(goodPayload), 0o600); err != nil {
		t.Fatal(err)
	}
	fetched := realNow.Add(-10 * time.Minute) // older than the 1m TTL
	if err := os.Chtimes(p, fetched, fetched); err != nil {
		t.Fatal(err)
	}

	_, staleFor, ok := resolveAs(dir, time.Minute, realNow, func() ([]byte, error) { return nil, errors.New("network down") })
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
	if err := writeOwned(payloadPath(dir, testOwner), []byte(goodPayload), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, staleFor, ok := resolveAs(dir, time.Minute, realNow, func() ([]byte, error) {
		t.Fatal("fetch called despite fresh cache")
		return nil, nil
	}); !ok || staleFor != 0 {
		t.Errorf("fresh cache: ok=%v staleFor=%v, want served with zero age", ok, staleFor)
	}

	dir = t.TempDir()
	if _, staleFor, ok := resolveAs(dir, time.Minute, realNow, func() ([]byte, error) { return []byte(goodPayload), nil }); !ok || staleFor != 0 {
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
	p := payloadPath(dir, testOwner)
	if err := writeOwned(p, []byte(payload), 0o600); err != nil {
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
	got, _, ok := resolveAs(dir, 10*time.Minute, realNow, func() ([]byte, error) { calls++; return []byte(rolled), nil })
	if !ok || string(got) != rolled || calls != 1 {
		t.Errorf("five_hour boundary: ok=%v calls=%d got=%q; want refetched rolled payload", ok, calls, got)
	}
	if b, _ := readOwned(payloadPath(dir, testOwner)); string(b) != rolled {
		t.Errorf("rolled payload not cached: %q", b)
	}

	// seven_day reset passed; five_hour still ahead — must also trigger.
	dir, _ = boundaryFixture(t, realNow, realNow.Add(2*time.Hour), realNow.Add(-time.Minute))
	calls = 0
	got, _, ok = resolveAs(dir, 10*time.Minute, realNow, func() ([]byte, error) { calls++; return []byte(rolled), nil })
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
	p := payloadPath(dir, testOwner)
	if err := writeOwned(p, []byte(stillPast), 0o600); err != nil {
		t.Fatal(err)
	}
	after := realNow.Add(-2 * time.Minute) // fetched AFTER the reset moment
	if err := os.Chtimes(p, after, after); err != nil {
		t.Fatal(err)
	}

	got, _, ok := resolveAs(dir, 10*time.Minute, realNow, func() ([]byte, error) {
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
	got, staleFor, ok := resolveAs(dir, 10*time.Minute, realNow, func() ([]byte, error) { return nil, errors.New("api down") })
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
	if err := writeOwned(payloadPath(dir, testOwner), []byte(noResets), 0o600); err != nil {
		t.Fatal(err)
	}
	got, _, ok := resolveAs(dir, time.Minute, realNow, func() ([]byte, error) {
		t.Fatal("fetch called despite fresh cache without resets_at")
		return nil, nil
	})
	if !ok || string(got) != noResets {
		t.Errorf("fresh cache without resets_at must serve: ok=%v", ok)
	}

	// Unparseable resets_at: same — plain TTL, no crash, no fetch.
	dir = t.TempDir()
	badReset := `{"five_hour": {"utilization": 5.9, "resets_at": "not-a-timestamp"}}`
	if err := writeOwned(payloadPath(dir, testOwner), []byte(badReset), 0o600); err != nil {
		t.Fatal(err)
	}
	got, _, ok = resolveAs(dir, time.Minute, realNow, func() ([]byte, error) {
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

func TestResetLabelUnix(t *testing.T) {
	// One formatting source of truth with resetLabel: same-day → short
	// form, other-day → weekday prefix, zero → no label. Fixed now for
	// determinism; locations resolved through now's zone like resetLabel.
	now := time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC)
	sameDay := time.Date(2026, 7, 3, 13, 30, 0, 0, time.UTC).Unix()
	otherDay := time.Date(2026, 7, 6, 10, 0, 0, 0, time.UTC).Unix()

	if got := ResetLabelUnix(sameDay, now); got != "1:30p" {
		t.Errorf("same-day label = %q, want \"1:30p\"", got)
	}
	if got := ResetLabelUnix(otherDay, now); got != "Mon 10:00a" {
		t.Errorf("other-day label = %q, want \"Mon 10:00a\"", got)
	}
	if got := ResetLabelUnix(0, now); got != "" {
		t.Errorf("zero epoch label = %q, want \"\"", got)
	}
}

// nullFiveHourPayload is the shape the usage endpoint serves when no
// five-hour window is in progress: five_hour is null while the weekly window
// and the plan limits are still present. Captured live 2026-11-02.
const nullFiveHourPayload = `{
  "five_hour": null,
  "seven_day": {"utilization": 43.0, "resets_at": "2026-11-06T22:00:00Z"},
  "extra_usage": {"is_enabled": false},
  "spend": {"used": {"amount_minor": 0, "exponent": 2}},
  "limits": [
    {"kind": "weekly_all", "percent": 43, "is_active": false, "resets_at": "2026-11-06T22:00:00Z", "scope": null},
    {"kind": "weekly_scoped", "percent": 47, "is_active": true, "resets_at": "2026-11-06T22:00:00Z",
     "scope": {"model": {"display_name": "Fable"}}}
  ]
}`

func TestParseAcceptsNullFiveHour(t *testing.T) {
	// A null five_hour means "no five-hour window in progress", not "not
	// usage data": the weekly and scoped readings are real and must render,
	// with the five-hour meter marked absent rather than fabricated as 0%.
	u, err := Parse([]byte(nullFiveHourPayload), now, "")
	if err != nil {
		t.Fatalf("Parse rejected a null five_hour payload: %v", err)
	}
	if !u.FiveHourAbsent {
		t.Error("FiveHourAbsent = false, want true")
	}
	if u.U5 != 0 || u.R5 != "" {
		t.Errorf("U5/R5 = %d/%q, want zero values when the window is absent", u.U5, u.R5)
	}
	if u.U7 != 43 || u.R7 == "" {
		t.Errorf("U7/R7 = %d/%q, want 43 with a reset label", u.U7, u.R7)
	}
	if len(u.Scoped) != 1 || u.Scoped[0].Name != "Fable" || u.Scoped[0].Pct != 47 {
		t.Errorf("Scoped = %+v, want one Fable limit at 47%%", u.Scoped)
	}
}

func TestParseKeepsFiveHourPresentFlagOnFullPayload(t *testing.T) {
	u, err := Parse([]byte(goodPayload), now, "")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if u.FiveHourAbsent {
		t.Error("FiveHourAbsent = true on a payload that carries five_hour")
	}
	if u.SevenDayAbsent {
		t.Error("SevenDayAbsent = true on a payload that carries seven_day")
	}
}

func TestParseTreatsNullUtilizationAsAbsentWindow(t *testing.T) {
	// A window object whose utilization is null carries no reading: it is
	// absent, exactly like a null window — never rendered as 0%.
	raw := `{"five_hour": {"utilization": null}, "seven_day": {"utilization": null, "resets_at": "2026-11-06T22:00:00Z"},
	  "limits": [{"kind": "weekly_scoped", "percent": 40, "is_active": true,
	    "scope": {"model": {"display_name": "Fable"}}}]}`
	u, err := Parse([]byte(raw), now, "")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !u.FiveHourAbsent || !u.SevenDayAbsent {
		t.Errorf("FiveHourAbsent/SevenDayAbsent = %v/%v, want true/true", u.FiveHourAbsent, u.SevenDayAbsent)
	}
}

func TestParseAcceptsWeeklyWindowAlone(t *testing.T) {
	// The shape gate admits a payload whose only reading is the weekly
	// window; rejecting it would freeze the row on a stale cache.
	u, err := Parse([]byte(`{"five_hour": null, "seven_day": {"utilization": 43.0}}`), now, "")
	if err != nil {
		t.Fatalf("Parse rejected a weekly-only payload: %v", err)
	}
	if u.SevenDayAbsent || u.U7 != 43 || !u.FiveHourAbsent {
		t.Errorf("SevenDayAbsent/U7/FiveHourAbsent = %v/%d/%v, want false/43/true", u.SevenDayAbsent, u.U7, u.FiveHourAbsent)
	}
}

func TestParseMarksAbsentWeeklyWindow(t *testing.T) {
	// A payload with neither window passes the shape gate on its plan
	// limits; the weekly window is marked absent so no "week 0%" renders.
	raw := `{"five_hour": null, "seven_day": null,
	  "limits": [{"kind": "weekly_scoped", "percent": 40, "is_active": true,
	    "resets_at": "2026-11-06T22:00:00Z", "scope": {"model": {"display_name": "Fable"}}}]}`
	u, err := Parse([]byte(raw), now, "")
	if err != nil {
		t.Fatalf("Parse rejected a limits-only payload: %v", err)
	}
	if !u.SevenDayAbsent || !u.FiveHourAbsent {
		t.Errorf("SevenDayAbsent/FiveHourAbsent = %v/%v, want true/true", u.SevenDayAbsent, u.FiveHourAbsent)
	}
	if u.U7 != 0 || u.R7 != "" {
		t.Errorf("U7/R7 = %d/%q, want zero values when the window is absent", u.U7, u.R7)
	}
	if len(u.Scoped) != 1 || u.Scoped[0].Name != "Fable" || u.Scoped[0].Pct != 40 {
		t.Errorf("Scoped = %+v, want one Fable limit at 40%%", u.Scoped)
	}
}

func TestParseRejectsPayloadWithoutAnyWindow(t *testing.T) {
	// The shape gate must stay strict: JSON that carries neither a window
	// nor a limit is not usage data, whatever else it contains.
	for _, raw := range []string{
		`{}`,
		`{"five_hour": null, "seven_day": null}`,
		`{"five_hour": null, "seven_day": null, "limits": []}`,
		`{"type":"error","error":{"type":"rate_limit_error","message":"Rate limited."}}`,
	} {
		if _, err := Parse([]byte(raw), now, ""); err == nil {
			t.Errorf("Parse accepted %s as usage data", raw)
		}
	}
}

func TestResolveReplacesStaleCacheWithNullFiveHourPayload(t *testing.T) {
	// The bug this guards: a live payload with five_hour null was rejected
	// by the shape gate, so the stale cache was served forever with a
	// growing age marker. The live payload must be cached and served fresh.
	realNow := time.Now().Truncate(time.Second)
	dir := t.TempDir()
	p := payloadPath(dir, testOwner)
	if err := writeOwned(p, []byte(goodPayload), 0o600); err != nil {
		t.Fatal(err)
	}
	old := realNow.Add(-69 * time.Hour)
	if err := os.Chtimes(p, old, old); err != nil {
		t.Fatal(err)
	}
	raw, staleFor, ok := resolveAs(dir, time.Minute, realNow, func() ([]byte, error) { return []byte(nullFiveHourPayload), nil })
	if !ok || staleFor != 0 {
		t.Fatalf("Resolve: ok=%v staleFor=%v, want the live payload served with zero age", ok, staleFor)
	}
	if string(raw) != nullFiveHourPayload {
		t.Errorf("Resolve served %q, want the live payload", raw)
	}
	cached, err := readOwned(p)
	if err != nil || string(cached) != nullFiveHourPayload {
		t.Errorf("cache not replaced: err=%v content=%q", err, cached)
	}
}

// staleFixture writes a cache file older than any TTL so every Resolve call
// below must decide whether to attempt a fetch.
func staleFixture(t *testing.T, realNow time.Time) string {
	t.Helper()
	dir := t.TempDir()
	p := payloadPath(dir, testOwner)
	if err := writeOwned(p, []byte(goodPayload), 0o600); err != nil {
		t.Fatal(err)
	}
	old := realNow.Add(-2 * time.Hour)
	if err := os.Chtimes(p, old, old); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestResolveNeverAttemptsFetchWithinMinFetchInterval(t *testing.T) {
	// A failed fetch must not be retried on the next render: with the cache
	// stale, renders arrive every few hundred milliseconds and the endpoint
	// rate-limits the storm. One attempt per MinFetchInterval, no matter
	// how the attempt ended; stale-good is served in between.
	realNow := time.Now().Truncate(time.Second)
	dir := staleFixture(t, realNow)
	calls := 0
	failing := func() ([]byte, error) { calls++; return nil, errors.New("network down") }

	if _, _, ok := resolveAs(dir, time.Minute, realNow, failing); !ok {
		t.Fatal("stale-good fallback not served on the first failure")
	}
	resolveAs(dir, time.Minute, realNow.Add(time.Second), failing)
	resolveAs(dir, time.Minute, realNow.Add(MinFetchInterval-time.Second), failing)
	if calls != 1 {
		t.Fatalf("fetch attempted %d times within MinFetchInterval, want exactly 1", calls)
	}
	resolveAs(dir, time.Minute, realNow.Add(MinFetchInterval+time.Second), failing)
	if calls != 2 {
		t.Errorf("fetch attempted %d times after MinFetchInterval elapsed, want 2", calls)
	}
}

func TestResolveThrottlesAfterRejectedPayloadToo(t *testing.T) {
	// A fetch that "succeeds" with a non-usage body (an error payload) is an
	// attempt like any other and must be throttled the same way.
	realNow := time.Now().Truncate(time.Second)
	dir := staleFixture(t, realNow)
	calls := 0
	errBody := func() ([]byte, error) { calls++; return []byte(`{"error":{"type":"rate_limit_error"}}`), nil }
	resolveAs(dir, time.Minute, realNow, errBody)
	resolveAs(dir, time.Minute, realNow.Add(5*time.Second), errBody)
	if calls != 1 {
		t.Errorf("fetch attempted %d times, want 1", calls)
	}
}

func TestResolveHonorsRetryAfterLongerThanFloor(t *testing.T) {
	// A 429 carrying Retry-After longer than the floor holds fetching off
	// for the server's full interval; shorter values still get the floor.
	realNow := time.Now().Truncate(time.Second)
	dir := staleFixture(t, realNow)
	calls := 0
	limited := func() ([]byte, error) { calls++; return nil, &RetryAfterError{After: 15 * time.Minute} }
	resolveAs(dir, time.Minute, realNow, limited)
	resolveAs(dir, time.Minute, realNow.Add(MinFetchInterval+time.Second), limited)
	if calls != 1 {
		t.Fatalf("fetch attempted %d times inside the server's Retry-After, want 1", calls)
	}
	resolveAs(dir, time.Minute, realNow.Add(15*time.Minute+time.Second), limited)
	if calls != 2 {
		t.Errorf("fetch attempted %d times after Retry-After elapsed, want 2", calls)
	}

	dir = staleFixture(t, realNow)
	calls = 0
	short := func() ([]byte, error) { calls++; return nil, &RetryAfterError{After: 5 * time.Second} }
	resolveAs(dir, time.Minute, realNow, short)
	resolveAs(dir, time.Minute, realNow.Add(10*time.Second), short)
	if calls != 1 {
		t.Errorf("fetch attempted %d times: a short Retry-After must not undercut the floor", calls)
	}
}

func TestResolveSuccessfulFetchKeepsTTLCadence(t *testing.T) {
	// After a good payload is cached the TTL governs as before: a fresh
	// cache is served without any fetch, and the cadence is the larger of
	// the configured TTL and the floor.
	realNow := time.Now()
	dir := t.TempDir()
	calls := 0
	good := func() ([]byte, error) { calls++; return []byte(goodPayload), nil }
	if _, _, ok := resolveAs(dir, MinFetchInterval, realNow, good); !ok || calls != 1 {
		t.Fatalf("first resolve: ok=%v calls=%d", ok, calls)
	}
	if _, staleFor, ok := resolveAs(dir, MinFetchInterval, realNow.Add(time.Second), good); !ok || staleFor != 0 || calls != 1 {
		t.Errorf("fresh cache served with a fetch: ok=%v staleFor=%v calls=%d", ok, staleFor, calls)
	}
}

func TestMinFetchIntervalFloor(t *testing.T) {
	if MinFetchInterval < 200*time.Second {
		t.Errorf("MinFetchInterval = %v, must be at least 200s (the endpoint's observed Retry-After)", MinFetchInterval)
	}
}

// testOwner is the account and token every single-account Resolve test
// runs as.
var testOwner = Owner{Account: "acct-A", Fingerprint: "fp-A"}

// resolveAs runs Resolve as testOwner, with the owner unchanged by the fetch.
func resolveAs(dir string, ttl time.Duration, now time.Time, fetch func() ([]byte, error)) ([]byte, time.Duration, bool) {
	return Resolve(dir, testOwner, func() Owner { return testOwner }, ttl, now, fetch)
}

var (
	ownerA = Owner{Account: "11111111-aaaa-4000-8000-000000000001", Fingerprint: "aaaaaaaaaaaaaaaa"}
	ownerB = Owner{Account: "22222222-bbbb-4000-8000-000000000002", Fingerprint: "bbbbbbbbbbbbbbbb"}
	// payloadB differs from goodPayload, so B's numbers are recognizable.
	payloadB = strings.Replace(goodPayload, `"utilization": 13,`, `"utilization": 77,`, 1)
)

// as returns a current-owner callback that always reports o.
func as(o Owner) func() Owner { return func() Owner { return o } }

func noFetch(t *testing.T) func() ([]byte, error) {
	return func() ([]byte, error) {
		t.Fatal("fetch must not run")
		return nil, nil
	}
}

// closeGate holds the shared fetch gate shut for an hour.
func closeGate(t *testing.T, dir string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, nextAttemptFile), []byte(strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10)), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestResolvePayloadIsKeyedByAccount(t *testing.T) {
	dir := t.TempDir()
	realNow := time.Now()
	if _, _, ok := Resolve(dir, ownerA, as(ownerA), time.Hour, realNow, func() ([]byte, error) { return []byte(goodPayload), nil }); !ok {
		t.Fatal("A's fetch not served")
	}
	// The gate is now shut for everyone; B has no payload of its own.
	if got, _, ok := Resolve(dir, ownerB, as(ownerB), time.Hour, realNow, noFetch(t)); ok {
		t.Errorf("B was served A's payload: %q", got)
	}
}

func TestResolveServesPayloadOnlyToTheTokenThatFetchedIt(t *testing.T) {
	// Same account, different token (a /login that rewrote the token but
	// not yet the record, or the reverse): the payload is not served.
	dir := t.TempDir()
	realNow := time.Now()
	Resolve(dir, ownerA, as(ownerA), time.Hour, realNow, func() ([]byte, error) { return []byte(goodPayload), nil })
	otherToken := Owner{Account: ownerA.Account, Fingerprint: ownerB.Fingerprint}
	if got, _, ok := Resolve(dir, otherToken, as(otherToken), time.Hour, realNow, noFetch(t)); ok {
		t.Errorf("payload served to another token: %q", got)
	}
	// Fresh and stale alike: age the file past the TTL.
	old := realNow.Add(-2 * time.Hour)
	files, _ := filepath.Glob(filepath.Join(dir, "usage-*"))
	for _, f := range files {
		_ = os.Chtimes(f, old, old)
	}
	if got, _, ok := Resolve(dir, otherToken, as(otherToken), time.Hour, realNow, noFetch(t)); ok {
		t.Errorf("stale payload served to another token: %q", got)
	}
	if _, staleFor, ok := Resolve(dir, ownerA, as(ownerA), time.Hour, realNow, noFetch(t)); !ok || staleFor == 0 {
		t.Errorf("owner's own stale payload must still serve with its age: ok=%v staleFor=%v", ok, staleFor)
	}
}

func TestResolveWithoutAnOwnerTouchesNothing(t *testing.T) {
	// No account record or no readable token: no fetch, no read, no write.
	for _, o := range []Owner{{}, {Account: ownerA.Account}, {Fingerprint: ownerA.Fingerprint}} {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "usage"), []byte(goodPayload), 0o600); err != nil {
			t.Fatal(err)
		}
		if got, _, ok := Resolve(dir, o, as(o), time.Hour, time.Now(), noFetch(t)); ok {
			t.Errorf("owner %+v was served %q", o, got)
		}
		entries, _ := os.ReadDir(dir)
		if len(entries) != 1 {
			t.Errorf("owner %+v wrote files: %v", o, entries)
		}
	}
}

func TestResolveNeverReadsTheLegacyPayload(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "usage"), []byte(goodPayload), 0o600); err != nil {
		t.Fatal(err)
	}
	closeGate(t, dir)
	if got, _, ok := Resolve(dir, ownerA, as(ownerA), time.Hour, time.Now(), noFetch(t)); ok {
		t.Errorf("legacy unkeyed payload was served: %q", got)
	}
}

func TestResolveDiscardsAFetchWhenTheOwnerChangesDuringIt(t *testing.T) {
	for name, after := range map[string]Owner{
		"account changed": ownerB,
		"token changed":   {Account: ownerA.Account, Fingerprint: ownerB.Fingerprint},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			got, _, ok := Resolve(dir, ownerA, as(after), time.Hour, time.Now(), func() ([]byte, error) { return []byte(goodPayload), nil })
			if ok {
				t.Errorf("a response fetched across an owner change was rendered: %q", got)
			}
			if files, _ := filepath.Glob(filepath.Join(dir, "usage-*")); len(files) != 0 {
				t.Errorf("a response fetched across an owner change was cached: %v", files)
			}
		})
	}
}

func TestResolveCacheFilesCarryNoAccountID(t *testing.T) {
	dir := t.TempDir()
	Resolve(dir, ownerA, as(ownerA), time.Hour, time.Now(), func() ([]byte, error) { return []byte(goodPayload), nil })
	entries, _ := os.ReadDir(dir)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
		if strings.Contains(e.Name(), ownerA.Account) || strings.Contains(e.Name(), "11111111") {
			t.Errorf("file name carries the account ID: %s", e.Name())
		}
	}
	if files, _ := filepath.Glob(filepath.Join(dir, "usage-*")); len(files) != 1 {
		t.Errorf("want one keyed payload file, got %v", names)
	}
}

func TestResolveAccountsInterleaved(t *testing.T) {
	// A renders, then B (whose fetch has some outcome), then A again. B never
	// gets A's payload; A keeps its own, fresh or stale with its age.
	outcomes := map[string]func() ([]byte, error){
		"B fetch succeeds": func() ([]byte, error) { return []byte(payloadB), nil },
		"B body rejected":  func() ([]byte, error) { return []byte(`{"error":{"type":"rate_limit_error"}}`), nil },
		"B network error":  func() ([]byte, error) { return nil, errors.New("network down") },
		"B told to retry":  func() ([]byte, error) { return nil, &RetryAfterError{After: 15 * time.Minute} },
	}
	for _, aStale := range []bool{false, true} {
		for name, bFetch := range outcomes {
			t.Run(fmt.Sprintf("%s, A stale=%v", name, aStale), func(t *testing.T) {
				dir := t.TempDir()
				realNow := time.Now().Truncate(time.Second)
				Resolve(dir, ownerA, as(ownerA), time.Hour, realNow, func() ([]byte, error) { return []byte(goodPayload), nil })
				if aStale {
					old := realNow.Add(-2 * time.Hour)
					files, _ := filepath.Glob(filepath.Join(dir, "usage-*"))
					for _, f := range files {
						_ = os.Chtimes(f, old, old)
					}
				}
				// B's turn: open the gate so B's fetch runs.
				_ = os.Remove(filepath.Join(dir, nextAttemptFile))
				gotB, _, okB := Resolve(dir, ownerB, as(ownerB), time.Hour, realNow, bFetch)
				if okB && string(gotB) == goodPayload {
					t.Fatalf("B was served A's payload")
				}
				if name == "B fetch succeeds" && (!okB || string(gotB) != payloadB) {
					t.Errorf("B's own fetch not served: ok=%v", okB)
				}
				// A again, gate shut by B's attempt: A's own payload only.
				gotA, staleFor, okA := Resolve(dir, ownerA, as(ownerA), time.Hour, realNow, noFetch(t))
				if !okA || string(gotA) != goodPayload {
					t.Fatalf("A lost its own payload: ok=%v got=%q", okA, gotA)
				}
				if aStale != (staleFor > 0) {
					t.Errorf("A stale=%v but staleFor=%v", aStale, staleFor)
				}
			})
		}
	}
}

// writeOwned writes payload to p in the cached form, owned by testOwner.
func writeOwned(p string, payload []byte, perm os.FileMode) error {
	return os.WriteFile(p, []byte(ownedFile(testOwner, payload)), perm)
}

// readOwned returns the payload of the cached file at p, owned by testOwner.
func readOwned(p string) ([]byte, error) {
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	payload, ok := ownedBy(string(b), testOwner)
	if !ok {
		return nil, fmt.Errorf("%s is not owned by the test owner", p)
	}
	return payload, nil
}

func TestResolveTwoTokensOfOneAccountKeepTheirOwnPayloads(t *testing.T) {
	// The same account logged in under two config directories has two
	// tokens; each token's payload is its own entry, so neither evicts the
	// other.
	dir := t.TempDir()
	realNow := time.Now()
	tok1 := Owner{Account: ownerA.Account, Fingerprint: "1111111111111111"}
	tok2 := Owner{Account: ownerA.Account, Fingerprint: "2222222222222222"}
	Resolve(dir, tok1, as(tok1), time.Hour, realNow, func() ([]byte, error) { return []byte(goodPayload), nil })
	_ = os.Remove(filepath.Join(dir, nextAttemptFile))
	Resolve(dir, tok2, as(tok2), time.Hour, realNow, func() ([]byte, error) { return []byte(payloadB), nil })
	if got, _, ok := Resolve(dir, tok1, as(tok1), time.Hour, realNow, noFetch(t)); !ok || string(got) != goodPayload {
		t.Errorf("token 1 lost its payload to token 2: ok=%v", ok)
	}
	if got, _, ok := Resolve(dir, tok2, as(tok2), time.Hour, realNow, noFetch(t)); !ok || string(got) != payloadB {
		t.Errorf("token 2 lost its payload: ok=%v", ok)
	}
}
