package card

import (
	"strings"
	"testing"
)

// The Streams row prints "N × per-stream = aggregate", which is an equation a
// reader checks. It only holds while every stream decodes over the same
// window. On the ExLlamaV3 two-stream take of 2026-09-15 the streams finished
// about ten seconds apart, so the aggregate is over a window whose tail holds
// one stream: 2 × 11.6 = 23.2, and the card printed "= 19.4 aggregate". The
// figures are right and the sentence is not, which is the same reason a
// multi-round run already lists its three figures instead of multiplying.
func TestStreamsRowDropsTheEquationWhenItDoesNotHold(t *testing.T) {
	s := Example()
	s.Rounds = 0
	s.Concurrency = 2
	s.Aggregate.Streams = 2
	s.Aggregate.PerStreamPredictedPerSecond = 11.6
	s.Aggregate.AggregatePredictedPerSecond = 19.4

	line := strings.Join(streamLines(s), " ")

	if strings.Contains(line, "×") || strings.Contains(line, "=") {
		t.Errorf("2 × 11.6 is 23.2, not the 19.4 aggregate, so the row must not multiply:\n%s", line)
	}
	for _, want := range []string{"2 streams", "11.6 tok/s each", "19.4 tok/s aggregate"} {
		if !strings.Contains(line, want) {
			t.Errorf("the row must still name every figure, missing %q:\n%s", want, line)
		}
	}
}

// And it keeps the equation when the equation is true: streams that ran over
// the same window are the case the multiplication was written for, and it is
// the clearest way to say "this is what N of them do".
func TestStreamsRowKeepsTheEquationWhenItHolds(t *testing.T) {
	s := Example()
	s.Rounds = 0
	s.Concurrency = 2
	s.Aggregate.Streams = 2
	s.Aggregate.PerStreamPredictedPerSecond = 11.6
	s.Aggregate.AggregatePredictedPerSecond = 23.1 // 0.4 % off 23.2: rounding, not a tail

	line := strings.Join(streamLines(s), " ")

	if !strings.Contains(line, "2 × 11.6 tok/s = 23.1 tok/s aggregate") {
		t.Errorf("the equation holds here and reads best as one:\n%s", line)
	}
}
