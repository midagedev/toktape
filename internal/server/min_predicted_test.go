package server

import (
	"testing"
	"time"

	"github.com/midagedev/toktape/internal/tape"
)

// TestAggregateRecordsTheShortestStream (lead, 2026-09-14). Timings.PredictedN
// is the per-stream mean above one stream, so a 10-token stream beside a
// 300-token one averages 155 and clears tape.MinDecodeTokens while one of the
// two is a sample. The minimum is what a reader needs to see that, and a
// stream that failed or produced nothing is not in it.
func TestAggregateRecordsTheShortestStream(t *testing.T) {
	recs := []tape.RequestRecord{
		synthStream(0, 0, 200*time.Millisecond, 25*time.Millisecond, 300),
		synthStream(1, 0, 210*time.Millisecond, 25*time.Millisecond, 10),
		{Index: 2, Error: "boom"},
	}
	agg := Aggregate(recs)
	if agg.MinPredictedN != 10 {
		t.Errorf("MinPredictedN = %d, want 10: the short stream is hidden inside a mean of %d",
			agg.MinPredictedN, agg.TotalPredictedN/2)
	}
	if got := Aggregate([]tape.RequestRecord{{Error: "boom"}}).MinPredictedN; got != 0 {
		t.Errorf("a run with no answered stream has MinPredictedN %d, want 0 (unknown)", got)
	}
}
