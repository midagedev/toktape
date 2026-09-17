package card

import (
	"strings"
	"testing"
)

// The Streams row used to print "N × per-stream = aggregate", an equation a
// reader checks, and to fall back to listing the three figures when it would
// have been false. Both forms repeated the Decode row's own two figures, so
// since 2026-09-17 (TTP-110, user: "중복정보 정리하자") the row carries neither:
// the rates are the Decode row's, and what this row says about them is the one
// thing that row cannot — whether the streams actually shared a window.
//
// streamsMultiply is still the predicate. It only holds while every stream
// decodes over the same window; on the ExLlamaV3 two-stream take of 2026-09-15
// the streams finished about ten seconds apart, so the aggregate was over a
// window whose tail held one stream — 2 × 11.6 = 23.2 against a 19.4
// aggregate. The card used to leave the reader to notice that the arithmetic
// did not work; now it says so.
func TestStreamsRowSaysWhenTheStreamsDidNotShareTheWindow(t *testing.T) {
	s := Example()
	s.Rounds = 0
	s.Concurrency = 2
	s.Aggregate.Streams = 2
	s.Aggregate.PerStreamPredictedPerSecond = 11.6
	s.Aggregate.AggregatePredictedPerSecond = 19.4

	line := strings.Join(streamLines(s), " ")

	if !strings.Contains(line, "2 streams") {
		t.Errorf("the row must still say how many streams there were:\n%s", line)
	}
	if !strings.Contains(line, "not all decoding at once") {
		t.Errorf("2 × 11.6 is 23.2, not the 19.4 aggregate, and the row must say so:\n%s", line)
	}
	// FAIL-first for the half this replaces: on the source before TTP-110 the
	// row read "2 streams · 11.6 tok/s each · 19.4 tok/s aggregate", so both
	// figures below were present.
	for _, gone := range []string{"11.6", "19.4", "×", "="} {
		if strings.Contains(line, gone) {
			t.Errorf("the rates are the Decode row's and must not be repeated here (%q):\n%s", gone, line)
		}
	}
}

// And when the streams did share the window there is nothing to warn about, so
// the clause is absent — it is a finding, not a decoration.
func TestStreamsRowIsQuietWhenTheStreamsSharedTheWindow(t *testing.T) {
	s := Example()
	s.Rounds = 0
	s.Concurrency = 2
	s.Aggregate.Streams = 2
	s.Aggregate.PerStreamPredictedPerSecond = 11.6
	s.Aggregate.AggregatePredictedPerSecond = 23.1 // 0.4 % off 23.2: rounding, not a tail

	line := strings.Join(streamLines(s), " ")

	if strings.Contains(line, "not all decoding at once") {
		t.Errorf("the streams shared the window here and the row must not say otherwise:\n%s", line)
	}
	if !strings.Contains(line, "2 streams") {
		t.Errorf("the row must still say how many streams there were:\n%s", line)
	}
}
