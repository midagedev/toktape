package card

import (
	"fmt"
	"sort"
	"strings"

	"github.com/midagedev/toktape/internal/bandwidth"
	"github.com/midagedev/toktape/internal/server"
	"github.com/midagedev/toktape/internal/tape"
)

// A card exists to qualify a number, and this file is where the qualifying is
// decided (TTP-74, 2026-09-14, the agent-ergonomics thread).
//
// The card has always carried the qualifications: "Sample" instead of "Decode"
// under 32 generated tokens, "contended: yes", "conditions changed", "cold", a
// prefix-cache hit of 0 %. A human reads them because they sit next to the
// figure. An agent does not: it runs `-o json`, pulls predicted_per_second, and
// reports it as the machine's speed. Every qualification on the card is then a
// field it did not think to look at, and the number it quotes is one the card
// was trying to argue with.
//
// So the qualifications are a list with stable codes, the same list the text
// card prints and `-o json` carries, derived in one place. Two properties make
// it worth the file:
//
//  1. Derived, not stored. Every code below is computable from what the tape
//     already records, so a tape recorded before this existed gets the same
//     treatment when it is re-rendered and the schema grows no field per
//     caveat. The one exception is RunSummary.Warnings, the free text the
//     recorder itself wrote; those are carried through under CodeRecorded so a
//     reader can tell "the recorder observed this" from "the card worked this
//     out", and their text is never rewritten.
//  2. One predicate per qualification. isSample and shortPrompt below are used
//     by the row label, by the PNG's eyebrow AND by the code. Before this
//     file, "is this a sample" was read off the recorder's stored verdict in
//     one renderer and could have been recomputed in another; a figure whose
//     label and whose warning can disagree is the defect this list exists to
//     close, not a second instance of it.
type Caveat struct {
	// Code is stable and greppable. A consumer branches on this, never on Text.
	Code string `json:"code"`
	// Severity says what the caveat costs the reader, in three levels — see
	// the Severity constants. It is the field that answers "can I quote the
	// headline number" without reading any sentence.
	Severity string `json:"severity"`
	// Text is the sentence the card prints. It names the figures it is about,
	// because a caveat a reader cannot check is one they will ignore.
	Text string `json:"text"`
}

// Caveat codes. Extend the list rather than re-spelling one: a consumer that
// branched on a code must keep working against a newer toktape.
const (
	// CodeStreamsFailed: some of the run's streams never produced a figure.
	CodeStreamsFailed = "streams_failed"
	// CodeStreamsNotConcurrent: the run sent several streams at once, every
	// one answered and none too short for a decode window, and the token
	// timeline still never had all of them decoding at one instant
	// (Aggregate.PeakDecodingStreams, lead, 2026-09-15). A 2-session
	// ExLlamaV3 take did exactly that — slots busy 2, one stream decoding at
	// a time — so the Streams row's "N × per-stream = aggregate" reads as N
	// concurrent sessions when the aggregate is one stream behind a queue.
	CodeStreamsNotConcurrent = "streams_not_concurrent"
	// CodeRaggedAggregate: the aggregate decode rate sits materially below
	// per-stream × streams, so the streams did not all decode across the
	// same window and the aggregate is not the sum (TTP-108, 2026-09-19).
	// The Streams block already said "not all decoding at once" for this;
	// this is the fuller version of that clause, on the caveat lines, with
	// the arithmetic named — a reader who multiplies the "each" figure by
	// the stream count gets a number the card printed nowhere, and no
	// sentence explained the one it did.
	//
	// Re-authored 2026-09-19 with the window figure (TTP-138), the same
	// change that made the Decode row lead with it: the sentence now says
	// what actually happened — the run had a tail in which fewer streams
	// were running — and, on a tape that carries the window, how much of
	// the wall that tail was. The predicate and the rank are untouched;
	// only the sentence learned to name the mechanism.
	CodeRaggedAggregate = "ragged_aggregate"
	// CodePlacementContradicted: the placement ESTIMATE puts weights on a GPU
	// the run's own reading says held (almost) nothing (lead, 2026-09-15).
	// The estimate cannot see CUDA_VISIBLE_DEVICES, so a one-card run on a
	// two-card box got a split across both and every figure derived from it —
	// the bandwidth ceiling, the "of peak" ratio — was measured against a
	// machine this run was not. The measured figures stand; the derived ones
	// print "?".
	CodePlacementContradicted = "placement_contradicted"
	// CodeBandwidthOverCeiling: a bandwidth figure — the host-bus view, the
	// verify step, or the one figure over every device — exceeded the bus or
	// ceiling that must have carried it by more than 5 % (2026-09-16), so the
	// card prints no bandwidth clause at all and this names both figures. A
	// figure over its ceiling is not a reading of anything: the qwen38 tapes
	// printed "≈ 1416 GB/s from RAM, 978% of peak" on a host bus that measures
	// 144.8 GB/s, because a 26.8 GiB embedding table read by row was counted
	// as streamed per-token weight traffic. The Decode row keeps its rate.
	CodeBandwidthOverCeiling = "bandwidth_over_ceiling"
	// CodeAnswerCut: the whole generation budget went to reasoning tokens and
	// no answer was produced.
	CodeAnswerCut = "answer_cut"
	// CodeShortGeneration: fewer than tape.MinDecodeTokens generated tokens,
	// so the decode figure is a sample and not a rate (lesson 2).
	CodeShortGeneration = "short_generation"
	// CodeColdCache: weights were being paged in from disk during decode, so
	// the decode rate is partly a measurement of the disk (lesson 3's cousin).
	CodeColdCache = "cold_cache"
	// CodeShortPromptForPrefill: the prompt was too short to be a prefill
	// measurement (TTP-65).
	CodeShortPromptForPrefill = "short_prompt_for_prefill"
	// CodeCachedPrefill: the prompt was substantially served from the prefix
	// cache, so the prefill rate describes a cache hit rather than an
	// evaluation (TTP-88, 2026-09-19). A second run of the same prompt
	// reports a flattering prefill figure with the Prefix cache row naming
	// the share on its own unlinked row; this is the sentence that connects
	// the two.
	CodeCachedPrefill = "cached_prefill"
	// CodeClientDisagrees: the client-side rate and the server's own differ by
	// more than tape.RateTolerance (lesson 1).
	CodeClientDisagrees = "client_disagrees_with_server"
	// CodeClientTimed: the server reported no timings, so every rate here is
	// the recorder's clock over the stream — not the engine's own count
	// (TTP-99, 2026-09-19). Severity run: the figures are what they say, but
	// a client-timed run compares only with other client-timed runs.
	CodeClientTimed = "client_timed"
	// CodeTokensUncounted: the server sent no usage figure, so the counted
	// chunks are not a token count and no decode rate is printed (TTP-99,
	// 2026-09-19). Severity run: the headline is absent, not wrong, but a
	// run without a decode figure compares with nothing on it.
	CodeTokensUncounted = "tokens_uncounted"
	// CodeThinkingIgnored: the request asked for thinking off and the run
	// reasoned anyway (TTP-106, 2026-09-17). llama-server drops a request's
	// chat_template_kwargs unless it was started with --jinja, and reports
	// nothing, so the switch is accepted and ignored. The rates stay true —
	// what is not true is the run: the tokens measured are reasoning, and a
	// card that printed the request alone said "thinking off" over a clip in
	// which every stream visibly thought.
	CodeThinkingIgnored = "thinking_ignored"
	// CodeRecorded: a free-text caveat the recorder wrote into the tape. Its
	// Text is the recorder's words, verbatim.
	CodeRecorded = "recorded"
	// CodeMachineContended: something else was using the box (lesson 6).
	CodeMachineContended = "machine_contended"
	// CodeConditionsChanged: the machine was not the same at the end of the
	// run as at the start (TTP-57).
	CodeConditionsChanged = "conditions_changed"
	// CodeRunCutByClock: the run's wall-clock budget ended the generation
	// (TTP-76).
	CodeRunCutByClock = "run_cut_by_clock"
	// CodeNoProcView: no /proc reading of the server process, so the memory
	// figures are absent rather than zero.
	CodeNoProcView = "no_proc_view"
	// CodeShortStream: above one stream, the shortest answered stream
	// generated fewer than tape.MinDecodeTokens tokens while the per-stream
	// mean did not (TTP-83). The aggregate is still a rate; the "each" figure
	// averages a sample in. Not short_generation: that code means the row is
	// labelled Sample, and consumers already branch on it.
	CodeShortStream = "short_stream"
)

