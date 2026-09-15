package card

import (
	"strconv"

	"github.com/midagedev/toktape/internal/tape"
)

// PrefillLabel names the run's prefill rate with the prompt length it was
// measured over, in the vocabulary engine benchmarks already print: llama-bench
// puts the length in the metric's own name ("pp 512"), community tables print
// "pp2048 @ d32768", and batched-bench's S_PP carries PP and B as row columns
// (docs/research, 2026-09-15). A prefill rate with no length is a rate over
// whatever prompt the run happened to send — on an offloaded box each call
// costs a fixed transfer over the bus, so the hero's "51.8 tok/s" describes a
// 279-token prompt, not the machine.
//
// The rate is the one the stream count makes authoritative: the aggregate
// above one stream (what the box put through), the stream's own server figure
// below it — the same choice the Prefill row makes. The count is
// Timings.PromptN, the tokens the server evaluated, cached prefix excluded,
// which is the denominator the rate was computed over; LlamaBenchTable's pp
// row prints the same count for the same reason, so the label and the table
// it is quotable beside agree. "short prompt" rides along through ShortPrompt,
// the predicate the Prefill row and the share image also ask: one question,
// one owner, however many renderings.
func PrefillLabel(s *tape.RunSummary) string {
	if s == nil {
		s = &tape.RunSummary{}
	}
	n := streamsSent(s)
	label := "pp" + formatInt(s.Timings.PromptN)
	if n > 1 {
		label += " × " + strconv.Itoa(n)
	}
	rate := s.Timings.PromptPerSecond
	if n > 1 {
		rate = s.Aggregate.AggregatePromptPerSecond
	}
	label += " · " + formatRateUnit(rate)
	if ShortPrompt(s) {
		label += " · short prompt"
	}
	return label
}

// TTFTMs is the reading this package calls the run's time to first token: the
// aggregate's p50 above one stream, the stream's own TTFT below it.
//
// It is exported beside TTFTPercentiles so a caller that draws the figure
// itself — the result modal draws it three rows tall, not as a string — asks
// for the number rather than reaching into the formatter TTFTPercentiles calls.
// One owner of "which reading is the headline", whether the caller wants it
// rendered or raw.
func TTFTMs(s *tape.RunSummary) float64 {
	if s == nil {
		return 0
	}
	if streamsSent(s) <= 1 {
		return s.Timings.TTFTMs
	}
	return s.Aggregate.TTFTp50Ms
}

// TTFTPercentiles renders the run's time to first token through the caller's
// formatter: the p50 always, and above one stream the p95 beside it, where the
// aggregate measured both. A single stream has no percentiles to print — its
// one TTFT is the whole distribution.
//
// pair reports whether the two renderings are two facts worth printing, by
// formatGHzPair's rule (conditions.go): a line that shows two identical
// numbers reads as a bug in the card rather than a fact about the run, and
// neither is a "?" — an unmeasured percentile is an absence, not a reading
// that agrees with the other one. When pair is false the caller says "first
// token" and drops the p95 rather than printing a spread the run did not have.
func TTFTPercentiles(s *tape.RunSummary, render func(ms float64) string) (p50, p95 string, pair bool) {
	if s == nil {
		s = &tape.RunSummary{}
	}
	if streamsSent(s) <= 1 {
		return render(TTFTMs(s)), "", false
	}
	p50, p95 = render(TTFTMs(s)), render(s.Aggregate.TTFTp95Ms)
	pair = p50 != p95 && p50 != unknown && p95 != unknown
	return p50, p95, pair
}
