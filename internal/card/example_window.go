package card

import (
	"time"

	"github.com/midagedev/toktape/internal/tape"
)

// ExampleWindow returns ExampleRagged's run as a tape that carries the
// concurrent-window figures (TTP-138, 2026-09-19): the same four streams, the
// same ragged whole wall, plus the window in which all four decoded at once
// and what was produced inside it.
//
// It is deliberately ExampleRagged plus three fields, because that is the
// relationship between the two tapes in the wild: one run, recorded by a
// version that computes the window and one that predates it. Every card that
// renders ExampleRagged's aggregate must render this one's window figure the
// way the Decode row contract says — lead with it, under the same word
// "aggregate" — and the ragged_aggregate caveat names the tail against the
// wall instead of saying it cannot.
//
// The derivation, in ExampleRagged's own story: the short stream's 60 tokens
// are the window — while it decoded, all four ran at 16.7 tok/s each, so the
// window is 60 / 16.7 ≈ 3.6 s and holds 4 × 60 = 240 tokens, 240 / 3.6 s =
// 66.7 tok/s. The window reconciles by construction: 4 × 16.7 = 66.7. The
// whole wall does not (4 × 17.4 = 69.6 against its 58.0), which is the ragged
// arithmetic, and 66.7 against 58.0 is the tail's cost said in figures.
func ExampleWindow() *tape.RunSummary {
	s := ExampleRagged()
	started := time.Date(2026, 9, 19, 13, 30, 0, 0, time.UTC)
	s.ID = "20260919-133000-r1-distill-llama-70b"
	s.StartedAt = started
	s.FinishedAt = started.Add(17200 * time.Millisecond)
	s.Aggregate.ConcurrentWindowMs = 3600
	s.Aggregate.ConcurrentPredictedN = 240
	s.Aggregate.ConcurrentPredictedPerSecond = 66.7
	s.Aggregate.ConcurrentPerStreamPredictedPerSecond = 16.7
	s.Aggregate.ConcurrentStreams = 4
	return s
}

// ExampleProbed returns Example() with the probe pass's figures filled in
// (TTP-137, 2026-09-19): the machine's own prefill at the margin and the
// server's fixed cost, the two numbers the two-point fit separates.
//
// The figures are the reference box's own measurement: 1182 tok/s at the
// margin and about 28 ms of fixed cost per request — the numbers the
// ProbeSummary doc uses, so the fixture, the schema and the recorded tape
// all tell one story. The points stay in the order the pass sent them, and
// the replay is the long prompt served from the prefix cache the second
// time, which is the honest reading of a warm cache.
func ExampleProbed() *tape.RunSummary {
	s := Example()
	s.Probe = &tape.ProbeSummary{
		Prefill: []tape.PrefillPoint{
			{PromptN: 128, PromptMs: 136.2, TTFTMs: 165},
			{PromptN: 2048, PromptMs: 1757.9, TTFTMs: 1786},
		},
		PrefillPerSecond: 1182,
		FixedMs:          28,
		Replay:           &tape.ReplayProbe{PromptN: 0, CacheN: 2048, PromptMs: 2.0},
	}
	return s
}
