package render

import (
	"testing"
	"time"
)

// The ~90 ms render budget is wall time INCLUDING real IO (git, cache,
// credential reads); these benchmarks guard the in-process compute path
// only — a regression here surfaces long before the budget is threatened.
// Observe-only by design decision on the card: no threshold assertion
// (shared CI runners make hard perf gates flaky).

// benchState is the heaviest realistic frame: every row present, hot
// meters forcing reset labels, compact badge, extra-usage badge, lock-age
// badge, stale-data marker, and the metered-billing alarm.
func benchState() State {
	return State{
		Model: "Fable 5", CtxSize: 1000000, Auth: "Sub",
		SessionID: "0a1b2c3d-0000-4000-8000-000000000000",
		CWD:       "/Users/dev/projects/demo-app",
		Home:      "/Users/dev", Branch: "main", Dirty: 2,
		CtxPct: 87, LockAge: 14 * time.Minute, APIKeySet: true,
		Usage: &Usage{
			Email: "dev@example.com",
			U5:    85, R5: "1:30p", U7: 82, R7: "Mon 10:00a",
			ModelFamily: "opus", ModelPct: 41, ModelReset: "Tue 3:00p",
			ExtraEnabled: true, CreditsMinor: 1250, CreditsExp: 2,
			MaxActive: 85, DataAge: 6 * time.Minute,
		},
		DurationMS: 62580000, LinesAdded: 1598, LinesRemoved: 8,
	}
}

func BenchmarkBuild(b *testing.B) {
	st := benchState()
	opts := DefaultOptions()
	b.ReportAllocs()
	for b.Loop() {
		if out := Build(st, opts); out == "" {
			b.Fatal("empty frame")
		}
	}
}
