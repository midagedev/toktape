package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/midagedev/toktape/internal/tape"
)

// resultModal renders the "c" overlay: the run's result over the live screen.
//
// It replaced a full-screen text card (user, 2026-09-14: "서머리 화면도 좀
// 생뚱맞아"). The card is seventy-two columns and forty rows; on a clip it
// arrived as a hard cut from the dashboard to a narrow column in a dark field,
// clipped at the bottom, with a local file path in its footer. The clip's last
// frame is the one a reader looks at longest, so it is now what the prompt
// modal already is — a box over the screen the reader has been watching —
// carrying only what a post needs to settle: the two rates, drawn large enough
// to read at feed size, the rig, the engine and where the model sits. The full
// card is still `toktape card`.
//
// Two rates and nothing else wear the accent, which is the emphasis contract
// the rest of the screen follows.
func resultModal(m Model, th Theme, boxW int) []string {
	inner := boxW - 4
	s := m.Summary
	var rows []string

	// The title sits in the top rule, the way charm draws a titled box, so the
	// box opens on the model's name rather than on a blank row.
	title := " " + strings.Join(nonEmpty(modelName(s.Model), s.Model.Quant), " ") + " "
	title = truncate(title, boxW-6)
	rows = append(rows, th.paint(th.dim, "┌─")+th.paint(th.text, title)+th.paint(th.dim, repeat('─', boxW-3-width(title))+"┐"))

	line := func(s string) {
		rows = append(rows, th.paint(th.dim, "│")+" "+pad(s, inner)+" "+th.paint(th.dim, "│"))
	}
	blank := func() { line("") }

	// Two hero columns: decode on the left, prefill on the right, each a
	// figure three rows tall with its unit beside the bottom row and one line
	// under it saying what the figure is.
	dec, pre := heroFigures(s)
	colW := inner / 2
	left, right := bigFigure(dec.figure), bigFigure(pre.figure)
	blank()
	for k := 0; k < bigRows; k++ {
		l := newLine(th, inner)
		l.space(2)
		l.add(th.accentBold, left[k])
		if k == bigRows-1 {
			l.add(th.dim, " tok/s")
		}
		l.gapTo(inner - colW)
		l.add(th.accentBold, right[k])
		if k == bigRows-1 {
			l.add(th.dim, " tok/s")
		}
		line(l.String())
	}
	l := newLine(th, inner)
	l.space(2)
	l.addTrunc(th.dim, dec.caption)
	l.gapTo(inner - colW)
	l.addTrunc(th.dim, pre.caption)
	line(l.String())
	blank()

	for _, s := range []string{rigLine(s.Host), engineLine(m)} {
		l := newLine(th, inner)
		l.space(2)
		l.addTrunc(th.textMid, s)
		line(l.String())
	}
	// The modal is the run's last frame, so the residency it names is the run's
	// last reading (TTP-63).
	res := tape.Residency(s.Placement, s.Memory.AtEnd)
	for _, row := range placementLines(th, s.Placement, res, majFaultsPerToken(m) >= tape.ColdMajFaultsPerToken, inner, 2) {
		line(row)
	}
	blank()

	// The run's ID on the left, the project on the right: the two things a
	// reader needs to find the tape and the tool. No file path — a path on the
	// recorder's disk means nothing to anyone else.
	brand := "toktape · github.com/midagedev/toktape"
	l = newLine(th, inner)
	l.space(2)
	l.add(th.dim, truncate(orUnknown(s.ID), inner-2-width(brand)-3))
	l.gapTo(width(brand))
	l.add(th.dim, brand)
	line(l.String())
	rows = append(rows, th.paint(th.dim, "└"+repeat('─', boxW-2)+"┘"))
	return rows
}

// heroFigure is one of the modal's two headline rates and the line under it.
type heroFigure struct {
	figure  string
	caption string
}

// heroFigures picks the run's two rates. A run of several streams leads with
// the aggregate, which is the figure that answers "what does this box do"; a
// single stream leads with its own server-reported rate. Both come from the
// summary, never re-derived from tokens: the summary is the record.
func heroFigures(s tape.RunSummary) (dec, pre heroFigure) {
	if n := s.Concurrency; n > 1 {
		a := s.Aggregate
		dec = heroFigure{fmtRate(a.AggregatePredictedPerSecond),
			fmt.Sprintf("decode · %d streams · %s tok/s each", n, fmtRate(a.PerStreamPredictedPerSecond))}
		pre = heroFigure{fmtRate(a.AggregatePromptPerSecond),
			"prefill · ttft p50 " + fmtMs(a.TTFTp50Ms)}
		return dec, pre
	}
	t := s.Timings
	dec = heroFigure{fmtRate(t.PredictedPerSecond), fmt.Sprintf("decode · %d tokens", t.PredictedN)}
	pre = heroFigure{fmtRate(t.PromptPerSecond), "prefill · ttft " + fmtMs(t.TTFTMs)}
	return dec, pre
}

