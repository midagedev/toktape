package png

import (
	"fmt"
	"image/color"
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
	version   string // "v0.1.0"
	runID     string
	modelFile string
	modelSub  string // "UD-Q4_K_M · 19.83 GiB · qwen3moe"

	// Hero.
	left, right heroCol

	// Memory.
	segments   []segment
	breakdown  []legendEntry // weights / kv / compute, when the split is known
	placedSum  string        // "23.7 GiB placed · host RSS 3.4 GiB"
	pills      []pill
	hasPlaced  bool
	hasProcMem bool

	// Footer grid: three columns of footerRows lines, first line emphasised.
	cols [footerCols]footerCol

	// Bottom strip.
	env   string // the environment rows the footer grid gave up (TTP-54)
	flags string
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
}

type footerCol struct {
	label string
	rows  [footerRows]string
}

// build derives the whole card from the summary. It takes the canvas because
// the footer's model column measures its rows before it commits to them
// (modelFooterRows) and the measuring is the canvas's job.
func build(cv *canvas, s *tape.RunSummary) *content {
	c := &content{}
	c.buildHeader(s)
	c.buildHero(s)
	c.buildMemory(s)
	c.buildFooter(cv, s)
	c.env = envLine(s)
	c.flags = flagsLine(s.Server)
	return c
}

// ---------------------------------------------------------------- header ---

func (c *content) buildHeader(s *tape.RunSummary) {
	c.version = versionString(s)
	c.runID = orUnknown(s.ID)
	// The header names the file, not the GGUF's general.name. For a split set
	// that is the variant label rather than part one's name, which every
	// variant of the model shares (TTP-32); card.ModelLabel returns the file
	// name unchanged for a single file.
	c.modelFile = orUnknown(card.ModelLabel(s.Model))
	c.modelSub = joinParts(" · ",
		orUnknown(s.Model.Quant),
		shardsPart(s.Model),
		formatFileGiB(s.Model.FileBytes),
		s.Model.Arch,
	)
}

