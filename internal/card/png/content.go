package png

import (
	"fmt"
	"image/color"
	"math"
	"strconv"
	"strings"

	"github.com/midagedev/toktape/internal/bandwidth"
	"github.com/midagedev/toktape/internal/card"
	"github.com/midagedev/toktape/internal/tape"
)

// content is every string the card prints, derived from the summary once so
// the drawing code contains no conditionals about what was observed.
//
// The derivations mirror internal/card/card.go field for field: the two
// renderings must settle the same argument with the same numbers.
type content struct {
	// Header.
	version string // "v0.1.0"
	runID   string
	when    string // the run's start time, right of the wordmark (2026-09-19)
	os      string // the os, under it

	// Hero.
	left, right heroCol

	// Memory.
	segments   []segment
	breakdown  []legendEntry // weights / kv / compute, when the split is known
	sum        fallbackLine  // "offloaded · 23.7 GiB placed · host RSS 3.4 GiB", whole parts only
	pills      []pill
	hasPlaced  bool
	hasProcMem bool

	// Identity band: model, rig, engine, one line each (2026-09-19).
	ident1   fallbackLine // the model
	identSub string       // the model's detail, one size below
	ident2   fallbackLine // the rig
	ident3   fallbackLine // the engine
}

// fallbackLine is one measured line of the card: a preferred string and the
// ordered fallbacks it gives its elements up to, resolved by pickWidest at
// draw time — the hero sub-lines' own mechanism. The identity band's three
// lines use it at stIdent against the full content width (2026-09-19); the
// memory summary uses it at stSmall against whatever width the pill row
// leaves.
type fallbackLine struct {
	preferred string
	fallbacks []string
}

type heroCol struct {
	eyebrow string
	number  string
	unit    string
	sub1    string
	sub2    string
	// sub1Fallbacks are to sub1 what sub2Fallbacks are to sub2.
	sub1Fallbacks []string
	// sub2Fallbacks are progressively shorter spellings of sub2, tried in
	// order when sub2 does not fit the column (TTP-69, 2026-09-14).
	//
	// The frame is a fixed 1200×675 and the hero columns are 504 px, so a
	// clause that does not fit is cut with an ellipsis wherever the glyphs run
	// out — which on the ws card fell inside the draft model's file name and
	// took the block size and the acceptance rate with it. A clause that knows
	// what it can give up loses the least important element instead of the
	// last one. Deciding WHICH element that is stays in build(); this is only
	// the list, and the canvas is what measures.
	sub2Fallbacks []string
}

type segment struct {
	label string // "GPU0"
	size  string // "6.8 GiB"
	bytes int64
	col   color.RGBA
	// parts subdivides a segment into lightness steps of its own hue: a GPU
	// into weights | kv cache | compute buffers, the three-way VRAM split
	// docs/research/01 ranks third among the things people cannot see today,
	// and the host into what is in RAM | what is read back from the file
	// (TTP-63). Empty when neither split was derivable.
	parts []subseg
	// legend replaces the segment's own "label size" entry under the bar when
	// one entry cannot say what the segment holds. The host segment uses it to
	// name its two halves separately; everything else leaves it nil.
	legend []legendEntry
}

// subseg is one lightness step inside a device segment.
type subseg struct {
	bytes int64
	col   color.RGBA
}

// legendEntry is one swatch-and-label pair under the bar.
type legendEntry struct {
	label string
	col   color.RGBA
}

type pill struct {
	text string
	col  color.RGBA
	// bg overrides the fill, which is otherwise the text colour at low
	// alpha. Only the cap pill sets it (vision, 2026-09-19): a warm fill
	// beside four cool ones was the card's only warm pixel anywhere — 111 of
	// them, all inside that one box, against 0 on a card without it — and
	// three signals stacked (a colour family the card uses nowhere else, a
	// brighter fill than its neighbours, and the last seat in a row of
	// all-clear readings) read as an alarm rather than a finding. The text
	// keeps the colour; the box rejoins the row.
	bg color.RGBA
}

// build derives the whole card from the summary. Nothing measures at build
// time since the identity band replaced the footer grid (2026-09-19): its
// lines are picked by pickWidest at draw time, against the full content width.
func build(s *tape.RunSummary) *content {
	c := &content{}
	c.buildHeader(s)
	c.buildHero(s)
	c.buildMemory(s)
	c.buildIdent(s)
	return c
}

// ---------------------------------------------------------------- header ---

func (c *content) buildHeader(s *tape.RunSummary) {
	c.version = versionString(s)
	c.runID = orUnknown(s.ID)
	// The header's right side is the run's moment, not the model's name: the
	// name is the identity band's first line now (2026-09-19), and printing
	// it twice is what that round removed. Both strings are dropped when
	// unobserved rather than drawn as "?" — they are facts about the run, not
	// figures a "?" would qualify. The hostname never comes here: the
	// published view strips the machine's name, and a card that shows it
	// locally and not when published would be two different cards.
	c.when = omitUnknown(startedString(s))
	c.os = omitUnknown(osString(s.Host))
}

// versionString mirrors internal/card/card.go: the summary's own version wins
// over the package var, and only a numeric version gets the "v".
func versionString(s *tape.RunSummary) string {
	v := s.ToktapeVersion
	if v == "" {
		v = card.Version
	}
	if v == "" {
		return unknown
	}
	if v[0] >= '0' && v[0] <= '9' {
		v = "v" + v
	}
	return v
}

// ------------------------------------------------------------------ hero ---

