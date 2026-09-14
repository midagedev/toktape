package card

import (
	"fmt"
	"sort"
	"strings"

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
	// CodeClientDisagrees: the client-side rate and the server's own differ by
	// more than tape.RateTolerance (lesson 1).
	CodeClientDisagrees = "client_disagrees_with_server"
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
// stream is a figure the run did not produce, a cut answer is a rate with
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
// The second group is a run that was not one measurement — a disagreement
// between the two clocks, the recorder's own caveats, a contended or drifting
// box, a run the clock cut. Each leaves every figure a real reading; what they
// cost is comparability with the next card. The last is a missing view.
var caveatRank = map[string]int{
	CodeStreamsFailed:         0,
	CodeAnswerCut:             1,
	CodeShortGeneration:       2,
	CodeShortStream:           3,
	CodeColdCache:             4,
	CodeShortPromptForPrefill: 5,
	CodeClientDisagrees:       6,
	CodeRecorded:              7,
	CodeMachineContended:      8,
	CodeConditionsChanged:     9,
	CodeRunCutByClock:         10,
	CodeNoProcView:            11,
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
	return shortPromptCount(promptTokens(s))
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
	count := formatInt(promptTokens(s)) + " prompt tokens"
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
// to be present for the flag to be a reading.
func clientDisagrees(s *tape.RunSummary) bool {
	t := s.Timings
	if t.PredictedPerSecond <= 0 || t.ClientPredictedPerSecond <= 0 {
		return false
	}
	return !t.ClientAgreesWithServer
}

// noProcView reports whether the server process was never read through /proc.
// The same test internal/card/png/content.go's hasProcMem uses, so the text
// card, the image and -o json agree about whether the memory figures exist.
func noProcView(s *tape.RunSummary) bool {
	return s.Memory.AtEnd.RSSBytes <= 0
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
	if w := answerCutWarning(s); w != "" {
		add(CodeAnswerCut, SeverityFigure, w)
	}
	if isSample(s) {
		add(CodeShortGeneration, SeverityFigure, fmt.Sprintf(
			"short generation: %s tokens is a sample, not a decode rate (under %d)",
			formatInt(s.Timings.PredictedN), tape.MinDecodeTokens))
	}
	if shortStream(s) {
		add(CodeShortStream, SeverityFigure, shortStreamText(s))
	}
	if s.Cache.Label == tape.CacheCold {
		add(CodeColdCache, SeverityFigure, fmt.Sprintf(
			"cold run: weights arrived from disk while it decoded, %s maj faults/token",
			formatFloat1(s.Memory.MajFaultsPerToken)))
	}
	if shortPrompt(s) {
		add(CodeShortPromptForPrefill, SeverityFigure, fmt.Sprintf(
			"short prompt: %d prompt tokens is under %d, so the prefill rate is not one",
			promptTokens(s), MinPrefillPromptTokens))
	}
	if clientDisagrees(s) {
		add(CodeClientDisagrees, SeverityRun, fmt.Sprintf(
			"the client measured %s where the server reported %s, over the %s tolerance",
			formatRateUnit(s.Timings.ClientPredictedPerSecond),
			formatRateUnit(s.Timings.PredictedPerSecond),
			formatPct(tape.RateTolerance)))
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
		rest := cs[1:]
		codes := make([]string, 0, len(rest))
		for _, c := range rest {
			if len(codes) == maxCaveatCodesListed {
				codes = append(codes, fmt.Sprintf("+%d more", len(rest)-maxCaveatCodesListed))
				break
			}
			codes = append(codes, c.Code)
		}
		line = fmt.Sprintf("%d caveats — %s · %s", len(cs), line, strings.Join(codes, " · "))
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
