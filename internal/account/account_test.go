package account

import (
	"io/fs"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
)

// fakeFile is one entry of the in-memory filesystem the resolver reads.
type fakeFile struct {
	data string
	mode fs.FileMode // permission bits; fs.ModeDir marks a directory
	// soleLink reports a link count of 1 (the Linux placeholder check).
	soleLink bool
}

// fakeFS serves Stat, ReadFile and ReadDir from a map keyed by on-disk path.
// foldCase makes path lookups case-insensitive, as on a default macOS volume,
// while ReadDir still lists the on-disk spelling.
type fakeFS struct {
	files    map[string]fakeFile
	foldCase bool
	statErr  map[string]error
	dirErr   error
	reads    []string
	stats    []string
}

func (f *fakeFS) lookup(p string) (string, fakeFile, bool) {
	if ff, ok := f.files[p]; ok {
		return p, ff, true
	}
	if f.foldCase {
		for k, ff := range f.files {
			if strings.EqualFold(k, p) {
				return k, ff, true
			}
		}
	}
	return "", fakeFile{}, false
}

type fakeInfo struct {
	name string
	ff   fakeFile
}

func (i fakeInfo) Name() string       { return i.name }
func (i fakeInfo) Size() int64        { return int64(len(i.ff.data)) }
func (i fakeInfo) Mode() fs.FileMode  { return i.ff.mode }
func (i fakeInfo) ModTime() time.Time { return time.Time{} }
func (i fakeInfo) IsDir() bool        { return i.ff.mode.IsDir() }

type fakeEntry struct{ info fakeInfo }

func (e fakeEntry) Name() string               { return e.info.name }
func (e fakeEntry) IsDir() bool                { return e.info.IsDir() }
func (e fakeEntry) Type() fs.FileMode          { return e.info.Mode().Type() }
func (e fakeEntry) Info() (fs.FileInfo, error) { return e.info, nil }

func (f *fakeFS) fs() FS {
	return FS{
		ReadFile: func(p string) ([]byte, error) {
			f.reads = append(f.reads, p)
			_, ff, ok := f.lookup(p)
			if !ok || ff.mode.IsDir() {
				return nil, fs.ErrNotExist
			}
			return []byte(ff.data), nil
		},
		Stat: func(p string) (fs.FileInfo, error) {
			f.stats = append(f.stats, p)
			if err := f.statErr[p]; err != nil {
				return nil, err
			}
			_, ff, ok := f.lookup(p)
			if !ok {
				return nil, fs.ErrNotExist
			}
			return fakeInfo{name: filepath.Base(p), ff: ff}, nil
		},
		ReadDir: func(dir string) ([]fs.DirEntry, error) {
			if f.dirErr != nil {
				return nil, f.dirErr
			}
			var out []fs.DirEntry
			for k, ff := range f.files {
				if filepath.Dir(k) == dir {
					out = append(out, fakeEntry{fakeInfo{name: filepath.Base(k), ff: ff}})
				}
			}
			return out, nil
		},
	}
}

func env(kv map[string]string) func(string) string {
	return func(k string) string { return kv[k] }
}

func record(email string) string {
	return `{"oauthAccount":{"accountUuid":"00000000-0000-4000-8000-000000000000","emailAddress":"` + email + `"}}`
}

func TestResolveReadsTheConfigDirRecord(t *testing.T) {
	f := &fakeFS{files: map[string]fakeFile{
		"/Users/dev/.claude.json":        {data: record("dev@example.com"), mode: 0o600},
		"/Users/dev/second/.claude.json": {data: record("second@example.com"), mode: 0o600},
	}}
	got := Resolve(env(map[string]string{"HOME": "/Users/dev", "CLAUDE_CONFIG_DIR": "/Users/dev/second"}), f.fs())
	if got.Email != "second@example.com" {
		t.Errorf("Email = %q, want second@example.com from $CLAUDE_CONFIG_DIR/.claude.json", got.Email)
	}
}

func TestResolveDefaultRecordIsInHome(t *testing.T) {
	f := &fakeFS{files: map[string]fakeFile{
		"/Users/dev/.claude.json": {data: record("dev@example.com"), mode: 0o600},
	}}
	got := Resolve(env(map[string]string{"HOME": "/Users/dev"}), f.fs())
	if got.Email != "dev@example.com" || got.AccountUUID != "00000000-0000-4000-8000-000000000000" {
		t.Errorf("Resolve = %+v, want dev@example.com and its account UUID", got)
	}
}