// Severity levels, in the order the card ranks them.
const (
	// SeverityFigure: the headline figure does not mean what it looks like.
	// A reader who quotes the number without this sentence is wrong.
	SeverityFigure = "figure"
	// SeverityRun: the figures are what they say, but the run was not one
	// clean measurement, so two cards are not comparable on it alone.
	SeverityRun = "run"
	// SeverityView: a view of the machine is missing, so part of the card
	// prints "?" rather than a reading.
	SeverityView = "view"
)

// caveatRank is the order the card lists them in, most serious first.
//
// The ranking is by what the caveat costs, not by how loud it sounds. The top
// group is every caveat that changes what the two hero figures MEAN: a failed
// stream is a figure the run did not produce, streams that never decoded
// together make the aggregate a queue's throughput and not N streams'
// (lead, 2026-09-15), a cut answer is a rate with
// nothing behind it, a short generation is a sample, a short stream is a
// sample inside the per-stream mean, a cold run's decode rate
// is partly the disk's rate, and a short prompt makes the prefill figure not a
// prefill measurement at all. cold_cache is up there deliberately: it is
// tempting to file it with "the machine was busy", but the machine being busy
// leaves the decode rate a true reading of a busy machine, whereas weights
// arriving from disk during decode means the decode number is partly a
// benchmark of the disk. It qualifies the headline, so it ranks with the
// headline.
//
// streams_not_concurrent sits directly under streams_failed because it
// qualifies the same Streams row one step less loudly: a failed stream means
// the aggregate figure does not exist for part of the run, streams that
// never shared an instant mean it exists but measures a queue. Both are the
// server's own concurrency story contradicting the row's "N at once".
//
// ragged_aggregate sits directly under streams_not_concurrent (TTP-108,
// 2026-09-19). It is the same concurrency story told by the arithmetic
// rather than the timeline: the aggregate is not N × per-stream because the
// streams did not share a window. It ranks under it because the timeline
// names the mechanism — a queue — where the arithmetic only names the
// shortfall.
//
// placement_contradicted sits directly under the two streams codes (lead,
// 2026-09-15). It qualifies a derived figure rather than a measured one — the
// ratio the card has already declined to print — but what it contradicts is
// the placement the whole MEMORY block is built on, which costs more than a
// run-level caveat: two cards of this run cannot be compared with anything,
// because the device rows themselves describe a machine this run was not.
//
// bandwidth_over_ceiling sits directly under placement_contradicted
// (2026-09-16). It is the same kind of caveat — a derived figure the card has
// already declined to print, with the two figures it compared named in the
// sentence — and it qualifies the same memory block: a bandwidth over the bus
// that carried it means the active split is wrong, so the placement story and
// the rate disagree about one run. It ranks under the contradiction because
// the contradiction unseats the device rows themselves, where this unseats
// only what was built on top of them.
//
// short_stream sits directly under short_generation and above cold_cache
// (TTP-83, 2026-09-14). It is short_generation's own question one stream
// down: a stream too short to be a rate, averaged into a per-stream mean that
// is not. It ranks under it because it costs less — the aggregate is still a
// rate and only the "each" figure has a sample in it, where short_generation
// means the row's own figure is not a rate at all. It ranks over cold_cache
// although cold qualifies BOTH figures, because what it names is an exact,
// token-counted fact about the figure beside it, while a cold run's decode
// rate is still a reading, of a slower machine; and when both fire the
// caveat line spells out one and names the other by code, so the cold run is
// on the card either way — in the cache pill and the maj-faults row as well.
//
// cached_prefill sits directly under short_prompt_for_prefill (TTP-88,
// 2026-09-19). Both qualify the Prefill row, but a short prompt was never a
// measurement while a cached one measured something — the cache path — only
// not the evaluation the row claims. The stronger disqualification ranks
// first.
//
// The second group is a run that was not one measurement — a disagreement
// between the two clocks, the recorder's own caveats, a contended or drifting
// box, a run the clock cut. Each leaves every figure a real reading; what they
// cost is comparability with the next card. The last is a missing view.
//
// client_timed and tokens_uncounted sit directly under client_disagrees
// (TTP-99, 2026-09-19): they are the same family — what the two clocks can
// and cannot say — one step further out. A client-timed run has only one
// clock, so there is nothing to disagree with, only a narrower comparison
// set; an uncounted run has no decode figure at all, which costs more than a
// narrow set and so ranks under it.
var caveatRank = map[string]int{
	CodeStreamsFailed:         0,
	CodeStreamsNotConcurrent:  1,
	CodeRaggedAggregate:       2,
	CodePlacementContradicted: 3,
	CodeBandwidthOverCeiling:  4,
	CodeAnswerCut:             5,
	CodeShortGeneration:       6,
	CodeShortStream:           7,
	CodeColdCache:             8,
	CodeShortPromptForPrefill: 9,
	CodeCachedPrefill:         10,
	CodeClientDisagrees:       11,
	CodeClientTimed:           12,
	CodeTokensUncounted:       13,
	CodeThinkingIgnored:       14,
	CodeRecorded:              15,
	CodeMachineContended:      16,
	CodeConditionsChanged:     17,
	CodeRunCutByClock:         18,
	CodeNoProcView:            19,
}