func (c *content) buildHero(s *tape.RunSummary) {
	t := s.Timings
	a := s.Aggregate
	concurrent := s.Concurrency > 1

	// Left: decode. Lesson 2 — a short generation is a "sample", never a
	// "decode" rate, and the eyebrow is the only place that says so. Asked
	// through card.IsSample, the predicate the text card and the TUI ask
	// (TTP-83, 2026-09-14): this read the recorder's stored DecodeLabel, so a
	// tape whose label and count disagreed was Sample on one and DECODE here.
	label := "decode"
	if card.IsSample(s) {
		label = "sample"
	}
	if concurrent {
		streams := a.Streams
		if streams == 0 {
			streams = s.Concurrency
		}
		// TTP-138 (2026-09-19): the window figure is the headline when the
		// tape carries one — the whole-wall aggregate averages a ragged tail
		// into the rate, and the same box capped so the streams end together
		// reports the window's number. Zero (a tape older than the field,
		// streams that never overlapped) keeps today's figure, and the text
		// card's Decode row makes the same choice through its own branch.
		//
		// The per-stream line moves with it (lead, 2026-09-19). This column
		// spells the multiplication out — "4 × 17.4 per stream" — so a
		// headline taken over the window beside a per-stream figure taken
		// over the whole wall does not merely permit the wrong product, it
		// prints the invitation. Both figures come from the same span or
		// neither does.
		number := formatRate(a.AggregatePredictedPerSecond)
		each := a.PerStreamPredictedPerSecond
		if a.ConcurrentPredictedPerSecond > 0 {
			number = formatRate(a.ConcurrentPredictedPerSecond)
			each = a.ConcurrentPerStreamPredictedPerSecond
			if a.ConcurrentStreams > 0 {
				streams = a.ConcurrentStreams
			}
		}
		perStream := fmt.Sprintf("%d × %s per stream",
			streams, formatRateUnit(each))
		c.left = heroCol{
			eyebrow: decodeEyebrow(s, "aggregate "+label),
			number:  number,
			unit:    "tok/s",
			// The queue note rides on the per-stream line because that is
			// the figure it qualifies: streams that waited for a slot were
			// partly serialised, and their per-stream rate is not the rate
			// of a run that fitted the server. The ragged clause rides it
			// for the same reason (TTP-108, 2026-09-19): it says why N ×
			// per-stream is not the aggregate above it, in the Streams
			// block's own words, asked through card.RaggedAggregate so the
			// image and the text card agree.
			sub1: joinParts(" · ",
				perStream,
				raggedString(s),
				queuedString(s)),
			// What goes when the column is full is the queue note before
			// the ragged clause (vision, 2026-09-19). The original order
			// gave up the ragged clause first, on the reading that the
			// figures outrank the qualification — but once the headline is
			// the window's own rate, that clause is the only place on the
			// image saying what span the headline is over, and the PNG has
			// no caveat band to say it anywhere else. Measured at 12
			// streams, where "all 12 overlapped for 12.4 s" is the longest
			// this clause gets: the whole line does not fit and the first
			// step decides which condition the reader loses.
			sub1Fallbacks: []string{
				joinParts(" · ", perStream, raggedString(s)),
				joinParts(" · ", perStream, card.RaggedClauseShort(s)),
				joinParts(" · ", perStream, queuedString(s)),
				perStream,
			},
			sub2: joinParts(" · ",
				formatInt(a.TotalPredictedN)+" tokens",
				formatSeconds(a.WallMs)+" wall",
				failedStreams(a),
			),
		}
	} else {
		c.left = heroCol{
			eyebrow: decodeEyebrow(s, label),
			number:  formatRate(t.PredictedPerSecond),
			unit:    "tok/s",
			sub1:    bandwidthString(s),
			sub2: joinParts(" · ",
				formatInt(t.PredictedN)+" tokens",
				thinkingString(reasoningTokens(s)),
				omitUnknown(formatMs(t.PredictedMs)),
				itlString(t),
			),
		}
	}

	// A multi-prompt run replaces the per-stream line with the median over its
	// rounds and their range (TTP-31, 2026-09-13). The frame is fixed, so the
	// clause takes the row rather than adding one. The median leads the line:
	// the hero number above it is the mean over every stream of every round,
	// and "median of 6 prompts" standing alone under it would read as that
	// number's label.
	if r := roundsString(s); r != "" {
		c.left.sub1 = r
	}
	// A speculative n_max sweep (TTP-35, 2026-09-13) takes the same row for the
	// answer it was run to find: the fastest block size and its median over
	// that value's prompts. The run-wide median mixes block sizes and settles
	// nothing; the text card keeps it on its Prompts row.
	if b := sweepString(s); b != "" {
		c.left.sub1 = b
	}

	// A speculative run replaces the decode column's second sub-line with the
	// draft clause (TTP-30, 2026-09-13). The frame is a fixed 1200×675 and
	// nothing else may move, so the clause takes a row rather than adding one.
	// It takes the whole row because it measures 416 px of the column's 504,
	// and appended to the token count it would be cut through "accepted".
	// Token count, ms and ITL stay on the text card. A failed-streams note is
	// a warning, not a figure, so it is kept.
	if d, fallbacks := draftString(s); d != "" {
		if concurrent {
			c.left.sub2 = joinParts(" · ", d, failedStreams(a))
			for i, alt := range fallbacks {
				fallbacks[i] = joinParts(" · ", alt, failedStreams(a))
			}
		} else {
			c.left.sub2 = d
		}
		c.left.sub2Fallbacks = fallbacks
	}

	// Right: prefill and TTFT.
	prompt := promptTokens(s)
	if concurrent {
		// The prefill side mirrors the decode side: the aggregate is the
		// headline and the per-stream rate sits under it, because "8250 tok/s"
		// on its own is a different claim from "8 streams at 1980 each".
		// s.Timings is the per-stream mean when Concurrency > 1 (schema).
		streams := a.Streams
		if streams == 0 {
			streams = s.Concurrency
		}
		c.right = heroCol{
			eyebrow: prefillEyebrow(s, "aggregate prefill"),
			number:  formatRate(a.AggregatePromptPerSecond),
			unit:    "tok/s",
			// No "N ×" here, unlike decode: prefill is batched server-wide, so
			// N × the per-request prompt rate is not the aggregate, and printing
			// it as a product was false arithmetic on the card (lead, 2026-09-13).
			//
			// TTP-65 (2026-09-14) put the two halves of TTFT on this row. Four
			// requests that arrive together are prefilled one after another,
			// so the time to a first token is the wait for a slot PLUS the
			// engine's work, and a card that shows only the total invites the
			// reader to read the whole of it as prefill — which is how the ws
			// four-stream tape came to claim 5.6 tok/s. The engine's half is
			// the server's own prompt_ms, never send-to-first-token.
			//
			// No total is printed beside them on purpose: these two are means
			// over the streams and the TTFT on the line below is a median, so
			// a total here would be a third TTFT figure that does not equal
			// either. The text card prints the same two means on its Prefill
			// row and its median once, in the Streams block — it used to print
			// the median on both rows, which is what TTP-110 took out; this
			// card never did.
			sub1: joinParts(" · ",
				fmt.Sprintf("%s per stream", formatRateUnit(t.PromptPerSecond)),
				enginePrefillString(s), queueWaitString(s)),
			sub1Fallbacks: []string{
				joinParts(" · ", fmt.Sprintf("%s per stream", formatRateUnit(t.PromptPerSecond)),
					queueWaitString(s)),
				fmt.Sprintf("%s per stream", formatRateUnit(t.PromptPerSecond)),
			},
			// The TTFT pair is card.TTFTPercentiles' to give (TTP-97,
			// 2026-09-19): percentiles computed from two samples
			// frequently render as the same string, which reads as a bug,
			// so alike renderings print the single figure instead — the
			// same rule the text card's Streams block follows.
			sub2: joinParts(" · ",
				append(ttftParts(s), formatInt(a.TotalPromptN)+" prompt tok")...,
			),
		}
	} else {
		// TTP-137 (2026-09-19): the probe's own figures ride this sub-line —
		// the machine's rate at the margin, the one figure here that does
		// not move with the stream count. The frame is fixed, so the clause
		// takes this line rather than adding one, and its fallbacks give up
		// the older, narrower element first: measured at stBody, the full
		// line is 630 px against the 504 px column, and the prompt count
		// beside the clause is 492. ctx 16384 is on the text card's Context
		// row; the machine's own rate is on nothing else the reader sees.
		sub1 := "TTFT " + formatMs(t.TTFTMs)
		sub2 := joinParts(" · ",
			formatInt(prompt)+" prompt tokens",
			contextString(s),
		)
		var sub2Fallbacks []string
		if probe := probeString(s); probe != "" {
			// The probe's pair takes the line to itself, and the prompt
			// count moves up beside the TTFT (vision, 2026-09-19). Three
			// clauses on one sub-line left the column bottom-heavy against
			// a nearly empty line above it, and the shared middle dot made
			// the machine's rate read as one more context figure. ctx is
			// what yields: it is already on the engine line at the foot of
			// the card, and the probe's rate is on nothing else the reader
			// sees.
			sub1 = joinParts(" · ", sub1, formatInt(prompt)+" prompt tokens")
			sub2 = probe
		}
		c.right = heroCol{
			eyebrow: prefillEyebrow(s, "prefill"),
			number:  formatRate(t.PromptPerSecond),
			unit:    "tok/s",
			sub1:    sub1,
			sub2:    sub2,
			// What goes when the column is full is the local fact, not the
			// machine's rate — the ordering above, made explicit.
			sub2Fallbacks: sub2Fallbacks,
		}
	}
}

