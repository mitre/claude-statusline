package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fixtureCredJSON is the documented credential shape — identical in the
// keychain item and the plaintext store.
func fixtureCredJSON(token string) string {
	return `{"claudeAiOauth":{"accessToken":"` + token + `","refreshToken":"fixture-refresh","expiresAt":1899999999999,"scopes":["user:inference","user:profile"]}}`
}

// writeCredFile places a plaintext credential store exactly where Claude
// Code would.
func writeCredFile(t *testing.T, dir, token string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, ".credentials.json")
	if err := os.WriteFile(p, []byte(fixtureCredJSON(token)), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func envMap(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

// lookupMap is os.LookupEnv over a fixed environment: a key present with
// "" is set-but-empty, which Claude Code treats differently from unset.
func lookupMap(m map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) {
		v, ok := m[k]
		return v, ok
	}
}

// keychainCall is one keychain lookup the token source asked for.
type keychainCall struct{ service, account string }

// fakeKeychain answers lookups with reply and records each one.
type fakeKeychain struct {
	reply func(service, account string) (string, error)
	calls []keychainCall
}

func (k *fakeKeychain) read(service, account string) (string, error) {
	k.calls = append(k.calls, keychainCall{service, account})
	return k.reply(service, account)
}

func noKeychainItem(string, string) (string, error) { return "", errors.New("exit status 44") }

func TestTokenKeychainWinsOverThePlaintextStore(t *testing.T) {
	// Claude Code reads the keychain first; a plaintext file left by an
	// earlier login must not decide the token.
	home := t.TempDir()
	writeCredFile(t, filepath.Join(home, ".claude"), "fixture-access-stale-file")
	kc := &fakeKeychain{reply: func(string, string) (string, error) {
		return fixtureCredJSON("fixture-access-from-keychain"), nil
	}}
	src := tokenSource{
		lookupEnv: lookupMap(map[string]string{"HOME": home, "USER": "dev"}),
		readFile:  os.ReadFile,
		keychain:  kc.read,
	}
	tok, err := src.token()
	if err != nil || tok != "fixture-access-from-keychain" {
		t.Errorf("token() = %q, %v; want the keychain token", tok, err)
	}
}

func TestTokenPlaintextStoreWhenTheKeychainHasNoItem(t *testing.T) {
	for name, reply := range map[string]func(string, string) (string, error){
		"no such item": noKeychainItem,
		"empty item":   func(string, string) (string, error) { return "  \n", nil },
		"unparsable":   func(string, string) (string, error) { return "not json", nil },
		"null":         func(string, string) (string, error) { return "null", nil },
	} {
		home := t.TempDir()
		writeCredFile(t, filepath.Join(home, ".claude"), "fixture-access-from-file")
		src := tokenSource{
			lookupEnv: lookupMap(map[string]string{"HOME": home, "USER": "dev"}),
			readFile:  os.ReadFile,
			keychain:  (&fakeKeychain{reply: reply}).read,
		}
		tok, err := src.token()
		if err != nil || tok != "fixture-access-from-file" {
			t.Errorf("%s: token() = %q, %v; want the plaintext token", name, tok, err)
		}
	}
}

func TestTokenParsedKeychainItemIsFinal(t *testing.T) {
	// A keychain item that parses is the store Claude Code uses, even with
	// no OAuth token in it: the plaintext file is not consulted.
	home := t.TempDir()
	writeCredFile(t, filepath.Join(home, ".claude"), "fixture-access-stale-file")
	src := tokenSource{
		lookupEnv: lookupMap(map[string]string{"HOME": home, "USER": "dev"}),
		readFile:  os.ReadFile,
		keychain:  (&fakeKeychain{reply: func(string, string) (string, error) { return `{"mcpOAuth":{}}`, nil }}).read,
	}
	if tok, err := src.token(); err == nil || tok != "" {
		t.Errorf("token() = %q, %v; want no token from a parsed item without one", tok, err)
	}
}

func TestTokenAnyParsedKeychainValueIsFinal(t *testing.T) {
	// Claude Code classifies the item with JSON.parse: every non-null value
	// is a present item, including primitives and numbers JavaScript reads
	// as Infinity. None of them falls back to the plaintext store.
	for _, item := range []string{
		`{"claudeAiOauth":{"accessToken":"fixture-access-from-keychain"},"unused":1e400}`,
		`false`, `0`, `"text"`, `[]`, `1e400`,
	} {
		home := t.TempDir()
		writeCredFile(t, filepath.Join(home, ".claude"), "fixture-access-stale-file")
		src := tokenSource{
			lookupEnv: lookupMap(map[string]string{"HOME": home, "USER": "dev"}),
			readFile:  os.ReadFile,
			keychain:  (&fakeKeychain{reply: func(string, string) (string, error) { return item, nil }}).read,
		}
		if tok, _ := src.token(); tok == "fixture-access-stale-file" {
			t.Errorf("keychain item %s: the plaintext store must not be read once an item parses", item)
		}
	}
}

func TestTokenKeychainItemIsTrimmedLikeJavaScript(t *testing.T) {
	// Claude Code trims the keychain output with String.prototype.trim,
	// which removes a byte-order mark but not U+0085.
	for _, tc := range []struct {
		name, item string
		final      bool
	}{
		{"leading byte-order mark", "\ufeff" + fixtureCredJSON("fixture-access-from-keychain"), true},
		{"surrounding U+0085", "\u0085" + fixtureCredJSON("fixture-access-from-keychain") + "\u0085", false},
	} {
		home := t.TempDir()
		writeCredFile(t, filepath.Join(home, ".claude"), "fixture-access-from-file")
		src := tokenSource{
			lookupEnv: lookupMap(map[string]string{"HOME": home, "USER": "dev"}),
			readFile:  os.ReadFile,
			keychain:  (&fakeKeychain{reply: func(string, string) (string, error) { return tc.item, nil }}).read,
		}
		want := "fixture-access-from-file"
		if tc.final {
			want = "fixture-access-from-keychain"
		}
		if tok, err := src.token(); tok != want {
			t.Errorf("%s: token() = %q, %v; want %q", tc.name, tok, err, want)
		}
	}
}

func TestTokenPlaintextStoreIsNotTrimmed(t *testing.T) {
	// Claude Code parses the plaintext file as read: JSON.parse rejects a
	// leading byte-order mark, so such a file holds no token.
	home := t.TempDir()
	dir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	body := "\ufeff" + fixtureCredJSON("fixture-access-from-file")
	if err := os.WriteFile(filepath.Join(dir, ".credentials.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	src := tokenSource{lookupEnv: lookupMap(map[string]string{"HOME": home, "USER": "dev"}), readFile: os.ReadFile, keychain: noKeychainItem}
	if tok, err := src.token(); err == nil || tok != "" {
		t.Errorf("token() = %q, %v; want no token from a file JSON.parse rejects", tok, err)
	}
}

func TestTokenKeychainServiceName(t *testing.T) {
	// Claude Code's service: "Claude Code" + OAuth suffix + "-credentials"
	// + "-" + the first 8 hex of sha256(config dir) when a config dir is in
	// effect. Hashes are of the literal paths.
	for _, tc := range []struct {
		name string
		env  map[string]string
		want string
	}{
		{"default", map[string]string{}, "Claude Code-credentials"},
		{"config dir", map[string]string{"CLAUDE_CONFIG_DIR": "/cfg"}, "Claude Code-credentials-2e96833a"},
		{"empty config dir", map[string]string{"CLAUDE_CONFIG_DIR": ""}, "Claude Code-credentials"},
		{"secure-storage dir", map[string]string{"CLAUDE_CONFIG_DIR": "/cfg", "CLAUDE_SECURESTORAGE_CONFIG_DIR": "/secure"}, "Claude Code-credentials-5be4b1b1"},
		{"empty secure-storage dir beats config dir", map[string]string{"CLAUDE_CONFIG_DIR": "/cfg", "CLAUDE_SECURESTORAGE_CONFIG_DIR": ""}, "Claude Code-credentials"},
		{"custom OAuth", map[string]string{"CLAUDE_CODE_CUSTOM_OAUTH_URL": "https://login.example.com"}, "Claude Code-custom-oauth-credentials"},
		{"custom OAuth with config dir", map[string]string{"CLAUDE_CODE_CUSTOM_OAUTH_URL": "https://login.example.com", "CLAUDE_CONFIG_DIR": "/cfg"}, "Claude Code-custom-oauth-credentials-2e96833a"},
		{"NFC before hashing", map[string]string{"CLAUDE_CONFIG_DIR": "/cfg/café"}, "Claude Code-credentials-efb347aa"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := map[string]string{"HOME": t.TempDir(), "USER": "dev"}
			for k, v := range tc.env {
				env[k] = v
			}
			kc := &fakeKeychain{reply: noKeychainItem}
			src := tokenSource{lookupEnv: lookupMap(env), readFile: os.ReadFile, keychain: kc.read}
			_, _ = src.token()
			if len(kc.calls) != 1 || kc.calls[0].service != tc.want {
				t.Errorf("keychain calls %v, want one for service %q", kc.calls, tc.want)
			}
		})
	}
}

func TestTokenKeychainAccountName(t *testing.T) {
	for _, tc := range []struct {
		name     string
		user     *string
		username func() (string, error)
		want     string
	}{
		{"USER", strPtr("dev.user-1_x"), nil, "dev.user-1_x"},
		{"USER unset: login name", nil, func() (string, error) { return "login", nil }, "login"},
		{"USER empty: login name", strPtr(""), func() (string, error) { return "login", nil }, "login"},
		{"no login name", nil, func() (string, error) { return "", errors.New("unknown user") }, "claude-code-user"},
		{"name outside the allowed set", strPtr("dev user"), nil, "claude-code-user"},
		{"shell metacharacters", strPtr("dev;rm"), nil, "claude-code-user"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := map[string]string{"HOME": t.TempDir()}
			if tc.user != nil {
				env["USER"] = *tc.user
			}
			kc := &fakeKeychain{reply: noKeychainItem}
			src := tokenSource{lookupEnv: lookupMap(env), readFile: os.ReadFile, keychain: kc.read, username: tc.username}
			_, _ = src.token()
			if len(kc.calls) != 1 || kc.calls[0].account != tc.want {
				t.Errorf("keychain calls %v, want one for account %q", kc.calls, tc.want)
			}
		})
	}
}

func strPtr(s string) *string { return &s }

func TestTokenPlaintextStoreLocation(t *testing.T) {
	// The plaintext store is <storage dir>/.credentials.json: the
	// secure-storage dir when set (empty means ~/.claude), else Claude
	// Code's config home (CLAUDE_CONFIG_DIR, else ~/.claude).
	home := t.TempDir()
	cfg := t.TempDir()
	secure := t.TempDir()
	writeCredFile(t, filepath.Join(home, ".claude"), "fixture-access-home")
	writeCredFile(t, cfg, "fixture-access-config-dir")
	writeCredFile(t, secure, "fixture-access-secure")
	for _, tc := range []struct {
		name string
		env  map[string]string
		want string
	}{
		{"default", map[string]string{}, "fixture-access-home"},
		{"config dir", map[string]string{"CLAUDE_CONFIG_DIR": cfg}, "fixture-access-config-dir"},
		{"secure-storage dir", map[string]string{"CLAUDE_CONFIG_DIR": cfg, "CLAUDE_SECURESTORAGE_CONFIG_DIR": secure}, "fixture-access-secure"},
		{"empty secure-storage dir means ~/.claude", map[string]string{"CLAUDE_CONFIG_DIR": cfg, "CLAUDE_SECURESTORAGE_CONFIG_DIR": ""}, "fixture-access-home"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := map[string]string{"HOME": home, "USER": "dev"}
			for k, v := range tc.env {
				env[k] = v
			}
			src := tokenSource{lookupEnv: lookupMap(env), readFile: os.ReadFile, keychain: noKeychainItem}
			tok, err := src.token()
			if err != nil || tok != tc.want {
				t.Errorf("token() = %q, %v; want %q", tok, err, tc.want)
			}
		})
	}
}