// MinPrefillPromptTokens is tape.MinPrefillPromptTokens, re-exported so this
// package's callers need not import the schema for one number. The rule and
// the reasoning live there, beside tape.MinDecodeTokens, because they are the
// same rule one step apart in the pipeline (moved by the lead, 2026-09-14).
const MinPrefillPromptTokens = tape.MinPrefillPromptTokens

// isSample reports whether the run's generation was too short for its decode
// figure to be a rate.
//
// It is the single owner of that question: the Decode row's label, the PNG's
// decode eyebrow and CodeShortGeneration all ask it here. The recorder's
// stored verdict is honoured, and the token count is checked as well, because
// the two can disagree — DecodeLabel is written once by the recorder while
// PredictedN is the per-stream mean under concurrency — and a figure whose
// label says "decode" while its own count says sample is the disagreement this
// file exists to prevent. Zero tokens is not a short generation; it is no
// generation, and the row already prints "?".
//
// It keeps that single-stream meaning above one stream on purpose (TTP-83,
// 2026-09-14). Timings.PredictedN is the per-stream mean there, and a mean
// over the floor with one stream under it is NOT "the run was too short": the
// aggregate is still a rate, and relabelling three healthy streams Sample
// because a fourth stopped at 10 tokens would under-report the run as loudly
// as the old card over-reported it. That case is shortStream below, a caveat
// beside the Decode label. Do not fold Aggregate.MinPredictedN in here.
func isSample(s *tape.RunSummary) bool { return IsSample(s) }

// IsSample is isSample for the other two renderers of a run — the PNG card's
// package and the TUI, which both label the same figure. Exported so all three
// ask one predicate rather than agreeing by coincidence, which is the whole
// point of this file: internal/tui/right.go read the recorder's stored verdict
// directly until 2026-09-14, so a tape whose label and whose token count
// disagreed would have been labelled two different ways in two panes of one
// program.
func IsSample(s *tape.RunSummary) bool {
	if s == nil {
		return false
	}
	t := s.Timings
	if t.DecodeLabel == "sample" {
		return true
	}
	return t.PredictedN > 0 && t.PredictedN < tape.MinDecodeTokens
}

// shortStream reports whether a run of several streams had one too short to be
// a rate while its per-stream mean was not (TTP-83, 2026-09-14).
//
// Aggregate.MinPredictedN is the fewest tokens any answered stream generated,
// and the one reading of that question the summary carries: Timings.PredictedN
// above one stream is a mean, and 10 and 300 average 155. 0 is unknown — a tape
// older than the field, or no answered stream — and fires nothing. When the
// mean itself is a sample, isSample has already relabelled the row and
// short_generation says why, so this stands aside rather than warn twice about
// the same tokens. At one stream the minimum is the count, so isSample covers
// it and no stream-count guard is needed.
func shortStream(s *tape.RunSummary) bool { return ShortStream(s) }

