package card

import (
	"strings"
	"testing"

	"github.com/midagedev/toktape/internal/tape"
)

// TTP-138 (FAIL-first, 2026-09-19): on a run of more than one stream that
// carries the concurrent-window figure, that figure is the one the Decode
// row leads with — in the place AggregatePredictedPerSecond occupies today
// and under the same word "aggregate". ExampleWindow is ExampleRagged plus
// the window fields, so the two cards differ in exactly this: 66.7 where
// 58.0 stood, 16.7 where 17.4 stood, and nothing else about the row.
//
// The "each" figure moved too (lead, 2026-09-19). The original round pinned
// it unchanged at 17.4, on the spec's clause that nothing else about the row
// moves — which was my error: 4 x 17.4 = 69.6 against a printed 66.7 is the
// very arithmetic ragged_aggregate disputes, reproduced inside the row that
// displaced it. The assertion that the per-stream figure stays at the
// whole-wall 17.4 described behaviour that is gone by design and is deleted
// with this note rather than re-pinned.
func TestDecodeRowLeadsWithTheWindowFigure(t *testing.T) {
	row := rowBlock(t, Text(ExampleWindow()), "Decode")
	if !strings.Contains(row, "66.7 tok/s aggregate") {
		t.Errorf("Decode row = %q, want it to lead with the window figure 66.7 under the word \"aggregate\"", row)
	}
	if !strings.Contains(row, "16.7 tok/s each") {
		t.Errorf("Decode row = %q, want the window's own per-stream figure 16.7 beside it", row)
	}
	if strings.Contains(row, "17.4 tok/s each") {
		t.Errorf("Decode row = %q, want the whole-wall per-stream figure gone from the row: it is measured over a different span than the aggregate beside it", row)
	}
	if strings.Contains(row, "58.0 tok/s aggregate") {
		t.Errorf("Decode row = %q, want the whole-wall figure displaced from the lead; it belongs to the caveat that explains the tail", row)
	}
}

// The row's two figures reconcile: that is the whole claim of leading with
// the window, and it is what the whole-wall pair cannot do (lead,
// 2026-09-19). Asserted on the figures rather than the rendered text so a
// formatting change cannot quietly satisfy it.
func TestWindowRowReconciles(t *testing.T) {
	a := ExampleWindow().Aggregate
	streams := float64(ExampleWindow().Concurrency)
	got := a.ConcurrentPerStreamPredictedPerSecond * streams
	if diff := got - a.ConcurrentPredictedPerSecond; diff > 0.1 || diff < -0.1 {
		t.Errorf("%v x %v = %v, want the aggregate %v: the row prints a product it never printed",
			streams, a.ConcurrentPerStreamPredictedPerSecond, got, a.ConcurrentPredictedPerSecond)
	}
	whole := a.PerStreamPredictedPerSecond * streams
	if diff := whole - a.AggregatePredictedPerSecond; diff < 1 && diff > -1 {
		t.Errorf("the whole-wall pair reconciles too (%v x %v vs %v), so this fixture no longer shows the difference the window exists for",
			streams, a.PerStreamPredictedPerSecond, a.AggregatePredictedPerSecond)
	}
}

// Where the window was not computed — one stream, a tape older than the
// field, no overlap — the row is exactly what it is today. The goldens pin
// the same bytes; this says the rule in words.
func TestDecodeRowWithoutAWindowIsWhatItWas(t *testing.T) {
	cases := []struct {
		name string
		s    func() *tape.RunSummary
		want string
	}{
		// No window on the tape: the whole-wall aggregate leads, as it
		// always did.
		{"no window recorded", ExampleRagged, "58.0 tok/s aggregate · 17.4 tok/s each"},
		{"balanced concurrent, no window", ExampleConcurrent, "72.9 tok/s aggregate · 9.1 tok/s each"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if row := rowBlock(t, Text(tc.s()), "Decode"); !strings.Contains(row, tc.want) {
				t.Errorf("Decode row = %q, want today's %q", row, tc.want)
			}
		})
	}
	// One stream has no aggregate at all: the row stays the single rate,
	// whatever ProbeSummary-style additions later fields might invite.
	if row := rowBlock(t, Text(Example()), "Decode"); strings.Contains(row, "aggregate") {
		t.Errorf("single-stream Decode row = %q, want no aggregate clause on a run of one stream", row)
	}
}