func TestResolveCustomOAuthRecordName(t *testing.T) {
	f := &fakeFS{files: map[string]fakeFile{
		"/Users/dev/.claude.json":              {data: record("dev@example.com"), mode: 0o600},
		"/Users/dev/.claude-custom-oauth.json": {data: record("custom@example.com"), mode: 0o600},
	}}
	got := Resolve(env(map[string]string{"HOME": "/Users/dev", "CLAUDE_CODE_CUSTOM_OAUTH_URL": "https://login.example.com"}), f.fs())
	if got.Email != "custom@example.com" {
		t.Errorf("Email = %q, want the -custom-oauth record's custom@example.com", got.Email)
	}
}

func TestResolveWithoutHomeOrConfigDirReadsNothing(t *testing.T) {
	f := &fakeFS{files: map[string]fakeFile{
		".claude.json":         {data: record("relative@example.com"), mode: 0o600},
		".claude/.config.json": {data: record("relative@example.com"), mode: 0o600},
	}}
	if got := Resolve(env(nil), f.fs()); got != (Identity{}) {
		t.Errorf("Resolve = %+v, want no identity", got)
	}
	if len(f.reads)+len(f.stats) != 0 {
		t.Errorf("no file may be touched without HOME or CLAUDE_CONFIG_DIR; reads %v stats %v", f.reads, f.stats)
	}
}

func TestResolveRelativeBaseReadsNothing(t *testing.T) {
	for name, e := range map[string]map[string]string{
		"relative CLAUDE_CONFIG_DIR": {"HOME": "/Users/dev", "CLAUDE_CONFIG_DIR": "cfg"},
		"relative HOME":              {"HOME": "dev"},
	} {
		f := &fakeFS{files: map[string]fakeFile{
			"cfg/.claude.json": {data: record("relative@example.com"), mode: 0o600},
			"dev/.claude.json": {data: record("relative@example.com"), mode: 0o600},
		}}
		if got := Resolve(env(e), f.fs()); got != (Identity{}) {
			t.Errorf("%s: Resolve = %+v, want no identity", name, got)
		}
		if len(f.reads)+len(f.stats) != 0 {
			t.Errorf("%s: a relative path resolves against the workspace and must not be read; reads %v stats %v", name, f.reads, f.stats)
		}
	}
}

func TestResolveAccountUUIDComesOnlyFromOauthAccount(t *testing.T) {
	f := &fakeFS{files: map[string]fakeFile{
		"/Users/dev/.claude.json": {mode: 0o600, data: `{
  "accountUuid": "11111111-1111-4111-8111-111111111111",
  "projects": {"/x": {"accountUuid": "22222222-2222-4222-8222-222222222222"}},
  "oauthAccount": {"accountUuid": "33333333-3333-4333-8333-333333333333", "emailAddress": "dev@example.com"}
}`},
	}}
	got := Resolve(env(map[string]string{"HOME": "/Users/dev"}), f.fs())
	if got.AccountUUID != "33333333-3333-4333-8333-333333333333" {
		t.Errorf("AccountUUID = %q, want oauthAccount.accountUuid", got.AccountUUID)
	}
	if len(f.reads) != 1 {
		t.Errorf("identity must come from one read of the record, got reads %v", f.reads)
	}
}

func TestResolveUUIDDoesNotDependOnEmail(t *testing.T) {
	// The account UUID keys per-account state; a record without an email
	// still identifies its account.
	for name, data := range map[string]string{
		"no email":    `{"oauthAccount":{"accountUuid":"33333333-3333-4333-8333-333333333333"}}`,
		"empty email": `{"oauthAccount":{"accountUuid":"33333333-3333-4333-8333-333333333333","emailAddress":""}}`,
	} {
		f := &fakeFS{files: map[string]fakeFile{"/Users/dev/.claude.json": {data: data, mode: 0o600}}}
		got := Resolve(env(map[string]string{"HOME": "/Users/dev"}), f.fs())
		if got != (Identity{AccountUUID: "33333333-3333-4333-8333-333333333333"}) {
			t.Errorf("%s: Resolve = %+v, want the UUID with no email", name, got)
		}
		if len(f.reads) != 1 {
			t.Errorf("%s: want one read, got %v", name, f.reads)
		}
	}
}