// ShortStream is shortStream for internal/card/png, which qualifies the decode
// eyebrow with it — the same predicate, so the image and the text card agree.
func ShortStream(s *tape.RunSummary) bool {
	if s == nil || IsSample(s) {
		return false
	}
	n := s.Aggregate.MinPredictedN
	return n > 0 && n < tape.MinDecodeTokens
}

// shortPrompt reports whether the prompt was too short for the run's prefill
// figure to be a prefill measurement (TTP-65).
//
// Single owner, the same way isSample is: the Prefill row's clause, the PNG's
// prefill eyebrow and CodeShortPromptForPrefill all ask it here. A run whose
// prompt length was never observed is not short — it is unknown, and the row
// already prints "?" for the count.
func shortPrompt(s *tape.RunSummary) bool { return ShortPrompt(s) }

// ShortPrompt is shortPrompt for internal/card/png, which qualifies the same
// figure in its own layout. Exported so the image and the text card ask one
// predicate rather than agreeing by coincidence.
func ShortPrompt(s *tape.RunSummary) bool {
	if s == nil {
		return false
	}
	return shortPromptCount(PromptTokens(s))
}

// streamsNotConcurrent reports whether the run sent several streams at once
// and the token timeline never had all of them decoding at one instant, with
// every cheaper explanation ruled out first: a failed stream is streams_failed's
// partial run, a stream too short for a window is short_stream's, and a peak of
// 0 is a tape older than the field — unknown, not observed. MinPredictedN is
// the witness that window lengths were knowable at all.
func streamsNotConcurrent(s *tape.RunSummary) bool {
	a := s.Aggregate
	return s.Concurrency >= 2 && a.StreamsFailed == 0 &&
		a.MinPredictedN > 0 && a.ShortStreams == 0 &&
		a.PeakDecodingStreams > 0 && a.PeakDecodingStreams < s.Concurrency
}

// streamsNotConcurrentText is the sentence for a run whose streams took turns.
//
// Two cases, because they tell the reader different things: a peak of 1 is the
// trial's own "one stream behind a queue" and says so, and a partial peak says
// how many of the N ever shared an instant — "at most 2 of 4" is a different
// machine finding than "one at a time", and one sentence for both would hide it.
func streamsNotConcurrentText(s *tape.RunSummary) string {
	n := s.Concurrency
	if s.Aggregate.PeakDecodingStreams == 1 {
		return fmt.Sprintf(
			"the %d streams decoded one at a time: the aggregate is one stream behind a queue, not %d at once", n, n)
	}
	return fmt.Sprintf(
		"at most %d of %d streams decoded at once: the aggregate is not %d concurrent streams",
		s.Aggregate.PeakDecodingStreams, n, n)
}

// RaggedAggregate reports whether the run's aggregate decode rate sits
// materially below per-stream × streams, so the aggregate is not the sum
// (TTP-108, 2026-09-19).
//
// It is the single owner of that question: the Streams block's "not all
// decoding at once" clause, the PNG's decode sub-line and
// CodeRaggedAggregate all ask it here, so the clause and the caveat cannot
// disagree about the same arithmetic. The test itself is streamsMultiply —
// no second test of the same fact — with the guards the Streams block
// already applied: the Decode row prints the aggregate beside the "each"
// figure only above one stream, and a multi-round run's aggregate is
// weighted by how long each round decoded, which "per round" already says.
func RaggedAggregate(s *tape.RunSummary) bool {
	if s == nil || s.Concurrency <= 1 || s.Rounds > 1 {
		return false
	}
	return !streamsMultiply(s.Aggregate, streamsSent(s))
}

// raggedAggregateText is the sentence for CodeRaggedAggregate: what
// actually happened — the run had a tail in which fewer streams were
// running — with every number the reader needs to check it.
//
// Re-authored 2026-09-19 (TTP-138), the change that gave the Decode row a
// window figure to lead with. The old sentence said only that the streams
// "did not all decode across the same window", which stopped being the whole
// story the moment the card could say which window they did share: on a tape
// that carries the window, the sentence names that window against the wall —
// the tail's length, in the only two figures that measure it — and both
// rates, so the gap between the window figure the row now prints and the
// whole-wall figure the wall deserves is explained at the site of the
// arithmetic. On a tape that does not carry one (recorded before the field,
// or streams that never overlapped), the arithmetic the old sentence named
// still holds exactly and stays: the stream count, the per-stream rate,
// their product, and the aggregate the card printed instead — now with the
// mechanism named and the tail's length honestly declared unknown, rather
// than the old sentence's shrug that said nothing about the shape at all.
func raggedAggregateText(s *tape.RunSummary) string {
	n := streamsSent(s)
	a := s.Aggregate
	if a.ConcurrentWindowMs > 0 {
		return fmt.Sprintf(
			"ragged run: all %d decoded together for only %s of the %s wall — %s over the whole wall, %s while they overlapped",
			n, formatMs(a.ConcurrentWindowMs), formatMs(a.WallMs),
			formatRate(a.AggregatePredictedPerSecond),
			formatRate(a.ConcurrentPredictedPerSecond))
	}
	per := a.PerStreamPredictedPerSecond
	return fmt.Sprintf(
		"ragged run: %d × %s is %s, not the %s aggregate — the run had a tail on fewer streams, and this tape cannot say how long it was",
		n, formatRate(per), formatRate(float64(n)*per),
		formatRate(a.AggregatePredictedPerSecond))
}

