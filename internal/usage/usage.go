// Package usage fetches and parses the subscription-limits payload from the
// OAuth usage endpoint, guarding against the failure mode found in the bash
// reference: an {"error":...} body cached and rendered as "0%".
package usage

import (
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/mitre/claude-statusline/internal/cache"
	"github.com/mitre/claude-statusline/internal/render"
)

// MinFetchInterval is the floor between two fetch attempts, whatever the
// previous attempt returned. The usage endpoint rate-limits faster polling
// (observed 2026-11-02: HTTP 429 with Retry-After 199 s once a stale cache
// had every render refetching), so a failed or rejected fetch must not be
// retried on the next render. Config enforces the same floor on ttl_seconds.
const MinFetchInterval = 200 * time.Second

// MaxRetryAfter bounds how long a server's Retry-After can hold fetching
// off: a longer value, or one sent in error, is cut to an hour.
const MaxRetryAfter = time.Hour

// staleGateAhead is how far ahead of the clock a gate must be to count as
// left by a clock that moved back: no hold this code writes is more than
// MaxRetryAfter ahead, and the extra hour keeps a legitimate hold in place
// through a clock step back of under an hour.
const staleGateAhead = MaxRetryAfter + time.Hour

// RetryAfterError reports that the endpoint rate-limited the fetch. After is
// the server's Retry-After interval, zero when the response carried none.
type RetryAfterError struct {
	After time.Duration
}

func (e *RetryAfterError) Error() string {
	if e.After <= 0 {
		return "usage endpoint rate-limited the fetch"
	}
	return fmt.Sprintf("usage endpoint rate-limited the fetch; retry after %s", e.After)
}

