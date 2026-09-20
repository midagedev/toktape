package card

import (
	"math"
	"time"

	"github.com/midagedev/toktape/internal/tape"
)

// The prefill sweep example is the run TTP-64 and TTP-66 exist for: one stream,
// a prompts file of four lines, and the question "how fast does this box
// prefill at 4k, 16k and 32k tokens, and what does the prefix cache save when
// the 16k prompt comes back".
//
// The rig is ExampleSpeculative()'s — the MoE with every expert tensor in host
// RAM on the eight-channel Threadripper — without the draft model and at one
// slot of 65536 tokens, because that is the shape of box whose prefill rig-log
// measured on ws (2026-09-14): 60 tok/s on the serving profile to 130 on a
// coding profile, falling as the prompt grows. The KV cache is the same 2.4 GiB
// it was at four slots of 16384: 65536 tokens either way. GPU0 no longer holds
// the draft model's 0.6 GiB.
//
// The derivation, so a reviewer can check the arithmetic rather than trust it.
// Every round generates 256 tokens; "prompt" is the whole rendered prompt,
// "eval" the tokens the server evaluated (timings.prompt_n) and "cached" the
// tokens it took from its prefix cache (timings.cache_n).
//
//	round       prompt  cached   eval   prompt_ms   prefill      decode   TTFT
//	diff          4103       0   4103     31 900   128.6 tok/s   19.6   31 960 ms
//	repo         16391       0  16391    157 600   104.0 tok/s   17.9  157 660 ms
//	monorepo     32776       0  32776    461 600    71.0 tok/s   15.8  461 660 ms
//	repo-again   16391   15875    516     10 200    50.6 tok/s   17.9   10 260 ms
//
// The first three lines are different documents, so nothing is shared and the
// server evaluates every token; prefill falls with length because attention
// over a longer context costs more per token. The fourth line is the second
// line again. rig-log's identical repeat of an 11k prefix evaluated 516 tokens
// — a server re-evaluates at least the tail of a prompt it has cached — and
// its 260-token extension evaluated 775 tokens in 15 s, 52 tok/s, at that
// depth. So the repeat evaluates 516 tokens at 50.6 tok/s and the first token
// lands in 10.3 s where the same prompt cold took 157.7 s. rig-log also saw a
// server's first request run slower than the rest; this fixture's first round
// does not, so the fall across the first three is length alone.
//
// decode is tok/s over the round's 256 tokens, and falls with the depth it
// decodes at: the repeat decodes at round 2's depth and at round 2's rate. A
// round's wall is its TTFT plus 256 / decode.
//
//	median     the four decode rates sorted are 15.8 17.9 17.9 19.6; the even
//	           count takes the mean of the middle two, 17.9 tok/s.
//	Timings    the mean over the four streams, as the recorder reduces it:
//	           prompt_n 53786 / 4 = 13447 (rounded), cache_n 15875 / 4 = 3969,
//	           prompt_per_second the mean of the four rates, 88.6 tok/s — the
//	           figure the Prefill row prints, and the one the Prompts row's
//	           prefill line exists to take apart.
//	cache      3969 of 17416 is 23 %, under tape.CachedHitRatio: the run is
//	           "warm" although one round in four was 97 % cached.
//	aggregate  1024 tokens over the four decode windows, 57.87 s, 17.7 tok/s;
//	           prefill 53786 tokens over 661.3 s of prompt_ms, 81.3 tok/s.
//	wall       the rounds' own walls summed, 719 407 ms; FinishedAt adds the
//	           150 ms the recorder spent between rounds.