// ttftParts is the hero's TTFT clause above one stream: the p50 beside its
// p95, or the single figure when the pair would say nothing twice —
// percentiles computed from two samples that render as the same string read
// as a bug in the card rather than a fact about the run (TTP-97,
// 2026-09-19). Asked through card.TTFTPercentiles, the predicate the text
// card's Streams block and the TUI ask, so every renderer prints the pair on
// the same runs.
func ttftParts(s *tape.RunSummary) []string {
	p50, p95, pair := card.TTFTPercentiles(s, formatMs)
	if !pair {
		return []string{"TTFT " + p50}
	}
	return []string{"TTFT p50 " + p50, "p95 " + p95}
}

// roundsString is the PNG's form of the text card's Prompts row:
// "14.8 median of 6 prompts · 9.1–19.3 tok/s". Empty for a single-round run,
// or when no round produced a rate to take a median of.
func roundsString(s *tape.RunSummary) string {
	if s.Rounds < 2 || s.Spread == nil {
		return ""
	}
	r := s.Spread.PerStreamPredictedPerSecond
	if r.Median <= 0 {
		return ""
	}
	return fmt.Sprintf("%s median of %d prompts · %s–%s tok/s",
		formatRate(r.Median), s.Rounds, formatRate(r.Min), formatRate(r.Max))
}

// sweepString is the PNG's form of the text card's Draft sweep row, reduced to
// its verdict: "best n_max 5 · 16.1 tok/s median of 6 prompts". Empty unless a
// sweep of two or more values has a single fastest one (card.FastestSpecNMax,
// so both renderings name the same value).
func sweepString(s *tape.RunSummary) string {
	i := card.FastestSpecNMax(s.BySpecNMax)
	if len(s.BySpecNMax) < 2 || i < 0 {
		return ""
	}
	g := s.BySpecNMax[i]
	return fmt.Sprintf("best n_max %d · %s median of %d prompts",
		g.NMax, formatRateUnit(g.Spread.PerStreamPredictedPerSecond.Median), g.Rounds)
}

func failedStreams(a tape.AggregateTimings) string {
	if a.StreamsFailed <= 0 {
		return ""
	}
	return strconv.Itoa(a.StreamsFailed) + " failed"
}

func slotsString(a tape.AggregateTimings) string {
	if a.SlotsBusyMax <= 0 {
		return ""
	}
	return "slots busy max " + strconv.Itoa(a.SlotsBusyMax)
}

func contextString(s *tape.RunSummary) string {
	if s.Server.CtxSize <= 0 {
		return ""
	}
	return "ctx " + strconv.Itoa(s.Server.CtxSize)
}

// probeString is the probe pass's own measurement for the prefill column
// (TTP-137, 2026-09-19): the machine's prefill rate at the margin, on one
// stream, and the server's fixed cost per request — the two figures the
// two-point fit separates, the number that does not move with the stream
// count. Mirrors internal/card's probeLine word for word; the two renderings
// settle the same argument with the same numbers. Empty when there was no
// probe or the fit was refused — never a "?" for a figure this column never
// asked about.
func probeString(s *tape.RunSummary) string {
	p := s.Probe
	if p == nil || p.PrefillPerSecond <= 0 {
		return ""
	}
	// "probe" is the word that separates the two prefill figures on a
	// single-stream card (vision, 2026-09-19): there the headline is also
	// one stream, so "on one stream" divides nothing and the reader is left
	// with two rates a factor apart and no clause saying why. The row's
	// figure is what this run's prompts cost, fixed cost included; the
	// probe's is the machine's rate at the margin with that cost named
	// separately. Provenance is the condition that differs, so provenance
	// is what leads.
	return fmt.Sprintf("probe %s on one stream · %s fixed",
		formatRateUnit(p.PrefillPerSecond), formatProbeMs(p.FixedMs))
}

// thinkingString is the decode column's "96 thinking" clause: how many of the
// tokens beside it were a thinking model's reasoning_content.
//
// This is the PNG's equivalent of the text card's Context row clause. The
// reasoning tokens are already counted in the "N tokens" figure to its left,
// because the server counts them in predicted_n and so does every rate on the
// card (TTP-20, 2026-09-13); without this clause a run that spent its whole
// budget thinking is indistinguishable from one that answered. Empty when
// there were none, so joinParts drops it.
func thinkingString(reasoningN int) string {
	if reasoningN <= 0 {
		return ""
	}
	return formatInt(reasoningN) + " thinking"
}

// reasoningTokens is how many of the run's generated tokens were
// reasoning_content, the same figure the text card's Context row prints.
func reasoningTokens(s *tape.RunSummary) int { return s.Timings.ReasoningN }

// draftString is the PNG's form of the text card's Draft row:
// "draft DSpark-0.6B-Q8_0.gguf · n_max 3 · 60% accepted", with a sweep's
// values in place of the flag (card.DraftNMax). Empty when the
// server reported no draft figure. The counts stay on the text card, and the
// rules are its rules: an unread model is "?", and zero drafted is "0 drafted"
// rather than a rate over nothing.
func draftString(s *tape.RunSummary) (clause string, fallbacks []string) {
	t := s.Timings
	if t.DraftN == nil {
		return "", nil
	}
	rate := "0 drafted"
	if *t.DraftN > 0 {
		accepted := 0
		if t.DraftNAccepted != nil {
			accepted = *t.DraftNAccepted
		}
		rate = formatPct(float64(accepted)/float64(*t.DraftN)) + " accepted"
	}
	name := "draft " + orUnknown(s.Server.Flags.DraftModel)
	figures := joinParts(" · ", "n_max "+card.DraftNMax(s), rate)
	// The bandwidth the verify steps really sustained (TTP-67). It rides this
	// clause rather than the line above because the line above is already
	// spoken for on a concurrent run — the stream arithmetic, a multi-prompt
	// median, a sweep's verdict — and because "per step" is a statement about
	// the draft, which is what this clause is.
	bw, bwNoRatio := verifyBandwidthString(s)
	return joinParts(" · ", name, figures, bw),
		[]string{
			// First to go is the model's name. A draft model's file name runs
			// to 46 characters on the rig this was written for and no size
			// that fits the column holds it (TTP-54 kept sizeBody at 15 for
			// exactly this clause and it still did not), so keeping it means
			// cutting the clause through "accepted" and losing the block size
			// and the acceptance rate — the two figures a reader needs to
			// reproduce the run. The name is printed whole on the FLAGS strip
			// at the foot of the card either way, and on the text card, so
			// dropping it here costs nothing but the glance.
			joinParts(" · ", "draft "+figures, bw),
			// Then the share of the host's memory ceiling, keeping the rate
			// itself: the rate is the figure another rig can be compared
			// against, while the ratio is only meaningful beside this box's
			// own peak. Both are on the text card's Decode row and in -o json.
			joinParts(" · ", "draft "+figures, bwNoRatio),
			// Then the bandwidth altogether. The block size and the
			// acceptance rate are the last things standing, which is the
			// order TTP-30 put them in.
			"draft " + figures,
		}
}

