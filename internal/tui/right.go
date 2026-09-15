package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/midagedev/toktape/internal/card"
	"github.com/midagedev/toktape/internal/tape"
)

// rightPane renders the machine: where the model sits, what the process is
// actually touching, how fast the server is answering, and what the host's
// resources were doing while it did.
//
// It always returns exactly rows lines of exactly cw columns. Sections are
// ordered so the measured result leads (user, 2026-09-15): speed first, then
// the machine that produced it — placement, memory, resources.
func rightPane(m Model, th Theme, t time.Duration, cw, rows int) []string {
	lines, _ := rightPaneLines(m, th, t, cw, rows)
	return fitRows(lines, strings.Repeat(" ", cw), rows)
}

// rightPaneLines builds the pane at the tallest resource graph that fits and
// reports the height it chose (TTP-39). Every section but RESOURCES keeps all
// its lines at every height; only the graphs give way, all resources alike,
// from three rows down to none. The pane is built and measured rather than
// sized from a table, so a section that grows a row moves the choice with it.
func rightPaneLines(m Model, th Theme, t time.Duration, cw, rows int) ([]string, int) {
	var out []string
	h := 0
	for _, h = range resourceHeights {
		out = buildRightPane(m, th, t, cw, h)
		if len(out) <= rows {
			break
		}
	}
	return out, h
}

// buildRightPane is the pane at resource graph height h, untruncated.
func buildRightPane(m Model, th Theme, t time.Duration, cw, h int) []string {
	var out []string
	blank := strings.Repeat(" ", cw)
	// A section title is dim small caps carrying a dim rule out to the panel
	// edge. The rule is what turns four stacked lists into four panels without
	// spending a row on a border.
	//
	// The title used to be accent and bold. Four lit headings down one column
	// are four invitations to look, and the pane has exactly one figure worth
	// looking at first (TTP-28, user 2026-09-13). A heading names a region; it
	// is not the thing in it, so it wears what every other label wears.
	//
	// A tag, when there is one, sits at the right end of the rule: a verdict
	// about the whole section that used to spend a line of its own.
	section := func(title, tag string, tagSt lipgloss.Style) {
		if len(out) > 0 {
			out = append(out, blank)
		}
		l := newLine(th, cw)
		l.add(th.dim, title)
		l.space(1)
		if tag == "" {
			l.add(th.dim, repeat('─', l.left()))
		} else {
			l.add(th.dim, repeat('─', l.left()-width(tag)-1))
			l.space(1)
			l.add(tagSt, tag)
		}
		out = append(out, l.String())
	}

	section("SPEED", "", th.dim)
	out = append(out, speedRows(m, th, t, cw)...)
	section("PLACEMENT", "", th.dim)
	out = append(out, placementRows(m, th, t, cw)...)
	section("MEMORY", "", th.dim)
	out = append(out, memoryRows(m, th, t, cw)...)
	// A contended run is tagged amber rather than hidden (handover lesson 6).
	contended := m.Summary.Contention.Contended
	tag := "contended no"
	if contended {
		tag = "contended yes"
	}
	section("RESOURCES", tag, styleFor(th, contended))
	out = append(out, resourceRows(m, th, t, cw, h)...)
	return out
}

// barRow is the shape every device line shares: a five-column label, a bar,
// and a right-aligned figure. Keeping one shape for all of them is what makes
// the pane read as a table instead of a list.
func barRow(th Theme, cw int, label string, frac float64, value string, st lipgloss.Style) string {
	const labelW, valueW = 5, 7
	barW := cw - labelW - valueW - 1
	l := newLine(th, cw)
	l.add(th.dim, pad(label, labelW))
	filled, empty := barCells(frac, barW)
	l.add(st, filled)
	l.add(th.darkFill, empty)
	l.space(1)
	l.add(th.text, padLeft(value, valueW))
	return l.String()
}

