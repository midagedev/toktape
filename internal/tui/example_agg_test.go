package tui

import "testing"

// TestExampleTapeNAggregateMatchesStreams pins the card's own arithmetic: the
// Streams row prints "N × per-stream = aggregate", so N must be the stream
// count the tape was built with. Found 2026-09-13 when the four-stream hero's
// card said "8 × 11.3 = 45.3".
func TestExampleTapeNAggregateMatchesStreams(t *testing.T) {
	for n := 2; n <= 8; n++ {
		tp := ExampleTapeN(n)
		a := tp.Summary.Aggregate
		if a.Streams != n || tp.Summary.Concurrency != n || a.SlotsBusyMax != n {
			t.Errorf("ExampleTapeN(%d): Streams=%d Concurrency=%d SlotsBusyMax=%d", n, a.Streams, tp.Summary.Concurrency, a.SlotsBusyMax)
		}
		if got := a.PerStreamPredictedPerSecond * float64(n); got < a.AggregatePredictedPerSecond*0.99 || got > a.AggregatePredictedPerSecond*1.01 {
			t.Errorf("ExampleTapeN(%d): %d × %.1f = %.1f, card aggregate %.1f", n, n, a.PerStreamPredictedPerSecond, got, a.AggregatePredictedPerSecond)
		}
	}
}
