package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// BenchmarkRun measures a full frame — parse, config, auth, git, usage,
// render — over the injected fakes, per fixture. Compute-path guard only;
// the ~90 ms budget includes real IO these fakes deliberately exclude
// (see internal/render/bench_test.go for the observe-only rationale).
func BenchmarkRun(b *testing.B) {
	for _, fx := range []string{"full.json", "degraded.json", "cwd-fallback.json"} {
		b.Run(fx, func(b *testing.B) {
			d := e2eDeps(b, fx)
			raw, err := os.ReadFile(filepath.Join("testdata", fx))
			if err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			for b.Loop() {
				d.stdin = bytes.NewReader(raw)
				if out, _ := run(d); out == "" {
					b.Fatal("empty frame")
				}
			}
		})
	}
}
