package card

import (
	"time"

	"github.com/midagedev/toktape/internal/tape"
)

// The rounds example is the run TTP-31 exists for: the speculative rig of
// ExampleSpeculative() measured on six prompts instead of one, because the
// same DSpark draft is accepted 13 % of the time on prose and 87 % on SQL and a
// card from either prompt alone would settle the wrong argument.
//
// The derivation, so a reviewer can check the arithmetic rather than trust it.
// Every round is four streams of the same prompt at --draft-max 3; "passes" is
// the verification passes of the target over the round's four streams, each
// drafting three tokens, and the tokens a round emits are its passes plus the
// drafts the target accepted.
//
//	round    rate    passes  drafted  accepted       tokens  window   TTFT
//	sql-1    19.3      75      225    196 (87.1 %)     271    3.51 s  620 ms
//	sql-2    18.4      80      240    199 (82.9 %)     279    3.79 s  630 ms
//	prose-1   9.1     300      900    117 (13.0 %)     417   11.46 s  660 ms
//	prose-2  10.2     280      840    176 (21.0 %)     456   11.18 s  650 ms
//	code-1   15.6     110      330    211 (63.9 %)     321    5.14 s  640 ms
//	chat-1   14.0     120      360    209 (58.1 %)     329    5.88 s  640 ms
//
// rate is tok/s per stream, and a round's aggregate is four streams at that
// rate; its decode window is tokens / aggregate and its wall is TTFT plus the
// window. Prose drafts badly, runs more passes and writes longer answers, so
// its rounds are both the slowest and the longest.
//
//	median     the six rates sorted are 9.1 10.2 14.0 15.6 18.4 19.3; the even
//	           count takes the mean of the middle two, (14.0 + 15.6) / 2 = 14.8.
//	           Acceptance sorted is 13.0 21.0 58.1 63.9 82.9 87.1 %, median 61 %.
//	mean       Timings is the mean over all 24 streams, 86.6 / 6 = 14.43 tok/s —
//	           not the median, which is why the card prints both.
//	aggregate  2073 tokens over the rounds' summed decode windows, 40.95 s,
//	           = 50.6 tok/s. It is not 4 × 14.43 = 57.7: the slow prose rounds
//	           last three times as long as the SQL ones and weigh accordingly.
//	wall       the rounds' own walls summed, 44 793 ms; FinishedAt adds the
//	           150 ms the recorder spent between rounds, which no figure counts.
//	draft      2895 drafted and 1108 accepted over the run, pooled: 38 %.
//	tokens     2073 / 24 streams = 86 per stream.

// ExampleRounds returns a realistic six-round, four-stream summary of a
// multi-prompt run with speculative decoding.
//
// It is what the Prompts row of the text card, the rounds clause of the PNG
// hero and compare's "tok/s median" row are drawn from, and the goldens under
// testdata pin all three.
func ExampleRounds() *tape.RunSummary {
	s := ExampleSpeculative()
	started := time.Date(2026, 9, 13, 17, 10, 3, 0, time.UTC)
	s.ID = "20260913-171003-deepseek-v4-1-flash-q4-k"
	s.StartedAt = started
	s.FinishedAt = started.Add(44943 * time.Millisecond)

	rounds := []struct {
		name              string
		rate              float64
		drafted, accepted int
		tokens            int
		ttftMs            float64
	}{
		{"sql-1", 19.3, 225, 196, 271, 620},
		{"sql-2", 18.4, 240, 199, 279, 630},
		{"prose-1", 9.1, 900, 117, 417, 660},
		{"prose-2", 10.2, 840, 176, 456, 650},
		{"code-1", 15.6, 330, 211, 321, 640},
		{"chat-1", 14.0, 360, 209, 329, 640},
	}
	const streams = 4
	s.PerRound = make([]tape.RoundSummary, len(rounds))
	for k, r := range rounds {
		drafted, accepted := r.drafted, r.accepted
		s.PerRound[k] = tape.RoundSummary{
			Index:                       k,
			Name:                        r.name,
			Streams:                     streams,
			PerStreamPredictedPerSecond: r.rate,
			AggregatePredictedPerSecond: streams * r.rate,
			PredictedN:                  r.tokens,
			TTFTp50Ms:                   r.ttftMs,
			DraftN:                      &drafted,
			DraftNAccepted:              &accepted,
		}
	}
	s.Rounds = len(rounds)
	// The spread is the reduction's rule applied to the rows above, written
	// out: the median of an even count is the mean of the two middle values.
	s.Spread = &tape.RoundSpread{
		PerStreamPredictedPerSecond: tape.Spread{Median: (14.0 + 15.6) / 2, Min: 9.1, Max: 19.3},
		DraftAcceptRate: tape.Spread{
			Median: (209.0/360 + 211.0/330) / 2,
			Min:    117.0 / 900,
			Max:    196.0 / 225,
		},
	}

	draftN, draftAccepted := 2895, 1108
	meanRate := (19.3 + 18.4 + 9.1 + 10.2 + 15.6 + 14.0) / 6
	s.Timings = tape.TimingsSummary{
		PromptN:                  384,
		CacheN:                   128,
		PromptMs:                 470.0,
		PromptPerSecond:          817.0,
		PredictedN:               86,
		PredictedMs:              6825.5,
		PredictedPerSecond:       meanRate,
		DraftN:                   &draftN,
		DraftNAccepted:           &draftAccepted,
		TTFTMs:                   640,
		ClientPromptPerSecond:    809.0,
		ClientPredictedPerSecond: 14.36,
		ClientAgreesWithServer:   true,
		DecodeLabel:              "decode",
		ITLp50Ms:                 69.3,
		ITLp95Ms:                 76.5,
		ITLp99Ms:                 93.5,
		// 6.72 GB read per pass × the mean rate.
		EffectiveBandwidthBytesPerSec: 96992000000,
	}
	s.Aggregate = tape.AggregateTimings{
		Streams:                     streams,
		WallMs:                      44793,
		TotalPromptN:                6 * 1536,
		TotalPredictedN:             2073,
		AggregatePredictedPerSecond: 2073 / 40.95286887737576,
		AggregatePromptPerSecond:    2130.0,
		PerStreamPredictedPerSecond: meanRate,
		TTFTp50Ms:                   640,
		TTFTp95Ms:                   720,
		SlotsBusyMax:                4,
	}
	return s
}