// verifyBandwidthString is the hero's spelling of the verify-step host
// bandwidth: "≈ 108 GB/s RAM/step". Empty when the run used no draft or the
// arithmetic is not derivable.
//
// It is terser than the text card's "≈ 108 GB/s from RAM per verify step"
// because it shares a 504 px row with the figures that give it its
// denominator, and because the clause it sits in has already said "draft".
// The two renderings ask internal/card for the same numbers (card.VerifyRAM),
// so they can differ in wording and never in value.
// It returns the clause twice: once whole, and once without the "of peak"
// ratio, which is the first thing the clause gives up when the column is full.
func verifyBandwidthString(s *tape.RunSummary) (full, withoutRatio string) {
	bps, ofPeak, ok := card.VerifyRAM(s)
	if !ok {
		return "", ""
	}
	withoutRatio = "≈ " + formatGBs(bps) + " RAM/step"
	full = withoutRatio
	if ofPeak > 0 {
		full += " · " + formatPct(ofPeak) + " of peak"
	}
	return full, withoutRatio
}

// raggedString is the decode column's "not all decoding at once" clause:
// the aggregate is not N × per-stream because the streams did not share a
// window (TTP-108, 2026-09-19). Asked through card.RaggedAggregate, the
// predicate the text card's Streams block and the ragged_aggregate caveat
// ask, so the image qualifies the same figure the same way. "" when the
// arithmetic holds, so joinParts drops it and the line is what it always
// was.
//
// On a tape that carries the window the clause names it instead (vision,
// 2026-09-19). This clause sits directly beside the figures, and once the
// headline is the window's own rate, "not all decoding at once" denies the
// number it is attached to: 66.7 is by definition the span in which all
// four were decoding. In `ragged.png` the clause did its job, explaining
// why 4 × 17.4 is not 58.0; with the window leading, the product holds and
// what the reader needs instead is how long that span was — the one fact
// the image has nowhere else, since the PNG has no caveat band.
func raggedString(s *tape.RunSummary) string {
	return card.RaggedClause(s)
}

// decodeEyebrow is prefillEyebrow for the decode column (TTP-83, 2026-09-14): a
// run of several streams — concurrent, or sequential rounds at one connection,
// which takes the single-stream branch — whose shortest stream was too short to be a rate
// keeps "decode" — the aggregate is a rate — and says "short stream" in the
// same slot, for the same reason. The count and the floor are in the
// short_stream sentence on the text card and in -o json.
func decodeEyebrow(s *tape.RunSummary, label string) string {
	if !card.ShortStream(s) {
		return label
	}
	return label + " · short stream"
}

// prefillEyebrow qualifies the prefill column's figure in the slot the card
// already uses for this exact job (TTP-65, 2026-09-14).
//
// The decode column's eyebrow flips from "decode" to "sample" when the
// generation was too short to be a rate, and it is the only place on the image
// that says so. A prompt too short to be a prefill measurement is the same
// defect one column over — the ws four-stream tape printed 5.6 tok/s for a box
// whose honest prefill is 60 to 130 — so it is answered in the same slot
// rather than in a sentence squeezed under the number.
//
// The word is short because the eyebrow is small caps at 12.5 px and this card
// is read at half its pixel width in a timeline. The full sentence, with the
// count and the threshold, is on the text card and under
// short_prompt_for_prefill in -o json.
func prefillEyebrow(s *tape.RunSummary, label string) string {
	if !card.ShortPrompt(s) {
		return label
	}
	return label + " · short prompt"
}

// enginePrefillString is the time the engine itself spent on the prompt: the
// server's prompt_ms, mean over the streams. "" when the server reported none.
func enginePrefillString(s *tape.RunSummary) string {
	if s.Timings.PromptMs <= 0 {
		return ""
	}
	return "prefill " + formatMs(s.Timings.PromptMs)
}

// queueWaitString is what is left of TTFT once the engine's prefill is taken
// out: how long the request sat before the engine started on it. "" unless
// both figures were observed and the difference is not negative — a negative
// one means the client's stopwatch and the server's disagree about the same
// window, and clamping it to zero would print "queue 0 ms" for a run that was
// in fact not decomposed. Zero itself is a reading: nothing queued.
func queueWaitString(s *tape.RunSummary) string {
	t := s.Timings
	if t.TTFTMs <= 0 || t.PromptMs <= 0 || t.TTFTMs < t.PromptMs {
		return ""
	}
	return "queue " + strconv.FormatFloat(t.TTFTMs-t.PromptMs, 'f', 0, 64) + " ms"
}

func itlString(t tape.TimingsSummary) string {
	if t.ITLp50Ms <= 0 {
		return ""
	}
	return "ITL p50 " + formatMs(t.ITLp50Ms)
}

// promptTokens is the whole prompt the run sent, cached prefix included.
func promptTokens(s *tape.RunSummary) int {
	if s.Cache.PromptTotal > 0 {
		return s.Cache.PromptTotal
	}
	return s.Timings.PromptN + s.Timings.CacheN
}

// bandwidthString renders "≈ 91 GB/s · 10% of peak", or "≈ 112–206 GB/s ·
// 15–27% of peak" above one stream on a sparse MoE — concurrent streams share
// a forward pass, and the truth is a range until their routed experts' overlap
// is known (lead, 2026-09-16). The "≈" marks it as an estimate (spec §3.2 S6).
// Empty when it was not derivable.
//
// "of peak" is measured against the ceiling this run's placement allows; see
// internal/bandwidth, which owns the arithmetic for both renderers (TTP-34,
// 2026-09-13).
//
// This clause rides the single-stream hero's sub1 line (buildHero); a
// concurrent hero carries no bandwidth clause, its two sub-rows being spoken
// for by the stream arithmetic and the token count. The renderer computes
// through CombinedRange regardless, so wherever it is asked to print, it
// cannot disagree with the text card's figures.
func bandwidthString(s *tape.RunSummary) string {
	// TTP-67, 2026-09-14; the reasoning is in internal/card/verify.go. With a
	// draft model the weights were read once per verify step and not once per
	// accepted token, so the verify-step figure replaces the other rather than
	// joining it.
	if bps, ofPeak, ok := card.VerifyRAM(s); ok {
		out := "≈ " + formatGBs(bps) + " from RAM per verify step"
		if ofPeak > 0 {
			out += " · " + formatPct(ofPeak) + " of peak"
		}
		return out
	}
	// TTP-56, 2026-09-14; the reasoning is in internal/card/card.go's twin.
	if r, ok := bandwidth.RAM(s); ok {
		out := "≈ " + formatGBs(r.BytesPerSec) + " from RAM"
		// The percentage only when the CPU's share is provably the whole of
		// what it reads. A model whose router or shared expert might sit in
		// host RAM gives a figure that can only be low, and a ratio computed
		// from it would be a claim the tape cannot support (RAMSide.Exact).
		if r.Exact && r.OfPeak > 0 {
			out += " · " + formatPct(r.OfPeak) + " of peak"
		}
		return out
	}
	low, high, ok := bandwidth.CombinedRange(s)
	if !ok {
		return ""
	}
	out := "≈ " + formatGBsRange(low, high) + " effective"
	if rlow, rhigh, ok := bandwidth.OfPeakRange(s); ok {
		out += " · " + formatPctRange(rlow, rhigh) + " of peak"
	}
	return out
}

// ---------------------------------------------------------------- memory ---

func (c *content) buildMemory(s *tape.RunSummary) {
	p := s.Placement

	// The KV cache and the compute buffers are recorded for the whole run,
	// not per device, so each GPU is given the share of them that matches its
	// share of the weights. That is an apportionment, not a measurement; the
	// legend figures below the bar are the recorded totals, which are.
	var gpuWeights int64
	for _, d := range p.Devices {
		if d.Bytes > 0 && strings.HasPrefix(d.Device, devicePrefixGPU) {
			gpuWeights += d.Bytes
		}
	}
	splitVRAM := gpuWeights > 0 && (p.VRAMKVBytes > 0 || p.VRAMComputeBytes > 0)

	// How much of the CPU placement is in RAM. Derived by internal/tape so the
	// pane, the modal, the text card and this one print the same split from the
	// same sample; it is never re-derived here.
	res := tape.Residency(p, s.Memory.AtEnd)

	gpuN := 0
	for _, d := range p.Devices {
		if d.Bytes <= 0 {
			continue
		}
		if !strings.HasPrefix(d.Device, devicePrefixGPU) {
			// The host is the sand segment. It gets no VRAM breakdown — that
			// split is a GPU one — but it does get its own: how much of what
			// was placed here is actually in RAM (TTP-63).
			c.segments = append(c.segments, hostSegment(d, res))
			continue
		}
		col := gpuColors[gpuN%len(gpuColors)]
		gpuN++
		seg := segment{label: orUnknown(d.Device), bytes: d.Bytes, col: col}
		if splitVRAM {
			share := float64(d.Bytes) / float64(gpuWeights)
			kv := int64(float64(p.VRAMKVBytes) * share)
			compute := int64(float64(p.VRAMComputeBytes) * share)
			seg.bytes = d.Bytes + kv + compute
			seg.parts = []subseg{
				{bytes: d.Bytes, col: shade(col, shadeWeights)},
				{bytes: kv, col: shade(col, shadeKV)},
				{bytes: compute, col: shade(col, shadeCompute)},
			}
		}
		seg.size = formatGiB(seg.bytes)
		c.segments = append(c.segments, seg)
	}

	// Lesson 3: never-loaded comes from the GGUF header, not total-minus-RSS,
	// and it is part of the model's footprint, so it belongs in the same bar.
	if n := p.NeverLoadedBytes; n > 0 {
		c.segments = append(c.segments, segment{
			label: "never loaded", size: formatGiB(n), bytes: n, col: colSurface,
		})
	}
	c.hasPlaced = len(c.segments) > 0

	// The breakdown legend explains the lightness steps, so its swatches use
	// the first GPU's hue at the same three steps the bar uses.
	if splitVRAM {
		hue := gpuColors[0]
		c.breakdown = []legendEntry{
			{label: "weights " + formatGiB(gpuWeights), col: shade(hue, shadeWeights)},
			{label: "kv " + formatGiB(p.VRAMKVBytes), col: shade(hue, shadeKV)},
			{label: "compute " + formatGiB(p.VRAMComputeBytes), col: shade(hue, shadeCompute)},
		}
	}

	var placed int64
	for _, seg := range c.segments {
		placed += seg.bytes
	}

	// Lesson 3 again: both memory figures need a /proc view to mean anything.
	// Without one a zero would be a lie, so it prints "?".
	m := s.Memory
	c.hasProcMem = m.AtEnd.RSSBytes > 0

	rss := unknown
	if c.hasProcMem {
		rss = formatGiB(m.AtEnd.RSSBytes)
	}
	placedStr := unknown
	if placed > 0 {
		placedStr = formatGiB(placed)
	}
	// The shape first (2026-09-14): a model entirely on the cards and one
	// whose experts sit in system memory are two different machines, and the
	// reader needs that word before the bar under it means anything. Nothing
	// when the placement was not derived.
	//
	// The line is measured (2026-09-19): the pill row beside it can leave
	// less room than the whole summary needs, and the old character
	// truncation cut inside a number — "host RSS 1.…" — which invites the
	// reader to finish it wrongly. It gives up its trailing parts whole,
	// through pickWidest against the width drawPills leaves; the shape word
	// leads, so it is the last thing standing.
	//
	// It gives up the word before it gives up the number (vision,
	// 2026-09-19). A fifth pill pushes the row 178 px left, leaving 89 px
	// where "· 46.6 GiB placed" needs 143 — and the line dropped the clause
	// whole although "· 46.6 GiB" fits in 80, with no ellipsis, so a reader
	// of that card cannot tell a figure was ever there. A measured figure is
	// not surrendered to make room for a derived pill while a shorter
	// spelling of it would fit. The steps are written out rather than
	// derived by trailingPartsLine, which only knows how to drop whole
	// parts.
	placedPart := placedStr + " placed"
	word := layoutWord(s.Placement)
	var spellings []string
	add := func(parts ...string) {
		if len(parts) > 0 && parts[0] == "" {
			parts = parts[1:]
		}
		if len(parts) == 0 {
			return
		}
		spellings = append(spellings, strings.Join(parts, " · "))
	}
	add(word, placedPart, "host RSS "+rss)
	add(word, placedPart)
	if placed > 0 {
		// The number without its noun. Not offered when the figure is "?":
		// a bare "?" on this line names nothing.
		add(word, placedStr)
	}
	add(word)
	c.sum = fallbackLine{preferred: spellings[0], fallbacks: spellings[1:]}

	c.pills = []pill{majFaultPill(s, c.hasProcMem), cachePill(s.Cache), contendedPill(s.Contention)}
	// The throttle verdict and the conditions clause are the environment
	// line's two survivors (2026-09-19): claims about the machine, which the
	// card always prints, now as pills beside the bar rather than a strip
	// line. The GPU state, the os and the start time left with the line —
	// the os and the start time moved to the header, the temperatures and
	// power draws stay in the tape and on the run page.
	c.pills = append(c.pills, throttledPill(s))
	if cs := card.ConditionsShort(s); cs != "" {
		// Present only when it fired, and in Warn rather than the neutral
		// colour: the machine's operating point moved under the run, which
		// is a caution about every figure above it.
		c.pills = append(c.pills, pill{text: cs, col: colWarn})
	}
	if p, ok := answerCutPill(s); ok {
		// Only when it happened, so the ordinary card keeps three pills and
		// its layout. This is the PNG's form of the text card's
		// "! answer cut" warning line (TTP-20, 2026-09-13).
		c.pills = append(c.pills, p)
	}
	if p, ok := cappedPill(s); ok {
		// TTP-135 (2026-09-19): the text card's Context row carries the
		// clause; the pill is the PNG's form of it. Only when endings were
		// observed, so the ordinary card keeps its pill count.
		c.pills = append(c.pills, p)
	}
}

// trailingPartsLine builds a measured line whose fallbacks drop whole
// trailing parts one at a time, down to the first part alone: the memory
// summary's form of fallbackLine (the identity lines hand-pick theirs). If
// even the first part does not fit, pickWidest still returns it and
// textOpts truncates — the pre-2026-09-19 behaviour, correct as the last
// resort.
func trailingPartsLine(parts []string) fallbackLine {
	l := fallbackLine{preferred: strings.Join(parts, " · ")}
	for i := len(parts) - 1; i > 0; i-- {
		l.fallbacks = append(l.fallbacks, strings.Join(parts[:i], " · "))
	}
	return l
}

// hostSegment is the sand block of the placement bar, split into what is in
// RAM and what is not (TTP-63, 2026-09-14, user: "cpu 388g 찍혀있는데 이게
// ram이랑 nvme랑 구분이 안되나?").
//
// A CPU placement is llama.cpp's backend assignment, not a residency: the ws
// rig places 388.1 GiB on a 252 GB box, and the difference is read from the
// model file on every touch — which is what the maj/tok pill beside the bar
// counts. Drawn as one block, "CPU 388.1 GiB" says the opposite of what
// happened, and it was the first thing the user asked about the bar.
//
// The word for the second half is "disk", not "NVMe": what was observed is
// that the pages are not resident and come back from the file. The medium
// behind that file is not in the tape, and naming it would be printing
// something nobody measured.
//
// Three cases, because the split is not always readable. r.Ok false — no /proc
// sample, a remote server — keeps the block and the legend the card always had;
// zero paged is a whole block that says "ram", which is a different claim from
// "CPU" and worth making; and only a real shortfall draws two.
func hostSegment(d tape.DevicePlacement, r tape.HostResidency) segment {
	seg := segment{label: orUnknown(d.Device), size: formatGiB(d.Bytes), bytes: d.Bytes, col: colHost}
	// The residency is derived over every CPU device at once, so it describes
	// this segment only when this segment is the whole of it. A placement with
	// a second host device, or a non-CPU host backend, keeps the single block
	// rather than being given a split that was measured against other bytes.
	if d.Device != tape.DeviceCPU || !r.Ok || r.Placed != d.Bytes {
		return seg
	}
	if r.Paged <= 0 {
		seg.legend = []legendEntry{{label: "ram " + formatGiB(r.Resident), col: colHost}}
		return seg
	}
	resident, paged := shade(colHost, shadeResident), shade(colHost, shadePaged)
	seg.parts = []subseg{{bytes: r.Resident, col: resident}, {bytes: r.Paged, col: paged}}
	seg.legend = []legendEntry{
		{label: "ram " + formatGiB(r.Resident), col: resident},
		{label: "disk " + formatGiB(r.Paged), col: paged},
	}
	return seg
}

// answerCutPill flags a run that spent every predicted token thinking and
// never reached an answer. Bad, like the other pills that mean "this run does
// not say what you think it says": the decode rate beside it is real, the
// empty completion is not the tool's doing, and the fix is --n-predict.
func answerCutPill(s *tape.RunSummary) (pill, bool) {
	t := s.Timings
	if t.PredictedN <= 0 || t.ReasoningN < t.PredictedN {
		return pill{}, false
	}
	return pill{text: "answer cut", col: colBad}, true
}

// cappedPill counts the streams the token cap ended, against the endings
// observed (TTP-135, 2026-09-19) — "4 of 4 hit the cap", the text card's
// Context row clause in the pill row's voice, not a bare flag: a reader
// asking "was the whole run guillotined or one long-winded stream" needs
// the denominator. Warn rather than Bad: the capped tokens are real tokens,
// truncated by a limit the user set — a caution about where the run stopped,
// not a defect in the figures beside it. Silent when EndingsObserved is 0:
// an engine that never said why streams stopped leaves the count unreadable,
// and no pill may invent it.
func cappedPill(s *tape.RunSummary) (pill, bool) {
	if s.Limit.CappedStreams <= 0 || s.Limit.EndingsObserved <= 0 {
		return pill{}, false
	}
	// "cap 4 of 4", not the text card's whole sentence (lead, 2026-09-19).
	// The other four pills are a label and a value — maj/tok 0.0, cache warm
	// 25% hit, contended no, throttled no — and a sentence among them reads
	// as an alarm rather than a reading, on a card that states conditions
	// and does not warn. It is also the shortest form that keeps the
	// denominator, which the pill row needs: the row shares its line with
	// the placement summary, and the sentence pushed "46.6 GiB placed" —
	// an observed figure — off the card to make room for itself.
	return pill{
		text: fmt.Sprintf("cap %d of %d", s.Limit.CappedStreams, s.Limit.EndingsObserved),
		col:  colWarn,
		bg:   alpha(colDim, 0x2b),
	}, true
}

func majFaultPill(s *tape.RunSummary, hasProc bool) pill {
	if !hasProc {
		return pill{text: "maj/tok " + unknown, col: colFaint}
	}
	v := s.Memory.MajFaultsPerToken
	col := colDim
	switch {
	case v >= tape.ColdMajFaultsPerToken:
		col = colBad
	case v > 0:
		col = colWarn
	}
	return pill{text: "maj/tok " + formatFloat1(v), col: col}
}

func cachePill(c tape.CacheSummary) pill {
	label := string(c.Label)
	if label == "" {
		return pill{text: "cache " + unknown, col: colFaint}
	}
	col := colDim
	if c.Label == tape.CacheCold {
		col = colBad
	}
	if c.PromptTotal <= 0 {
		return pill{text: "cache " + label, col: col}
	}
	ratio := c.HitRatio
	if ratio == 0 && c.HitTokens > 0 {
		ratio = float64(c.HitTokens) / float64(c.PromptTotal)
	}
	return pill{text: "cache " + label + " " + formatPct(ratio) + " hit", col: col}
}

// contendedPill prints the label lesson 6 asks for. "no" is a claim that the
// machine was measured and found quiet, so a summary that carries no reading
// at all — no load average, no other GPU processes, no reasons, not flagged —
// prints "?" instead. A card that says "contended no" about a run nobody
// looked at is exactly the kind of unearned number the repo rule forbids.
func contendedPill(ci tape.ContentionInfo) pill {
	if !contentionObserved(ci) {
		return pill{text: "contended " + unknown, col: colFaint}
	}
	col := colDim
	if ci.Contended {
		col = colBad
	}
	return pill{text: "contended " + yesNo(ci.Contended), col: col}
}

// contentionObserved reports whether anything in ci came from a reading.
func contentionObserved(ci tape.ContentionInfo) bool {
	// A witness (TTP-36) is a reading even when every figure in it was quiet.
	return ci.Contended || ci.LoadAvg1 > 0 || ci.OtherGPUProcs > 0 || len(ci.Reasons) > 0 || len(ci.Witnesses) > 0
}

// ---------------------------------------------------------------- footer ---

// buildIdent derives the identity band (2026-09-19): the model, the rig and
// the engine, one line each at 26 px, each with the fallbacks it gives its
// elements up to when the whole does not fit the content width. The canvas
// does the measuring, in pickWidest, at draw time.
func (c *content) buildIdent(s *tape.RunSummary) {
	c.ident1 = modelIdent(s.Model)
	c.identSub = modelDetail(s.Model)
	c.ident2 = rigIdent(s.Host)
	c.ident3 = engineIdent(s.Server)
}

// modelIdent is line 1: the model that ran, its quant and the file's size.
// The name is card.ModelName — the variant directory for a shard set, the
// file's stem for one file, never the GGUF header's general.name (the rule
// modelFooterRows row 0 already carried; see card.ModelName for why).
//
// The quant part prints only when the name does not already carry it,
// compared case-blind (2026-09-19): the name is the file's stem or the
// variant directory, and both are named after the quant —
// "DeepSeek-V4.1-Flash-Q3_K_M-engramQ8-tokembdBF16" contains Q3_K_M just as
// "qwen3-30b-a3b-q4_k_m" contains Q4_K_M — so a second printing said the
// same thing twice on one of the few lines a feed-size reader can actually
// read, the same redundancy the header's model round removed. A repo-named
// directory ("DeepSeek-V4.1-Flash-engramQ8-tokembdBF16") keeps the part.
// The name itself is never stripped: the name is the name.
func modelIdent(m tape.ModelInfo) fallbackLine {
	name := orUnknown(card.ModelName(m))
	// The quant and the size are dropped when unobserved, the envLine rule:
	// a bare "?" in a joined list says nothing about which field is missing.
	// The name keeps its "?" — it is the line's subject, and a nameless card
	// must say so rather than print the quant alone.
	quant := omitUnknown(orUnknown(m.Quant))
	if quant != "" && containsFold(name, quant) {
		quant = ""
	}
	size := omitUnknown(formatFileGiB(m.FileBytes))
	return fallbackLine{
		preferred: joinParts(" · ", name, quant, size),
		fallbacks: []string{
			joinParts(" · ", name, quant),
			name,
		},
	}
}

// containsFold reports whether s contains sub, ignoring case. It is
// internal/card/model.go's twin (card.ModelNameQuant's quant rule), kept
// local by the same deliberate-duplicate rule format.go states for
// internal/card's formatters: exporting it from another track's package
// would be a wider change than the copy.
func containsFold(s, sub string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(sub))
}

// modelDetail is the sub-line under the model: what the model IS, in the
// number formats the old model column used (formatParamsB for the counts,
// plain integers for the layer and expert pair). Every element is dropped
// when unobserved — a dense model has no experts and no active-params
// figure, and simply prints fewer parts.
func modelDetail(m tape.ModelInfo) string {
	total := omitUnknown(formatParamsB(m.Params))
	active := ""
	if ap := activeParams(m); ap > 0 && ap != m.Params {
		active = formatParamsB(ap) + " active per token"
	}
	layers := ""
	if m.NLayers > 0 {
		layers = strconv.Itoa(m.NLayers) + " layers"
	}
	experts := ""
	if m.NExperts > 0 {
		// The pair modelShape printed, spelled out for a line that has the
		// room: "8 of 256 experts", or just the count when the used figure
		// was never read.
		if m.NExpertsUsed > 0 {
			experts = fmt.Sprintf("%d of %d experts", m.NExpertsUsed, m.NExperts)
		} else {
			experts = strconv.Itoa(m.NExperts) + " experts"
		}
	}
	return joinParts(" · ", total, active, m.Arch, layers, experts)
}

// activeParams is the parameter count a token actually touches. It is
// internal/publish/index.go's derivation character for character — the hub's
// index and the card must agree on the figure — duplicated here rather than
// exported from internal/publish, which is another track's package, by the
// same deliberate-duplicate rule format.go states for internal/card's
// formatters.
func activeParams(m tape.ModelInfo) int64 {
	if m.NExperts == 0 {
		return m.Params
	}
	if m.ActiveBytesPerToken > 0 && m.FileBytes > 0 && m.Params > 0 {
		return int64(math.Round(float64(m.Params) * float64(m.ActiveBytesPerToken) / float64(m.FileBytes)))
	}
	return 0
}

// rigIdent is line 2: the cards, the CPU and the RAM, from the same helpers
// the rig column used (rigGPUs, orUnknown, ramString). The core-count row
// left the card with the grid: cores are in the tape and on the run page,
// and at this size the CPU's own name is the part that names the machine.
//
// The fallback chain gives the CPU up in measured steps, and every step
// exists because of a measured case (2026-09-19, at the 15 px/char the bold
// face advances at 25 px — theme.go): the ws rig's preferred spelling is 82
// characters, 1230 px against the 1080 px content width, and dropping the
// vendor prefix (1170) or the core tail (1095) alone still does not fit —
// "Ryzen Threadripper PRO 5975WX" without either is 1035 px, the rung that
// keeps the machine's name on the two-card card. The hero recording's
// one-card form fits a rung higher and keeps its "AMD ". Below the CPU come
// the RAM, then the GPUs alone — the line's floor.
func rigIdent(h tape.HostInfo) fallbackLine {
	gpus := strings.Join(rigGPUs(h.GPUs), " + ")
	cpu := omitUnknown(orUnknown(h.CPU))
	ram := omitUnknown(ramString(h))
	return fallbackLine{
		preferred: joinParts(" · ", gpus, cpu, ram),
		fallbacks: []string{
			joinParts(" · ", gpus, stripCPUVendor(cpu), ram),
			joinParts(" · ", gpus, stripCoreSuffix(cpu), ram),
			joinParts(" · ", gpus, stripCPUVendor(stripCoreSuffix(cpu)), ram),
			joinParts(" · ", gpus, ram),
			gpus,
		},
	}
}

// cpuVendors are the leading vendor words stripCPUVendor drops, longest
// first: "Intel(R) Core(TM) " begins with "Intel(R) ", so the shorter entry
// checked first would leave a "Core(TM) " that names no machine either.
var cpuVendors = []string{"Intel(R) Core(TM) ", "Intel(R) ", "Intel ", "AMD "}

// stripCPUVendor drops a CPU name's leading vendor words — the rig line's
// rung between the preferred spelling and the core-stripped one (2026-09-19).
// The vendor word is the one part of the name that names no machine: every
// CPU AMD or Intel sells begins with it, and "Ryzen Threadripper PRO
// 5975WX" is the useful remainder. Like stripCoreSuffix it shortens and
// never invents: only a LEADING occurrence of only the vendor words goes, so
// a vendor word later in the string stays, and a name with no vendor prefix
// ("Apple M2 Max") comes back unchanged.
func stripCPUVendor(cpu string) string {
	for _, prefix := range cpuVendors {
		if strings.HasPrefix(cpu, prefix) {
			return cpu[len(prefix):]
		}
	}
	return cpu
}

// stripCoreSuffix drops a CPU name's core-count tail — " 32-Cores",
// " 96-Core", " 96-Core Processor" — the rig line's first fallback. The tail
// is the one part of the name the sub-line's figures already imply, and
// "AMD Ryzen Threadripper PRO 5975WX" still names the CPU without it.
//
// The tail is the string's END, digits and all: lscpu reports "5975WX
// 32-Cores", so matching " -Cores" alone never fires — the count sits between
// the space and the suffix. Anything that is not that shape (an Intel suffix
// like "CPU @ 3.00GHz", or a name with no count) comes back unchanged.
func stripCoreSuffix(cpu string) string {
	s := strings.TrimSuffix(cpu, " Processor")
	for _, suffix := range []string{"-Cores", "-Core"} {
		if !strings.HasSuffix(s, suffix) {
			continue
		}
		i := len(s) - len(suffix)
		for i > 0 && s[i-1] >= '0' && s[i-1] <= '9' {
			i--
		}
		// A count with nothing before it, or no space to cut at, is not a
		// core count the name carries as a tail.
		if i < len(s)-len(suffix) && i > 0 && s[i-1] == ' ' {
			return s[:i-1]
		}
	}
	return cpu
}

// engineIdent is line 3: the engine, its argument row and the context. The
// middle part is the row buildFooter's column 2 drew — the fa/ctk/ctv triple
// for a llama.cpp server, or an engine's own argv joined to one line rather
// than the two rows the old column wrapped it into (the identity line
// measures the whole thing itself). The b/ub/ngl row left the card with the
// grid; it is on the run page and in -o md.
func engineIdent(srv tape.ServerInfo) fallbackLine {
	eng := engineString(srv)
	var middle string
	if card.LlamaCPPFlags(srv) {
		// The five argument-starters distinguish "?" (no argv was read) from
		// "default" (a read argv did not set it); the same rule the strip
		// carried, on the one row that stayed.
		read := argvObserved(srv)
		f := srv.Flags
		middle = engineFlagRow([]string{"fa", "ctk", "ctv"},
			[]string{flagValue(f.FlashAttn, read), flagValue(f.CacheTypeK, read), flagValue(f.CacheTypeV, read)})
	} else {
		middle = strings.Join(srv.Flags.Other, " ")
		if middle == "" {
			middle = unknown
		}
	}
	// The context pair, dropped element by element when unobserved — a bare
	// "?" between separators is the thing joinParts exists to prevent, so the
	// pair is joined plainly and the outer joinParts drops it whole when
	// neither figure was read.
	var ctxSlots []string
	if srv.CtxSize > 0 {
		ctxSlots = append(ctxSlots, "ctx "+strconv.Itoa(srv.CtxSize))
	}
	if srv.NSlots > 0 {
		ctxSlots = append(ctxSlots, strconv.Itoa(srv.NSlots)+" slots")
	}
	tail := strings.Join(ctxSlots, " · ")
	return fallbackLine{
		preferred: joinParts(" · ", eng, middle, tail),
		fallbacks: []string{
			joinParts(" · ", eng, tail),
			eng,
		},
	}
}

