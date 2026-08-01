// Package usage fetches and parses the subscription-limits payload from the
// OAuth usage endpoint, guarding against the failure mode found in the bash
// reference: an {"error":...} body cached and rendered as "0%".
package usage

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mitre/claude-statusline/internal/cache"
	"github.com/mitre/claude-statusline/internal/render"
)

type window struct {
	Utilization *float64 `json:"utilization"`
	ResetsAt    string   `json:"resets_at"`
}

type payload struct {
	FiveHour   *window `json:"five_hour"`
	SevenDay   *window `json:"seven_day"`
	ExtraUsage struct {
		IsEnabled bool `json:"is_enabled"`
	} `json:"extra_usage"`
	Spend struct {
		Used struct {
			AmountMinor int  `json:"amount_minor"`
			Exponent    *int `json:"exponent"`
		} `json:"used"`
	} `json:"spend"`
	Limits []struct {
		IsActive bool        `json:"is_active"`
		Percent  int         `json:"percent"`
		ResetsAt string      `json:"resets_at"`
		Scope    *limitScope `json:"scope"`
	} `json:"limits"`
}

// limitScope is the narrowing a plan limit applies. Unscoped limits carry a
// null scope; scoped ones name their subject, which the renderer displays
// verbatim.
type limitScope struct {
	Model *struct {
		DisplayName string `json:"display_name"`
	} `json:"model"`
}

// scopeLabel names a scoped limit from the payload alone — no model name is
// compiled in, so the next scoped model the vendor introduces flows straight
// through. A scope the payload cannot name still gets a label rather than
// being dropped: a binding limit the owner cannot see is worse than a
// generically named one.
func scopeLabel(s *limitScope) string {
	if s.Model != nil && s.Model.DisplayName != "" {
		return s.Model.DisplayName
	}
	return "scoped"
}

// sevenDayPrefix marks the payload's per-scope weekly windows.
const sevenDayPrefix = "seven_day_"

