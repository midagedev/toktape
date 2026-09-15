package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/midagedev/toktape/internal/card"
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
// carrying only what a post needs to settle: the decode rate and the time to
// first token, drawn large enough to read at feed size, the rig, the engine
// and where the model sits. The full card is still `toktape card`.
//
// The two figures were two rates until 2026-09-15, when the hierarchy changed
// (user-approved): engine benchmarks put the prompt length in the prefill
// metric's own name and serving benchmarks print TTFT instead of a prefill
// rate at all, so a bare prefill tok/s at feed size is a figure the reader
// cannot compare with anything. The prefill rate now lives in the caption,
// named with its length by card.PrefillLabel.
//
// The two figures and nothing else wear the accent, which is the emphasis
// contract the rest of the screen follows.
func resultModal(m Model, th Theme, boxW int) []string {
	inner := boxW - 4
	s := m.Summary
	var rows []string
	var title string

	// The title sits in the top rule, the way charm draws a titled box, so the
	// box opens on the model's name rather than on a blank row.
	//
	// Name, quant and size, in that order (user, 2026-09-14: "모델도 모델
	// 크기와 양자화가 잘 나와야 되고"). The three together are what identifies
	// a run's model to someone comparing cards: the same name at two quants is
	// two different machines' worth of bytes, and the size is the figure every
	// "will it fit" question starts from.
	title = " " + strings.Join(nonEmpty(modelName(s.Model), s.Model.Quant, fmtG(s.Model.FileBytes)), " · ") + " "
	// The date rides the other end of the same rule (user, 2026-09-14:
	// "날짜도"). A card with no date is undatable evidence — engines move
	// weekly, and last spring's rate is a different claim from today's — and
	// the rule is the one place on the modal with room for it. The footer's ID
	// begins with the same day, but as an identifier nobody reads as a date.
	// Omitted rather than faked when the tape carries no time.
	var dateSeg string
	if !s.StartedAt.IsZero() {
		dateSeg = " " + s.StartedAt.Local().Format("2006-01-02") + " "
	}
	title = truncate(title, boxW-6-width(dateSeg))
	fill := boxW - 3 - width(title) - width(dateSeg)
	if fill < 0 {
		fill = 0
	}
	rows = append(rows, th.paint(th.dim, "┌─")+th.paint(th.text, title)+
		th.paint(th.dim, repeat('─', fill))+th.paint(th.textMid, dateSeg)+th.paint(th.dim, "┐"))

	line := func(s string) {
		rows = append(rows, th.paint(th.dim, "│")+" "+pad(s, inner)+" "+th.paint(th.dim, "│"))
	}
	blank := func() { line("") }

	// Two hero columns: decode on the left, time to first token on the right,
	// each a figure five rows tall with its unit beside the bottom row and
	// one line under it saying what the figure is.
	dec, ttft := heroFigures(s)
	colW := inner / 2
	left, right := bigFigure(dec.figure), bigFigure(ttft.figure)
	// The gleam: while the card is younger than GleamSweep, a band of accent
	// lightnesses crosses both figures once, together, each over its own
	// columns (gleamStyle). After it — and on every frame of the card's
	// settled tail, the poster included — the figures are painted accentBold
	// in one piece, byte-identical to the modal before the gleam existed.
	p := float64(m.CardAge) / float64(GleamSweep)
	addFigure := func(l *lineBuf, row string, r int) {
		if p >= 1 {
			l.add(th.accentBold, row)
			return
		}
		// One figure row's runes are all single-column — half blocks and the
		// spaces between glyphs — so the rune index is the figure's own
		// column. Spaces are left unpainted: a style on a blank advances
		// nothing the eye can see and would spend escapes on gaps.
		for x, g := range []rune(row) {
			if g == ' ' {
				l.space(1)
				continue
			}
			l.add(gleamStyle(th, p, len([]rune(row)), r, x), string(g))
		}
	}
	blank()
	for k := 0; k < bigRows; k++ {
		l := newLine(th, inner)
		l.space(2)
		addFigure(l, left[k], k)
		if k == bigRows-1 {
			l.add(th.dim, " tok/s")
		}
		l.gapTo(inner - colW)
		addFigure(l, right[k], k)
		// The TTFT's unit is whichever half fmtMsParts gave, and nothing when
		// the TTFT itself was never measured — a unit beside "?" would claim a
		// precision the run did not observe.
		if k == bigRows-1 && ttft.unit != "" {
			l.add(th.dim, " "+ttft.unit)
		}
		line(l.String())
	}
	// The right caption starts at the column the right figure does and runs to
	// the modal's inner edge, so its budget is what is left of inner from
	// there — not colW-2, which is the left caption's.
	capRows := ttftCaption(ttft, colW)
	for i, cap := range capRows {
		l := newLine(th, inner)
		l.space(2)
		if i == 0 {
			l.addTrunc(th.dim, dec.caption)
		}
		l.gapTo(inner - colW)
		l.addTrunc(th.dim, cap)
		line(l.String())
	}
	blank()

	for _, s := range []string{rigLine(s.Host), engineLine(m)} {
		l := newLine(th, inner)
		l.space(2)
		l.addTrunc(th.textMid, s)
		line(l.String())
	}
	// One word for where the weights live, over the bar that shows it (user,
	// 2026-09-14: "통합메모리 계열인지 순수 vram인지 오프로딩인지 … 성격이 많이
	// 다르니까"). A bar is a proportion and a reader has to decode it; the
	// sentence is the thing they needed before any of the rates above meant
	// anything.
	if lay := tape.Layout(s.Placement); lay.Shape != tape.ShapeUnknown {
		l := newLine(th, inner)
		l.space(2)
		l.add(th.text, layoutWord(lay))
		if rest := layoutDetail(lay); rest != "" {
			l.addTrunc(th.textMid, rest)
		}
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
	l := newLine(th, inner)
	l.space(2)
	l.add(th.dim, truncate(orUnknown(s.ID), inner-2-width(brand)-3))
	l.gapTo(width(brand))
	l.add(th.dim, brand)
	line(l.String())
	rows = append(rows, th.paint(th.dim, "└"+repeat('─', boxW-2)+"┘"))
	return rows
}

// heroFigure is one of the modal's two headline figures, the unit drawn dim
// beside its bottom row, and the line under it.
type heroFigure struct {
	figure string
	// unit is empty when the figure itself is unknown, so the modal draws no
	// unit beside "?" (the decode column's " tok/s" is drawn literally below
	// and always present: an unmeasured rate is still a rate).
	unit string
	// caption is the fullest line under the figure. falls is the caption's
	// fallback ladder, fullest first, down to the line that must fit; the
	// renderer walks it against the column budget. words and label are the
	// ladder's two halves, kept apart so that a caption too wide for any rung
	// can be drawn on two lines instead of losing one of them.
	caption string
	falls   []string
	words   string
	label   string
}

// heroFigures picks the modal's two headline figures: the decode rate and the
// time to first token. A run of several streams leads with the aggregate
// decode, which is the figure that answers "what does this box do"; a single
// stream leads with its own server-reported rate. Both come from the summary,
// never re-derived from tokens: the summary is the record.
//
// The right-hand figure was the prefill rate until 2026-09-15. Engine
// benchmarks put the prompt length in the prefill metric's name and serving
// benchmarks print TTFT instead of a prefill rate at all, so the headline pair
// is now decode and TTFT, and the prefill rate rides in the caption as
// card.PrefillLabel — with its prompt length in llama-bench's vocabulary.
func heroFigures(s tape.RunSummary) (dec, ttft heroFigure) {
	// The decode figure's own label says "sample" when the generation was too
	// short to be a rate, and a stream too short to be a rate inside a mean
	// that is one says so beside it (TTP-65/74/83, 2026-09-14). Both go
	// through the predicates in internal/card, which the text card and the
	// share image also ask: this modal is the third rendering of one run, and
	// three renderers deciding for themselves whether a figure needs
	// qualifying is how a figure ends up quoted without one. The prefill
	// qualification now rides on PrefillLabel, which asks card.ShortPrompt
	// itself, for the same reason.
	decode := "decode"
	if card.IsSample(&s) {
		decode = "sample"
	}
	if card.ShortStream(&s) {
		decode += " · short stream"
	}
	if n := s.Concurrency; n > 1 {
		a := s.Aggregate
		dec = heroFigure{figure: fmtRate(a.AggregatePredictedPerSecond),
			caption: fmt.Sprintf("%s · %d streams · %s tok/s each", decode, n, fmtRate(a.PerStreamPredictedPerSecond))}
	} else {
		t := s.Timings
		dec = heroFigure{figure: fmtRate(t.PredictedPerSecond), caption: fmt.Sprintf("%s · %d tokens", decode, t.PredictedN)}
	}

	// card owns which reading is the headline TTFT (the aggregate's p50 above
	// one stream, the stream's own below it) and whether the p95 beside it is
	// a second fact. The big figure is drawn three rows tall rather than
	// printed, so it asks card.TTFTMs for the number and splits it here; the
	// p50's own rendering is not used — the figure above the caption IS the
	// p50 and the caption's words name it — so only the p95 string and the
	// pair verdict come back from TTFTPercentiles.
	num, unit := fmtMsParts(card.TTFTMs(&s))
	_, p95, pair := card.TTFTPercentiles(&s, fmtMs)
	label := card.PrefillLabel(&s)
	// "p50" earns its place only on the rung that prints the p95 beside it. A
	// median with no spread next to it is not a median to the reader, it is
	// four columns that name nothing — and those four are exactly what pushed
	// the whole "first token" clause off the eight-stream example's line at
	// the smallest screen, leaving the figure above it unnamed (lead,
	// 2026-09-15: candidate 40 columns against a 39-column budget).
	// Every rung carries the words AND the label: dropping the words was the
	// old bottom rung and it is what left the figure unnamed. When no rung
	// fits, the caption wraps to two lines rather than losing either half.
	words := "first token"
	falls := []string{words + " · " + label}
	if pair {
		words = "first token p50 · p95 " + p95
		falls = append([]string{words + " · " + label}, falls...)
	}
	ttft = heroFigure{figure: num, unit: unit, caption: falls[0], falls: falls, words: words, label: label}
	return dec, ttft
}

// ttftCaption is the caption under the TTFT figure, as one line where one line
// will carry it and as two where it will not.
//
// The ladder exists because the label is the payload: the p95 is the first
// thing to give up, and a caption truncated mid-label would qualify nothing —
// " · short prompt" is exactly the clause a reader must not lose. But the
// bottom rung of that ladder is the label ALONE, and a run whose label is long
// (four streams of a 63-token prompt: "pp63 × 4 · 2927 tok/s · short prompt",
// 36 columns of a 39-column budget) then leaves the figure above it unnamed —
// "810 ms" over a line that says only what the prefill was (lead, 2026-09-15).
//
// So the words wrap rather than fall off. Two lines are only ever spent when
// one will not do, which is why this returns a slice instead of always drawing
// a second row: the hero and every run like it still read as one line.
func ttftCaption(f heroFigure, budget int) []string {
	for _, c := range f.falls {
		if width(c) <= budget {
			return []string{c}
		}
	}
	// Nothing worded fits beside the label, so the words take a line of their
	// own. The label is still never cut: at this width it is drawn as it is
	// and the column it overruns is the modal's own padding.
	return []string{f.words, f.label}
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

// layoutWord is the one word the modal leads the placement with.
//
// They are the reader's words, not the schema's: "offloaded" is what the
// forums call a model whose experts sit in system memory, and "all in VRAM" is
// the state everyone is trying to reach. tape.Layout's doc says why unified
// memory is not among them yet.
func layoutWord(lay tape.MemoryLayout) string {
	switch lay.Shape {
	case tape.ShapeVRAM:
		return "all in VRAM"
	case tape.ShapeHost:
		return "all in host RAM"
	case tape.ShapeOffload:
		return "offloaded"
	}
	return ""
}

// layoutDetail is the split that follows the word, for the only shape that has
// one to give. The share is of the placed bytes, so it answers "how much of
// this model is on the cards" and not "how full are the cards" — the bar under
// it answers the second.
func layoutDetail(lay tape.MemoryLayout) string {
	if lay.Shape != tape.ShapeOffload {
		return ""
	}
	return fmt.Sprintf(" · %s in VRAM · %s in host RAM",
		fmtPct(lay.VRAMShare), fmtPct(1-lay.VRAMShare))
}
