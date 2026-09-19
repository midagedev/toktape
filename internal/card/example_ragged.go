package card

import (
	"time"

	"github.com/midagedev/toktape/internal/tape"
)

// ExampleRagged returns the same rig running four streams that did not share
// a decode window.
//
// It is the run TTP-108 (2026-09-19) is about: three streams generate 300
// tokens and one stops at 60, so the aggregate — the run's 960 tokens over
// a window whose tail holds one stream — is 58.0 tok/s while four times the
// 17.4 per-stream mean is 69.6. The Decode row prints both figures and the
// Streams block keeps "not all decoding at once"; only the ragged_aggregate
// caveat names the arithmetic.
//
// The derivation: 960 tokens over 16552 ms is 58.0 tok/s aggregate; the
// per-stream mean stays Example's 17.4. The shortest stream's 60 tokens
// clear tape.MinDecodeTokens, so this is not a short stream averaged in —
// the aggregate is still a rate, only not the sum. The timeline peak is left
// unrecorded (0, unknown), so streams_not_concurrent stays out: this fixture
// is about the arithmetic, not the timeline. The TTFT percentiles differ
// (1200 against 1500 ms), so the pair still prints as a pair.
func ExampleRagged() *tape.RunSummary {
	s := Example()
	started := time.Date(2026, 9, 19, 12, 5, 0, 0, time.UTC)
	s.ID = "20260919-120500-r1-distill-llama-70b"
	s.StartedAt = started
	s.FinishedAt = started.Add(17200 * time.Millisecond)
	s.Concurrency = 4
	s.Timings.PredictedN = 300
	s.Timings.PredictedMs = 17241.4
	s.Aggregate = tape.AggregateTimings{
		Streams:                     4,
		WallMs:                      16552,
		TotalPromptN:                2048,
		TotalPredictedN:             960,
		MinPredictedN:               60,
		AggregatePredictedPerSecond: 58.0,
		AggregatePromptPerSecond:    1540.0,
		PerStreamPredictedPerSecond: 17.4,
		TTFTp50Ms:                   1200,
		TTFTp95Ms:                   1500,
		SlotsBusyMax:                4,
	}
	return s
}
