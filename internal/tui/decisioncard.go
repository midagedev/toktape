package tui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/midagedev/toktape/internal/card"
	"github.com/midagedev/toktape/internal/tape"
)

// decisionModal is the result modal of a decision run: the warm p50 in the big
// face, what kind of time it is, and the figures that qualify it. It is
// resultModal's own mechanism (a box over the screen, held at the end of the
// clip, one gleam on the hero) with a decision's figures in it.
//
// Every figure is the summary's, never re-derived from the records. Unknown
// prints "?"; a row that only exists when something happened (cache hits, a
// reference file) is left out otherwise.
func decisionModal(m Model, th Theme, boxW int) []string {
	inner := boxW - 4
	s := m.Summary
	d := s.Decision
	if d == nil {
		d = &tape.DecisionSummary{}
	}
	var rows []string

	titleSegs := []string{card.ModelName(s.Model), fmtG(s.Model.FileBytes)}
	if card.ModelNameQuant(s.Model) != card.ModelName(s.Model) {
		titleSegs = []string{card.ModelName(s.Model), s.Model.Quant, fmtG(s.Model.FileBytes)}
	}
	title := " " + strings.Join(nonEmpty(titleSegs...), " · ") + " "
	var dateSeg string
	if !s.StartedAt.IsZero() {
		dateSeg = " " + tape.Stamp(s.StartedAt) + " "
	}
	title = truncate(title, boxW-6-width(dateSeg))
	fill := max(0, boxW-3-width(title)-width(dateSeg))
	rows = append(rows, th.paint(th.dim, "┌─")+th.paint(th.text, title)+
		th.paint(th.dim, repeat('─', fill))+th.paint(th.textMid, dateSeg)+th.paint(th.dim, "┐"))
	line := func(s string) {
		rows = append(rows, th.paint(th.dim, "│")+" "+pad(s, inner)+" "+th.paint(th.dim, "│"))
	}

	timing := "client, end to end"
	if d.TimingSource == tape.DecisionTimingServer {
		timing = "engine"
	}
	fig := bigFigure(fmtLat(d.WarmP50Ms))
	p := float64(m.CardAge) / float64(GleamSweep)
	line("")
	for k := 0; k < bigRows; k++ {
		l := newLine(th, inner)
		l.space(2)
		row := fig[k]
		if p >= 1 {
			l.add(th.accentBold, row)
		} else {
			for x, g := range []rune(row) {
				if g == ' ' {
					l.space(1)
					continue
				}
				l.add(gleamStyle(th, p, len([]rune(row)), k, x), string(g))
			}
		}
		if k == bigRows-1 {
			l.add(th.dim, " ms")
		}
		line(l.String())
	}
	l := newLine(th, inner)
	l.space(2)
	l.addTrunc(th.dim, "p50 · warm · "+timing)
	line(l.String())
	line("")

	long := 0
	for _, c := range d.PerCase {
		if c.InputTokens >= tape.DecisionLongPromptTokens {
			long = max(long, c.InputTokens)
		}
	}
	type kv struct {
		k, v string
		st   style
	}
	ms := func(v float64) string {
		if v <= 0 {
			return unknown
		}
		return fmtLat(v) + " ms"
	}
	longVal := ms(d.LongWarmP50Ms)
	if long > 0 {
		longVal += " · " + fmtThousands(long) + " tok"
	}
	prefill := unknown
	if d.PrefillPerSecond > 0 {
		prefill = fmtThousands(int(d.PrefillPerSecond+0.5)) + " tok/s"
	}
	thr := unknown
	if d.RequestsPerSecond > 0 {
		thr = strconv.FormatFloat(d.RequestsPerSecond, 'f', 1, 64) + " req/s"
		thr += fmt.Sprintf(" · c%d", d.Concurrency)
	}
	figs := []kv{
		{"cold", ms(d.ColdMs), th.textMid},
		{"short p50", ms(d.ShortWarmP50Ms), th.textMid},
		{"long p50", longVal, th.textMid},
		{"p95", ms(d.WarmP95Ms), th.textMid},
		{"prefill", prefill, th.textMid},
		{"throughput", thr, th.textMid},
		{"requests", fmt.Sprintf("%d · errors %d", d.Requests, d.Errors), th.textMid},
	}
	if d.CacheHits > 0 {
		figs = append(figs, kv{"cache hits", strconv.Itoa(d.CacheHits), th.warn})
	}
	if r := d.Reference; r != nil {
		figs = append(figs, kv{"check", fmt.Sprintf("vs %s: max |Δp| %s · %d flips", r.File,
			strconv.FormatFloat(r.MaxAbsDeltaP, 'f', 3, 64), r.TopFlips), th.textMid})
	}
	const labelW = 12
	for _, f := range figs {
		l := newLine(th, inner)
		l.space(2)
		l.add(th.dim, pad(f.k, labelW))
		l.addTrunc(f.st, f.v)
		line(l.String())
	}
	line("")
	for _, r := range []string{rigLine(s.Host), engineLine(m)} {
		l := newLine(th, inner)
		l.space(2)
		l.addTrunc(th.textMid, r)
		line(l.String())
	}
	if s.Note != "" {
		l := newLine(th, inner)
		l.space(2)
		l.addTrunc(th.dim, s.Note)
		line(l.String())
	}
	rows = append(rows, th.paint(th.dim, "└"+repeat('─', boxW-2)+"┘"))
	return rows
}