// nextAttemptFile sits beside the usage cache and holds, as Unix seconds,
// the earliest moment the next fetch may be attempted.
const nextAttemptFile = "usage.next-attempt"

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
// never be rendered as zeros. The shape gate is "at least one window or
// limit". A null window, or one whose utilization is null, is absent rather
// than invalid: it sets FiveHourAbsent / SevenDayAbsent and never renders
// as 0%. modelName is the session model's display name: when the payload
// carries a weekly window whose own key matches it, the model segment
// fields are populated; otherwise they stay zero and the segment is
// omitted.
func Parse(raw []byte, now time.Time, modelName string) (render.Usage, error) {
	var p payload
	if err := json.Unmarshal(raw, &p); err != nil {
		return render.Usage{}, err
	}
	fiveHour := p.FiveHour != nil && p.FiveHour.Utilization != nil
	sevenDay := p.SevenDay != nil && p.SevenDay.Utilization != nil
	if !fiveHour && !sevenDay && len(p.Limits) == 0 {
		return render.Usage{}, errors.New("payload carries no usage window or limit — not usage data")
	}

	// A null five_hour or seven_day is the endpoint's "no window in
	// progress": the other readings are real, that meter is absent — never
	// 0%.
	u := render.Usage{
		FiveHourAbsent: !fiveHour,
		SevenDayAbsent: !sevenDay,
		ExtraEnabled:   p.ExtraUsage.IsEnabled,
		CreditsMinor:   p.Spend.Used.AmountMinor,
		CreditsExp:     2,
	}
	if p.Spend.Used.Exponent != nil {
		u.CreditsExp = *p.Spend.Used.Exponent
	}
	if fiveHour {
		u.U5 = int(math.Floor(*p.FiveHour.Utilization))
		u.R5 = resetLabel(p.FiveHour.ResetsAt, now)
	}
	if p.SevenDay != nil {
		if p.SevenDay.Utilization != nil {
			u.U7 = int(math.Floor(*p.SevenDay.Utilization))
		}
		u.R7 = resetLabel(p.SevenDay.ResetsAt, now)
	}
	for _, l := range p.Limits {
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

// Owner binds a cached usage payload to the account and the access token
// it was fetched with.
type Owner struct {
	// Account is the account record's oauthAccount.accountUuid.
	Account string
	// Fingerprint is the first 16 hex digits of the sha256 of the access
	// token; never the token itself.
	Fingerprint string
}

// Resolve returns a usage payload: fresh cache if young enough, else a live
// fetch (cached only when shape-valid), else the last good cache regardless
// of age. staleFor is the served payload's age when that last stale-good
// fallback is engaged — the fetch failed (or returned an error body) and old
// data is being served — and zero on the fresh paths; the render layer keys
// its dim age marker on it. ok=false means no usable data — the limits row
// must collapse.
//
// Fetch attempts are throttled independently of the TTL: once an attempt is
// made — success, network error, rate limit or a rejected body alike — no
// further attempt happens before MinFetchInterval has passed since the
// response (or the server's Retry-After, when longer, up to MaxRetryAfter).
// Renders in between serve the cache. Every gate time is read from the gate
// edge's clock; now serves only the payload (freshness, reset labels, stale
// age).
// The gate is one file shared by every account. Only the render holding the
// fetch lock fetches (see lockFetch); the others serve their cache without
// waiting.
//
// The payload belongs to owner: it is cached per account and token and
// served, fresh or stale, only while the cached fingerprint matches. An
// owner missing either part gets nothing, and nothing is fetched, read or
// written. current reports the owner after any fetch attempt; if it
// differs from owner (a /login during the request), nothing is returned or
// cached: neither the response nor the cached payload.
func Resolve(gateDir, cacheDir string, owner Owner, current func() Owner, ttl time.Duration, now time.Time, fetch func() ([]byte, error)) (raw []byte, staleFor time.Duration, ok bool) {
	return resolve(osGateFS(gateDir), cacheDir, owner, current, ttl, now, fetch)
}

// resolve is Resolve with the gate and the fetch lock reached through g.
func resolve(g gateFS, cacheDir string, owner Owner, current func() Owner, ttl time.Duration, now time.Time, fetch func() ([]byte, error)) (raw []byte, staleFor time.Duration, ok bool) {
	if owner.Account == "" || owner.Fingerprint == "" {
		return nil, 0, false
	}
	p := payloadPath(cacheDir, owner)
	if s, ok := cache.ReadFresh(p, ttl); ok {
		if b, mine := ownedBy(s, owner); mine && !boundaryExpired(p, b, now) {
			return b, 0, true
		}
	}
	if b, sent, err := fetchUnderLock(g, fetch); sent {
		if current == nil || current() != owner {
			// The record or the token changed while the request was out:
			// neither the response nor this owner's cache is shown.
			return nil, 0, false
		}
		if err == nil {
			if _, perr := Parse(b, now, ""); perr == nil {
				_ = cache.Write(p, ownedFile(owner, b))
				return b, 0, true
			}
		}
	}
	if s, ok := cache.ReadStale(p); ok {
		if b, mine := ownedBy(s, owner); mine {
			return b, staleAge(p, now), true
		}
	}
	return nil, 0, false
}

// payloadPath is owner's payload file: "usage-" and the FNV-1a 64-bit hash
// of the account ID and token fingerprint in hex, so neither appears in the
// name. Each token of an account has its own entry: two logins of one
// account never evict each other, and a refreshed token starts a new entry.
// The unkeyed "usage" file of earlier versions is never read.
func payloadPath(cacheDir string, owner Owner) string {
	h := fnv.New64a()
	_, _ = h.Write([]byte(owner.Account + "\x00" + owner.Fingerprint))
	return filepath.Join(cacheDir, fmt.Sprintf("usage-%016x", h.Sum64()))
}

// ownedFile is the cached form of payload: owner's token fingerprint on the
// first line, the payload after it.
func ownedFile(owner Owner, payload []byte) string {
	return owner.Fingerprint + "\n" + string(payload)
}

// ownedBy splits a cached file into its token fingerprint line and the
// payload, and reports whether owner's token fetched it.
func ownedBy(file string, owner Owner) ([]byte, bool) {
	fp, payload, found := strings.Cut(file, "\n")
	if !found || fp != owner.Fingerprint {
		return nil, false
	}
	return []byte(payload), true
}

// readGate returns the moment the throttle gate opens. A missing gate
// opens at the zero time. ok is false when the gate exists but cannot be
// read or holds no time: whether the last hold has passed is then unknown.
func readGate(g gateFS) (at time.Time, ok bool) {
	s, err := g.read()
	if errors.Is(err, fs.ErrNotExist) {
		return time.Time{}, true
	}
	if err != nil {
		return time.Time{}, false
	}
	unix, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil {
		return time.Time{}, false
	}
	return time.Unix(unix, 0), true
}

// lockFile sits beside the gate. Only the render holding an exclusive lock
// on it fetches. It is created once and never removed or renamed: if a
// render removed it while another held the lock, a third could lock a new
// file and fetch alongside the holder.
const lockFile = "usage.lock"

// gateFS is the gate and the fetch lock. Tests replace it to fail or
// interleave individual steps.
type gateFS struct {
	// lock takes the fetch lock without waiting; unlock releases it. Any
	// error, including another render holding it, means no fetch.
	lock func() (unlock func(), err error)
	// read returns the gate's contents, or the error reading it; a missing
	// gate reports an error matching fs.ErrNotExist.
	read func() (contents string, err error)
	// write replaces the gate's contents.
	write func(contents string) error
	// now is the clock every gate time decision reads: whether the gate is
	// open, the gate written before the request and the hold after the
	// response.
	now func() time.Time
}

// osGateFS is the gate and the fetch lock in dir.
func osGateFS(dir string) gateFS {
	gate := filepath.Join(dir, nextAttemptFile)
	return gateFS{
		lock: func() (func(), error) {
			// Created only when a fetch is wanted, after the owner check: a
			// render without an owner touches nothing. A directory that
			// cannot be created takes no lock, so it authorizes no fetch.
			if err := os.MkdirAll(dir, 0o700); err != nil {
				return nil, err
			}
			return lockFetch(dir)
		},
		read: func() (string, error) {
			root, err := os.OpenRoot(dir)
			if err != nil {
				return "", err
			}
			defer func() { _ = root.Close() }()
			b, err := root.ReadFile(nextAttemptFile)
			return string(b), err
		},
		write: func(contents string) error { return cache.Write(gate, contents) },
		now:   time.Now,
	}
}

// lockFetch takes an exclusive flock on dir's lock file without waiting.
// Each call opens its own file description, so two renders never share a
// lock, and the file is opened close-on-exec through an os.Root, so no
// child process (the keychain read) inherits it and the name cannot
// resolve outside dir. The kernel releases the lock when the holder exits
// or is killed; a stopped holder keeps it. unlock closes the file, which
// releases the lock: its descriptor is the only one on that description.
func lockFetch(dir string) (unlock func(), err error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	return lockIn(root)
}

// lockDir is the access to the gate directory that lockIn needs; *os.Root
// provides it, confined to the directory.
type lockDir interface {
	Lstat(name string) (fs.FileInfo, error)
	OpenFile(name string, flag int, perm fs.FileMode) (*os.File, error)
}

// lockIn takes the exclusive lock on d's lock file without waiting.
func lockIn(d lockDir) (unlock func(), err error) {
	// The entry is checked without following it, before anything opens it:
	// only a plain file is locked. Through a link two renders could lock
	// different files (the gate is replaced on every write), and opening a
	// special file can block. A missing entry is created exclusively, so a
	// link planted after the check is never followed. A refused entry is
	// left as it is.
	flag := os.O_RDWR
	st, err := d.Lstat(lockFile)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		flag |= os.O_CREATE | os.O_EXCL
	case err != nil:
		return nil, err
	case !st.Mode().IsRegular():
		return nil, errLockNotPlainFile
	}
	f, err := d.OpenFile(lockFile, flag, 0o600)
	if err != nil {
		return nil, err
	}
	if err := flock(f, syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		return nil, err
	}
	// The entry may have been replaced since it was checked: the lock holds
	// only if the entry is still the plain file that was locked.
	if err := sameLockFile(d, f); err != nil {
		_ = f.Close()
		return nil, err
	}
	return func() { _ = f.Close() }, nil
}

// sameLockFile checks that f is a plain file and that d's lock entry,
// examined without following it, is still f.
func sameLockFile(d lockDir, f *os.File) error {
	opened, err := f.Stat()
	if err != nil {
		return err
	}
	entry, err := d.Lstat(lockFile)
	if err != nil {
		return err
	}
	if !opened.Mode().IsRegular() || !os.SameFile(entry, opened) {
		return errLockNotPlainFile
	}
	return nil
}

// errLockNotPlainFile refuses a lock entry that is a link, a directory or a
// special file.
var errLockNotPlainFile = errors.New("usage.lock is not a plain file")

// flock applies how to f's lock, retrying a call a signal interrupted.
func flock(f *os.File, how int) error {
	for {
		err := syscall.Flock(int(f.Fd()), how)
		if !errors.Is(err, syscall.EINTR) {
			return err
		}
	}
}

// fetchUnderLock runs one attempt while holding the fetch lock: it reads
// the gate, writes it MinFetchInterval ahead, sends, records the attempt
// and releases the lock. The gate is read only under the lock, so a render
// never fetches through an opening another render used while it waited to
// run. sent is false, and nothing is sent, when another render holds the
// lock, the lock cannot be taken, the gate is closed, or the gate cannot be
// written: a render that sent without a written gate could not hold the
// others off once it released the lock.
func fetchUnderLock(g gateFS, fetch func() ([]byte, error)) (b []byte, sent bool, err error) {
	unlock, lerr := g.lock()
	if lerr != nil {
		return nil, false, nil
	}
	defer unlock()
	now := g.now()
	at, ok := readGate(g)
	if !ok {
		// The last hold is unknown: hold the fetch off for one floor and
		// rewrite the gate so the next opening fetches.
		_ = writeGate(g, now.Add(MinFetchInterval))
		return nil, false, nil
	}
	// A gate more than staleGateAhead ahead was left by a clock that has
	// since moved back, and counts as open rather than holding every
	// render off until the clock catches up.
	held := now.Before(at) && at.Sub(now) <= staleGateAhead
	if held || writeGate(g, now.Add(MinFetchInterval)) != nil {
		return nil, false, nil
	}
	b, err = fetch()
	// The hold runs from when the response arrived, not from when the
	// request was sent.
	recordAttempt(g, g.now(), err)
	return b, true, err
}

// writeGate sets the gate to open at the given moment.
func writeGate(g gateFS, at time.Time) error {
	return g.write(strconv.FormatInt(at.Unix(), 10))
}

// recordAttempt writes the earliest moment the next fetch may run, counted
// from now, when the response arrived: the floor, stretched to the server's
// Retry-After when that is longer, up to MaxRetryAfter. A write failure
// leaves the gate written before the request in place.
func recordAttempt(g gateFS, now time.Time, err error) {
	hold := MinFetchInterval
	var ra *RetryAfterError
	if errors.As(err, &ra) {
		hold = max(MinFetchInterval, min(ra.After, MaxRetryAfter))
	}
	_ = writeGate(g, now.Add(hold))
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