// placementRows draws one bar per device plus the two figures that settle the
// "is it really loaded" argument: the VRAM split and the bytes the server
// never reads at all.
func placementRows(m Model, th Theme, t time.Duration, cw int) []string {
	var out []string
	cur, prev := m.sampleAt(t)

	gpus := m.Summary.Host.GPUs
	for i, g := range gpus {
		used, total := int64(0), g.VRAMBytes
		if cur != nil {
			used = gpuUsed(cur.GPUs, g.Index)
		}
		if used == 0 && len(m.Summary.GPUsAtEnd) > i {
			used = m.Summary.GPUsAtEnd[i].UsedBytes
		}
		// The first sample has nothing to ease from: a bar that grew out of
		// zero would be an animation of a number nobody measured.
		pu, since := used, time.Duration(0)
		if prev != nil {
			pu = gpuUsed(prev.GPUs, g.Index)
			since = cur.T
		}
		eased := ease(float64(pu), float64(used), since, t)
		frac := 0.0
		if total > 0 {
			frac = eased / float64(total)
		}
		out = append(out, barRow(th, cw, fmt.Sprintf("GPU%d", g.Index), frac, fmtG(int64(eased)), th.accentMuted))
	}

	out = append(out, hostRows(m, th, t, cw, cur, prev)...)

	p := m.Summary.Placement
	if split := p.VRAMWeightsBytes + p.VRAMKVBytes + p.VRAMComputeBytes; split > 0 {
		const labelW, valueW = 5, 7
		barW := cw - labelW - valueW - 1
		segs := segmentBar(
			[]float64{float64(p.VRAMWeightsBytes), float64(p.VRAMKVBytes), float64(p.VRAMComputeBytes)},
			float64(split), barW)
		l := newLine(th, cw)
		l.add(th.dim, pad("vram", labelW))
		// Three shades descending, none of them the accent itself: the bar
		// is a proportion, and the pane's lit figure is the decode row that
		// leads the SPEED section above it (TTP-28). Weights is the largest
		// share and the lightest shade, so the bar reads in the same order as
		// the legend under it.
		l.add(th.accentMuted, segs[0])
		l.add(th.accentLow, segs[1])
		l.add(th.dim, segs[2])
		l.add(th.darkFill, segs[3])
		l.space(1)
		l.add(th.text, padLeft(fmtG(split), valueW))
		out = append(out, l.String())
		l2 := newLine(th, cw)
		l2.space(labelW)
		for _, key := range []struct {
			st    lipgloss.Style
			label string
		}{{th.accentMuted, " weights  "}, {th.accentLow, " kv  "}, {th.dim, " buf"}} {
			l2.add(key.st, string(barGlyph))
			l2.add(th.dim, key.label)
		}
		out = append(out, l2.String())
	}

	// "never loaded" is the figure that settles the mmap argument, and it is
	// also a figure a fully offloaded dense model does not have. Zero bytes is
	// not "0.0G", and it is not the "?" fmtG would print either — there is
	// nothing to report, so the row is absent, the way the card and the PNG
	// already handle it.
	if p.NeverLoadedBytes > 0 {
		out = append(out, kvRow(th, cw, "never loaded", fmtG(p.NeverLoadedBytes), th.text))
	}
	return out
}

