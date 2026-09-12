package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/midagedev/toktape/internal/tape"
)

// rightPane renders the machine: where the model sits, what the process is
// actually touching, how fast the server is answering, and whether the box was
// busy while it did.
//
// It always returns exactly rows lines of exactly cw columns. Sections are
// ordered so the reader's eye runs placement → memory → speed → host, which is
// the causal order of the question the tool exists to answer.
func rightPane(m Model, th Theme, t time.Duration, cw, rows int) []string {
	var out []string
	blank := strings.Repeat(" ", cw)
	// A section title is accent, bold, and carries a dim rule out to the panel
	// edge. The rule is what turns four stacked lists into four panels without
	// spending a row on a border.
	section := func(title string) {
		if len(out) > 0 {
			out = append(out, blank)
		}
		l := newLine(th, cw)
		l.add(th.accentBold, title)
		l.space(1)
		l.add(th.dim, repeat('─', l.left()))
		out = append(out, l.String())
	}

	section("PLACEMENT")
	out = append(out, placementRows(m, th, t, cw)...)
	section("MEMORY")
	out = append(out, memoryRows(m, th, t, cw)...)
	section("SPEED")
	out = append(out, speedRows(m, th, t, cw)...)
	section("HOST")
	out = append(out, hostRows(m, th, t, cw)...)

	return fitRows(out, blank, rows)
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
		out = append(out, barRow(th, cw, fmt.Sprintf("GPU%d", g.Index), frac, fmtG(int64(eased)), th.accent))
	}

	if cpu := deviceBytes(m.Summary.Placement, tape.DeviceCPU); cpu > 0 {
		frac := 0.0
		if ram := m.Summary.Host.RAMBytes; ram > 0 {
			frac = float64(cpu) / float64(ram)
		}
		out = append(out, barRow(th, cw, "CPU", frac, fmtG(cpu), th.accent))
	}

	p := m.Summary.Placement
	if split := p.VRAMWeightsBytes + p.VRAMKVBytes + p.VRAMComputeBytes; split > 0 {
		const labelW, valueW = 5, 7
		barW := cw - labelW - valueW - 1
		segs := segmentBar(
			[]float64{float64(p.VRAMWeightsBytes), float64(p.VRAMKVBytes), float64(p.VRAMComputeBytes)},
			float64(split), barW)
		l := newLine(th, cw)
		l.add(th.dim, pad("vram", labelW))
		l.add(th.accent, segs[0])
		l.add(th.accentMid, segs[1])
		l.add(th.accentLow, segs[2])
		l.add(th.darkFill, segs[3])
		l.space(1)
		l.add(th.text, padLeft(fmtG(split), valueW))
		out = append(out, l.String())
		l2 := newLine(th, cw)
		l2.space(labelW)
		for _, key := range []struct {
			st    lipgloss.Style
			label string
		}{{th.accent, " weights  "}, {th.accentMid, " kv  "}, {th.accentLow, " buf"}} {
			l2.add(key.st, "█")
			l2.add(th.dim, key.label)
		}
		out = append(out, l2.String())
	}

	out = append(out, kvRow(th, cw, "never loaded", fmtG(p.NeverLoadedBytes), th.text))
	return out
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

	// maj/tok is one of the three figures the screen exists to show, so it is
	// accent bold the moment it is not zero, and red once the run qualifies as
	// cold (tape.ColdMajFaultsPerToken).
	perTok := majFaultsPerToken(m)
	st := th.dim
	switch {
	case perTok >= tape.ColdMajFaultsPerToken:
		st = th.bad
	case perTok > 0:
		st = th.accentBold
	}
	out = append(out, kvRow(th, cw, "maj/tok", fmtFloat1(perTok), st))

	l := newLine(th, cw)
	cells := Sparkline(m.majFaultSeries(cw), cw, tape.ColdMajFaultsPerToken)
	// Newest on the right: the line scrolls left under a fixed edge rather
	// than growing rightward out of nothing.
	l.space(cw - len(cells))
	writeCells(l, th, cells, th.accent)
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

	// Per stream, to match the decode row directly below it. Mixing a
	// server-wide prefill figure with a per-stream decode figure on adjacent
	// rows is exactly the confusion M3 exists to end
	// (docs/toktape-spec.ko.md §3); the aggregate has its own line lower down.
	prefill := m.Summary.Timings.PromptPerSecond
	if prefill <= 0 && m.Summary.Concurrency == 1 {
		prefill = m.Summary.Aggregate.AggregatePromptPerSecond
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

	// The decode rate is a step function of token arrivals, so the tween needs
	// no stored "previous value": the previous figure is the same reduction
	// over one token fewer, and the step time is that token's arrival.
	prevAgg, curAgg, since := m.decodeRateAt(t)
	agg := ease(prevAgg, curAgg, since, t)
	n := len(m.Streams)
	if n < 1 {
		n = 1
	}
	perStreamRate := agg / float64(n)
	// Once the run is over the server's own timings are the record and the
	// client-side reduction goes back to being the cross-check (CLAUDE.md).
	// While it is running there is no server figure yet, so the live count is
	// all there is — and it must be the same number the stream headers show.
	if m.Done || agg <= 0 {
		if r := m.Summary.Aggregate.PerStreamPredictedPerSecond; r > 0 {
			perStreamRate = r
		}
		if r := m.Summary.Aggregate.AggregatePredictedPerSecond; r > 0 {
			agg = r
		}
	}
	label := "decode"
	if m.Summary.Timings.DecodeLabel == "sample" {
		label = "sample"
	}
	out = append(out, kvRow(th, cw, label, fmtRate(perStreamRate)+" tok/s", th.accentBold))

	ttft := m.Summary.Aggregate.TTFTp50Ms
	if ttft <= 0 {
		ttft = m.Summary.Timings.TTFTMs
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
		// "aggregate" spelled out does not fit a 30-column pane beside the
		// stream count, so the unit carries the word in short form. The
		// figure itself is a headline: accent, bold, and no arrow.
		l = newLine(th, cw)
		l.add(th.dim, fmt.Sprintf("%d streams", c))
		num, unit := fmtRate(agg), " tok/s agg"
		l.gapTo(width(num) + width(unit))
		l.add(th.accentBold, num)
		l.add(th.dim, unit)
		out = append(out, l.String())
	}
	return out
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

// hostRows is the "are these numbers even valid" section: load, other GPU
// processes, and per-device temperature and power. A contended run is tagged
// amber rather than hidden (handover lesson 6).
func hostRows(m Model, th Theme, t time.Duration, cw int) []string {
	var out []string
	load := m.Summary.Contention.LoadAvg1
	if cur, _ := m.sampleAt(t); cur != nil && cur.LoadAvg1 > 0 {
		load = cur.LoadAvg1
	}
	contended := m.Summary.Contention.Contended
	l := newLine(th, cw)
	l.add(th.dim, "load")
	loadTxt := fmtFloat1(load)
	tag := "contended no"
	if contended {
		tag = "contended yes"
	}
	l.gapTo(width(loadTxt) + 3 + width(tag))
	l.add(th.text, loadTxt)
	l.add(th.dim, " · ")
	l.add(styleFor(th, contended), tag)
	out = append(out, l.String())

	samples := m.Summary.GPUsAtEnd
	if cur, _ := m.sampleAt(t); cur != nil && len(cur.GPUs) > 0 {
		samples = cur.GPUs
	}
	for _, g := range samples {
		temp, power, util := tempStr(g.TempC), powerStr(g.PowerW), utilStr(g.UtilPct)
		l := newLine(th, cw)
		l.add(th.dim, fmt.Sprintf("GPU%d", g.Index))
		l.gapTo(width(temp) + 2 + width(power) + 2 + width(util))
		l.add(styleFor(th, g.Throttled), temp)
		l.space(2)
		l.add(th.text, power)
		l.space(2)
		l.add(th.dim, util)
		out = append(out, l.String())
	}
	return out
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

// deviceBytes is the bytes the placement puts on one device.
func deviceBytes(p tape.PlacementSummary, device string) int64 {
	for _, d := range p.Devices {
		if d.Device == device {
			return d.Bytes
		}
	}
	return 0
}
