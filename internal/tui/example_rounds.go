package tui

import (
	"sort"
	"strings"
	"time"

	"github.com/midagedev/toktape/internal/card"
	"github.com/midagedev/toktape/internal/tape"
)

// The rounds example is a `record --prompts` run (TTP-31): the example rig
// answering three prompts one after another, four streams each, all in one
// tape. It exists so the screen, the replay and the clip can be checked at a
// round boundary (TTP-38) — the place where round 2's tokens used to pile onto
// round 1's tiles.
//
// The rounds are chosen to be told apart at a glance: a SQL prompt the rig
// decodes at 17 tok/s a stream, prose at 9 and code at 13, so a frame in the
// middle of round 2 shows rates nobody could mistake for round 1's. Answers
// are shorter than ExampleTapeN's (a 128-token cap rather than 320), which
// keeps the whole run near half a minute, and a round waits
// exampleRoundGap after the last token of the one before it.
const (
	exampleRoundMaxTokens = 128
	exampleRoundGap       = 1500 * time.Millisecond
)

// exampleRoundSpec is one line of the prompts file the example replays.
type exampleRoundSpec struct {
	name string
	rate float64 // tok/s per stream
	// answerFrom is the first answer paragraph the round's streams draw from,
	// and wordFrom how far into it they start. The example text has eight
	// paragraphs and the run needs twelve streams, so the third round reuses
	// the first four paragraphs from well past the words round 1 spoke.
	answerFrom, wordFrom int
}

var exampleRoundSpecs = []exampleRoundSpec{
	{name: "sql-1", rate: 17, answerFrom: 0, wordFrom: 0},
	{name: "prose-1", rate: 9, answerFrom: 4, wordFrom: 0},
	{name: "code-1", rate: 13, answerFrom: 0, wordFrom: 160},
}

// ExampleRoundsTape returns the multi-round example: len(exampleRoundSpecs)
// rounds of streams (2..8) concurrent requests each. rounds below 2 or above
// the three the example defines are clamped, so a caller always gets a tape
// that exercises the rounds path.
func ExampleRoundsTape(streams, rounds int) *tape.Tape {
	streams = clampInt(streams, 2, 8)
	rounds = clampInt(rounds, 2, len(exampleRoundSpecs))

	summary := *card.ExampleConcurrent()
	summary.Concurrency = streams
	summary.Server.NSlots = streams
	tp := &tape.Tape{Schema: tape.SchemaVersion, Summary: summary}

	var (
		roundStart time.Duration
		runEnd     time.Duration
		wall       time.Duration
		perRound   []tape.RoundSummary
	)
	for k := 0; k < rounds; k++ {
		spec := exampleRoundSpecs[k]
		itl := time.Duration(float64(time.Second) / spec.rate)
		var (
			roundEnd time.Duration
			rateSum  float64
			tokensN  int
			ttfts    []float64
		)
		for i := 0; i < streams; i++ {
			ttft := exampleTTFT(i)
			n := 96 + (i*7)%21
			// One stream of the first round thinks briefly before it answers,
			// so the badge a round leaves behind is something the next round
			// has to clear.
			reasoningN := 0
			if k == 0 && i == 1 {
				reasoningN = 24
			}
			words := exampleRoundWords(spec.answerFrom+i, spec.wordFrom, i, n, reasoningN)

			req := tape.RequestRecord{
				Index:     i,
				Round:     k,
				Slot:      i,
				StartedAt: roundStart,
				Prompt: tape.PromptRecord{
					Name:           spec.name,
					Messages:       []tape.Message{{Role: "user", Content: examplePrompt(spec.answerFrom + i)}},
					RenderedPrompt: exampleRendered(spec.answerFrom + i),
					MaxTokens:      exampleRoundMaxTokens,
					Params:         map[string]any{"max_tokens": exampleRoundMaxTokens},
					FinishReason:   "stop",
					ReasoningN:     reasoningN,
				},
				Cache: tape.CacheSummary{
					HitTokens:   examplePromptCache,
					PromptTotal: examplePromptTotal,
					HitRatio:    float64(examplePromptCache) / float64(examplePromptTotal),
					Label:       tape.CacheWarm,
				},
				Progress: exampleProgress(i),
			}
			at := ttft
			for j := 0; j < n; j++ {
				req.Tokens = append(req.Tokens, tape.TokenEvent{
					T:           at,
					Index:       j,
					Text:        words[j],
					Reasoning:   j < reasoningN,
					PredictedN:  j + 1,
					PredictedMs: msOf(at - ttft),
				})
				at += time.Duration(float64(itl) * itlJitter[(j+i+k)%len(itlJitter)])
			}
			last := req.Tokens[len(req.Tokens)-1].T
			if abs := roundStart + last; abs > roundEnd {
				roundEnd = abs
			}
			win := last - req.Tokens[0].T
			rate := float64(n-1) / win.Seconds()

			var answer, thinking strings.Builder
			for _, tk := range req.Tokens {
				if tk.Reasoning {
					thinking.WriteString(tk.Text)
				} else {
					answer.WriteString(tk.Text)
				}
			}
			req.Prompt.Completion = answer.String()
			req.Prompt.Reasoning = thinking.String()
			req.Timings = tape.TimingsSummary{
				PromptN:                  examplePromptTotal - examplePromptCache,
				CacheN:                   examplePromptCache,
				PromptMs:                 examplePrefillMs(),
				PromptPerSecond:          examplePrefillRate,
				PredictedN:               n,
				PredictedMs:              msOf(win),
				PredictedPerSecond:       rate,
				ReasoningN:               reasoningN,
				TTFTMs:                   msOf(ttft),
				ClientPredictedPerSecond: rate,
				ClientAgreesWithServer:   true,
				DecodeLabel:              "decode",
				ITLp50Ms:                 1000 / spec.rate,
				ITLp95Ms:                 1000 / spec.rate * 1.08,
				ITLp99Ms:                 1000 / spec.rate * 1.19,
			}
			tp.Requests = append(tp.Requests, req)
			rateSum += rate
			tokensN += n
			ttfts = append(ttfts, msOf(ttft))
		}
		perRound = append(perRound, tape.RoundSummary{
			Index:                       k,
			Name:                        spec.name,
			Streams:                     streams,
			PerStreamPredictedPerSecond: rateSum / float64(streams),
			AggregatePredictedPerSecond: rateSum,
			PredictedN:                  tokensN,
			TTFTp50Ms:                   percentile(ttfts, 0.5),
		})
		wall += roundEnd - roundStart
		runEnd = roundEnd
		roundStart = roundEnd + exampleRoundGap
	}

	var rateSum, predicted, ttftSum float64
	for _, req := range tp.Requests {
		rateSum += req.Timings.PredictedPerSecond
		predicted += float64(req.Timings.PredictedN)
		ttftSum += req.Timings.TTFTMs
	}
	all := float64(len(tp.Requests))
	mean := rateSum / all
	s := &tp.Summary
	s.Rounds = rounds
	s.PerRound = perRound
	s.Spread = &tape.RoundSpread{PerStreamPredictedPerSecond: roundRateSpread(perRound)}
	s.Timings.PredictedN = int(predicted / all)
	s.Timings.PredictedPerSecond = mean
	s.Timings.ClientPredictedPerSecond = mean
	s.Timings.TTFTMs = ttftSum / all
	s.Timings.EffectiveBandwidthBytesPerSec = int64(float64(s.Model.ActiveBytesPerToken) * mean)
	s.Cache = tp.Requests[0].Cache
	// Aggregate.WallMs is the sum of the rounds' own windows, never the gaps
	// between them (tape.RunSummary.Rounds).
	s.Aggregate.WallMs = msOf(wall)
	s.Aggregate.TotalPredictedN = int(predicted)
	s.Aggregate.TotalPromptN = len(tp.Requests) * (examplePromptTotal - examplePromptCache)
	s.Aggregate.Streams = streams
	s.Aggregate.SlotsBusyMax = streams
	s.Aggregate.PerStreamPredictedPerSecond = mean
	s.Aggregate.AggregatePredictedPerSecond = predicted / wall.Seconds()
	s.FinishedAt = s.StartedAt.Add(runEnd)

	tp.Samples = exampleSamples(tp, runEnd)
	// exampleSamples counts a stream busy until its last token, which for a
	// round that has not started yet is a slot the server never gave out.
	for i := range tp.Samples {
		tp.Samples[i].SlotsBusy = roundSlotsBusyAt(tp, tp.Samples[i].T)
	}
	s.Memory.MajFaultsTotal = 0
	s.Memory.MajFaultsDecode = 0
	s.Memory.MajFaultsPerToken = 0
	return tp
}