// shortStreamText says how much of the per-stream rate is a sample.
//
// With AggregateTimings.ShortStreams recorded (2026-09-14) it names how many
// streams were under the floor and how short the shortest was: "2 of 4
// streams" tells a reader whether the mean is half sample or one stream out of
// eight, which is the difference between a figure to throw away and one to
// note. A tape carrying MinPredictedN but not the count — recorded in the
// window between the two commits, and in the fixtures — keeps the older
// sentence rather than inventing a count nobody recorded.
//
// The denominator is the answered streams, not Streams: a failed stream
// produced no rate, so it is not one of the streams the mean is over.
func shortStreamText(s *tape.RunSummary) string {
	a := s.Aggregate
	if answered := a.Streams - a.StreamsFailed; a.ShortStreams > 0 && answered > 0 {
		return fmt.Sprintf(
			"short stream: %s of %s streams generated under %d tokens, the shortest %s, so the per-stream rate averages in samples",
			formatInt(a.ShortStreams), formatInt(answered), tape.MinDecodeTokens, formatInt(a.MinPredictedN))
	}
	return fmt.Sprintf(
		"short stream: the shortest stream generated %s tokens, under %d, so the per-stream rate averages in a sample",
		formatInt(a.MinPredictedN), tape.MinDecodeTokens)
}

// shortPromptCount is the rule both of the card's prefill questions ask: is n
// prompt tokens too few for a rate over them to be a prefill measurement. The
// run's Prefill row asks it through ShortPrompt, and each round of the Prompts
// row asks it through readRoundPrompt (TTP-64, 2026-09-14), so there is one
// threshold and one comparison, not a second copy that can drift.
//
// The two callers pass different counts, and on purpose. The run passes its
// whole prompt, cached prefix included, which is what the Prefill row has
// always printed beside the rate. A round passes the tokens one stream
// evaluated, because a round's rate is over exactly those: a round whose 16k
// prompt came 97 % from the cache has a rate over a few hundred tokens, and
// four streams of a 63-token prompt sum to 252 without any one of them being
// a measurement. 0 is unknown and is never short.
func shortPromptCount(n int) bool {
	return n > 0 && n < MinPrefillPromptTokens
}

// cacheHitCached is the run- and round-level "mostly served from the prefix
// cache" test: hits of total at or above server.CachedHitRatio (TTP-88,
// 2026-09-19).
//
// It is the single owner of that question: CodeCachedPrefill asks it here
// with the run's recorded cache share, and the Prompts row's readRoundPrompt
// asks it with each round's — the same threshold the cache explanation in
// explain.go prints, so the caveat and the explanation cannot disagree about
// where "cached" starts. The ratio is recomputed from the counts rather than
// read off Cache.HitRatio, the way the Prefix cache row and the explanation
// already derive it, because the recorded ratio is a reading of these two
// numbers and the predicate must fire on what was observed. 0 total is
// unknown — a tape older than the fields — and fires nothing, and a hit
// count of 0 is not "mostly served from the cache" however the ratio reads.
func cacheHitCached(hits, total int) bool {
	if total <= 0 || hits <= 0 {
		return false
	}
	return float64(hits)/float64(total) >= server.CachedHitRatio
}

// cachedPrefill reports whether the run's prompt was substantially served
// from the prefix cache, so its prefill rate describes a cache hit rather
// than an evaluation (TTP-88, 2026-09-19).
func cachedPrefill(s *tape.RunSummary) bool {
	if s == nil {
		return false
	}
	return cacheHitCached(s.Cache.HitTokens, s.Cache.PromptTotal)
}

// probeCold reports whether the probe pass itself paged the weights in: its
// fault figure at or above tape.ColdMajFaultsPerToken, the same threshold
// the run's own reading is judged by (TTP-143, 2026-09-19).
//
// The pass measures the cold-start cost on a clean instrument — one stream,
// known prompt lengths, nothing else in flight — where the run's reading was
// inferred from a decode window that was also busy generating. 0 (no probe,
// no /proc view, a platform without fault counters) is not observed and
// never fires: a warm probe over a cold run falls to the label, which is
// the pre-probe tape's own verdict.
func probeCold(s *tape.RunSummary) bool {
	if s == nil || s.Probe == nil {
		return false
	}
	return s.Probe.MajFaultsPerToken >= tape.ColdMajFaultsPerToken
}

// cachedPrefillText is the sentence for CodeCachedPrefill: how much of the
// prompt was a hit, and what the prefill rate is a rate over instead — the
// tokens the server evaluated — because a caveat a reader cannot check is
// one they will ignore.
func cachedPrefillText(s *tape.RunSummary) string {
	hits, total := s.Cache.HitTokens, s.Cache.PromptTotal
	return fmt.Sprintf(
		"cached prefill: %s of %s prompt tokens (%s) were prefix-cache hits, so the prefill rate is over the %s the server evaluated",
		formatInt(hits), formatInt(total), formatPct(float64(hits)/float64(total)), formatInt(total-hits))
}

