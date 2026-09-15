package server

import (
	"testing"
	"time"

	"github.com/midagedev/toktape/internal/tape"
)

// TestPeakDecodingStreams (lead, 2026-09-15) is the token timeline's answer to
// "were these streams ever decoding at the same instant", which slots_busy_max
// answers wrongly for an engine that takes N requests into N slots and runs
// one job: the exl3 trial had slots busy 2 and two streams that never shared a
// millisecond. A stream's window runs from its second token to its
// second-to-last (tape.AggregateTimings.PeakDecodingStreams), windows touching
// at an endpoint overlap, and every case below is a window arithmetic fact the
// sweep has to get right rather than a plausible rate.
func TestPeakDecodingStreams(t *testing.T) {
	ms := time.Millisecond
	// windowOf spells out synthStream's window: [first+gap, first+(n-2)*gap],
	// both absolute once StartedAt is added.
	windowOf := func(startedAt, first, gap time.Duration, n int) (time.Duration, time.Duration) {
		return startedAt + first + gap, startedAt + first + time.Duration(n-2)*gap
	}
	gap := 25 * ms
	n := 100
	cases := []struct {
		name string
		recs []tape.RequestRecord
		want int
	}{
		// The trial's shape: stream 1 decodes 1.0–3.5 s (window 1.025–3.45),
		// stream 0's window only opens at 3.625. Serial, whatever the slots
		// said.
		{"serial, the trial's shape", []tape.RequestRecord{
			synthStream(0, 0, 1000*ms, gap, n),
			synthStream(1, 3*time.Second, 600*ms, gap, n),
		}, 1},
		{"two streams decoding together", []tape.RequestRecord{
			synthStream(0, 0, 200*ms, gap, n),
			synthStream(1, 0, 300*ms, gap, n),
		}, 2},
		// A [225,2650], B [1325,3750], C [2775,5195]: both neighbouring pairs
		// overlap, the two ends never do, and the answer is 2, not 3.
		{"three streams, a 2-overlap and no 3-overlap", []tape.RequestRecord{
			synthStream(0, 0, 200*ms, gap, n),
			synthStream(1, 0, 1300*ms, gap, n),
			synthStream(2, 0, 2750*ms, gap, n),
		}, 2},
		// A's window ends 2650, on its second-to-last token; B's second token
		// lands exactly on 2650. Touching is overlap: the engine handed one
		// stream's step straight to the other.
		{"a window starting on the other's second-to-last token", []tape.RequestRecord{
			synthStream(0, 0, 200*ms, gap, n),
			synthStream(1, 0, 2625*ms, gap, n),
		}, 2},
		// B's second token lands at 2675, A's LAST one — a step the window
		// excludes on purpose, so this is not overlap.
		{"a window starting on the other's last token", []tape.RequestRecord{
			synthStream(0, 0, 200*ms, gap, n),
			synthStream(1, 0, 2650*ms, gap, n),
		}, 1},
		{"a two-token stream has no window", []tape.RequestRecord{
			synthStream(0, 0, 200*ms, gap, n),
			synthStream(1, 0, 300*ms, gap, 2),
		}, 1},
		{"a failed stream is not an answered one", []tape.RequestRecord{
			synthStream(0, 0, 200*ms, gap, n),
			{Index: 1, Slot: -1, StartedAt: 0, Tokens: synthStream(1, 0, 300*ms, gap, n).Tokens, Error: "boom"},
		}, 1},
		{"no stream has a window", []tape.RequestRecord{
			synthStream(0, 0, 200*ms, gap, 2),
			synthStream(1, 0, 300*ms, gap, 2),
		}, 0},
		// B's own relative window [110,190] misses A's [1100,1900]; with its
		// 1.5 s send it lands inside it. StartedAt is the whole difference.
		{"StartedAt offsets are applied", []tape.RequestRecord{
			synthStream(0, 0, 1000*ms, 100*ms, 11),
			synthStream(1, 1500*ms, 100*ms, 10*ms, 11),
		}, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := PeakDecodingStreams(tc.recs); got != tc.want {
				t.Errorf("PeakDecodingStreams = %d, want %d", got, tc.want)
			}
		})
	}
	// The window arithmetic the cases above assume, asserted rather than
	// trusted: one line per shape the sweep decides on.
	_, a1 := windowOf(0, 200*ms, gap, n)
	b0, _ := windowOf(0, 2625*ms, gap, n)
	if a1 != b0 {
		t.Errorf("the endpoint-touch case does not touch: %v vs %v", a1, b0)
	}
	c0, _ := windowOf(0, 2650*ms, gap, n)
	if c0 <= a1 {
		t.Errorf("the last-token case overlaps: %v vs %v", c0, a1)
	}
}

// TestAggregateCarriesThePeak: Aggregate sets the field, so every caller —
// the recorder's single-round path, the rounds reduction, a tape re-read by
// the card — gets the timeline's answer without deriving it again.
func TestAggregateCarriesThePeak(t *testing.T) {
	ms := time.Millisecond
	recs := []tape.RequestRecord{
		synthStream(0, 0, 200*ms, 25*ms, 100),
		synthStream(1, 0, 300*ms, 25*ms, 100),
	}
	if got := Aggregate(recs).PeakDecodingStreams; got != 2 {
		t.Errorf("Aggregate.PeakDecodingStreams = %d, want 2", got)
	}
	// Nothing answered: the field stays the schema's unknown, not a guess.
	failed := []tape.RequestRecord{{Index: 0, Slot: -1, Error: "boom"}}
	if got := Aggregate(failed).PeakDecodingStreams; got != 0 {
		t.Errorf("Aggregate(all failed).PeakDecodingStreams = %d, want 0", got)
	}
}
