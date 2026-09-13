package tui

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/midagedev/toktape/internal/tape"
)

// The RESOURCES section (TTP-39, user 2026-09-13: "cpu(ram) gpu0 gpu1 각 리소스
// 별로 스파크를 더 이쁘게 … 한 3줄로"): one header and one fixed-ceiling area
// graph per resource, CPU first and then the GPUs, each under a header that
// packs its figures on one line.

// utilCeiling is what a utilisation graph is scaled against. A per cent is a
// per cent; the window's own maximum would draw a steady load as mountains.
const utilCeiling = 100

// resourceSeen records which utilisation series a tape measured at all.
//
// GPUSample.UtilPct is omitempty, so a 0 on one sample cannot say whether the
// device sat idle or nobody read it. The whole tape decides: a device that
// reported a utilisation above zero anywhere was being read, and every one of
// its zeros is a measurement. Deciding from the whole tape rather than from
// the samples up to t keeps the header from flipping between "?" and "0%" as
// a replay moves.
type resourceSeen struct {
	gpuUtil map[int]bool
	cpuTime bool
}

// seenIn scans samples for the series they measured.
func seenIn(samples []tape.RunSample) *resourceSeen {
	s := &resourceSeen{gpuUtil: map[int]bool{}}
	for _, sm := range samples {
		if sm.Mem.CPUSeconds > 0 {
			s.cpuTime = true
		}
		for _, g := range sm.GPUs {
			if g.UtilPct > 0 {
				s.gpuUtil[g.Index] = true
			}
		}
	}
	return s
}

// observed is the whole-tape decision when the model was cut from a tape, and
// the samples so far for a live model, which has no future to look at. The
// live answer only ever turns from unobserved to observed, so it too stops
// moving once it has been made.
func (m Model) observed() *resourceSeen {
	if m.seen != nil {
		return m.seen
	}
	return seenIn(m.Samples)
}

// samplesUpTo is the samples at or before t, oldest first.
func (m Model) samplesUpTo(t time.Duration) []tape.RunSample {
	n := 0
	for n < len(m.Samples) && m.Samples[n].T <= t {
		n++
	}
	return m.Samples[:n]
}

// gpuUtilSeries is device idx's utilisation, one value per sample up to t. It
// is nil when the tape never measured that device's utilisation, and a sample
// that does not carry the device is NaN.
func gpuUtilSeries(m Model, t time.Duration, idx int) []float64 {
	if !m.observed().gpuUtil[idx] {
		return nil
	}
	samples := m.samplesUpTo(t)
	out := make([]float64, len(samples))
	for i, sm := range samples {
		out[i] = math.NaN()
		for _, g := range sm.GPUs {
			if g.Index == idx {
				out[i] = g.UtilPct
				break
			}
		}
	}
	return out
}

// cpuCoresSeries is the server process's CPU use in cores between each sample
// and the one before it: ΔCPUSeconds / ΔT. The first sample has no delta, and
// a sample whose counter was not read or went backwards (a PID reused, a
// counter reset) is NaN rather than a spike. nil when the tape never read the
// counter.
func cpuCoresSeries(m Model, t time.Duration) []float64 {
	if !m.observed().cpuTime {
		return nil
	}
	samples := m.samplesUpTo(t)
	out := make([]float64, len(samples))
	for i := range samples {
		out[i] = math.NaN()
		if i == 0 {
			continue
		}
		a, b := samples[i-1], samples[i]
		dt := (b.T - a.T).Seconds()
		d := b.Mem.CPUSeconds - a.Mem.CPUSeconds
		if a.Mem.CPUSeconds <= 0 || b.Mem.CPUSeconds <= 0 || dt <= 0 || d < 0 {
			continue
		}
		out[i] = d / dt
	}
	return out
}

// cpuUtilSeries is cpuCoresSeries as a per cent of every host thread. nil when
// either the counter or the thread count was not observed.
func cpuUtilSeries(m Model, t time.Duration) []float64 {
	threads := m.Summary.Host.CPUThreads
	cores := cpuCoresSeries(m, t)
	if cores == nil || threads <= 0 {
		return nil
	}
	out := make([]float64, len(cores))
	for i, c := range cores {
		out[i] = 100 * c / float64(threads)
	}
	return out
}

