package server

import (
	"testing"
	"time"

	"github.com/midagedev/toktape/internal/tape"
)

// TestAggregateCountsTheShortStreams (TTP-85, lead, 2026-09-14). The minimum
// says the shortest stream was too short to be a rate; the count says how many
// were, so the card can say "2 of 4 streams". A stream exactly at
// MinDecodeTokens is a rate and is not counted, and a failed stream is not an
// answered one.
func TestAggregateCountsTheShortStreams(t *testing.T) {
	gap := 25 * time.Millisecond
	recs := []tape.RequestRecord{
		synthStream(0, 0, 200*time.Millisecond, gap, 300),
		synthStream(1, 0, 210*time.Millisecond, gap, 10),
		synthStream(2, 0, 220*time.Millisecond, gap, tape.MinDecodeTokens-1),
		synthStream(3, 0, 230*time.Millisecond, gap, tape.MinDecodeTokens),
		{Index: 4, Error: "boom"},
	}
	if got := Aggregate(recs).ShortStreams; got != 2 {
		t.Errorf("ShortStreams = %d, want 2: streams of 10 and %d tokens are under %d, one of %d is not",
			got, tape.MinDecodeTokens-1, tape.MinDecodeTokens, tape.MinDecodeTokens)
	}
}