// exampleRoundWords is exampleWords with a starting word: the first reasoningN
// tokens come from stream i's thinking paragraph, the rest from answer
// paragraph a beginning at word from.
func exampleRoundWords(a, from, i, n, reasoningN int) []string {
	answer := exampleFields(exampleAnswers[a%len(exampleAnswers)])
	thinking := exampleFields(exampleThinking[i%len(exampleThinking)])
	out := make([]string, 0, n)
	for j := 0; j < n; j++ {
		fields, at := answer, from+j-reasoningN
		if j < reasoningN {
			fields, at = thinking, j
		}
		w := fields[at%len(fields)]
		if j == 0 || j == reasoningN {
			out = append(out, w)
			continue
		}
		out = append(out, " "+w)
	}
	return out
}

// roundSlotsBusyAt is how many streams have been sent and not yet finished at t.
func roundSlotsBusyAt(tp *tape.Tape, t time.Duration) int {
	n := 0
	for _, req := range tp.Requests {
		if len(req.Tokens) == 0 || req.StartedAt > t {
			continue
		}
		if req.StartedAt+req.Tokens[len(req.Tokens)-1].T >= t {
			n++
		}
	}
	return n
}

// roundRateSpread is the median and range of the rounds' per-stream rates, by
// the reduction's rule: an even count takes the mean of the middle two.
func roundRateSpread(rounds []tape.RoundSummary) tape.Spread {
	vals := make([]float64, len(rounds))
	for i, r := range rounds {
		vals[i] = r.PerStreamPredictedPerSecond
	}
	sort.Float64s(vals)
	if len(vals) == 0 {
		return tape.Spread{}
	}
	med := vals[len(vals)/2]
	if len(vals)%2 == 0 {
		med = (vals[len(vals)/2-1] + vals[len(vals)/2]) / 2
	}
	return tape.Spread{Median: med, Min: vals[0], Max: vals[len(vals)-1]}
}

// ExampleRoundStart is when round k of tp was sent: the earliest StartedAt of
// its requests, or -1 when the tape has no such round. The tests, tuidump and
// the self-check captures use it to frame an instant inside a given round.
func ExampleRoundStart(tp *tape.Tape, k int) time.Duration {
	start := time.Duration(-1)
	for _, req := range tp.Requests {
		if req.Round == k && (start < 0 || req.StartedAt < start) {
			start = req.StartedAt
		}
	}
	return start
}