// hostRows is the CPU placement: a bar whose full width is what llama.cpp put
// on the host, split into the part that is in RAM and the part that is read
// back from the model file, with a legend naming both. Nothing when nothing
// was placed on the CPU.
//
// The row used to be placed ÷ RAM (TTP-63, user 2026-09-14: "cpu 388g
// 찍혀있는데 이게 ram이랑 nvme랑 구분이 안되나?"). On the ws rig that is 388
// GiB over 252 GB — 1.54, clamped to a full bar — and a full bar is the shape
// of "it fits". What was actually happening is the opposite: about 193 GiB of
// the mapping is not resident and comes back from the file on every touch,
// which is what the maj/tok sparkline one section down counts. DeviceCPU is
// llama.cpp's backend assignment, never a residency, so the bar now measures
// the placement against itself and the split inside it is the answer.
//
// The split is tape.Residency's and is never re-derived here: the pane, the
// modal, the text card and the PNG all read that one function, so they cannot
// drift apart. The word is "disk" rather than "NVMe" — what was observed is
// that the pages are not resident and come back from the file; the medium
// behind that file is not in the tape.
func hostRows(m Model, th Theme, t time.Duration, cw int, cur, prev *tape.RunSample) []string {
	p := m.Summary.Placement
	// The sample the rest of the section animates on, with the run's final
	// reading as the fallback, the way memoryRows does it.
	sample := m.Summary.Memory.AtEnd
	if cur != nil {
		sample = cur.Mem
	}
	res := tape.Residency(p, sample)
	if res.Placed <= 0 {
		return nil
	}
	const labelW, valueW = 5, 7
	value := fmtG(res.Placed)

	// No /proc sample: a remote server, where the placement is all that was
	// observed. The bar is the plain full one and there is no legend — a
	// "disk 0G" here would be a reading nobody took.
	if !res.Ok {
		return []string{barRow(th, cw, "CPU", 1, value, th.accentMuted)}
	}

	// What moves during a run is how much of the mapping is resident, not how
	// much was placed, so the resident share is what eases — the same tween
	// the GPU bars run on the bytes in VRAM.
	prevRes := res
	since := time.Duration(0)
	if prev != nil {
		prevRes = tape.Residency(p, prev.Mem)
		since = cur.T
	}
	resident := ease(float64(prevRes.Resident), float64(res.Resident), since, t)
	if resident > float64(res.Placed) {
		resident = float64(res.Placed)
	}
	// A run with nothing paged draws no paged segment at all, mid-tween
	// included: the remainder of a bar that is still filling is the empty
	// shade, which is what a GPU bar does, and a warm segment that appears for
	// a third of a second and vanishes would be an alarm about nothing.
	paged := 0.0
	if res.Paged > 0 {
		paged = float64(res.Placed) - resident
		if paged < 0 {
			paged = 0
		}
	}

	// The paged share wears the warm hue only once the run is cold. The
	// palette gives that hue to alarms (CLAUDE.md) and this is the thing it
	// alarms about; a rig that keeps a little of the mapping out of RAM and
	// never faults during decode is working fine and must not be lit.
	pagedSt := th.dim
	if majFaultsPerToken(m) >= tape.ColdMajFaultsPerToken {
		pagedSt = th.warn
	}

	barW := cw - labelW - valueW - 1
	segs := segmentBar([]float64{resident, paged}, float64(res.Placed), barW)
	l := newLine(th, cw)
	l.add(th.dim, pad("CPU", labelW))
	l.add(th.accentMuted, segs[0])
	l.add(pagedSt, segs[1])
	l.add(th.darkFill, segs[2])
	l.space(1)
	l.add(th.text, padLeft(value, valueW))
	out := []string{l.String()}

	// The legend under it, shaped like the vram split's: the glyph in its
	// segment's own shade, the word dim, the figure a step brighter so the two
	// numbers are what the eye lands on. It indents under the bar when there
	// is room and slides left when there is not, rather than losing a figure
	// to the clip.
	ramTxt, diskTxt := " ram "+fmtG(int64(resident)), " disk "+fmtG(int64(paged))
	legendW := 1 + width(ramTxt)
	if paged > 0 {
		legendW += 2 + 1 + width(diskTxt)
	}
	ind := labelW
	if ind+legendW > cw {
		ind = max(0, cw-legendW)
	}
	l2 := newLine(th, cw)
	l2.space(ind)
	l2.add(th.accentMuted, string(barGlyph))
	l2.add(th.dim, " ram ")
	l2.add(th.textMid, fmtG(int64(resident)))
	if paged > 0 {
		l2.space(2)
		l2.add(pagedSt, string(barGlyph))
		l2.add(th.dim, " disk ")
		l2.add(th.textMid, fmtG(int64(paged)))
	}
	return append(out, l2.String())
}