// ExamplePrefillSweep returns a realistic single-stream summary of a
// four-prompt run that measures prefill by prompt length and the prefix cache.
//
// It is the only fixture whose rounds carry PromptN, PromptPerSecond and
// CacheN, and so the only golden carrying the Prompts row's prefill and cache
// lines.
func ExamplePrefillSweep() *tape.RunSummary {
	s := ExampleSpeculative()
	started := time.Date(2026, 9, 14, 9, 2, 41, 0, time.UTC)
	s.ID = "20260914-090241-deepseek-v4-1-flash-q4-k"
	s.StartedAt = started

	s.Server.Args = []string{
		"/usr/local/bin/llama-server",
		"-m", "/models/DeepSeek-V4.1-Flash-Q4_K_M.gguf",
		"-c", "65536",
		"--parallel", "1",
		"-ngl", "99",
		"-ncmoe", "48",
		"-fa", "on",
		"-b", "2048",
		"-ub", "512",
		"-ctk", "q8_0",
		"-ctv", "q8_0",
		"-t", "16",
	}
	s.Server.Flags.DraftModel, s.Server.Flags.DraftMax, s.Server.Flags.DraftMin = "", "", ""
	s.Server.NSlots = 1
	s.Server.CtxSize = 65536
	s.Concurrency = 1
	// A run of several prompts has no one rendered prompt to fingerprint.
	s.Template.RenderedPromptSHA256 = ""
	s.Template.RenderedPromptTokens = 0
	// The draft model's 0.6 GiB is gone from GPU0.
	s.GPUsAtEnd[0].UsedBytes -= 644245094
	s.GPUsAtEnd[0].ProcBytes -= 644245094
	s.Contention.LoadAvg1 = 4.1

	rounds := []struct {
		name             string
		promptN, cacheN  int
		promptMs, decode float64
	}{
		{"diff", 4103, 0, 31900, 19.6},
		{"repo", 16391, 0, 157600, 17.9},
		{"monorepo", 32776, 0, 461600, 15.8},
		{"repo-again", 516, 15875, 10200, 17.9},
	}
	const tokens = 256
	// The gap between the end of the prompt evaluation and the first token
	// arriving: one decode step and the SSE frame, the same on every round.
	const firstTokenMs = 60.0

	s.PerRound = make([]tape.RoundSummary, len(rounds))
	var (
		t                                    tape.TimingsSummary
		promptN, cacheN, promptToks          int
		promptMs, predictedMs, windowSec     float64
		wallMs, ttftSum, prefillSum, rateSum float64
		clientPrefillSum                     float64
	)
	ttfts := make([]float64, len(rounds))
	for k, r := range rounds {
		prefill := float64(r.promptN) / (r.promptMs / 1000)
		ttft := r.promptMs + firstTokenMs
		window := tokens / r.decode
		s.PerRound[k] = tape.RoundSummary{
			Index:                       k,
			Name:                        r.name,
			Streams:                     1,
			PerStreamPredictedPerSecond: r.decode,
			AggregatePredictedPerSecond: r.decode,
			PredictedN:                  tokens,
			TTFTp50Ms:                   ttft,
			PromptN:                     r.promptN,
			PromptPerSecond:             prefill,
			CacheN:                      r.cacheN,
		}
		promptN += r.promptN
		cacheN += r.cacheN
		promptToks += r.promptN
		promptMs += r.promptMs
		predictedMs += 1000 * window
		windowSec += window
		wallMs += ttft + 1000*window
		ttftSum += ttft
		prefillSum += prefill
		rateSum += r.decode
		// server.reduce's client prefill: prompt_n over the client's own
		// send-to-first-token window.
		clientPrefillSum += float64(r.promptN) / (ttft / 1000)
		ttfts[k] = ttft
	}
	n := float64(len(rounds))
	wall := math.Round(wallMs)
	s.FinishedAt = started.Add(time.Duration(wall+150) * time.Millisecond)

	s.Rounds = len(rounds)
	s.Spread = &tape.RoundSpread{
		PerStreamPredictedPerSecond: tape.Spread{Median: (17.9 + 17.9) / 2, Min: 15.8, Max: 19.6},
	}

	meanRate := rateSum / n
	t.PromptN = int(float64(promptN)/n + 0.5)
	t.CacheN = int(float64(cacheN)/n + 0.5)
	t.PromptMs = promptMs / n
	t.PromptPerSecond = prefillSum / n
	t.PredictedN = tokens
	t.PredictedMs = predictedMs / n
	t.PredictedPerSecond = meanRate
	t.TTFTMs = ttftSum / n
	t.ClientPromptPerSecond = clientPrefillSum / n
	t.ClientPredictedPerSecond = meanRate * 0.995
	t.ClientAgreesWithServer = true
	t.DecodeLabel = "decode"
	t.ITLp50Ms = 1000 / meanRate
	t.ITLp95Ms = 1000 / 15.8 * 1.08
	t.ITLp99Ms = 1000 / 15.8 * 1.31
	t.EffectiveBandwidthBytesPerSec = int64(float64(s.Model.ActiveBytesPerToken)*meanRate + 0.5)
	s.Timings = t

	// Nearest-rank percentiles of the four TTFTs: p50 is the second, p95 the
	// fourth.
	sorted := append([]float64(nil), ttfts...)
	for i := 1; i < len(sorted); i++ {
		for j := i; j > 0 && sorted[j] < sorted[j-1]; j-- {
			sorted[j], sorted[j-1] = sorted[j-1], sorted[j]
		}
	}
	s.Aggregate = tape.AggregateTimings{
		Streams:                     1,
		WallMs:                      wall,
		TotalPromptN:                promptToks,
		TotalPredictedN:             tokens * len(rounds),
		AggregatePredictedPerSecond: float64(tokens*len(rounds)) / windowSec,
		AggregatePromptPerSecond:    float64(promptToks) / (promptMs / 1000),
		PerStreamPredictedPerSecond: meanRate,
		TTFTp50Ms:                   sorted[1],
		TTFTp95Ms:                   sorted[3],
		SlotsBusyMax:                1,
	}
	total := t.CacheN + t.PromptN
	s.Cache = tape.CacheSummary{
		HitTokens:   t.CacheN,
		PromptTotal: total,
		HitRatio:    float64(t.CacheN) / float64(total),
		Label:       tape.CacheWarm,
	}
	return s
}