func TestTokenPlaintextStoreIsNFCNormalized(t *testing.T) {
	root := t.TempDir()
	nfc := filepath.Join(root, "café")
	writeCredFile(t, nfc, "fixture-access-nfc")
	var read []string
	src := tokenSource{
		lookupEnv: lookupMap(map[string]string{"HOME": root, "USER": "dev", "CLAUDE_CONFIG_DIR": filepath.Join(root, "café")}),
		readFile: func(p string) ([]byte, error) {
			read = append(read, p)
			return os.ReadFile(p)
		},
		keychain: noKeychainItem,
	}
	_, _ = src.token()
	if want := filepath.Join(nfc, ".credentials.json"); len(read) != 1 || read[0] != want {
		t.Errorf("read %q, want exactly %q", read, want)
	}
}

func TestTokenNeverReadsARelativeStore(t *testing.T) {
	for name, env := range map[string]map[string]string{
		"no HOME":                     {"USER": "dev"},
		"relative config dir":         {"HOME": "/Users/dev", "USER": "dev", "CLAUDE_CONFIG_DIR": "cfg"},
		"empty config dir":            {"HOME": "/Users/dev", "USER": "dev", "CLAUDE_CONFIG_DIR": ""},
		"relative secure-storage dir": {"HOME": "/Users/dev", "USER": "dev", "CLAUDE_SECURESTORAGE_CONFIG_DIR": "secure"},
	} {
		var read []string
		src := tokenSource{
			lookupEnv: lookupMap(env),
			readFile: func(p string) ([]byte, error) {
				read = append(read, p)
				return nil, os.ErrNotExist
			},
			keychain: noKeychainItem,
		}
		if _, err := src.token(); err == nil {
			t.Errorf("%s: token() resolved without a store", name)
		}
		if len(read) != 0 {
			t.Errorf("%s: a relative store path resolves against the workspace and must not be read; read %v", name, read)
		}
	}
}