// promptTokensPart is the Prefill row's prompt-token count WITH the
// consequence of that count, as one part.
//
// One part and not two, deliberately. The row is laid out by wrapJoin, which
// breaks between parts, so a count and a separate qualifying clause can end up
// on different lines with the rate the clause is about on a third — and the
// defect TTP-65 is about is precisely a figure that got separated from its
// condition. Joined here, the card cannot render the count without the reason
// the rate above it is not a prefill rate.
func promptTokensPart(s *tape.RunSummary) string {
	count := formatInt(PromptTokens(s)) + " prompt tokens"
	if !shortPrompt(s) {
		return count
	}
	// Short enough to survive the Prefill row's 54 writable columns beside a
	// four-digit token count: wrapJoin truncates a part that does not fit, and
	// a qualification cut to "…measurem…" is the defect wearing a disguise.
	return count + " — not a prefill measurement"
}

// clientDisagrees reports whether the client-side decode rate and the server's
// own differ by more than tape.RateTolerance (lesson 1).
//
// ClientAgreesWithServer is false on a summary where neither rate was ever
// measured, which is not a disagreement — it is two absences. Both rates have
// to be present for the flag to be a reading. A client-timed run (TTP-99)
// never disagrees either: its rate IS the client clock, so there is no
// server figure to differ from, even though both rate fields are set.
func clientDisagrees(s *tape.RunSummary) bool {
	t := s.Timings
	if t.Source == "client" {
		return false
	}
	if t.PredictedPerSecond <= 0 || t.ClientPredictedPerSecond <= 0 {
		return false
	}
	return !t.ClientAgreesWithServer
}

// clientTimed reports whether the run's headline rates are the recorder's own
// clock: the server reported no timings (TTP-99).
func clientTimed(s *tape.RunSummary) bool {
	return s != nil && s.Timings.Source == "client"
}

// tokensUncounted reports whether the run counted stream chunks instead of
// tokens: the server sent no usage figure (TTP-99), so no decode rate exists.
func tokensUncounted(s *tape.RunSummary) bool {
	return s != nil && s.Timings.PredictedNSource == "chunks"
}

// clientDisagreesText is the sentence for CodeClientDisagrees, which is two
// facts wearing one code (lead, 2026-09-15): the run-level figures disagree,
// or they agree while some of the streams behind their mean did not. Above one
// stream Timings is a mean, and a mean of disagreeing and agreeing streams can
// itself agree — the real tape this split is about read 11.7 against 11.5 at
// run level (1.3 %, inside the tolerance) while one of its two streams read
// 11.79 against 11.49 (2.6 %). One sentence for both made the caveat
// contradict its own numbers, so the run-level case keeps the sentence that
// names the run-level figures and the per-stream case says the count the
// schema field carries. The run-level disagreement outranks: it is the one
// that qualifies the figures the card actually prints.
func clientDisagreesText(s *tape.RunSummary) string {
	if clientDisagrees(s) {
		return fmt.Sprintf(
			"the client measured %s where the server reported %s, over the %s tolerance",
			formatRateUnit(s.Timings.ClientPredictedPerSecond),
			formatRateUnit(s.Timings.PredictedPerSecond),
			formatPct(tape.RateTolerance))
	}
	// 0 is every answered stream agreed, or a tape older than the field —
	// either way there is no per-stream fact to say.
	d := s.Aggregate.DisagreeingStreams
	if d <= 0 {
		return ""
	}
	return fmt.Sprintf(
		"%d of %d streams measured a rate the server did not confirm within %s: the card's figures are the mean over the streams",
		d, answeredStreams(s), formatPct(tape.RateTolerance))
}

// noProcView reports whether the server process was never read through /proc.
// The same test internal/card/png/content.go's hasProcMem uses, so the text
// card, the image and -o json agree about whether the memory figures exist.
func noProcView(s *tape.RunSummary) bool {
	return s.Memory.AtEnd.RSSBytes <= 0
}

// bandwidthOverCeilingText is the sentence for CodeBandwidthOverCeiling, and
// like the placement contradiction's it is the two figures compared, because
// they are the whole case: a reader who cannot check a caveat against the
// numbers it names will ignore it (2026-09-16).
//
// One sentence shape per view — the bus that was exceeded differs. The ram
// view names the HOST BUS; the combined figure names the CEILING the
// placement allows, the harmonic mean of the devices it reads, which is a
// derived limit rather than a measured one. What the two share is the
// consequence clause: the figure is gone from the card, and the reason is
// always the same class of defect — something the split counted as streamed
// was read by row.
func bandwidthOverCeilingText(r *bandwidth.FigureRefusal) string {
	read := formatGBs(r.BytesPerSec)
	over := formatGBs(r.LimitBytesPerSec)
	tail := "so no bandwidth is derived: some of what the placement counts as streamed is read by row"
	if r.Which == bandwidth.RefusedRAM {
		return fmt.Sprintf("the bytes this run's placement implies read %s from a %s bus, %s", read, over, tail)
	}
	return fmt.Sprintf("the bytes this run's placement implies read %s against the %s this placement allows, %s", read, over, tail)
}