func TestResolveUnusableRecordYieldsNoIdentity(t *testing.T) {
	for name, data := range map[string]string{
		"malformed":          "not json",
		"no oauthAccount":    `{"numStartups": 1, "accountUuid": "11111111-1111-4111-8111-111111111111"}`,
		"empty oauthAccount": `{"oauthAccount": {}}`,
	} {
		f := &fakeFS{files: map[string]fakeFile{"/Users/dev/.claude.json": {data: data, mode: 0o600}}}
		if got := Resolve(env(map[string]string{"HOME": "/Users/dev"}), f.fs()); got != (Identity{}) {
			t.Errorf("%s: Resolve = %+v, want no identity", name, got)
		}
	}
	f := &fakeFS{files: map[string]fakeFile{}}
	if got := Resolve(env(map[string]string{"HOME": "/Users/dev"}), f.fs()); got != (Identity{}) {
		t.Errorf("absent record: Resolve = %+v, want no identity", got)
	}
}

func TestResolveReflectsTheRecordImmediately(t *testing.T) {
	// Identity is never cached: a login change shows on the very next
	// render (a stale identity label once confused a live session).
	f := &fakeFS{files: map[string]fakeFile{"/Users/dev/.claude.json": {data: record("dev@example.com"), mode: 0o600}}}
	e := env(map[string]string{"HOME": "/Users/dev"})
	if got := Resolve(e, f.fs()); got.Email != "dev@example.com" {
		t.Fatalf("first Email = %q", got.Email)
	}
	f.files["/Users/dev/.claude.json"] = fakeFile{data: record("other@example.com"), mode: 0o600}
	if got := Resolve(e, f.fs()); got.Email != "other@example.com" {
		t.Errorf("Email after the record changed = %q, want other@example.com", got.Email)
	}
}

// The legacy selector: Claude Code prefers a usable legacy .config.json in
// its config home over the current record. Each case names the file the
// identity must be read from.
func TestResolveLegacyRecordSelection(t *testing.T) {
	const (
		home    = "/Users/dev"
		current = "/Users/dev/.claude.json"
		legacy  = "/Users/dev/.claude/.config.json"
	)
	full := func(email string) fakeFile { return fakeFile{data: record(email), mode: 0o600} }
	emptyWritable := fakeFile{mode: 0o600}
	placeholder := fakeFile{mode: 0o444, soleLink: true}
	// A real directory reports a non-zero size (its entry table).
	dirWithEntries := fakeFile{mode: fs.ModeDir | 0o700, data: "entries"}
	linuxOnly := current
	if runtime.GOOS == "linux" {
		linuxOnly = legacy
	}
	denied := &fs.PathError{Op: "stat", Path: current, Err: syscall.EACCES}

	for _, tc := range []struct {
		name     string
		files    map[string]fakeFile
		foldCase bool
		statErr  map[string]error
		dirErr   error
		want     string
	}{
		{name: "no legacy file", files: map[string]fakeFile{current: full("c@example.com")}, want: current},
		{name: "non-empty legacy beats an existing record", files: map[string]fakeFile{current: full("c@example.com"), legacy: full("l@example.com")}, want: legacy},
		{name: "non-empty legacy, record missing", files: map[string]fakeFile{legacy: full("l@example.com")}, want: legacy},
		{name: "non-empty legacy, record empty", files: map[string]fakeFile{current: {mode: 0o600}, legacy: full("l@example.com")}, want: legacy},
		{name: "non-empty legacy, record is a directory", files: map[string]fakeFile{current: dirWithEntries, legacy: full("l@example.com")}, want: legacy},
		// An unusable record decides for the legacy file before the
		// exact-name listing is consulted.
		{name: "case-folded legacy, record empty", foldCase: true, files: map[string]fakeFile{current: {mode: 0o600}, "/Users/dev/.claude/.Config.json": full("l@example.com")}, want: legacy},
		{name: "case-folded legacy, record is a directory", foldCase: true, files: map[string]fakeFile{current: dirWithEntries, "/Users/dev/.claude/.Config.json": full("l@example.com")}, want: legacy},
		{name: "empty writable legacy, record missing", files: map[string]fakeFile{legacy: emptyWritable}, want: legacy},
		{name: "empty writable legacy, record present", files: map[string]fakeFile{current: full("c@example.com"), legacy: emptyWritable}, want: current},
		{name: "empty writable legacy, record is the Linux placeholder", files: map[string]fakeFile{current: placeholder, legacy: emptyWritable}, want: linuxOnly},
		{name: "empty read-only legacy", files: map[string]fakeFile{legacy: {mode: 0o444}}, want: current},
		{name: "legacy is a directory", files: map[string]fakeFile{current: full("c@example.com"), legacy: {mode: fs.ModeDir | 0o700}}, want: current},
		{name: "legacy matched only by case folding, record missing", foldCase: true, files: map[string]fakeFile{"/Users/dev/.claude/.Config.json": full("l@example.com")}, want: legacy},
		{name: "legacy matched only by case folding", foldCase: true, files: map[string]fakeFile{current: full("c@example.com"), "/Users/dev/.claude/.Config.json": full("l@example.com")}, want: current},
		{name: "record stat denied, non-empty legacy", files: map[string]fakeFile{current: full("c@example.com"), legacy: full("l@example.com")}, statErr: map[string]error{current: denied}, want: legacy},
		{name: "record stat denied, empty writable legacy", files: map[string]fakeFile{current: full("c@example.com"), legacy: emptyWritable}, statErr: map[string]error{current: denied}, want: legacy},
		{name: "directory listing denied", files: map[string]fakeFile{current: full("c@example.com"), legacy: full("l@example.com")}, dirErr: syscall.EACCES, want: legacy},
		{name: "directory listing gone", files: map[string]fakeFile{current: full("c@example.com"), legacy: full("l@example.com")}, dirErr: fs.ErrNotExist, want: current},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeFS{files: tc.files, foldCase: tc.foldCase, statErr: tc.statErr, dirErr: tc.dirErr}
			Resolve(env(map[string]string{"HOME": home}), f.fs())
			if len(f.reads) != 1 || f.reads[0] != tc.want {
				t.Errorf("read %v, want exactly %s", f.reads, tc.want)
			}
		})
	}
}