// shardsPart is the "9 shards" element of the header sub-line, empty for a
// single file so joinParts drops it and the line is what it always was.
func shardsPart(m tape.ModelInfo) string {
	if !card.Sharded(m) {
		return ""
	}
	return strconv.Itoa(m.Shards) + " shards"
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
		perStream := fmt.Sprintf("%d × %s per stream",
			streams, formatRateUnit(a.PerStreamPredictedPerSecond))
		c.left = heroCol{
			eyebrow: decodeEyebrow(s, "aggregate "+label),
			number:  formatRate(a.AggregatePredictedPerSecond),
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
			// What goes when the column is full is the qualification, not
			// the figures: the per-stream rate and the queue note are what
			// another rig is compared against.
			sub1Fallbacks: []string{
				joinParts(" · ", perStream, queuedString(s)),
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
		c.right = heroCol{
			eyebrow: prefillEyebrow(s, "prefill"),
			number:  formatRate(t.PromptPerSecond),
			unit:    "tok/s",
			sub1:    "TTFT " + formatMs(t.TTFTMs),
			sub2: joinParts(" · ",
				formatInt(prompt)+" prompt tokens",
				contextString(s),
			),
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
func raggedString(s *tape.RunSummary) string {
	if !card.RaggedAggregate(s) {
		return ""
	}
	return "not all decoding at once"
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
	c.placedSum = placedStr + " placed · host RSS " + rss
	if word := layoutWord(s.Placement); word != "" {
		c.placedSum = word + " · " + c.placedSum
	}

	c.pills = []pill{majFaultPill(s, c.hasProcMem), cachePill(s.Cache), contendedPill(s.Contention)}
	if p, ok := answerCutPill(s); ok {
		// Only when it happened, so the ordinary card keeps three pills and
		// its layout. This is the PNG's form of the text card's
		// "! answer cut" warning line (TTP-20, 2026-09-13).
		c.pills = append(c.pills, p)
	}
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

func (c *content) buildFooter(cv *canvas, s *tape.RunSummary) {
	c.cols[0] = footerCol{label: "model", rows: modelFooterRows(cv, s.Model)}

	c.cols[1] = footerCol{label: "rig", rows: [footerRows]string{
		strings.Join(rigGPUs(s.Host.GPUs), " + "),
		orUnknown(s.Host.CPU),
		coreString(s.Host),
		ramString(s.Host),
	}}

	f := s.Server.Flags
	// An engine's argv is its own, so the two flag rows carry it verbatim
	// instead of the llama.cpp token set; the kind and the context row are the
	// same for everyone (2026-09-15, ExLlamaV3).
	var row1, row2 string
	if card.LlamaCPPFlags(s.Server) {
		// The five argument-starters distinguish "?" (no argv was read) from
		// "default" (a read argv did not set it); ngl is not one of the five.
		read := argvObserved(s.Server)
		row1 = engineFlagRow([]string{"fa", "ctk", "ctv"},
			[]string{flagValue(f.FlashAttn, read), flagValue(f.CacheTypeK, read), flagValue(f.CacheTypeV, read)})
		row2 = engineFlagRow([]string{"b", "ub", "ngl"},
			[]string{flagValue(f.Batch, read), flagValue(f.UBatch, read), orUnknown(f.NGL)})
	} else {
		args := strings.Join(f.Other, " ")
		if args == "" {
			args = unknown
		}
		row1, row2 = wrapEngineArgs(cv, args)
	}
	c.cols[2] = footerCol{label: "engine", rows: [footerRows]string{
		engineString(s.Server),
		row1,
		row2,
		joinParts(" · ", "ctx "+formatInt(s.Server.CtxSize), "slots "+formatInt(s.Server.NSlots)),
	}}
}

// envLine is what used to be the footer's fourth column: the os, the throttle
// and contention verdicts, the GPUs' state at the end and when the run started
// (TTP-54, 2026-09-14). Growing the body type left room for three columns, and
// these are the four rows that settle no argument, so they became one line on
// the bottom strip instead — where they are set larger than the column ever
// gave them.
//
// Two rules shape the order. The throttle and contention verdicts are always
// printed, labelled, and keep their "?" — lesson 6, they are claims about a
// machine and an unread one must say so. The rest are dropped when unobserved
// rather than printed as a bare "?" in a list with nothing to say which field
// it is. And the GPU state goes last because it is the only part that grows
// with the rig: on a board with eight cards it is what the cut eats first.
func envLine(s *tape.RunSummary) string {
	return joinParts(" · ",
		omitUnknown(osString(s.Host)),
		"throttled "+throttledString(s),
		"contended "+contendedString(s.Contention),
		// TTP-57, 2026-09-14: the machine's operating point moved under the
		// run. It sits with the other two claims about the machine — capped,
		// busy, changed — and is dropped like every other unobserved element
		// when it did not, rather than printed as a "?".
		card.ConditionsShort(s),
		omitUnknown(startedString(s)),
		omitUnknown(gpuStateString(s.GPUsAtEnd)),
	)
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

// modelFooterRows is the four-row model column of the footer grid.
//
// 2026-09-15 (user: "모델이 다 실제값으로 찍혀야해"): row 0 is card.ModelName —
// the variant directory for a shard set, the file's stem for one file — never
// the GGUF header's general.name, which a re-quantised variant keeps from the
// base model it was cut from. The variantTag the size row used to close with
// is gone with it: row 0 is the variant now, and printing parts of it twice is
// what this change removed.
//
// A name wider than the column — the real recording's 45-character directory
// measures 376 px against 346 — wraps at the last "-", "_" or "." that lets
// row 0 keep its separator, and the two rows that frees spend as one: the
// quant, the shard count, the size and the parameter count join with " · " so
// the architecture row stays the last row. The joined row gives its parts up
// in order of least importance when it is too wide — the parameter count
// first, then the shard count — and never the quant or the size. A name too
// wide for any separator falls to the canvas's own ellipsis, the same cut
// every other footer cell takes.
//
// Widths are measured, not estimated, so this takes the canvas (TTP-32's
// lesson: the column is 346 px and a 40-character directory is 320 of them).
func modelFooterRows(cv *canvas, m tape.ModelInfo) [footerRows]string {
	name := orUnknown(card.ModelName(m))
	shape := modelShape(m)
	quant := orUnknown(m.Quant)
	size := formatFileGiB(m.FileBytes)
	params := formatParamsB(m.Params)
	fits := footerColW - footerSlack

	if cv.measure(name, stSmallB) <= fits {
		if !card.Sharded(m) {
			return [footerRows]string{name, quant, joinParts(" · ", size, params), shape}
		}
		// The TTP-32 layout minus the variant tag: the shard count and the
		// parameter count join the quant, the size row is the size alone.
		return [footerRows]string{name, joinParts(" · ", quant, shardsPart(m), params), size, shape}
	}

	// The wrap: rows 0 and 1 spell the name across the separator, and the
	// middle two rows become one.
	row0, rest := wrapModelName(cv, name)
	mid := joinParts(" · ", quant, shardsPart(m), size, params)
	if cv.measure(mid, stSmall) > fits {
		mid = joinParts(" · ", quant, shardsPart(m), size)
	}
	return [footerRows]string{row0, rest, mid, shape}
}

// wrapModelName splits name at the last "-", "_" or "." whose prefix (separator
// included, so the name reads as folded rather than glued) still fits the
// column. A name no separator helps is returned whole and the canvas's own
// ellipsis takes it, the same cut every other footer cell takes.
func wrapModelName(cv *canvas, name string) (row0, rest string) {
	fits := footerColW - footerSlack
	rs := []rune(name)
	best := -1
	for i, r := range rs {
		if r != '-' && r != '_' && r != '.' {
			continue
		}
		if cv.measure(string(rs[:i+1]), stSmallB) <= fits {
			best = i
		}
	}
	if best < 0 {
		return name, ""
	}
	return string(rs[:best+1]), string(rs[best+1:])
}

// wrapEngineArgs is wrapModelName for an engine's argv: rows 1 and 2 of the
// footer's engine column spell it across a space when it is wider than the
// column, measured (never estimated — TTP-32's lesson) at the row style. The
// break lands before the space, so row 1 carries whole arguments; a tail still
// too wide for row 2 falls to the canvas's own ellipsis, the same cut every
// other footer cell takes. An argv that fits is returned whole with an empty
// second row (2026-09-15, ExLlamaV3).
func wrapEngineArgs(cv *canvas, args string) (row1, row2 string) {
	fits := footerColW - footerSlack
	if cv.measure(args, stSmall) <= fits {
		return args, ""
	}
	rs := []rune(args)
	best := -1
	for i, r := range rs {
		if r != ' ' {
			continue
		}
		if cv.measure(string(rs[:i]), stSmall) <= fits {
			best = i
		}
	}
	if best < 0 {
		return args, ""
	}
	return string(rs[:best]), string(rs[best+1:])
}

// modelShape is the architecture line: dense models have no expert counts, so
// the parts that were not observed are dropped rather than printed as "?".
func modelShape(m tape.ModelInfo) string {
	parts := []string{m.Arch}
	if m.NLayers > 0 {
		parts = append(parts, strconv.Itoa(m.NLayers)+"L")
	}
	if m.NExperts > 0 {
		e := strconv.Itoa(m.NExperts) + " exp"
		if m.NExpertsUsed > 0 {
			e = fmt.Sprintf("%d/%d exp", m.NExpertsUsed, m.NExperts)
		}
		parts = append(parts, e)
	}
	return joinParts(" · ", parts...)
}

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

func coreString(h tape.HostInfo) string {
	switch {
	case h.CPUCores > 0 && h.CPUThreads > 0:
		return fmt.Sprintf("%dC / %dT", h.CPUCores, h.CPUThreads)
	case h.CPUCores > 0:
		return fmt.Sprintf("%dC", h.CPUCores)
	case h.CPUThreads > 0:
		return fmt.Sprintf("%dT", h.CPUThreads)
	}
	return unknown
}

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

// contendedString is contendedPill's text form for the footer grid.
func contendedString(ci tape.ContentionInfo) string {
	if !contentionObserved(ci) {
		return unknown
	}
	return yesNo(ci.Contended)
}

func gpuStateString(gs []tape.GPUSample) string {
	if len(gs) == 0 {
		return unknown
	}
	parts := make([]string, 0, len(gs))
	for _, g := range gs {
		parts = append(parts, fmt.Sprintf("GPU%d %s %s", g.Index, tempString(g.TempC), powerString(g)))
	}
	return strings.Join(parts, " · ")
}

func tempString(c float64) string {
	if c <= 0 {
		return unknown + "°C"
	}
	return strconv.FormatFloat(c, 'f', 0, 64) + "°C"
}

// powerString is one GPU's power figure in the strip's compact spacing: the
// draw against the limit when both were read, the draw alone on a tape
// recorded before the limit was (2026-09-15). Mirrors internal/card's
// powerString figure for figure — the two renderings must agree.
func powerString(g tape.GPUSample) string {
	draw := unknown
	if g.PowerW > 0 {
		draw = strconv.FormatFloat(g.PowerW, 'f', 0, 64)
	}
	if g.PowerLimitW <= 0 {
		return draw + "W"
	}
	return draw + " of " + strconv.FormatFloat(g.PowerLimitW, 'f', 0, 64) + "W"
}

// startedString formats the recorded start time. It never reads the clock: a
// replayed tape must render the moment it was recorded.
func startedString(s *tape.RunSummary) string {
	if s.StartedAt.IsZero() {
		return unknown
	}
	return s.StartedAt.UTC().Format("2006-01-02 15:04:05 MST")
}

// ----------------------------------------------------------------- flags ---

// flagsLine renders the flag strip. The five argument-starters (-fa, -b, -ub,
// -ctk, -ctv) are always printed and show "?" when unobserved — they are the
// ones that end comment threads (docs/research/02-sharing-artifacts.md §5.2).
// The rest are omitted when empty. The -ot group goes last because it is the
// only part that can be arbitrarily long, so truncation eats it first.
//
// An engine's argv is not llama.cpp's, so the strip carries the engine's own
// arguments joined with single spaces — the way they were typed — and "?" when
// it named none (2026-09-15, ExLlamaV3).
func flagsLine(srv tape.ServerInfo) string {
	if !card.LlamaCPPFlags(srv) {
		if len(srv.Flags.Other) == 0 {
			return unknown
		}
		return strings.Join(srv.Flags.Other, " ")
	}
	f := srv.Flags
	read := argvObserved(srv)
	var base []string
	if f.NGL != "" {
		base = append(base, "-ngl "+f.NGL)
	}
	base = append(base,
		"-fa "+flagValue(f.FlashAttn, read),
		"-b "+flagValue(f.Batch, read),
		"-ub "+flagValue(f.UBatch, read),
		"-ctk "+flagValue(f.CacheTypeK, read),
		"-ctv "+flagValue(f.CacheTypeV, read),
	)
	if f.LoadMode != "" {
		base = append(base, "--load-mode "+f.LoadMode)
	}
	if f.CPUMoE != "" {
		// The field holds either a bare count or the verbatim flag.
		if strings.HasPrefix(f.CPUMoE, "-") {
			base = append(base, f.CPUMoE)
		} else {
			base = append(base, "-ncmoe "+f.CPUMoE)
		}
	}
	if f.Threads != "" {
		base = append(base, "-t "+f.Threads)
	}
	// Speculative decoding (TTP-30). These used to reach the card inside
	// Other, because the parser did not name them; now that it does, they are
	// printed here rather than dropped — a flag that was on the command line
	// belongs on the FLAGS line. The Draft row in the speed section repeats
	// the model and the block size because it needs them beside the acceptance
	// rate, but --draft-min and --draft-p-min have no other home at all.
	if f.DraftModel != "" {
		base = append(base, "-md "+f.DraftModel)
	}
	if f.DraftMax != "" {
		base = append(base, "--draft-max "+f.DraftMax)
	}
	if f.DraftMin != "" {
		base = append(base, "--draft-min "+f.DraftMin)
	}
	if f.DraftPMin != "" {
		base = append(base, "--draft-p-min "+f.DraftPMin)
	}
	base = append(base, f.Other...)
	for _, p := range f.OverrideTens {
		if p == "" {
			continue
		}
		base = append(base, "-ot "+p)
	}
	return strings.Join(base, "  ")
}

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