// memoryRows is the process memory picture and the sparkline the clip is
// remembered for: major faults per token, scrolling left as tokens arrive.
func memoryRows(m Model, th Theme, t time.Duration, cw int) []string {
	mem := m.Summary.Memory.AtEnd
	if cur, _ := m.sampleAt(t); cur != nil {
		mem = cur.Mem
	}
	var out []string
	// virt sits next to rss on purpose: the gap between the two is the mmap
	// illusion that started this whole tool (handover lesson 3).
	out = append(out, pairRow(th, cw, "rss", fmtG(mem.RSSBytes), "file", fmtG(mem.RSSFileBytes)))
	out = append(out, pairRow(th, cw, "virt", fmtG(mem.VirtBytes), "anon", fmtG(mem.RSSAnonBytes)))

	// maj/tok reads as plain text until the run is actually cold.
	//
	// It used to go accent bold the moment it was not zero, which lit a third
	// figure on a screen with room for one (TTP-28) — and lit it at 0.3, which
	// is a run doing nothing wrong. At or above tape.ColdMajFaultsPerToken the
	// weights are being paged in from disk during decode, which is a warning,
	// and it wears the warm hue like "contended yes".
	perTok := majFaultsPerToken(m)
	cold := perTok >= tape.ColdMajFaultsPerToken
	out = append(out, kvRow(th, cw, "maj/tok", fmtFloat1(perTok), styleFor(th, cold)))

	l := newLine(th, cw)
	cells := Sparkline(m.majFaultSeries(cw), cw, tape.ColdMajFaultsPerToken)
	// Newest on the right: the line scrolls left under a fixed edge rather
	// than growing rightward out of nothing. Dim while the run is warm — an
	// unbroken row of ▁ is the shape of nothing happening, and it has no
	// business being the brightest thing in the section.
	pal := cellPalette{base: th.dim, warn: th.warn, bad: th.warn}
	if cold {
		pal.base = th.warn
	}
	l.space(cw - len(cells))
	writeCells(l, th, cells, pal)
	out = append(out, l.String())
	return out
}

// majFaultsPerToken is the run's average so far: total faults over tokens seen.
// It is a live reduction rather than the summary field so the number moves
// during the run and matches the sparkline beside it.
func majFaultsPerToken(m Model) float64 {
	var faults uint64
	var n int
	for _, s := range m.Streams {
		for _, tk := range s.Tokens {
			faults += tk.MajFaultsDelta
			n++
		}
	}
	if n == 0 {
		return m.Summary.Memory.MajFaultsPerToken
	}
	return float64(faults) / float64(n)
}