func TestTokenNoSourceErrors(t *testing.T) {
	// Neither store resolves: error — the badge degrades to "?" and the
	// account row collapses; nothing is fabricated, nothing logged.
	src := tokenSource{
		lookupEnv: lookupMap(map[string]string{"HOME": t.TempDir(), "USER": "dev"}),
		readFile:  os.ReadFile,
		keychain:  noKeychainItem,
	}
	tok, err := src.token()
	if err == nil || tok != "" {
		t.Errorf("token() = %q, %v; want an error with no source", tok, err)
	}
	if err := src.ok(); err == nil {
		t.Error("ok() must error when no store resolves")
	}
	if err != nil && strings.Contains(err.Error(), "fixture-") {
		t.Errorf("error text leaks token material: %q", err)
	}
}

func TestTokenWithoutAKeychainUsesThePlaintextStore(t *testing.T) {
	// A nil keychain seam (no OS keychain mechanism) is a clean miss.
	home := t.TempDir()
	writeCredFile(t, filepath.Join(home, ".claude"), "fixture-access-from-file")
	src := tokenSource{lookupEnv: lookupMap(map[string]string{"HOME": home}), readFile: os.ReadFile}
	tok, err := src.token()
	if err != nil || tok != "fixture-access-from-file" {
		t.Errorf("token() = %q, %v; want the plaintext token", tok, err)
	}
}