// lastOf is a series' newest value, NaN for an empty one.
func lastOf(vals []float64) float64 {
	if len(vals) == 0 {
		return math.NaN()
	}
	return vals[len(vals)-1]
}

// pctStr prints a utilisation that has been judged observed. A NaN is a
// sample that did not carry it; 0 is a measurement and prints as one, which
// is why utilStr, which folds 0 into "?", is not used here.
func pctStr(v float64) string {
	if math.IsNaN(v) {
		return unknown
	}
	return fmt.Sprintf("%.0f%%", v)
}

// headerSeg is one painted run of a header figure.
type headerSeg struct {
	st   lipgloss.Style
	text string
}

// headerFig is one figure on a resource header. drop orders the figures that
// give way when the header does not fit: the highest goes first, and 0 never
// goes.
type headerFig struct {
	segs []headerSeg
	drop int
}

func (f headerFig) width() int {
	n := 0
	for _, s := range f.segs {
		n += width(s.text)
	}
	return n
}

// resourceHeader is a dim label on the left and the figures right-aligned,
// two columns apart. Figures are dropped by rank until the line fits.
func resourceHeader(th Theme, cw int, label string, figs []headerFig) string {
	for {
		need := width(label) + 1
		for i, f := range figs {
			if i > 0 {
				need += 2
			}
			need += f.width()
		}
		if need <= cw {
			break
		}
		worst := -1
		for i, f := range figs {
			if f.drop > 0 && (worst < 0 || f.drop > figs[worst].drop) {
				worst = i
			}
		}
		if worst < 0 {
			break // only the undroppable figures are left; the line clips
		}
		figs = append(append([]headerFig{}, figs[:worst]...), figs[worst+1:]...)
	}
	total := 0
	for i, f := range figs {
		if i > 0 {
			total += 2
		}
		total += f.width()
	}
	l := newLine(th, cw)
	l.add(th.dim, label)
	l.space(max(1, l.left()-total))
	for i, f := range figs {
		if i > 0 {
			l.space(2)
		}
		for _, s := range f.segs {
			l.add(s.st, s.text)
		}
	}
	return l.String()
}

// graphRow paints one row of a resource graph as a lit ridge over a dark body
// (TTP-39, user 2026-09-13: "너무 배낀거 같지는 않게", "너무 색상 튀지않게").
//
// A utilisation graph is drawn against a fixed 100 % ceiling, so a GPU working
// at 85 % fills most of its height, and a graph painted evenly reads as a
// solid slab: the mass is what the eye finds, and the movement at the top is
// lost. So a cell with lit cells above it in the same column is the body and
// wears the darkest demoted shade, and the topmost lit cell of each column —
// the ridge — wears a brighter one. The ridge is the reading; the body says
// how far down it reaches. Neither is the full accent, which the emphasis
// contract keeps for the rates.
//
// The unlit cells of a column that was measured are the track, spaces on the
// dark the placement bars leave unfilled, so each graph reads as a
// measure of its own height: a CPU at 6 % is a line at the foot of its own
// track, not an underline for the header below it. A column was measured
// exactly when its bottom cell is lit, because Graph lights at least the
// bottom level of every observed sample; a column with no sample stays blank.
func graphRow(th Theme, cw int, row, above, bottom string) string {
	cells, over, foot := []rune(row), []rune(above), []rune(bottom)
	l := newLine(th, cw)
	for i := 0; i < len(cells); {
		kind := graphCellKind(cells, over, foot, i)
		j := i
		for j < len(cells) && graphCellKind(cells, over, foot, j) == kind {
			j++
		}
		seg := string(cells[i:j])
		switch kind {
		case cellBody:
			l.add(th.accentLow, seg)
		case cellRidge:
			l.add(th.graphRidge, seg)
		case cellTrack:
			l.add(th.graphTrack, strings.Repeat(" ", j-i))
		default:
			l.space(j - i)
		}
		i = j
	}
	return l.String()
}

