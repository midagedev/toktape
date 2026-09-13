package png

import (
	"fmt"
	"image/color"
	"strconv"
	"strings"

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

	// Footer grid: four columns of footerRows lines, first line emphasised.
	cols [footerCols]footerCol

	// Bottom strip.
	flags string
}

type heroCol struct {
	eyebrow string
	number  string
	unit    string
	sub1    string
	sub2    string
}

type segment struct {
	label string // "GPU0"
	size  string // "6.8 GiB"
	bytes int64
	col   color.RGBA
	// parts subdivides a GPU segment into weights | kv cache | compute
	// buffers, the three-way VRAM split docs/research/01 ranks third among
	// the things people cannot see today. Empty when the placement did not
	// report the split, or for host RAM, where the split is not VRAM.
	parts []subseg
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

// build derives the whole card from the summary.
func build(s *tape.RunSummary) *content {
	c := &content{}
	c.buildHeader(s)
	c.buildHero(s)
	c.buildMemory(s)
	c.buildFooter(s)
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
	// "decode" rate, and the eyebrow is the only place that says so.
	label := "decode"
	if t.DecodeLabel == "sample" {
		label = "sample"
	}
	if concurrent {
		streams := a.Streams
		if streams == 0 {
			streams = s.Concurrency
		}
		c.left = heroCol{
			eyebrow: "aggregate " + label,
			number:  formatRate(a.AggregatePredictedPerSecond),
			unit:    "tok/s",
			// The queue note rides on the per-stream line because that is
			// the figure it qualifies: streams that waited for a slot were
			// partly serialised, and their per-stream rate is not the rate
			// of a run that fitted the server.
			sub1: joinParts(" · ",
				fmt.Sprintf("%d × %s per stream",
					streams, formatRateUnit(a.PerStreamPredictedPerSecond)),
				queuedString(s)),
			sub2: joinParts(" · ",
				formatInt(a.TotalPredictedN)+" tokens",
				formatSeconds(a.WallMs)+" wall",
				failedStreams(a),
			),
		}
	} else {
		c.left = heroCol{
			eyebrow: label,
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

	// A speculative run replaces the decode column's second sub-line with the
	// draft clause (TTP-30, 2026-09-13). The frame is a fixed 1200×675 and
	// nothing else may move, so the clause takes a row rather than adding one.
	// It takes the whole row because it measures 416 px of the column's 504,
	// and appended to the token count it would be cut through "accepted".
	// Token count, ms and ITL stay on the text card. A failed-streams note is
	// a warning, not a figure, so it is kept.
	if d := draftString(s); d != "" {
		if concurrent {
			c.left.sub2 = joinParts(" · ", d, failedStreams(a))
		} else {
			c.left.sub2 = d
		}
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
			eyebrow: "aggregate prefill",
			number:  formatRate(a.AggregatePromptPerSecond),
			unit:    "tok/s",
			// No "N ×" here, unlike decode: prefill is batched server-wide, so
			// N × the per-request prompt rate is not the aggregate, and printing
			// it as a product was false arithmetic on the card (lead, 2026-09-13).
			sub1: fmt.Sprintf("%s per stream", formatRateUnit(t.PromptPerSecond)),
			sub2: joinParts(" · ",
				"TTFT p50 "+formatMs(a.TTFTp50Ms),
				"p95 "+formatMs(a.TTFTp95Ms),
				formatInt(a.TotalPromptN)+" prompt tok",
			),
		}
	} else {
		c.right = heroCol{
			eyebrow: "prefill",
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
// "draft DSpark-0.6B-Q8_0.gguf · n_max 3 · 60% accepted". Empty when the
// server reported no draft figure. The counts stay on the text card, and the
// rules are its rules: an unread model is "?", and zero drafted is "0 drafted"
// rather than a rate over nothing.
func draftString(s *tape.RunSummary) string {
	t := s.Timings
	if t.DraftN == nil {
		return ""
	}
	f := s.Server.Flags
	rate := "0 drafted"
	if *t.DraftN > 0 {
		accepted := 0
		if t.DraftNAccepted != nil {
			accepted = *t.DraftNAccepted
		}
		rate = formatPct(float64(accepted)/float64(*t.DraftN)) + " accepted"
	}
	return joinParts(" · ", "draft "+orUnknown(f.DraftModel), "n_max "+orUnknown(f.DraftMax), rate)
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

// bandwidthString renders "≈ 91 GB/s · 10% of peak". The "≈" marks it as an
// estimate (spec §3.2 S6). Empty when it was not derivable.
func bandwidthString(s *tape.RunSummary) string {
	if s.Timings.EffectiveBandwidthBytesPerSec <= 0 {
		return ""
	}
	out := "≈ " + formatGBs(s.Timings.EffectiveBandwidthBytesPerSec) + " effective"
	var peak int64
	for _, g := range s.Host.GPUs {
		peak += g.PeakBandwidthBytesPerSec
	}
	if peak > 0 {
		ratio := float64(s.Timings.EffectiveBandwidthBytesPerSec) / float64(peak)
		out += " · " + formatPct(ratio) + " of peak"
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

	gpuN := 0
	for _, d := range p.Devices {
		if d.Bytes <= 0 {
			continue
		}
		if !strings.HasPrefix(d.Device, devicePrefixGPU) {
			// Host RAM is the warm hue and is not subdivided: the three-way
			// split is a VRAM breakdown.
			c.segments = append(c.segments, segment{
				label: orUnknown(d.Device), size: formatGiB(d.Bytes), bytes: d.Bytes, col: colAmber,
			})
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
	c.placedSum = placedStr + " placed · host RSS " + rss

	c.pills = []pill{majFaultPill(s, c.hasProcMem), cachePill(s.Cache), contendedPill(s.Contention)}
	if p, ok := answerCutPill(s); ok {
		// Only when it happened, so the ordinary card keeps three pills and
		// its layout. This is the PNG's form of the text card's
		// "! answer cut" warning line (TTP-20, 2026-09-13).
		c.pills = append(c.pills, p)
	}
}

// answerCutPill flags a run that spent every predicted token thinking and
// never reached an answer. Red, like the other pills that mean "this run does
// not say what you think it says": the decode rate beside it is real, the
// empty completion is not the tool's doing, and the fix is --n-predict.
func answerCutPill(s *tape.RunSummary) (pill, bool) {
	t := s.Timings
	if t.PredictedN <= 0 || t.ReasoningN < t.PredictedN {
		return pill{}, false
	}
	return pill{text: "answer cut", col: colRed}, true
}

func majFaultPill(s *tape.RunSummary, hasProc bool) pill {
	if !hasProc {
		return pill{text: "maj/tok " + unknown, col: colFaint}
	}
	v := s.Memory.MajFaultsPerToken
	col := colGreen
	switch {
	case v >= tape.ColdMajFaultsPerToken:
		col = colRed
	case v > 0:
		col = colAmber
	}
	return pill{text: "maj/tok " + formatFloat1(v), col: col}
}

func cachePill(c tape.CacheSummary) pill {
	label := string(c.Label)
	if label == "" {
		return pill{text: "cache " + unknown, col: colFaint}
	}
	col := colGreen
	if c.Label == tape.CacheCold {
		col = colRed
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
	col := colGreen
	if ci.Contended {
		col = colRed
	}
	return pill{text: "contended " + yesNo(ci.Contended), col: col}
}

// contentionObserved reports whether anything in ci came from a reading.
func contentionObserved(ci tape.ContentionInfo) bool {
	return ci.Contended || ci.LoadAvg1 > 0 || ci.OtherGPUProcs > 0 || len(ci.Reasons) > 0
}

// ---------------------------------------------------------------- footer ---

func (c *content) buildFooter(s *tape.RunSummary) {
	c.cols[0] = footerCol{label: "model", rows: modelFooterRows(s.Model)}

	c.cols[1] = footerCol{label: "rig", rows: [footerRows]string{
		strings.Join(rigGPUs(s.Host.GPUs), " + "),
		orUnknown(s.Host.CPU),
		coreString(s.Host),
		ramString(s.Host),
	}}

	f := s.Server.Flags
	// The five argument-starters distinguish "?" (no argv was read) from
	// "default" (a read argv did not set it); ngl is not one of the five.
	read := argvObserved(s.Server)
	c.cols[2] = footerCol{label: "engine", rows: [footerRows]string{
		engineString(s.Server),
		engineFlagRow([]string{"fa", "ctk", "ctv"},
			[]string{flagValue(f.FlashAttn, read), flagValue(f.CacheTypeK, read), flagValue(f.CacheTypeV, read)}),
		engineFlagRow([]string{"b", "ub", "ngl"},
			[]string{flagValue(f.Batch, read), flagValue(f.UBatch, read), orUnknown(f.NGL)}),
		joinParts(" · ", "ctx "+formatInt(s.Server.CtxSize), "slots "+formatInt(s.Server.NSlots)),
	}}

	c.cols[3] = footerCol{label: "environment", rows: [footerRows]string{
		osString(s.Host),
		"throttled " + throttledString(s) + " · contended " + contendedString(s.Contention),
		gpuStateString(s.GPUsAtEnd),
		startedString(s),
	}}
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

// modelDisplayName is what the footer's model column calls the model: the
// GGUF's own general.name when it has one, else the file name — or, for a
// split set, the variant label the text card's MODEL line uses, because part
// one's name is the same string for every variant of the model (TTP-32).
// card.ModelLabel returns the file name unchanged for a single file, so this
// is byte-identical to what it was for every non-sharded card.
func modelDisplayName(m tape.ModelInfo) string {
	if strings.TrimSpace(m.Name) != "" {
		return m.Name
	}
	return orUnknown(card.ModelLabel(m))
}

// modelFooterRows is the four-row model column of the footer grid.
//
// A split set (TTP-32) spends its middle two rows differently. Two variants of
// one model are hard-linked side by side and share general.name as well as
// every file name, so the directory can be the only thing on the whole card
// that says which of them ran, and it has to be readable: the part count joins
// the quant, the parameter count goes up to join it, and the size row closes
// with the variant. A single-file model is untouched — every row is the string
// it always was — and the architecture row never moves.
//
// The column is 255px and a 40-character directory does not fit beside a size
// and a parameter count; it was measured truncating to "DeepSeek-V4.1-F…",
// which is the one thing on the card that must not be cut. variantTag drops the
// part the row above already says.
func modelFooterRows(m tape.ModelInfo) [footerRows]string {
	quant := orUnknown(m.Quant)
	size := joinParts(" · ", formatFileGiB(m.FileBytes), formatParamsB(m.Params))
	if card.Sharded(m) {
		quant = joinParts(" · ", orUnknown(m.Quant), shardsPart(m), formatParamsB(m.Params))
		size = joinParts(" · ", formatFileGiB(m.FileBytes), variantTag(m))
	}
	return [footerRows]string{modelDisplayName(m), quant, size, modelShape(m)}
}

// variantTag is the part of the model's directory the rest of the column does
// not already say: "DeepSeek-V4.1-Flash-engramQ8-tokembdBF16" beside a model
// called DeepSeek V4.1 Flash is "engramQ8-tokembdBF16", which is exactly what
// separates the hard-linked variants of one model set. It is a substring of the
// recorded directory, never a rewrite of it, and the text card and the tape
// keep the directory whole. A directory that shares no prefix with the file
// name is printed as it stands.
func variantTag(m tape.ModelInfo) string {
	stem := card.ModelStem(m.FileName)
	if stem == "" || !strings.HasPrefix(m.Dir, stem) {
		return m.Dir
	}
	return strings.TrimLeft(strings.TrimPrefix(m.Dir, stem), "-_. ")
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
// sample, "no" would be a claim we never measured.
func throttledString(s *tape.RunSummary) string {
	if len(s.GPUsAtEnd) == 0 {
		return unknown
	}
	for _, g := range s.GPUsAtEnd {
		if g.Throttled {
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
		parts = append(parts, fmt.Sprintf("GPU%d %s %s", g.Index, tempString(g.TempC), powerString(g.PowerW)))
	}
	return strings.Join(parts, " · ")
}

func tempString(c float64) string {
	if c <= 0 {
		return unknown + "°C"
	}
	return strconv.FormatFloat(c, 'f', 0, 64) + "°C"
}

func powerString(w float64) string {
	if w <= 0 {
		return unknown + "W"
	}
	return strconv.FormatFloat(w, 'f', 0, 64) + "W"
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
func flagsLine(srv tape.ServerInfo) string {
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