func TestLocationSeparatesCredentialStores(t *testing.T) {
	// The login-status cache is keyed by location: two environments get the
	// same key exactly when token would read the same stores.
	home := "/Users/dev"
	base := map[string]string{"HOME": home, "USER": "dev"}
	with := func(extra map[string]string) map[string]string {
		m := map[string]string{}
		for k, v := range base {
			m[k] = v
		}
		for k, v := range extra {
			m[k] = v
		}
		return m
	}
	loc := func(env map[string]string) string {
		return tokenSource{lookupEnv: lookupMap(env), readFile: os.ReadFile, keychain: noKeychainItem}.location()
	}
	differ := []struct {
		name string
		a, b map[string]string
	}{
		{"config dir", base, with(map[string]string{"CLAUDE_CONFIG_DIR": "/Users/dev/.claude-second"})},
		{"two config dirs", with(map[string]string{"CLAUDE_CONFIG_DIR": "/cfg/a"}), with(map[string]string{"CLAUDE_CONFIG_DIR": "/cfg/b"})},
		{"secure-storage dir", base, with(map[string]string{"CLAUDE_SECURESTORAGE_CONFIG_DIR": "/secure"})},
		{"custom OAuth", base, with(map[string]string{"CLAUDE_CODE_CUSTOM_OAUTH_URL": "https://login.example.com"})},
		{"keychain account", base, with(map[string]string{"USER": "other"})},
		{"home", base, with(map[string]string{"HOME": "/Users/other"})},
	}
	for _, tc := range differ {
		if loc(tc.a) == loc(tc.b) {
			t.Errorf("%s: different stores share location %q", tc.name, loc(tc.a))
		}
	}
	same := []struct {
		name string
		a, b map[string]string
	}{
		{"identical", base, with(nil)},
		{"unrelated variable", base, with(map[string]string{"ANTHROPIC_MODEL": "opus"})},
		{"secure-storage dir decides over config dir", with(map[string]string{"CLAUDE_SECURESTORAGE_CONFIG_DIR": "/secure", "CLAUDE_CONFIG_DIR": "/cfg/a"}), with(map[string]string{"CLAUDE_SECURESTORAGE_CONFIG_DIR": "/secure", "CLAUDE_CONFIG_DIR": "/cfg/b"})},
	}
	for _, tc := range same {
		if loc(tc.a) != loc(tc.b) {
			t.Errorf("%s: the same stores got different locations %q and %q", tc.name, loc(tc.a), loc(tc.b))
		}
	}
}

func TestLocationReadsNoCredential(t *testing.T) {
	// The cache key must never come from a token: location names the
	// stores without opening them.
	kc := &fakeKeychain{reply: func(string, string) (string, error) { return fixtureCredJSON("fixture-access-secret"), nil }}
	reads := 0
	src := tokenSource{
		lookupEnv: lookupMap(map[string]string{"HOME": "/Users/dev", "USER": "dev"}),
		readFile:  func(string) ([]byte, error) { reads++; return []byte(fixtureCredJSON("fixture-access-secret")), nil },
		keychain:  kc.read,
	}
	got := src.location()
	if len(kc.calls) != 0 || reads != 0 {
		t.Errorf("location read credentials: %d keychain calls, %d file reads", len(kc.calls), reads)
	}
	if strings.Contains(got, "fixture-access-secret") {
		t.Errorf("location carries the token: %q", got)
	}
}

func TestFingerprintIdentifiesTheTokenOneWay(t *testing.T) {
	// The usage cache is bound to the token it was fetched with: the same
	// token gives the same fingerprint, another token a different one, and
	// the fingerprint is the first 16 hex digits of its sha256.
	src := func(token string) tokenSource {
		return tokenSource{
			lookupEnv: lookupMap(map[string]string{"HOME": "/Users/dev", "USER": "dev"}),
			readFile:  os.ReadFile,
			keychain:  func(string, string) (string, error) { return fixtureCredJSON(token), nil },
		}
	}
	a1, err := src("fixture-access-a").fingerprint()
	if err != nil {
		t.Fatal(err)
	}
	a2, _ := src("fixture-access-a").fingerprint()
	b, _ := src("fixture-access-b").fingerprint()
	if a1 != a2 || a1 == b {
		t.Errorf("fingerprints: a=%q a again=%q b=%q", a1, a2, b)
	}
	if want := sha256Prefix16("fixture-access-a"); a1 != want || len(a1) != 16 {
		t.Errorf("fingerprint = %q, want %q", a1, want)
	}
	if strings.Contains(a1, "fixture-access") {
		t.Errorf("fingerprint carries the token: %q", a1)
	}
}

func TestFingerprintFailsWithoutAToken(t *testing.T) {
	src := tokenSource{
		lookupEnv: lookupMap(map[string]string{"HOME": t.TempDir(), "USER": "dev"}),
		readFile:  os.ReadFile,
		keychain:  noKeychainItem,
	}
	if fp, err := src.fingerprint(); err == nil || fp != "" {
		t.Errorf("fingerprint without a token = %q, %v; want an error", fp, err)
	}
}

// sha256Prefix16 is the expected fingerprint, computed with the standard
// library directly rather than through the code under test.
func sha256Prefix16(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:8])
}