// Caveats is every qualification that applies to this run, most serious first.
//
// An empty result is the claim the card is really making when it prints no
// warning line: nothing about this run makes its figures mean something other
// than what they say. That is the one field read `-o json` needs to answer "is
// this number quotable" — see the package doc on JSON.
func Caveats(s *tape.RunSummary) []Caveat {
	if s == nil {
		return nil
	}
	var out []Caveat
	add := func(code, severity, text string) {
		out = append(out, Caveat{Code: code, Severity: severity, Text: text})
	}

	if n := s.Aggregate.StreamsFailed; n > 0 {
		add(CodeStreamsFailed, SeverityFigure, fmt.Sprintf(
			"%d of %d streams failed: the aggregate is over the ones that finished",
			n, streamsSent(s)))
	}
	if streamsNotConcurrent(s) {
		add(CodeStreamsNotConcurrent, SeverityFigure, streamsNotConcurrentText(s))
	}
	if RaggedAggregate(s) {
		add(CodeRaggedAggregate, SeverityFigure, raggedAggregateText(s))
	}
	if c := bandwidth.Contradiction(s); c != nil {
		add(CodePlacementContradicted, SeverityFigure, fmt.Sprintf(
			"the placement estimate puts %s of weights on GPU%d, which held %s: the split and everything derived from it are not this run's",
			formatGiB(c.PlacedBytes), c.Device, formatGiB(c.MeasuredBytes)))
	}
	if r := bandwidth.RefusedFigure(s); r != nil {
		add(CodeBandwidthOverCeiling, SeverityFigure, bandwidthOverCeilingText(r))
	}
	if w := answerCutWarning(s); w != "" {
		add(CodeAnswerCut, SeverityFigure, w)
	}
	// An uncounted run stands aside from both shortness caveats (TTP-99):
	// their sentences count tokens, and chunks are not tokens —
	// tokens_uncounted below says what the count is.
	if isSample(s) && !tokensUncounted(s) {
		add(CodeShortGeneration, SeverityFigure, fmt.Sprintf(
			"short generation: %s tokens is a sample, not a decode rate (under %d)",
			formatInt(s.Timings.PredictedN), tape.MinDecodeTokens))
	}
	if shortStream(s) && !tokensUncounted(s) {
		add(CodeShortStream, SeverityFigure, shortStreamText(s))
	}
	// TTP-143 (2026-09-19): the probe pass sends the first prompts a cold
	// server ever sees and pays its faults, and the run's sampler latches
	// only after the pass — so the shape a cold box actually records is a
	// cold probe figure over a run that reads warm. The probe's figure is
	// the verdict when there is one; the recorded label, the run's own
	// reading and every tape from before the probe existed, is it
	// otherwise. One caveat either way: two sources, one fact.
	if probeCold(s) {
		add(CodeColdCache, SeverityFigure, fmt.Sprintf(
			"cold run: weights arrived from disk while the probe prefilled, %s maj faults/token",
			formatFloat1(s.Probe.MajFaultsPerToken)))
	} else if s.Cache.Label == tape.CacheCold {
		add(CodeColdCache, SeverityFigure, fmt.Sprintf(
			"cold run: weights arrived from disk while it decoded, %s maj faults/token",
			formatFloat1(s.Memory.MajFaultsPerToken)))
	}
	if shortPrompt(s) {
		add(CodeShortPromptForPrefill, SeverityFigure, fmt.Sprintf(
			"short prompt: %d prompt tokens is under %d, so the prefill rate is not one",
			PromptTokens(s), MinPrefillPromptTokens))
	}
	if cachedPrefill(s) {
		add(CodeCachedPrefill, SeverityFigure, cachedPrefillText(s))
	}
	if text := clientDisagreesText(s); text != "" {
		add(CodeClientDisagrees, SeverityRun, text)
	}
	if clientTimed(s) {
		add(CodeClientTimed, SeverityRun,
			"client-timed: the server reported no timings, so every rate here is the recorder's clock over the stream — not the engine's own count; compare only with other client-timed runs")
	}
	if tokensUncounted(s) {
		add(CodeTokensUncounted, SeverityRun, fmt.Sprintf(
			"tokens uncounted: the server sent no usage figure, so the %s stream chunks are not a token count and no decode rate is printed",
			formatInt(s.Timings.PredictedN)))
	}
	if n := s.Sampling.ThoughtAnyway; n > 0 {
		// Only the positive case speaks: a zero is "none seen", which on a
		// tape older than the field cannot be told from "never looked".
		add(CodeThinkingIgnored, SeverityRun, fmt.Sprintf(
			"thinking off was asked for and %d of %d streams thought anyway: the server dropped the switch, so this run reasoned",
			n, streamsSent(s)))
	}
	for _, w := range s.Warnings {
		if strings.TrimSpace(w) == "" {
			continue
		}
		// Verbatim: these are the recorder's own words about something it
		// observed while it still could, and rewording them here would be the
		// card claiming to have seen it.
		add(CodeRecorded, SeverityRun, w)
	}
	if s.Contention.Contended {
		// Without the reasons. They are printed whole under HOST on the text
		// card and are contention.reasons in -o json, so repeating them here
		// would put the same two sentences on the card twice — which is the
		// wall this block was reshaped to avoid.
		add(CodeMachineContended, SeverityRun,
			"the machine was contended while this run was measured")
	}
	if line := conditionsLine(s); line != "" {
		add(CodeConditionsChanged, SeverityRun,
			"the machine changed under the run: "+strings.TrimPrefix(line, conditionsPrefix))
	}
	if text := runCutByClockText(s); text != "" {
		add(CodeRunCutByClock, SeverityRun, text)
	}
	if noProcView(s) {
		add(CodeNoProcView, SeverityView,
			"no /proc view of the server: the memory figures were not read, not zero")
	}

	sort.SliceStable(out, func(i, j int) bool {
		return caveatRank[out[i].Code] < caveatRank[out[j].Code]
	})
	return out
}