// modelToken reduces a model display name or a payload key suffix to
// comparable lowercase alphanumerics, so an underscored key ("zephyr_compact")
// lines up with a spaced display name ("Zephyr Compact 5") and punctuation in
// either never blocks a match.
func modelToken(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// modelWindowKey picks the payload's weekly window for this session's model,
// returning the key and the family it names. Candidates are the payload's own
// seven_day_* keys — the vendor's vocabulary, never a compiled-in model list —
// so a newly shipped model gets its window the moment the payload carries one,
// with no change here. Candidates are tried longest-first so a more specific
// key wins over one whose token it contains, and the ordering is explicit:
// map iteration order must never decide what renders. No match returns "",
// and the segment is then omitted rather than shown as a fabricated zero.
func modelWindowKey(fields map[string]json.RawMessage, modelName string) (key, family string) {
	name := modelToken(modelName)
	if name == "" {
		return "", ""
	}
	type candidate struct{ family, token string }
	var cands []candidate
	for k := range fields {
		if !strings.HasPrefix(k, sevenDayPrefix) || len(k) == len(sevenDayPrefix) {
			continue
		}
		fam := strings.TrimPrefix(k, sevenDayPrefix)
		if tok := modelToken(fam); tok != "" {
			cands = append(cands, candidate{family: fam, token: tok})
		}
	}
	sort.Slice(cands, func(i, j int) bool {
		if len(cands[i].token) != len(cands[j].token) {
			return len(cands[i].token) > len(cands[j].token)
		}
		return cands[i].family < cands[j].family
	})
	for _, c := range cands {
		if strings.Contains(name, c.token) {
			return sevenDayPrefix + c.family, c.family
		}
	}
	return "", ""
}

// Parse validates and extracts a usage payload. Payloads without the
// expected shape (e.g. rate-limit error bodies) return an error — they must
// never be rendered as zeros. modelName is the session model's display name:
// when the payload carries a weekly window whose own key matches it, the
// model segment fields are populated; otherwise they stay zero and the
// segment is omitted.
func Parse(raw []byte, now time.Time, modelName string) (render.Usage, error) {
	var p payload
	if err := json.Unmarshal(raw, &p); err != nil {
		return render.Usage{}, err
	}
	if p.FiveHour == nil || p.FiveHour.Utilization == nil {
		return render.Usage{}, errors.New("payload lacks five_hour.utilization — not usage data")
	}

	u := render.Usage{
		U5:           int(math.Floor(*p.FiveHour.Utilization)),
		R5:           resetLabel(p.FiveHour.ResetsAt, now),
		ExtraEnabled: p.ExtraUsage.IsEnabled,
		CreditsMinor: p.Spend.Used.AmountMinor,
		CreditsExp:   2,
	}
	if p.Spend.Used.Exponent != nil {
		u.CreditsExp = *p.Spend.Used.Exponent
	}
	if p.SevenDay != nil {
		if p.SevenDay.Utilization != nil {
			u.U7 = int(math.Floor(*p.SevenDay.Utilization))
		}
		u.R7 = resetLabel(p.SevenDay.ResetsAt, now)
	}
	for _, l := range p.Limits {
		if l.IsActive && l.Percent > u.MaxActive {
			u.MaxActive = l.Percent
		}
		if l.Scope != nil {
			u.Scoped = append(u.Scoped, render.ScopedLimit{
				Name:  scopeLabel(l.Scope),
				Pct:   l.Percent,
				Reset: resetLabel(l.ResetsAt, now),
			})
		}
	}

	if modelName != "" {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err == nil {
			if key, family := modelWindowKey(fields, modelName); key != "" {
				var w window
				if err := json.Unmarshal(fields[key], &w); err == nil && w.Utilization != nil {
					u.ModelFamily = family
					u.ModelPct = int(math.Floor(*w.Utilization))
					u.ModelReset = resetLabel(w.ResetsAt, now)
				}
			}
		}
	}
	return u, nil
}

// resetLabel formats an RFC3339 reset timestamp (the endpoint's encoding).
// Unparseable or empty input renders no label.
func resetLabel(iso string, now time.Time) string {
	if iso == "" {
		return ""
	}
	t, err := time.Parse(time.RFC3339, iso)
	if err != nil {
		return ""
	}
	return labelAt(t, now)
}

// ResetLabelUnix formats an epoch-seconds reset moment (the stdin payload's
// encoding). Zero or negative renders no label. Same formatting core as
// resetLabel — one source of truth for reset labels.
func ResetLabelUnix(unix int64, now time.Time) string {
	if unix <= 0 {
		return ""
	}
	return labelAt(time.Unix(unix, 0), now)
}

// labelAt is the shared reset-label core, in local time: same-day → "1:30p",
// other day → "Mon 10:00a" (mirrors the reference's python formatter).
func labelAt(t, now time.Time) string {
	t = t.In(now.Location())
	h := t.Hour() % 12
	if h == 0 {
		h = 12
	}
	ap := "a"
	if t.Hour() >= 12 {
		ap = "p"
	}
	label := fmt.Sprintf("%d:%02d%s", h, t.Minute(), ap)
	if t.Year() == now.Year() && t.YearDay() == now.YearDay() {
		return label
	}
	return t.Format("Mon ") + label
}

// Resolve returns a usage payload: fresh cache if young enough, else a live
// fetch (cached only when shape-valid), else the last good cache regardless
// of age. staleFor is the served payload's age when that last stale-good
// fallback is engaged — the fetch failed (or returned an error body) and old
// data is being served — and zero on the fresh paths; the render layer keys
// its dim age marker on it. ok=false means no usable data — the limits row
// must collapse.
func Resolve(cacheDir string, ttl time.Duration, now time.Time, fetch func() ([]byte, error)) (raw []byte, staleFor time.Duration, ok bool) {
	p := filepath.Join(cacheDir, "usage")
	if s, ok := cache.ReadFresh(p, ttl); ok && !boundaryExpired(p, []byte(s), now) {
		return []byte(s), 0, true
	}
	if b, err := fetch(); err == nil {
		if _, perr := Parse(b, now, ""); perr == nil {
			_ = cache.Write(p, string(b))
			return b, 0, true
		}
	}
	if s, ok := cache.ReadStale(p); ok {
		return []byte(s), staleAge(p, now), true
	}
	return nil, 0, false
}

// staleAge is the age of a fallback-served cache file: now minus its mtime,
// clamped at zero — an unstat-able file or a future mtime (clock skew)
// reports no age rather than a fabricated one, so no marker renders.
func staleAge(path string, now time.Time) time.Duration {
	st, err := os.Stat(path)
	if err != nil {
		return 0
	}
	if age := now.Sub(st.ModTime()); age > 0 {
		return age
	}
	return 0
}

// boundaryExpired reports whether a TTL-fresh cached payload is provably
// pre-boundary: some window's resets_at has passed AND the file was written
// before that reset — the reset moment is precisely when a young cache is
// guaranteed wrong. The mtime clause is the storm guard: a payload refetched
// after the boundary that still carries a past resets_at (API lag, clock
// skew) has mtime >= resets_at, so it keeps plain TTL cadence instead of
// fetching every render. Missing or unparseable resets_at never expires.
func boundaryExpired(path string, raw []byte, now time.Time) bool {
	var p payload
	if err := json.Unmarshal(raw, &p); err != nil {
		return false
	}
	st, err := os.Stat(path)
	if err != nil {
		return false
	}
	for _, w := range []*window{p.FiveHour, p.SevenDay} {
		if w == nil || w.ResetsAt == "" {
			continue
		}
		reset, err := time.Parse(time.RFC3339, w.ResetsAt)
		if err != nil {
			continue
		}
		if reset.Before(now) && st.ModTime().Before(reset) {
			return true
		}
	}
	return false
}

// TokenFromCredentialJSON extracts the OAuth access token from Claude Code's
// credential JSON — the same claudeAiOauth shape whether it came from the
// macOS keychain item or a .credentials.json file.
func TokenFromCredentialJSON(raw string) (string, error) {
	var cred struct {
		ClaudeAiOauth struct {
			AccessToken string `json:"accessToken"`
		} `json:"claudeAiOauth"`
	}
	if err := json.Unmarshal([]byte(raw), &cred); err != nil {
		return "", err
	}
	if cred.ClaudeAiOauth.AccessToken == "" {
		return "", errors.New("no accessToken in keychain credential")
	}
	return cred.ClaudeAiOauth.AccessToken, nil
}