// The four things a graph cell can be.
const (
	cellBlank = iota
	cellTrack
	cellBody
	cellRidge
)

// graphCellKind is blank for a cell of a column with no sample, track for an
// unlit cell of a measured column, body for a lit cell under a lit cell, and
// ridge for the topmost lit cell of its column.
func graphCellKind(cells, above, bottom []rune, i int) int {
	if cells[i] == ' ' {
		if i < len(bottom) && bottom[i] != ' ' {
			return cellTrack
		}
		return cellBlank
	}
	if i < len(above) && above[i] != ' ' {
		return cellBody
	}
	return cellRidge
}

// resourceGPUs is the devices the section draws, in Host.GPUs order. A tape
// with no static GPU list still has devices in its samples, and dropping them
// would hide the very readings the section exists for, so those are the
// fallback.
func resourceGPUs(m Model, t time.Duration) []int {
	var out []int
	for _, g := range m.Summary.Host.GPUs {
		out = append(out, g.Index)
	}
	if len(out) > 0 {
		return out
	}
	gs := m.Summary.GPUsAtEnd
	if cur, _ := m.sampleAt(t); cur != nil && len(cur.GPUs) > 0 {
		gs = cur.GPUs
	}
	for _, g := range gs {
		out = append(out, g.Index)
	}
	sort.Ints(out)
	return out
}

// resourceRows is the section body: for CPU and then each GPU, a header and h
// graph rows. h = 0 is headers only.
func resourceRows(m Model, th Theme, t time.Duration, cw, h int) []string {
	var out []string
	cur, _ := m.sampleAt(t)
	graph := func(vals []float64) {
		if h <= 0 {
			return
		}
		rows := Graph(vals, utilCeiling, cw, h, resourceGraphStyle)
		for i, row := range rows {
			above := ""
			if i > 0 {
				above = rows[i-1]
			}
			out = append(out, graphRow(th, cw, row, above, rows[len(rows)-1]))
		}
	}

	// CPU: the share of every host thread, the cores that is, and the load
	// average that used to have a line of its own.
	cpu := cpuUtilSeries(m, t)
	pct, cores := unknown, unknown
	if cpu != nil {
		pct = pctStr(lastOf(cpu))
		if c := lastOf(cpuCoresSeries(m, t)); !math.IsNaN(c) {
			cores = fmtFloat1(c)
		}
	}
	load := unknown
	if cur != nil && cur.LoadAvg1 > 0 {
		load = fmtFloat1(cur.LoadAvg1)
	}
	out = append(out, resourceHeader(th, cw, "CPU", []headerFig{
		{segs: []headerSeg{{th.text, pct}}},
		{segs: []headerSeg{{th.text, cores}, {th.dim, " cores"}}, drop: 2},
		{segs: []headerSeg{{th.dim, "load "}, {th.text, load}}, drop: 3},
	}))
	graph(cpu)

	for _, idx := range resourceGPUs(m, t) {
		util := gpuUtilSeries(m, t, idx)
		pct, temp, power := unknown, unknown, unknown
		throttled := false
		if cur != nil {
			for _, g := range cur.GPUs {
				if g.Index != idx {
					continue
				}
				temp, power, throttled = tempStr(g.TempC), powerStr(g.PowerW), g.Throttled
			}
		}
		if util != nil {
			pct = pctStr(lastOf(util))
		}
		out = append(out, resourceHeader(th, cw, fmt.Sprintf("GPU%d", idx), []headerFig{
			{segs: []headerSeg{{th.text, pct}}},
			{segs: []headerSeg{{styleFor(th, throttled), temp}}},
			{segs: []headerSeg{{th.text, power}}, drop: 1},
		}))
		graph(util)
	}
	return out
}

// resourceHeights is the order the graph height is tried in: the tallest the
// pane can hold wins, and 0 is today's density, headers only.
var resourceHeights = []int{3, 2, 1, 0}