// speedRows separates the three speeds that community benchmarks keep mixing:
// prefill, decode, and time to first token (docs/toktape-spec.ko.md §3 M3).
func speedRows(m Model, th Theme, t time.Duration, cw int) []string {
	var out []string

	// Every figure in this pane is a client-side measurement while the run is
	// live, and the server's own timing once it is over — server figures are
	// the record and client figures the check (CLAUDE.md).
	//
	// Which way round that goes is not cosmetic. A replayed tape carries the
	// whole run from its first frame, so m.Summary already holds the final
	// prefill rate, decode rate and TTFT at t=0. Reading one of them before
	// the run has produced it puts "decode 11.3 tok/s" on a screen where all
	// eight streams still say "waiting for the first token" — a figure nobody
	// has observed yet, which is what CLAUDE.md's "never print a default you
	// did not observe" forbids. So until a measurement exists the row prints
	// "?", and each one appears at the moment it becomes real: prefill when
	// the server reports prompt progress, TTFT when a first token lands, a
	// decode rate when a second one does.
	//
	// The box's rate, whichever the run is (2026-09-14, user: "개별 tps때문에
	// 병렬세션에서 좀 애매하게 느껴진다"). A one-stream run's rate is that
	// stream's; a run of several streams leads with the aggregate on both the
	// prefill and the decode row, so the two rows are the same kind of figure
	// (the confusion M3 exists to end, docs/toktape-spec.ko.md §3), and the
	// per-stream mean is the "each" row under them. The tiles carry every
	// stream's own rate already.
	many := m.Summary.Concurrency > 1

	// The decode rate is a step function of token arrivals, so the tween needs
	// no stored "previous value": the previous figure is the same reduction
	// over one token fewer, and the step time is that token's arrival.
	//
	// The row leads the section (2026-09-15): the measured result comes first,
	// and the prefill that produced it reads under it.
	prevAgg, curAgg, since := m.decodeRateAt(t)
	agg := ease(prevAgg, curAgg, since, t)
	n := len(m.Streams)
	if n < 1 {
		n = 1
	}
	perStreamRate := agg / float64(n)
	// Once the run is over the server's own timings are the record and the
	// client-side reduction goes back to being the cross-check (CLAUDE.md).
	// While it is running the live count is all there is — and it must be the
	// same number the stream headers show. Before the second token of any
	// stream there is no count either, and the row says so rather than
	// reaching for the summary.
	if m.Done {
		if r := m.Summary.Aggregate.PerStreamPredictedPerSecond; r > 0 {
			perStreamRate = r
		}
		if r := m.Summary.Aggregate.AggregatePredictedPerSecond; r > 0 {
			agg = r
		}
	}
	// Lesson 2, through the one predicate that owns it (TTP-74, 2026-09-14).
	// This read Timings.DecodeLabel directly, which is the recorder's stored
	// verdict; the card asks card.IsSample, which also checks the count. A
	// tape where the two disagree would have been labelled "decode" here and
	// "Sample" on the card it is rendered into.
	label := "decode"
	if card.IsSample(&m.Summary) {
		label = "sample"
	}
	lead := perStreamRate
	if many {
		lead = agg
	}
	out = append(out, kvRow(th, cw, label, fmtRate(lead)+" tok/s", th.accentBold))

	prefill := livePromptRate(m, many)
	if m.Done {
		prefill = m.Summary.Timings.PromptPerSecond
		if many {
			prefill = m.Summary.Aggregate.AggregatePromptPerSecond
		}
		if prefill <= 0 {
			prefill = m.Summary.Aggregate.AggregatePromptPerSecond
		}
	}
	l := newLine(th, cw)
	l.add(th.dim, "prefill ")
	if m.prefilling() {
		l.add(th.accent, spinnerAt(t))
	} else {
		l.space(1)
	}
	val := fmtRate(prefill) + " tok/s"
	l.gapTo(width(val))
	l.add(th.text, val)
	out = append(out, l.String())

	ttft := liveTTFT(m)
	if m.Done {
		ttft = m.Summary.Aggregate.TTFTp50Ms
		if ttft <= 0 {
			ttft = m.Summary.Timings.TTFTMs
		}
	}
	// The badge is amber when the prefix cache was missed outright, which is
	// the case that starts arguments about a "slow" prefill. It deliberately
	// does not follow tape.CacheCold: that label is about major faults paging
	// weights in during decode, a different measurement, and the maj/tok row
	// above already reports it. Colouring one by the other would tell the
	// reader the prompt cache missed when what actually happened was a disk
	// read.
	cache := m.Summary.Cache
	cacheSt := th.text
	if cache.HitRatio <= 0 {
		cacheSt = th.warn
	}
	l = newLine(th, cw)
	l.add(th.dim, "ttft")
	ttftTxt, cacheTxt := fmtMs(ttft), fmtPct(cache.HitRatio)+" cache"
	l.gapTo(width(ttftTxt) + 3 + width(cacheTxt))
	l.add(th.text, ttftTxt)
	l.add(th.dim, " · ")
	l.add(cacheSt, cacheTxt)
	out = append(out, l.String())

	if c := m.Summary.Concurrency; c > 1 {
		// The per-stream mean, dim: the headline above it is the aggregate,
		// and this row says what each stream saw of it.
		l = newLine(th, cw)
		l.add(th.dim, fmt.Sprintf("%d streams", c))
		num, unit := fmtRate(perStreamRate), " tok/s each"
		l.gapTo(width(num) + width(unit))
		l.add(th.text, num)
		l.add(th.dim, unit)
		out = append(out, l.String())
	}

	// The draft line (TTP-30, 2026-09-13): the pooled acceptance rate of the
	// streams that have finished. Only the server's final timings object
	// carries draft figures, so the line cannot appear before a stream ends.
	// A replayed tape holds every stream's final timings from t=0, so reading
	// them early would print a figure nobody had observed yet.
	//
	// The percentage is plain text and the counts are dim. It is not the
	// muted accent: the emphasis contract lights only the rate figures, and
	// TestOnlyTheRateIsAccent counts every accent shade, the muted one
	// included, as lit.
	if drafted, accepted, ok := liveDraft(m); ok {
		l = newLine(th, cw)
		l.add(th.dim, "draft")
		if drafted == 0 {
			l.gapTo(width("0 drafted"))
			l.add(th.text, "0 drafted")
		} else {
			pct := fmtPct(float64(accepted) / float64(drafted))
			counts := fmt.Sprintf("%d/%d", accepted, drafted)
			l.gapTo(width(pct) + 3 + width(counts))
			l.add(th.text, pct)
			l.add(th.dim, " · ")
			l.add(th.dim, counts)
		}
		out = append(out, l.String())
	}
	return out
}

