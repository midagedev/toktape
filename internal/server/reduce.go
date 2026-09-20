package server

import (
	"math"
	"sort"
	"time"

	"github.com/midagedev/toktape/internal/tape"
)

// CachedHitRatio lived here from TTP-88 until 2026-09-20, when it moved to
// internal/tape beside the label it interprets: the card reads it too, and
// this package is not worth 390 KB of prompt text in the browser player that
// imports the card. tape.CachedHitRatio is the one reading.

// Reduce computes the client-side cross-check of one stream and returns the
// complete tape.TimingsSummary: the server figures from rec.Timings unchanged
// (they are the record) plus TTFT, the client rates, the agreement flag, the
// decode label, the inter-token latency percentiles and the effective
// bandwidth. It is pure and idempotent.
//
// The rules each come from a measured mistake (docs/research/00-handover-brief.md,
// lesson 1):
//
//   - TTFT is the arrival of the first token that carried text. The role-only
//     chunk arrives earlier and is not it.
//   - The client decode rate is measured over the decode window: the n-1 gaps
//     between the first and the last token that carried text. Neither the
//     finish chunk nor wall time that keeps running after generation stopped
//     may enter the window.
//   - Counting n tokens over that window instead of n-1 inflates the rate by
//     n/(n-1), which is outside RateTolerance for any realistic stream.
//
// "Token" here means every token the server counted in predicted_n, a thinking
// model's reasoning_content tokens included: they are decode steps taken at
// the same speed, and excluding them is what made a run whose whole budget
// went to thinking report TTFT 0 and no client rate at all (TTP-20,
// 2026-09-13). rec.Tokens already holds both kinds, so this function needs no
// case for them; tape.TokenEvent.Reasoning says which is which.
//
// sentAt is the instant the request was sent. tape.TokenEvent.T is by schema
// already relative to it, so Reduce does no arithmetic with sentAt; it is part
// of the signature so the caller keeps the send instant explicit and so a
// future record that stores absolute token times can be reduced here too.
//
// activeBytesPerToken is tape.ModelInfo.ActiveBytesPerToken, or 0 when the
// model shape is unknown, in which case EffectiveBandwidthBytesPerSec stays 0.
func Reduce(rec *tape.RequestRecord, sentAt time.Time, activeBytesPerToken int64) tape.TimingsSummary {
	_ = sentAt
	out := rec.Timings
	out.ReasoningN = 0
	out.TTFTMs = 0
	out.ClientPromptPerSecond = 0
	out.ClientPredictedPerSecond = 0
	out.ClientAgreesWithServer = false
	out.ITLp50Ms, out.ITLp95Ms, out.ITLp99Ms = 0, 0, 0
	out.EffectiveBandwidthBytesPerSec = 0

	toks := rec.Tokens
	// How many of the generated tokens were thinking. Counted from the tokens
	// rather than copied from rec.Prompt.ReasoningN so that Reduce stays a
	// function of the timeline it measures: a caller that assembles a record
	// by hand gets a figure that matches the tokens it supplied.
	for _, tk := range toks {
		if tk.Reasoning {
			out.ReasoningN++
		}
	}
	if len(toks) > 0 {
		out.TTFTMs = msOf(toks[0].T)
	}
	if len(toks) >= 2 {
		window := toks[len(toks)-1].T - toks[0].T
		if window > 0 {
			out.ClientPredictedPerSecond = float64(len(toks)-1) / window.Seconds()
		}
		gaps := make([]float64, 0, len(toks)-1)
		for i := 1; i < len(toks); i++ {
			gaps = append(gaps, msOf(toks[i].T-toks[i-1].T))
		}
		sort.Float64s(gaps)
		out.ITLp50Ms = percentile(gaps, 0.50)
		out.ITLp95Ms = percentile(gaps, 0.95)
		out.ITLp99Ms = percentile(gaps, 0.99)
	}
	// The client's view of prefill is TTFT: it also contains queueing and the
	// first decode step, so it is a lower bound on the prompt rate and only a
	// sanity check against the server's prompt_per_second.
	if out.PromptN > 0 && out.TTFTMs > 0 {
		out.ClientPromptPerSecond = float64(out.PromptN) / (out.TTFTMs / 1000)
	}
	if out.PredictedPerSecond > 0 && out.ClientPredictedPerSecond > 0 {
		out.ClientAgreesWithServer = RatesAgree(out.ClientPredictedPerSecond, out.PredictedPerSecond)
	}

	// A client-timed stream (TTP-99): the server reported no timings, so the
	// recorder's clock is the record and Source says so. The server path
	// below is never entered for it — not the label, not the silent client
	// fallback — because neither was measured against a server figure.
	//
	// The token count is the final chunk's usage.completion_tokens
	// (PredictedNSource "usage"), recalibrated over the observed window:
	// the usage count, not the chunk count, over last-minus-first chunk.
	// Without a usage figure (PredictedNSource "chunks") the chunk count is
	// kept and no rate is derived at all — chunks are not tokens (lesson 1).
	// TTFT and the ITL percentiles are still measured: they are per-chunk
	// latencies and TTFT, honestly labelled. There is nothing to agree with,
	// so ClientAgreesWithServer stays false.
	if out.Source == "client" {
		return reduceClientTimed(out, toks, activeBytesPerToken)
	}

	// Label from the server's token count, falling back to what we saw, so a
	// long stream from a build without timings is not mislabelled "sample".
	n := out.PredictedN
	if n == 0 {
		n = len(toks)
	}
	out.DecodeLabel = "sample"
	if n >= tape.MinDecodeTokens {
		out.DecodeLabel = "decode"
	}

	rate := out.PredictedPerSecond // server figure is the record
	if rate == 0 {
		rate = out.ClientPredictedPerSecond
	}
	if activeBytesPerToken > 0 && rate > 0 {
		out.EffectiveBandwidthBytesPerSec = int64(float64(activeBytesPerToken) * rate)
	}
	return out
}

