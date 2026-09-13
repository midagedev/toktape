package recorder

import (
	"testing"

	"github.com/midagedev/toktape/internal/tape"
)

// TestRepresentativeTimingsCarriesReasoningN: the thinking-token count has to
// survive the per-stream mean, or the card's Context row prints "N out" with
// no thinking clause for exactly the mode this project calls first-class —
// N concurrent streams of an agent workload, which is where thinking models
// live (TTP-20, 2026-09-13).
//
// ReasoningN is a token count among PredictedN, so it is averaged the way
// PredictedN is: summed over the streams that produced tokens, then divided
// and rounded.
func TestRepresentativeTimingsCarriesReasoningN(t *testing.T) {
	stream := func(predicted, reasoning int) tape.RequestRecord {
		return tape.RequestRecord{Timings: tape.TimingsSummary{
			PromptN:            43,
			PredictedN:         predicted,
			ReasoningN:         reasoning,
			PredictedMs:        2436,
			PredictedPerSecond: 39.4,
			TTFTMs:             222,
		}}
	}

	t.Run("single stream is copied whole", func(t *testing.T) {
		got := representativeTimings([]tape.RequestRecord{stream(96, 96)})
		if got.ReasoningN != 96 {
			t.Errorf("ReasoningN = %d, want 96", got.ReasoningN)
		}
	})

	t.Run("mean over streams", func(t *testing.T) {
		got := representativeTimings([]tape.RequestRecord{
			stream(96, 96), stream(100, 40), stream(104, 41),
		})
		// (96 + 40 + 41) / 3 = 59, the same rounding PredictedN gets.
		if want := 59; got.ReasoningN != want {
			t.Errorf("ReasoningN = %d, want %d", got.ReasoningN, want)
		}
		if want := 100; got.PredictedN != want {
			t.Errorf("PredictedN = %d, want %d (guard: the mean itself still works)", got.PredictedN, want)
		}
	})

	t.Run("failed streams are excluded", func(t *testing.T) {
		bad := stream(0, 0)
		bad.Error = "stream ended without a finish chunk"
		got := representativeTimings([]tape.RequestRecord{stream(96, 96), bad})
		if want := 96; got.ReasoningN != want {
			t.Errorf("ReasoningN = %d, want %d (a failed stream must not halve the figure)", got.ReasoningN, want)
		}
	})

	t.Run("a model that does not think reports zero", func(t *testing.T) {
		got := representativeTimings([]tape.RequestRecord{stream(96, 0), stream(96, 0)})
		if got.ReasoningN != 0 {
			t.Errorf("ReasoningN = %d, want 0", got.ReasoningN)
		}
	})
}

// TestRepresentativeTimingsSumsDraftFigures (TTP-30, 2026-09-13).
//
// The run-level draft figures are the SUM over the streams that reported them,
// not the first stream's pair and not a mean. Accepted over drafted is a
// ratio, so the only reduction that produces the run's real acceptance rate is
// to pool the numerator and the denominator: a mean of the per-stream rates
// would weight a stream that drafted twelve tokens the same as one that
// drafted three hundred, and the first stream's pair alone describes an eighth
// of a run of eight streams.
//
// FAIL-first: before this commit representativeTimings copied the first
// reporting stream's pointers and returned 120/200 for the fixture below.
func TestRepresentativeTimingsSumsDraftFigures(t *testing.T) {
	stream := func(predicted int, draftN, accepted *int) tape.RequestRecord {
		return tape.RequestRecord{Timings: tape.TimingsSummary{
			PromptN:            43,
			PredictedN:         predicted,
			PredictedMs:        2436,
			PredictedPerSecond: 39.4,
			TTFTMs:             222,
			DraftN:             draftN,
			DraftNAccepted:     accepted,
		}}
	}
	ptr := func(n int) *int { return &n }
	got := func(t *testing.T, recs []tape.RequestRecord) (int, int) {
		t.Helper()
		out := representativeTimings(recs)
		if out.DraftN == nil || out.DraftNAccepted == nil {
			t.Fatalf("DraftN/DraftNAccepted = %v/%v, want a reported pair", out.DraftN, out.DraftNAccepted)
		}
		return *out.DraftN, *out.DraftNAccepted
	}

	t.Run("two streams pool their figures", func(t *testing.T) {
		drafted, accepted := got(t, []tape.RequestRecord{
			stream(96, ptr(200), ptr(120)),
			stream(96, ptr(90), ptr(54)),
		})
		if drafted != 290 || accepted != 174 {
			t.Errorf("drafted/accepted = %d/%d, want 290/174", drafted, accepted)
		}
	})

	t.Run("a stream that reported nothing contributes nothing", func(t *testing.T) {
		drafted, accepted := got(t, []tape.RequestRecord{
			stream(96, nil, nil),
			stream(96, ptr(200), ptr(120)),
			stream(96, ptr(90), ptr(54)),
		})
		if drafted != 290 || accepted != 174 {
			t.Errorf("drafted/accepted = %d/%d, want 290/174", drafted, accepted)
		}
	})

	t.Run("a failed stream is excluded like every other figure", func(t *testing.T) {
		bad := stream(0, ptr(1000), ptr(0))
		bad.Error = "stream ended without a finish chunk"
		drafted, accepted := got(t, []tape.RequestRecord{
			stream(96, ptr(200), ptr(120)),
			bad,
			stream(96, ptr(90), ptr(54)),
		})
		if drafted != 290 || accepted != 174 {
			t.Errorf("drafted/accepted = %d/%d, want 290/174 (a failed stream must not be pooled)", drafted, accepted)
		}
	})

	t.Run("no draft at all stays nil", func(t *testing.T) {
		out := representativeTimings([]tape.RequestRecord{stream(96, nil, nil), stream(96, nil, nil)})
		if out.DraftN != nil || out.DraftNAccepted != nil {
			t.Errorf("DraftN/DraftNAccepted = %v/%v, want nil/nil: no stream reported a draft", out.DraftN, out.DraftNAccepted)
		}
	})

	t.Run("the sum does not alias a stream's own figures", func(t *testing.T) {
		a, b := ptr(200), ptr(120)
		out := representativeTimings([]tape.RequestRecord{
			stream(96, a, b),
			stream(96, ptr(90), ptr(54)),
		})
		if out.DraftN == a || out.DraftNAccepted == b {
			t.Error("the run-level pair points at stream 0's own ints; writing the sum would rewrite the request record")
		}
		if *a != 200 || *b != 120 {
			t.Errorf("stream 0's figures moved to %d/%d", *a, *b)
		}
	})

	t.Run("a single stream is copied whole", func(t *testing.T) {
		drafted, accepted := got(t, []tape.RequestRecord{stream(96, ptr(200), ptr(120))})
		if drafted != 200 || accepted != 120 {
			t.Errorf("drafted/accepted = %d/%d, want 200/120", drafted, accepted)
		}
	})
}