// liveDraft sums the draft figures over the streams that have finished
// successfully and reported them. ok is false while none has. Failed streams
// are left out, as the recorder's run-level reduction leaves them out, so the
// finished screen and the card agree.
func liveDraft(m Model) (drafted, accepted int, ok bool) {
	for _, s := range m.Streams {
		if !s.Done || s.Err != "" || s.Timings.DraftN == nil {
			continue
		}
		ok = true
		drafted += *s.Timings.DraftN
		if s.Timings.DraftNAccepted != nil {
			accepted += *s.Timings.DraftNAccepted
		}
	}
	return drafted, accepted, ok
}

// livePromptRate is the prompt-processing rate observed so far: the mean over
// the streams whose newest return_progress row says how many prompt tokens the
// server has put through and how long that took.
//
// Zero — printed "?" — until the first row arrives. A stream that reports no
// progress at all contributes nothing rather than a guess: without a progress
// row the prompt's length is not known live, and the only place it exists is
// the summary, which is the figure this function is here to stop leaking.
// livePromptRate is the prefill rate the streams' progress rows report right
// now: the sum across streams when the run is several (the box's throughput),
// the one stream's own rate otherwise.
func livePromptRate(m Model, sum bool) float64 {
	var total float64
	n := 0
	for _, s := range m.Streams {
		if len(s.Progress) == 0 {
			continue
		}
		p := s.Progress[len(s.Progress)-1]
		// The evaluated share is processed minus cache: the server counts the
		// cached prefix inside processed but not inside the elapsed time, and
		// its own README divides the two the same way
		// ((processed-cache)/(total-cache), checked against upstream
		// 2026-09-13). Dividing processed by the time would credit the cache's
		// tokens to the prefill and inflate the rate.
		evaluated := p.Processed - p.Cache
		if evaluated <= 0 || p.TimeMs <= 0 {
			continue
		}
		total += float64(evaluated) / (p.TimeMs / 1000)
		n++
	}
	if n == 0 {
		return 0
	}
	if sum {
		return total
	}
	return total / float64(n)
}

// liveTTFT is the median time to first token over the streams that have one.
//
// The median, to match the summary figure the row falls back to once the run
// is over (AggregateTimings.TTFTp50Ms), so the number does not change meaning
// as the screen crosses that boundary. Zero — printed "?" — until a first
// token has landed anywhere.
func liveTTFT(m Model) float64 {
	vals := make([]float64, 0, len(m.Streams))
	for _, s := range m.Streams {
		if len(s.Tokens) == 0 {
			continue
		}
		if d := s.Tokens[0].T - s.StartedAt; d > 0 {
			vals = append(vals, msOf(d))
		}
	}
	if len(vals) == 0 {
		return 0
	}
	return percentile(vals, 0.5)
}

// prefilling reports whether any stream is still waiting for its first token.
func (m Model) prefilling() bool {
	for _, s := range m.Streams {
		if len(s.Tokens) == 0 && s.Err == "" && !s.Done {
			return true
		}
	}
	return false
}

func tempStr(c float64) string {
	if c <= 0 {
		return unknown
	}
	return fmt.Sprintf("%.0f°C", c)
}

func powerStr(w float64) string {
	if w <= 0 {
		return unknown
	}
	return fmt.Sprintf("%.0fW", w)
}

func utilStr(p float64) string {
	if p <= 0 {
		return unknown
	}
	return fmt.Sprintf("%.0f%%", p)
}

// kvRow is a dim label on the left and a right-aligned value.
func kvRow(th Theme, cw int, label, value string, st lipgloss.Style) string {
	l := newLine(th, cw)
	l.add(th.dim, label)
	l.gapTo(width(value))
	l.add(st, value)
	return l.String()
}

// pairRow puts two label/value pairs on one line, each in its own half, so the
// four memory figures stay in two tabular columns.
func pairRow(th Theme, cw int, l1, v1, l2, v2 string) string {
	half := cw / 2
	l := newLine(th, cw)
	l.add(th.dim, l1)
	l.gapTo(cw - half + width(v1))
	l.add(th.text, v1)
	l.space(1)
	l.add(th.dim, l2)
	l.gapTo(width(v2))
	l.add(th.text, v2)
	return l.String()
}

// gpuUsed finds device idx in a sample's GPU readings.
func gpuUsed(gs []tape.GPUSample, idx int) int64 {
	for _, g := range gs {
		if g.Index == idx {
			return g.UsedBytes
		}
	}
	return 0
}