func TestResolveLegacyRecordFollowsConfigDir(t *testing.T) {
	f := &fakeFS{files: map[string]fakeFile{
		"/cfg/.claude.json":               {data: record("c@example.com"), mode: 0o600},
		"/cfg/.config.json":               {data: record("l@example.com"), mode: 0o600},
		"/Users/dev/.claude/.config.json": {data: record("home@example.com"), mode: 0o600},
	}}
	got := Resolve(env(map[string]string{"HOME": "/Users/dev", "CLAUDE_CONFIG_DIR": "/cfg"}), f.fs())
	if got.Email != "l@example.com" {
		t.Errorf("Email = %q, want l@example.com from $CLAUDE_CONFIG_DIR/.config.json", got.Email)
	}
}

func TestResolveCurrentRecordPathIsNotNormalized(t *testing.T) {
	// Only the config home is NFC-normalized; the current record is read
	// from CLAUDE_CONFIG_DIR exactly as given.
	nfd := "/cfg/cafe\u0301"
	f := &fakeFS{files: map[string]fakeFile{
		nfd + "/.claude.json":         {data: record("c@example.com"), mode: 0o600},
		"/cfg/caf\u00e9/.claude.json": {data: record("composed@example.com"), mode: 0o600},
	}}
	got := Resolve(env(map[string]string{"HOME": "/Users/dev", "CLAUDE_CONFIG_DIR": nfd}), f.fs())
	if got.Email != "c@example.com" {
		t.Errorf("Email = %q, want c@example.com from the path as given; reads %v", got.Email, f.reads)
	}
}

func TestResolveLegacyPathIsNFCNormalized(t *testing.T) {
	// Claude Code normalizes its config home to NFC for the legacy path but
	// not for the current record: a decomposed é in CLAUDE_CONFIG_DIR finds
	// the legacy file under the composed spelling.
	nfd := "/cfg/café"
	nfc := "/cfg/café"
	f := &fakeFS{files: map[string]fakeFile{
		nfd + "/.claude.json": {data: record("c@example.com"), mode: 0o600},
		nfc + "/.config.json": {data: record("l@example.com"), mode: 0o600},
	}}
	got := Resolve(env(map[string]string{"HOME": "/Users/dev", "CLAUDE_CONFIG_DIR": nfd}), f.fs())
	if got.Email != "l@example.com" {
		t.Errorf("Email = %q, want l@example.com from the NFC legacy path; reads %v", got.Email, f.reads)
	}
}
