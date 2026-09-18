package tui

// TTP-123 bench (go1.26.4 darwin/arm64, Apple M1 Pro).
// Run: go test ./internal/tui/ -run '^$' -bench BenchmarkView -benchmem -count 3
//
// Baseline 2026-09-19, unchanged source:
//	start	~870µs/op	~280KB/op	4991 allocs/op
//	mid	~2045µs/op	~1230KB/op	10619 allocs/op
//	end	~4115µs/op	~6260KB/op	13662 allocs/op
//
// After 2026-09-19 (textRuns Builder, bodyBands slicing, precomputed SGR
// pairs, ASCII fast paths in cellLine/writeCells, bodyStyle array ladder):
//	start	~407µs/op	~218KB/op	896 allocs/op	(18% of baseline allocs)
//	mid	~1100µs/op	~655KB/op	2041 allocs/op	(19%)
//	end	~2243µs/op	~1961KB/op	2842 allocs/op	(21%; B/op 31%)

import (
	"testing"
	"time"

	"github.com/midagedev/toktape/internal/tape"
)

// sweepRunEnd lives in view_alloc_test.go (same package).

var benchSweepTape *tape.Tape

func loadBenchTape(b *testing.B) *tape.Tape {
	b.Helper()
	if benchSweepTape != nil {
		return benchSweepTape
	}
	tp, err := tape.Read("testdata/qwen36-35b-a3b-q6k-4stream-ik-sweep-0.2.4.tape")
	if err != nil {
		b.Fatalf("read sweep tape: %v", err)
	}
	benchSweepTape = tp
	return tp
}

// BenchmarkView measures View at the start, middle and end of the 4-stream
// sweep run (TTP-123). Model via ModelAt; View with the colour theme the way
// render.FrameText calls it (Replay set, Mode live).
func BenchmarkView(b *testing.B) {
	tp := loadBenchTape(b)
	end := sweepRunEnd(tp)
	cases := map[string]time.Duration{
		"start": 0,
		"mid":   end / 2,
		"end":   end,
	}
	for name, at := range cases {
		m := ModelAt(tp, at)
		m.Theme = ColourTheme()
		m.Replay = true
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_ = View(m, at, 120, 36)
			}
		})
	}
}
