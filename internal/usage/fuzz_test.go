package usage

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// FuzzParse pins the trust boundary: usage-endpoint bodies are untrusted
// (rate-limit error bodies are valid JSON with the wrong shape), and Parse
// must never panic on any (body, family) pair — reject by error only.
func FuzzParse(f *testing.F) {
	const good = `{"five_hour":{"utilization":5.9,"resets_at":"2026-07-03T13:30:00Z"},` +
		`"seven_day":{"utilization":13,"resets_at":"2026-07-07T10:00:00Z"},` +
		`"seven_day_opus":{"utilization":41,"resets_at":"2026-07-08T15:00:00Z"},` +
		`"extra_usage":{"is_enabled":false},` +
		`"spend":{"used":{"amount_minor":0,"exponent":2}},"limits":[{"is_active":true,"percent":7}]}`
	f.Add([]byte(good), "opus")
	f.Add([]byte(good), "")
	f.Add([]byte(`{"error":{"type":"rate_limit_error","message":"exceeded"}}`), "sonnet")
	f.Add([]byte(`{"five_hour":null}`), "")
	f.Add([]byte(`{"five_hour":{"utilization":null}}`), "haiku")
	f.Add([]byte(`{"five_hour":{"utilization":1e308,"resets_at":"9999-12-31T23:59:59Z"}}`), "opus")
	f.Add([]byte(`{"five_hour":{"utilization":-1e308,"resets_at":"not-a-time"}}`), "opus")
	f.Add([]byte(`{"five_hour":{"utilization":5},"limits":[{"is_active":true,"percent":-3}]}`), "")
	f.Add([]byte(""), "opus")
	f.Add([]byte("null"), "\xff\xfe")
	f.Add([]byte("{\"five_hour\":\xff}"), "opus")

	now := time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC)
	f.Fuzz(func(t *testing.T, raw []byte, modelName string) {
		u, err := Parse(raw, now, modelName)
		if err != nil {
			return
		}
		// A model segment is never fabricated: the family it names must be a
		// window the payload actually carries, and must be implied by the
		// session model's own name.
		if u.ModelFamily == "" {
			return
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil {
			t.Errorf("ModelFamily %q from an unparseable payload: %q", u.ModelFamily, raw)
			return
		}
		if _, ok := fields[sevenDayPrefix+u.ModelFamily]; !ok {
			t.Errorf("ModelFamily %q has no %s%s key: %q", u.ModelFamily, sevenDayPrefix, u.ModelFamily, raw)
		}
		if !strings.Contains(modelToken(modelName), modelToken(u.ModelFamily)) {
			t.Errorf("ModelFamily %q not implied by model name %q: %q", u.ModelFamily, modelName, raw)
		}
	})
}
