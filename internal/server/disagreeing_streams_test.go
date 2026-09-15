package server

import (
	"testing"
	"time"

	"github.com/midagedev/toktape/internal/tape"
)

// withServerRate twists a synthStream's server figure away from what its
// timeline measured, the way a real server's final timings object can, and
// settles the agreement flag the way Reduce does: the same one owner, so the
// fixture cannot disagree with itself about what a disagreement is.
func withServerRate(rec tape.RequestRecord, rate float64) tape.RequestRecord {
	rec.Timings.PredictedPerSecond = rate
	rec.Timings.ClientAgreesWithServer = RatesAgree(rec.Timings.ClientPredictedPerSecond, rate)
	return rec
}

// TestAggregateCountsDisagreeingStreams (lead, 2026-09-15): the run-level
// TimingsSummary above one stream is a mean, and its agreement flag now
// describes that mean, so the per-stream fact survives only as
// Aggregate.DisagreeingStreams — the count the caveat says out loud. A stream
// with either rate at 0 is an absence, not a disagreement (the guard
// clientDisagrees uses on the summary), and a failed stream never answered, so
// it is neither agreeing nor disagreeing.
func TestAggregateCountsDisagreeingStreams(t *testing.T) {
	ms := time.Millisecond
	gap := 25 * ms
	// synthStream's own rate: the server figure equals the timeline's, so the
	// flag is true without any twisting.
	agreeing := synthStream(0, 0, 200*ms, gap, 100)
	if !agreeing.Timings.ClientAgreesWithServer {
		t.Fatal("fixture: synthStream's own rates must agree")
	}
	// 40 tok/s measured, 30 reported: 25 % apart.
	disagreeing := withServerRate(synthStream(1, 0, 210*ms, gap, 100), 30)
	if disagreeing.Timings.ClientAgreesWithServer {
		t.Fatal("fixture: 30 against 40 is outside the tolerance")
	}

	t.Run("one of two streams", func(t *testing.T) {
		if got := Aggregate([]tape.RequestRecord{agreeing, disagreeing}).DisagreeingStreams; got != 1 {
			t.Errorf("DisagreeingStreams = %d, want 1", got)
		}
	})
	t.Run("both agreeing", func(t *testing.T) {
		if got := Aggregate([]tape.RequestRecord{agreeing, agreeing}).DisagreeingStreams; got != 0 {
			t.Errorf("DisagreeingStreams = %d, want 0", got)
		}
	})
	t.Run("both disagreeing", func(t *testing.T) {
		if got := Aggregate([]tape.RequestRecord{disagreeing, disagreeing}).DisagreeingStreams; got != 2 {
			t.Errorf("DisagreeingStreams = %d, want 2", got)
		}
	})
	t.Run("a stream with no client rate is an absence", func(t *testing.T) {
		noClient := withServerRate(synthStream(1, 0, 210*ms, gap, 100), 30)
		noClient.Timings.ClientPredictedPerSecond = 0
		noClient.Timings.ClientAgreesWithServer = false
		if got := Aggregate([]tape.RequestRecord{agreeing, noClient}).DisagreeingStreams; got != 0 {
			t.Errorf("DisagreeingStreams = %d, want 0: a rate that was never measured is not a disagreement", got)
		}
	})
	t.Run("a stream with no server rate is an absence", func(t *testing.T) {
		noServer := synthStream(1, 0, 210*ms, gap, 100)
		noServer.Timings.PredictedPerSecond = 0
		noServer.Timings.ClientAgreesWithServer = false
		if got := Aggregate([]tape.RequestRecord{agreeing, noServer}).DisagreeingStreams; got != 0 {
			t.Errorf("DisagreeingStreams = %d, want 0: no server figure, nothing to disagree with", got)
		}
	})
	t.Run("a failed stream is not an answered one", func(t *testing.T) {
		failed := withServerRate(synthStream(1, 0, 210*ms, gap, 100), 30)
		failed.Error = "boom"
		if got := Aggregate([]tape.RequestRecord{agreeing, failed}).DisagreeingStreams; got != 0 {
			t.Errorf("DisagreeingStreams = %d, want 0: the failed stream is in no count but StreamsFailed", got)
		}
	})
}
