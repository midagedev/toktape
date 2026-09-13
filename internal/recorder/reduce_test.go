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
