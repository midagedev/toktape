package card

import (
	"math"
	"time"

	"github.com/midagedev/toktape/internal/tape"
)

// The sweep example is the run TTP-35 exists for: the six prompts of
// ExampleRounds() sent once at speculative.n_max 3 and once at 5, into one
// tape, to settle which draft block size is fastest on this prompt mix.
//
// The n_max 3 rounds are ExampleRounds' rows unchanged. The n_max 5 rounds use
// the same rule — "passes" are the target's verification passes over the
// round's four streams, each now drafting five tokens, and a round emits its
// passes plus the drafts the target accepted — and the same answers, so every
// prompt writes as many tokens as it did at 3 and takes the same TTFT.
//
//	round    n_max  rate  passes  drafted  accepted       tok/pass  window
//	sql-1      5    20.9     58      290    213 (73.4 %)    4.67     3.24 s
//	sql-2      5    20.0     62      310    217 (70.0 %)    4.50     3.49 s
//	prose-1    5     8.0    330     1650     87 ( 5.3 %)    1.26    13.03 s
//	prose-2    5     9.2    300     1500    156 (10.4 %)    1.52    12.39 s
//	code-1     5    16.8     88      440    233 (53.0 %)    3.65     4.78 s
//	chat-1     5    15.4     95      475    234 (49.3 %)    3.46     5.34 s
//
// A longer block is accepted less often but emits more tokens per pass where
// the draft agrees with the target: SQL gains about 29 % tokens per pass and
// runs about 8 % faster, since a five-token verification costs more than a
// three-token one. Prose gains nothing to pay for that, and is slower.
//
//	n_max 3    the rounds example: median 14.8 tok/s (9.1–19.3), accepted 61 %.
//	n_max 5    rates sorted 8.0 9.2 15.4 16.8 20.0 20.9, median
//	           (15.4 + 16.8) / 2 = 16.1 tok/s — the fastest. Acceptance sorted
//	           5.3 10.4 49.3 53.0 70.0 73.4 %, median 51 %.
//	run        all twelve rates, median (15.4 + 15.6) / 2 = 15.5 (8.0–20.9);
//	           acceptance median (53.0 + 58.1) / 2 = 56 % (5–87 %).
//	mean       Timings is the mean over all 48 streams, 176.9 / 12 = 14.74.
//	aggregate  4146 tokens over the rounds' summed decode windows, 83.22 s,
//	           = 49.8 tok/s.
//	wall       the rounds' own walls summed, 90 902 ms; FinishedAt adds the
//	           330 ms the recorder spent in the eleven gaps between rounds.
//	draft      7560 drafted and 2248 accepted over the run, pooled: 30 %.
//	tokens     4146 / 48 streams = 86 per stream.
//
// Every figure below the table is computed from the rows in code, except the
// spreads, which are written out by the reduction's rule so a reviewer can
// read them; internal/recorder's sweep test checks them against the reduction.