// reduceClientTimed completes a client-timed summary: out already carries
// TTFT, the per-chunk ITL percentiles, ReasoningN and the client prompt
// figure from Reduce's shared head, plus the PredictedN/PredictedNSource
// finish() set. It sets the label, the recalibrated rates and the bandwidth,
// and nothing else.
func reduceClientTimed(out tape.TimingsSummary, toks []tape.TokenEvent, activeBytesPerToken int64) tape.TimingsSummary {
	out.PromptPerSecond = 0 // TTFT is not a prefill measurement
	out.ClientAgreesWithServer = false
	out.PredictedPerSecond = 0
	out.ClientPredictedPerSecond = 0
	out.EffectiveBandwidthBytesPerSec = 0
	if out.PredictedNSource == "usage" && out.PredictedN > 0 {
		out.DecodeLabel = "sample"
		if out.PredictedN >= tape.MinDecodeTokens {
			out.DecodeLabel = "decode"
		}
		if len(toks) >= 2 {
			if window := toks[len(toks)-1].T - toks[0].T; window > 0 && out.PredictedN > 1 {
				rate := float64(out.PredictedN-1) / window.Seconds()
				out.ClientPredictedPerSecond = rate
				out.PredictedPerSecond = rate
			}
		}
	} else {
		// No usage figure: the chunk count is not a token count.
		out.PredictedN = len(toks)
		out.PredictedNSource = "chunks"
		out.DecodeLabel = "sample"
	}
	if out.PredictedPerSecond > 0 && activeBytesPerToken > 0 {
		out.EffectiveBandwidthBytesPerSec = int64(float64(activeBytesPerToken) * out.PredictedPerSecond)
	}
	return out
}

// RatesAgree reports whether a client-side rate is within tape.RateTolerance
// of the server's. A zero or negative server rate never agrees: there is no
// figure to agree with, and claiming agreement would hide that.
func RatesAgree(client, server float64) bool {
	if server <= 0 || client <= 0 {
		return false
	}
	return math.Abs(client-server)/server <= tape.RateTolerance
}

// CacheVerdict labels the run cold, warm or cached.
//
// Cold wins over everything: major faults during decode mean weights were
// being paged in from disk while generating, which is the finding the card
// exists to show (tape.ColdMajFaultsPerToken). Only then does the prefix-cache
// hit ratio decide between cached and warm.
//
// majFaultsDecode is the number of major faults between the first and the last
// token, from the process recorder; pass 0 when there is no /proc view, which
// yields warm or cached but never a cold claim that was not measured.
func CacheVerdict(t tape.TimingsSummary, majFaultsDecode uint64, predictedN int) tape.CacheSummary {
	out := tape.CacheSummary{
		HitTokens:   t.CacheN,
		PromptTotal: t.CacheN + t.PromptN,
	}
	if out.PromptTotal > 0 {
		out.HitRatio = float64(out.HitTokens) / float64(out.PromptTotal)
	}
	switch {
	case predictedN > 0 && float64(majFaultsDecode)/float64(predictedN) >= tape.ColdMajFaultsPerToken:
		out.Label = tape.CacheCold
	case out.PromptTotal > 0 && out.HitRatio >= tape.CachedHitRatio:
		out.Label = tape.CacheCached
	default:
		out.Label = tape.CacheWarm
	}
	return out
}

// percentile returns the nearest-rank percentile of an ascending slice.
// p is a fraction in (0, 1]. An empty slice gives 0.
func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	rank := int(math.Ceil(p * float64(len(sorted))))
	if rank < 1 {
		rank = 1
	}
	if rank > len(sorted) {
		rank = len(sorted)
	}
	return sorted[rank-1]
}

func msOf(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }
