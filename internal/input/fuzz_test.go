package input

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// FuzzParse pins the trust boundary: the stdin JSON is untrusted, and Parse
// must never panic on any byte sequence — error or defaulted Session only.
func FuzzParse(f *testing.F) {
	for _, fx := range []string{"full.json", "degraded.json", "cwd-fallback.json"} {
		b, err := os.ReadFile(filepath.Join("..", "..", "testdata", fx))
		if err != nil {
			f.Fatalf("seed fixture %s: %v", fx, err)
		}
		f.Add(b)
	}
	f.Add([]byte(""))
	f.Add([]byte("null"))
	f.Add([]byte("{}"))
	f.Add([]byte(`{"model":{"display_name":""},"cwd":""}`))
	f.Add([]byte(`{"context_window":{"used_percentage":1e308,"context_window_size":-1}}`))
	f.Add([]byte(`{"cost":{"total_duration_ms":-9223372036854775808}}`))
	f.Add([]byte(`{"error":{"type":"rate_limit_error","message":"exceeded"}}`))
	f.Add([]byte("{\"model\":\xff\xfe}"))
	f.Add([]byte(`{"model":{"display_name":"Fable 5"}}{"trailing":"value"}`))

	f.Fuzz(func(t *testing.T, raw []byte) {
		s, err := Parse(bytes.NewReader(raw))
		if err != nil {
			return
		}
		// The documented defaults hold on every accepted payload.
		if s.ModelName == "" {
			t.Errorf("accepted payload produced empty ModelName (default \"?\" missing): %q", raw)
		}
		if s.CtxSize == 0 {
			t.Errorf("accepted payload produced zero CtxSize (default 200000 missing): %q", raw)
		}
	})
}