// rigLine is the box: GPUs, CPU and RAM, each only when observed.
func rigLine(h tape.HostInfo) string {
	var parts []string
	if g := rigSummary(h); g != "" {
		parts = append(parts, g)
	}
	if h.CPU != "" {
		parts = append(parts, h.CPU)
	}
	if h.RAMBytes > 0 {
		parts = append(parts, fmt.Sprintf("%.0f GB", float64(h.RAMBytes)/gib))
	}
	if len(parts) == 0 {
		return unknown
	}
	return strings.Join(parts, " · ")
}

// engineLine is the server and the settings that decide what the rates mean:
// context size, and the draft acceptance when the run was speculative.
func engineLine(m Model) string {
	srv := m.Summary.Server
	kind := string(srv.Kind)
	if kind == "" || srv.Kind == tape.ServerUnknown {
		kind = unknown
	}
	engine := kind
	if srv.Build != "" {
		engine += " " + srv.Build
	}
	parts := []string{engine}
	if srv.CtxSize > 0 {
		parts = append(parts, fmt.Sprintf("ctx %d", srv.CtxSize))
	}
	if drafted, accepted, ok := liveDraft(m); ok && drafted > 0 {
		parts = append(parts, fmt.Sprintf("draft %s accepted", fmtPct(float64(accepted)/float64(drafted))))
	}
	return strings.Join(parts, " · ")
}

// placementLines is where the model sits: one bar split across the devices
// that hold weights, in the pane's descending shades, and a legend naming
// each device's share, both indented by ind columns. Nothing when the
// placement was not derived.
//
// The host's share is split again inside the bar (TTP-63, user 2026-09-14:
// "cpu 388g 찍혀있는데 이게 ram이랑 nvme랑 구분이 안되나?"). A CPU placement
// is llama.cpp's backend assignment, not a residency, and a reader looking at
// the last frame of a clip reads that segment as "and the rest is in RAM". On
// the ws rig half of it is not: res says how much, and the part that is read
// back from the file takes the empty shade, which is the one the eye already
// reads as "not filled". Its legend entry carries both figures rather than a
// second entry, so the legend still has one entry per device.
//
// res is tape.Residency's, taken at the sample the caller is showing; a
// residency that is not Ok, or a run with nothing paged, draws exactly what
// this function drew before.
//
// cold is the pane's own verdict (maj/tok at or above tape.ColdMajFaultsPerToken):
// the weights are being read back from the file while the model decodes, and
// then the paged share wears the warm hue here exactly as it does in the pane.
// Without it the surface that alarms is the live screen and the surface that
// does not is the modal — which is the frame a reader looks at longest.
func placementLines(th Theme, p tape.PlacementSummary, res tape.HostResidency, cold bool, w, ind int) []string {
	// One entry per device in the legend, whatever the bar does inside it.
	// The value is a run of spans rather than a string because the host's
	// carries two figures and the words between them are dim, the way the
	// pane's legend paints the same fact: words dim, figures a step brighter.
	type span struct {
		st lipgloss.Style
		s  string
	}
	type entry struct {
		st    lipgloss.Style
		label string
		value []span
	}
	var parts []float64
	var styles []lipgloss.Style
	var legend []entry
	total := 0.0
	// The host is split once: Residency sums every CPU device, so splitting a
	// second one would spend the same bytes twice.
	split := res.Ok && res.Paged > 0
	shades := []lipgloss.Style{th.accentMuted, th.accentLow, th.dim}
	for _, d := range p.Devices {
		if d.Bytes <= 0 {
			continue
		}
		st := shades[min(len(legend), len(shades)-1)]
		total += float64(d.Bytes)
		if d.Device == tape.DeviceCPU && split {
			split = false
			paged := th.darkFill
			if cold {
				paged = th.warn
			}
			parts = append(parts, float64(res.Resident), float64(res.Paged))
			styles = append(styles, st, paged)
			legend = append(legend, entry{st, d.Device, []span{
				{th.textMid, fmtG(res.Resident)},
				{th.dim, " ram · "},
				{th.textMid, fmtG(res.Paged)},
				{th.dim, " disk"},
			}})
			continue
		}
		parts = append(parts, float64(d.Bytes))
		styles = append(styles, st)
		legend = append(legend, entry{st, d.Device, []span{{th.textMid, fmtG(d.Bytes)}}})
	}
	if len(legend) == 0 || w-2*ind < 20 {
		return nil
	}
	segs := segmentBar(parts, total, w-2*ind)
	bar := newLine(th, w)
	bar.space(ind)
	for i := range parts {
		bar.add(styles[i], segs[i])
	}
	l := newLine(th, w)
	l.space(ind)
	for i, e := range legend {
		if i > 0 {
			l.space(2)
		}
		l.add(e.st, string(barGlyph))
		l.add(th.dim, " "+e.label+" ")
		for _, sp := range e.value {
			l.add(sp.st, sp.s)
		}
	}
	return []string{bar.String(), l.String()}
}
