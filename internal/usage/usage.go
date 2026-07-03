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
		IsActive bool `json:"is_active"`
		Percent  int  `json:"percent"`
	} `json:"limits"`
}

// FamilyFromModelName maps a session model display name to its payload
// window family. Unknown families (fable, mythos, "?") return "" — the
// model segment is then omitted entirely.
func FamilyFromModelName(name string) string {
	lower := strings.ToLower(name)
	for _, f := range []string{"opus", "sonnet", "haiku"} {
		if strings.Contains(lower, f) {
			return f
		}
	}
	return ""
}

// Parse validates and extracts a usage payload. Payloads without the
// expected shape (e.g. rate-limit error bodies) return an error — they must
// never be rendered as zeros. When family is non-empty and the payload
// carries a seven_day_<family> window with a utilization, the model segment
// fields are populated; otherwise they stay zero and the segment is omitted.
func Parse(raw []byte, now time.Time, family string) (render.Usage, error) {
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
	}

	if family != "" {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err == nil {
			if wraw, ok := fields["seven_day_"+family]; ok {
				var w window
				if err := json.Unmarshal(wraw, &w); err == nil && w.Utilization != nil {
					u.ModelFamily = family
					u.ModelPct = int(math.Floor(*w.Utilization))
					u.ModelReset = resetLabel(w.ResetsAt, now)
				}
			}
		}
	}
	return u, nil
}

// resetLabel formats a reset timestamp in local time: same-day → "1:30p",
// other day → "Mon 10:00a" (mirrors the reference's python formatter).
func resetLabel(iso string, now time.Time) string {
	if iso == "" {
		return ""
	}
	t, err := time.Parse(time.RFC3339, iso)
	if err != nil {
		return ""
	}
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
// of age. ok=false means no usable data — the limits row must collapse.
func Resolve(cacheDir string, ttl time.Duration, now time.Time, fetch func() ([]byte, error)) ([]byte, bool) {
	p := filepath.Join(cacheDir, "usage")
	if s, ok := cache.ReadFresh(p, ttl); ok && !boundaryExpired(p, []byte(s), now) {
		return []byte(s), true
	}
	if b, err := fetch(); err == nil {
		if _, perr := Parse(b, now, ""); perr == nil {
			_ = cache.Write(p, string(b))
			return b, true
		}
	}
	if s, ok := cache.ReadStale(p); ok {
		return []byte(s), true
	}
	return nil, false
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

// TokenFromKeychain extracts the OAuth access token from the keychain
// credential JSON.
func TokenFromKeychain(raw string) (string, error) {
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