// streamsSent is how many streams the run asked for, which is the denominator
// a failure count is against.
//
// AggregateTimings.Streams is already that total: internal/server/concurrent.go
// sets it to len(recs) and counts the failures as a subset of it ("streams that
// failed or produced no token contribute to Streams and StreamsFailed"), so
// adding the two would report "1 of 9 streams failed" for an eight-stream run.
// Concurrency is the fallback for a summary whose aggregate was never reduced.
func streamsSent(s *tape.RunSummary) int {
	if n := s.Aggregate.Streams; n > 0 {
		return n
	}
	return s.Concurrency
}

// answeredStreams is how many streams produced a figure: the sent count less
// the failures. The denominator of a per-stream count — a failed stream has no
// rate to disagree, so it is not one of the streams the caveat counts over.
func answeredStreams(s *tape.RunSummary) int {
	return streamsSent(s) - s.Aggregate.StreamsFailed
}

// runCutByClockText is the sentence for a run the wall-clock budget ended
// (TTP-76), or "" when the clock never cut.
//
// It has to distinguish the two cases, because they say opposite things about
// the machine. Cut at the budget is the ordinary one: the run was asked for
// twenty seconds and got twenty seconds. CutAt past For is the box telling on
// itself — the token floor held the cut back until every live stream had
// tape.MinCutTokens, so this machine could not produce a decode rate inside
// the budget and the clip is longer than was asked for. A single sentence for
// both would hide the second, which is the one worth knowing.
func runCutByClockText(s *tape.RunSummary) string {
	l := s.Limit
	if l.CutAt <= 0 {
		return ""
	}
	// The cut always lands a shade past the budget: the clock waits out its
	// timer, then polls for the floor, then the streams have to be cancelled
	// and drained. So an overshoot on its own is not the floor holding a run
	// back, and the two figures decide it, not their order — a run cut 0.2 ms
	// past a 30 s budget printed "the clock cut at 30s, not the 30s asked for",
	// a sentence that names two identical numbers and says they differ (lead,
	// 2026-09-14, on the v0.2.0 hero take). The same reasoning is in
	// formatGHzPair: a line that shows two identical numbers reads as a bug in
	// the card rather than a fact about the run. Here it settles the claim as
	// well as the wording, because a difference the card cannot print is a
	// difference no reader can act on.
	if cut, asked := formatDuration(l.CutAt), formatDuration(l.For); l.For > 0 && cut != asked {
		return fmt.Sprintf(
			"the clock cut at %s, not the %s asked for: the %d-token floor held it back on this box",
			cut, asked, l.MinTokens)
	}
	if l.For > 0 {
		return fmt.Sprintf(
			"the clock cut this run at its %s budget: the streams stopped where the clock was",
			formatDuration(l.For))
	}
	return fmt.Sprintf(
		"the clock cut this run at %s: the streams stopped where the clock was",
		formatDuration(l.CutAt))
}

// maxCaveatCodesListed is how many codes the caveat line names before it says
// how many more there were. Six is the same idea as maxRoundsListed: the block
// is an index, not a table, and a reader who needs the seventh is already in
// `-o json`.
const maxCaveatCodesListed = 6

// caveatLines is the card's warning block: one line, wrapped, or nothing.
//
// The list can be several entries long and the card has a visual budget, so the
// line says how many there are and spells out the most serious one; the rest
// are named by code, which is the handle a reader uses to find the sentence in
// `-o json`. A single caveat is just its sentence — a count of one is noise.
func caveatLines(s *tape.RunSummary) []string {
	cs := Caveats(s)
	if len(cs) == 0 {
		return nil
	}
	line := cs[0].Text
	if len(cs) > 1 {
		line = fmt.Sprintf("%d caveats — %s · %s", len(cs), line, strings.Join(listedCaveatCodes(cs[1:]), " · "))
	}
	// Wrapped word by word, never truncated: a caveat cut off mid-sentence is
	// worse than no caveat (the rule internal/card/conditions.go states for
	// the conditions line, which is one of these).
	lines := wrapJoin(strings.Fields(line), " ", innerWidth-2)
	out := make([]string, 0, len(lines))
	for i, l := range lines {
		if i == 0 {
			out = append(out, "! "+l)
			continue
		}
		out = append(out, "  "+l)
	}
	return out
}

// listedCaveatCodes is the rest of the caveat line: each code once, in the
// rank order the list gave them, with a count when the code repeats (lead,
// 2026-09-15). Two recorder warnings are two caveats — `-o json` keeps both —
// but the real tape's line read "recorded · recorded", which spends two of the
// line's slots saying one thing. The count is the "×" the Streams row already
// spells ("2 × 11.5 tok/s"), so "recorded ×2" reads as the same kind of
// plural. maxCaveatCodesListed caps the codes, not the caveats: "%d caveats"
// stays the true count, and a repeated code costs one slot instead of pushing
// a distinct warning past the cap.
func listedCaveatCodes(rest []Caveat) []string {
	counts := map[string]int{}
	var order []string
	for _, c := range rest {
		if _, dup := counts[c.Code]; !dup {
			order = append(order, c.Code)
		}
		counts[c.Code]++
	}
	out := make([]string, 0, len(order))
	for _, code := range order {
		if len(out) == maxCaveatCodesListed {
			out = append(out, fmt.Sprintf("+%d more", len(order)-maxCaveatCodesListed))
			break
		}
		if n := counts[code]; n > 1 {
			out = append(out, fmt.Sprintf("%s ×%d", code, n))
			continue
		}
		out = append(out, code)
	}
	return out
}