// throttledPill is the environment line's throttle verdict as a pill
// (2026-09-19), the same terms contendedPill prints the contention verdict
// on: always printed, labelled, "?" when no GPU reading exists — "no" would
// be a claim nobody measured. "yes" is a caution in Warn, per the palette's
// emphasis contract (Bad is a run that is contended, cold or cut; a machine
// that throttled explains the rate rather than invalidating it).
func throttledPill(s *tape.RunSummary) pill {
	v := throttledString(s)
	col := colDim
	switch v {
	case "yes":
		col = colWarn
	case unknown:
		col = colFaint
	}
	return pill{text: "throttled " + v, col: col}
}

// engineFlagRow renders one row of the ENGINE column: "fa on · ctk q8_0 · ctv
// q8_0". When every flag in the row is at the server's default it says so once
// instead of three times: three "default"s measure 266px against a 255px
// column and would be cut mid-word, and the collapsed form is the same claim
// in fewer glyphs. Only the fa/ctk/ctv row can reach it — ngl is omitted, not
// defaulted, so its row always carries a value.
func engineFlagRow(names, values []string) string {
	allDefault := true
	for _, v := range values {
		if v != serverDefault {
			allDefault = false
			break
		}
	}
	if allDefault {
		return strings.Join(names, " · ") + " at defaults"
	}
	parts := make([]string, 0, len(names))
	for i, n := range names {
		parts = append(parts, n+" "+values[i])
	}
	return strings.Join(parts, " · ")
}

// modelFooterRows, wrapModelName, wrapEngineArgs and modelShape were deleted
// on 2026-09-19 with the footer grid they laid out: the identity band carries
// the same fields as three measured lines (buildIdent), and a line that does
// not fit gives its own parts up (modelIdent's fallbacks) rather than wrapping
// into a second row.

// rigGPUs collapses identical devices into "2× RTX 3090 24G", exactly as the
// text card's RIG line does.
func rigGPUs(gpus []tape.GPUInfo) []string {
	if len(gpus) == 0 {
		return []string{unknown}
	}
	type group struct {
		name string
		vram int64
		n    int
	}
	var order []*group
	seen := map[string]*group{}
	for _, g := range gpus {
		name := shortGPUName(g.Name)
		key := name + "|" + strconv.FormatInt(g.VRAMBytes, 10)
		e, ok := seen[key]
		if !ok {
			e = &group{name: name, vram: g.VRAMBytes}
			seen[key] = e
			order = append(order, e)
		}
		e.n++
	}
	out := make([]string, 0, len(order))
	for _, e := range order {
		label := orUnknown(e.name)
		if e.vram > 0 {
			label += fmt.Sprintf(" %.0fG", float64(e.vram)/gib)
		}
		if e.n > 1 {
			label = strconv.Itoa(e.n) + "× " + label
		}
		out = append(out, label)
	}
	return out
}

// shortGPUName drops the vendor prefixes nvidia-smi reports. It shortens, it
// never invents.
func shortGPUName(n string) string {
	n = strings.TrimSpace(n)
	for _, prefix := range []string{"NVIDIA GeForce ", "NVIDIA ", "GeForce "} {
		if strings.HasPrefix(n, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(n, prefix))
		}
	}
	return n
}

// coreString was deleted on 2026-09-19 with the footer grid's rig column: the
// core count is on the run page and in -o md, and the identity band's rig line
// spends its 26 px on the names.

func ramString(h tape.HostInfo) string {
	// An unread RAM size is "?", not "? GB": the unit would dress an
	// unobserved field up as a measurement with a missing digit.
	if h.RAMBytes <= 0 {
		if h.RAMSpeed != "" {
			return unknown + " · " + h.RAMSpeed
		}
		return unknown
	}
	s := fmt.Sprintf("%.0f GB", float64(h.RAMBytes)/gib)
	// RAM speed is omitted when unknown rather than printed as "?": it is a
	// nice-to-have detail, not one of the argument-settling fields.
	if h.RAMSpeed != "" {
		s += " " + h.RAMSpeed
	}
	return s
}

func engineString(srv tape.ServerInfo) string {
	kind := string(srv.Kind)
	if kind == "" || srv.Kind == tape.ServerUnknown {
		kind = unknown
	}
	if kind == unknown && srv.Build == "" && srv.Commit == "" {
		return unknown
	}
	var b strings.Builder
	b.WriteString(kind)
	switch {
	case srv.Build != "" && srv.Commit != "":
		b.WriteString(" " + srv.Build + " (" + srv.Commit + ")")
	case srv.Build != "":
		b.WriteString(" " + srv.Build)
	case srv.Commit != "":
		b.WriteString(" " + bareCommit(srv))
	default:
		b.WriteString(" " + unknown)
	}
	return b.String()
}

// bareCommit is internal/card/card.go's rule character for character: a commit
// with no build number beside it keeps its parentheses, except on
// ik_llama.cpp, which has no build number to be set apart from (TTP-33).
func bareCommit(srv tape.ServerInfo) string {
	if srv.Kind == tape.ServerIKLlama {
		return srv.Commit
	}
	return "(" + srv.Commit + ")"
}

func osString(h tape.HostInfo) string {
	return orUnknown(strings.TrimSpace(h.OS + " " + h.Kernel))
}

// throttledString is "?" until at least one GPU reading exists: with no
// sample, "no" would be a claim we never measured. The verdict itself is the
// text card's own (card.GPUThrottled), so the image cannot print "yes" where
// the card prints "no".
func throttledString(s *tape.RunSummary) string {
	if len(s.GPUsAtEnd) == 0 {
		return unknown
	}
	for _, g := range s.GPUsAtEnd {
		if card.GPUThrottled(g) {
			return "yes"
		}
	}
	return "no"
}

// contendedString, gpuStateString, tempString and powerString were deleted on
// 2026-09-19 with the environment line they printed: the verdicts live in the
// pills beside the placement bar, and the GPU state stays in the tape, on the
// run page and in -o md, where the card's 1080 px no longer has to hold it.

// startedString formats the recorded start time. It never reads the clock: a
// replayed tape must render the moment it was recorded.
func startedString(s *tape.RunSummary) string {
	if s.StartedAt.IsZero() {
		return unknown
	}
	return s.StartedAt.UTC().Format("2006-01-02 15:04:05 MST")
}

// ----------------------------------------------------------------- flags ---

// flagsLine was deleted on 2026-09-19 with the flag strip: the five
// argument-starters' always-printed "?" rule survives on the identity band's
// engine line (engineIdent carries the fa/ctk/ctv row), and the rest of the
// argv is on the run page and in -o md, where it wraps.

// layoutWord names where the weights live, in the reader's words rather than
// the schema's. The text and the rules are internal/tui's twin (layoutWord
// there) and tape.Layout's doc says why unified memory is not among them yet.
func layoutWord(p tape.PlacementSummary) string {
	switch tape.Layout(p).Shape {
	case tape.ShapeVRAM:
		return "all in VRAM"
	case tape.ShapeHost:
		return "all in host RAM"
	case tape.ShapeOffload:
		return "offloaded"
	}
	return ""
}