// ExampleSweep returns a realistic summary of a speculative n_max sweep:
// ExampleRounds' six prompts at n_max 3 and again at n_max 5, twelve rounds
// of four streams in one tape.
//
// It is what the Draft sweep row of the text card, the best-n_max clause of
// the PNG hero and compare's "spec n_max" change are drawn from, and the
// goldens under testdata pin the first.
func ExampleSweep() *tape.RunSummary {
	s := ExampleRounds()
	started := time.Date(2026, 9, 13, 17, 35, 12, 0, time.UTC)
	s.ID = "20260913-173512-deepseek-v4-1-flash-q4-k"
	s.StartedAt = started

	const streams = 4
	per := make([]tape.RoundSummary, 0, 2*len(s.PerRound))
	for _, p := range s.PerRound {
		p.SpecNMax = 3
		per = append(per, p)
	}
	for _, r := range []struct {
		name              string
		rate              float64
		drafted, accepted int
		tokens            int
		ttftMs            float64
	}{
		{"sql-1", 20.9, 290, 213, 271, 620},
		{"sql-2", 20.0, 310, 217, 279, 630},
		{"prose-1", 8.0, 1650, 87, 417, 660},
		{"prose-2", 9.2, 1500, 156, 456, 650},
		{"code-1", 16.8, 440, 233, 321, 640},
		{"chat-1", 15.4, 475, 234, 329, 640},
	} {
		drafted, accepted := r.drafted, r.accepted
		per = append(per, tape.RoundSummary{
			Index:                       len(per),
			SpecNMax:                    5,
			Name:                        r.name,
			Streams:                     streams,
			PerStreamPredictedPerSecond: r.rate,
			AggregatePredictedPerSecond: streams * r.rate,
			PredictedN:                  r.tokens,
			TTFTp50Ms:                   r.ttftMs,
			DraftN:                      &drafted,
			DraftNAccepted:              &accepted,
		})
	}
	s.PerRound = per
	s.Rounds = len(per)
	s.SpecNMax = []int{3, 5}

	// The spreads are the reduction's rule applied to the rows above, written
	// out. mid takes its arguments as float64 values, so the mean of the two
	// middle values is rounded exactly as the reduction rounds it, one
	// operation at a time.
	mid := func(a, b float64) float64 { return (a + b) / 2 }
	s.Spread = &tape.RoundSpread{
		PerStreamPredictedPerSecond: tape.Spread{Median: mid(15.4, 15.6), Min: 8.0, Max: 20.9},
		DraftAcceptRate:             tape.Spread{Median: mid(233.0/440, 209.0/360), Min: 87.0 / 1650, Max: 196.0 / 225},
	}
	s.BySpecNMax = []tape.SpecNMaxGroup{
		{NMax: 3, Rounds: 6, Spread: tape.RoundSpread{
			PerStreamPredictedPerSecond: tape.Spread{Median: mid(14.0, 15.6), Min: 9.1, Max: 19.3},
			DraftAcceptRate:             tape.Spread{Median: mid(209.0/360, 211.0/330), Min: 117.0 / 900, Max: 196.0 / 225},
		}},
		{NMax: 5, Rounds: 6, Spread: tape.RoundSpread{
			PerStreamPredictedPerSecond: tape.Spread{Median: mid(15.4, 16.8), Min: 8.0, Max: 20.9},
			DraftAcceptRate:             tape.Spread{Median: mid(234.0/475, 233.0/440), Min: 87.0 / 1650, Max: 213.0 / 290},
		}},
	}

	var rateSum, windowSec, wallMs float64
	var tokens, draftN, draftAccepted int
	for _, p := range per {
		window := float64(p.PredictedN) / p.AggregatePredictedPerSecond
		rateSum += p.PerStreamPredictedPerSecond
		windowSec += window
		wallMs += p.TTFTp50Ms + 1000*window
		tokens += p.PredictedN
		draftN += *p.DraftN
		draftAccepted += *p.DraftNAccepted
	}
	meanRate := rateSum / float64(len(per))
	wall := math.Round(wallMs)
	s.FinishedAt = started.Add(time.Duration(wall+330) * time.Millisecond)

	s.Timings.PredictedN = tokens / (streams * len(per))
	s.Timings.PredictedPerSecond = meanRate
	s.Timings.DraftN, s.Timings.DraftNAccepted = &draftN, &draftAccepted
	s.Timings.ClientPredictedPerSecond = 14.67
	// 6.72 GB read per pass × the mean rate, 14.74.
	s.Timings.EffectiveBandwidthBytesPerSec = 99064000000

	s.Aggregate.WallMs = wall
	s.Aggregate.TotalPromptN = len(per) * 1536
	s.Aggregate.TotalPredictedN = tokens
	s.Aggregate.AggregatePredictedPerSecond = float64(tokens) / windowSec
	s.Aggregate.PerStreamPredictedPerSecond = meanRate
	return s
}
