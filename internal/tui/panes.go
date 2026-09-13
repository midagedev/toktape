package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/midagedev/toktape/internal/card"
	"github.com/midagedev/toktape/internal/tape"
)

// paneRow is one row of the answer pane.
//
// Almost every row is plain content that View frames with a space gutter on
// each side. A rule row is the horizontal line between two rows of tiles: it
// has to reach the frame on both sides, so View draws its gutters as rule and
// its borders as junctions instead of as "│".
type paneRow struct {
	text string
	rule bool
}

// paneLayout is the answer pane, plus what View needs in order to join the
// pane's own chrome into the frame around it.
type paneLayout struct {
	rows []paneRow
	// vrules are the display columns, inside the pane's content, of the rules
	// between the tile columns of the bottom grid row; empty when the pane
	// draws none.
	vrules []int
	// vruleAtBottom says those rules reach the pane's last row, which is where
	// the frame's divider runs and therefore where it needs a ┴.
	//
	// There is deliberately no vruleAtTop. The row above the pane is the title
	// bar, which is not chrome with a gap in it: it carries the run's identity
	// and the shimmer that sweeps the rest of the rule. At the sizes this
	// layout is drawn at the model name still covers the rule's column, and
	// where it does not, a junction would sit in the shimmer's path. Either
	// way a ┬ there breaks something the reader is looking at, so the column
	// rule starts at the first tile header instead.
	vruleAtBottom bool

	// page is the page of tiles drawn, from zero, and pages how many there
	// are. The footer prints them; View takes them from here rather than
	// recomputing the grid, so one frame can never disagree with itself.
	page, pages int
}

// plainRows wraps content lines that need no junctions.
func plainRows(lines []string) paneLayout {
	out := make([]paneRow, len(lines))
	for i, line := range lines {
		out[i] = paneRow{text: line}
	}
	return paneLayout{rows: out, page: 0, pages: 1}
}

// leftPane renders the answers as a page of tiles, one tile per concurrent
// stream, each with its own rate and the last few lines of what it has said.
//
// It always returns exactly rows lines of exactly cw columns. The grid is
// resolved here, from the pane's full height: the done footer comes off
// afterwards so that a run finishing cannot change the page size.
func leftPane(m Model, th Theme, t time.Duration, cw, rows int) paneLayout {
	blank := strings.Repeat(" ", cw)

	if len(m.Streams) == 0 {
		l := newLine(th, cw)
		l.add(th.accent, spinnerAt(t)+" ")
		l.add(th.dim, "waiting for the first request")
		return plainRows(fitRows([]string{blank, l.String()}, blank, rows))
	}

	g := m.Grid.resolve(cw, rows)
	avail := rows
	var footer []string
	if m.Done {
		footer = doneFooter(m, th, cw)
		avail -= len(footer)
	}
	return tilePane(m, th, t, g, cw, avail, footer)
}

// streamBlock is one stream's masthead and its answer: the header, the stat
// line, then rows lines of what it has said.
//
// It is the unit a tile is built from, and it is where the two rows above the
// answer are kept together, so nothing can draw a stream's rate without the
// name of the stream it belongs to.
func streamBlock(m Model, th Theme, t time.Duration, s Stream, cw, rows int, showIndex, active bool) []string {
	out := make([]string, 0, rows+2)
	out = append(out, streamHeader(m, th, s, cw, showIndex, active))
	out = append(out, tileStatLine(th, s, cw))
	if rows > 0 {
		out = append(out, streamBody(m, th, t, s, cw, rows, active)...)
	}
	return out
}

// streamHeader is "stream 3/8" on the left and what that stream is doing on
// the right.
//
// The header used to carry the rate. It does not any more: the rate is the
// figure a reader of a concurrent run wants first, and a small dim number
// squeezed against the right edge of a half-width tile was not first (user,
// 2026-09-13). It moved down one row into tileStatLine, and the room that
// freed goes to the state — which, while the prompt is still being evaluated,
// is a bar and two counts rather than a word.
func streamHeader(m Model, th Theme, s Stream, cw int, showIndex, active bool) string {
	l := newLine(th, cw)
	// Every stream's name is ordinary text, the active one included.
	//
	// It used to be accent and bold on whichever tile had the newest token,
	// which put a lit word directly above the one figure the tile exists to
	// report, and moved it from tile to tile every few frames (TTP-28, user
	// 2026-09-13: "화면에 너무 많은 요소들이 강조되어 있다"). Which stream is
	// talking is still said by the cursor breathing at the end of its answer,
	// which sits beside the text it describes rather than on top of a number.
	// (An accent gutter down the left of the answer said it too, until the
	// gutter went, TTP-50.)
	label := th.text
	name, total := "stream", ""
	if showIndex {
		name = fmt.Sprintf("stream %d", s.Index+1)
		total = fmt.Sprintf("/%d", len(m.Streams))
	}
	// The badge sits with the stream's name: it says what the stream is doing,
	// and a thinking model's state is part of how the tile below it reads.
	// Dim, because it is a state label, and absent the moment an answer token
	// arrives.
	badge := thinkingBadge(s)
	if badge != "" {
		badge = " · " + badge
	}

	// The right-hand state, and the room the left-hand identity leaves it. A
	// stream whose badge already names its state prints nothing here rather
	// than the same word twice: "stream 4/8 · thinking" needs no "thinking"
	// against the other edge.
	state, kind := streamState(s), "dim"
	if s.Err != "" {
		kind = "bad"
	} else if badge != "" {
		state = ""
	}
	room := func(withTotal string) int { return cw - width(name) - width(withTotal) - width(badge) - 1 }

	// Prefill draws a bar, so it is a segment rather than a word: it is fitted
	// against the room it has and painted piece by piece below. Unlike the
	// state word it reserves nothing for the badge — a stream with no token
	// cannot be thinking yet, and the bar is gone by the time it can be.
	var seg prefillSeg
	if s.Err == "" && len(s.Tokens) == 0 {
		state = ""
		seg = fitPrefill(s, room(total))
		if seg.w == 0 && room("") >= width(prefillWord) {
			total = ""
			seg = fitPrefill(s, room(total))
		}
	}

	// What the header gives up when it runs out of room, in order: the stream
	// count, then the state itself. Nothing is ever cut in half, and the
	// stream's own name always survives, because it is the only thing naming
	// the tile.
	if state != "" {
		if total != "" && width(state) > room(total) {
			total = ""
		}
		if width(state) > room(total) {
			state = ""
		}
	}

	l.add(label, name)
	l.add(th.dim, total)
	l.add(th.dim, badge)
	if seg.w > 0 {
		l.gapTo(seg.w)
		writePrefillSeg(l, th, seg)
		return l.String()
	}
	if state == "" {
		return l.String()
	}
	l.gapTo(width(state) + 1)
	l.space(1)
	if kind == "bad" {
		l.add(th.bad, state)
	} else {
		l.add(th.dim, state)
	}
	return l.String()
}

// streamState is the word on the right of the header: what this stream is
// doing, not how fast it is doing it.
//
//	failed    the request errored
//	prefill   the prompt is still being evaluated (drawn as a bar, not a word)
//	cut       it finished having produced nothing but reasoning
//	done      it finished
//	thinking  reasoning tokens are arriving and no answer token has
//	answer    it is answering
func streamState(s Stream) string {
	switch {
	case s.Err != "":
		return "failed"
	case len(s.Tokens) == 0:
		return prefillWord
	case thinkingBadge(s) == "thinking · cut":
		return "cut"
	case s.Done:
		return "done"
	case thinkingBadge(s) == "thinking":
		return "thinking"
	}
	return "answer"
}

// streamRate is the client-side decode rate of one stream, used only until the
// server's own timings arrive. Server figures are the record (CLAUDE.md); this
// is the check, and it is measured over the content window alone — first token
// to last, excluding the prefill gap (handover lesson 1).
func streamRate(s Stream) float64 {
	if len(s.Tokens) < 2 {
		return 0
	}
	win := s.Tokens[len(s.Tokens)-1].T - s.Tokens[0].T
	if win <= 0 {
		return 0
	}
	return float64(len(s.Tokens)-1) / win.Seconds()
}

// streamBody is the last few lines of the answer, or the prefill progress when
// no token has arrived yet.
func streamBody(m Model, th Theme, t time.Duration, s Stream, cw, rows int, active bool) []string {
	out := make([]string, 0, rows)
	blank := strings.Repeat(" ", cw)

	if len(s.Tokens) == 0 {
		out = append(out, prefillLine(th, t, cw))
		return fitRows(out, blank, rows)
	}

	// The answer starts at the tile's first column. It used to open every
	// line with a one-cell rule and a space (TTP-50, user 2026-09-14: "팬에
	// 왼쪽에 라인 그려진거 불필요하게 자리 차지하는 것 같아"): the rule
	// separated stacked stream blocks in the list layout, which is gone, and in
	// the grid the tiles are separated by rules of their own. The stream that
	// is talking is still marked by its breathing cursor.
	//
	// Two columns are held back so the cursor never forces a re-wrap when it
	// appears at the end of the last line.
	lines := streamTextLines(s, cw-2)
	// The bands are computed over the whole text and then cut to the visible
	// tail with it, so scrolling a tile cannot move the glow relative to the
	// words it belongs to.
	bands := bodyBands(s, lines, t)
	first := len(lines) - len(tail(lines, rows))
	lastIsText := true
	for i, bl := range tail(lines, rows) {
		l := newLine(th, cw)
		for _, sg := range bands[first+i] {
			l.add(bodyStyle(th, bl, sg.band), sg.text)
		}
		out = append(out, l.String())
		lastIsText = !bl.marker
	}
	// The cursor follows the newest token, so it has no business sitting after
	// the answer marker — that line is chrome, and the token that triggered it
	// starts the line below.
	if !s.Done && lastIsText && len(out) > 0 {
		out[len(out)-1] = appendCursor(th, t, out[len(out)-1], cw, active)
	}
	return fitRows(out, blank, rows)
}

// appendCursor puts the block cursor after the last character of a stream that
// is still generating. On the active stream it breathes through three shades
// of the accent over 1.2 s; the others hold the dimmest shade, so the eye is
// told where the newest token landed without anything blinking.
func appendCursor(th Theme, t time.Duration, line string, cw int, active bool) string {
	trimmed := strings.TrimRight(line, " ")
	used := width(trimmed)
	if used+1 > cw {
		return line
	}
	st := th.accentLow
	if active {
		switch breathPhase(t) {
		case 1:
			st = th.accentMid
		case 2:
			st = th.accentHigh
		}
	}
	return pad(trimmed+th.paint(st, "▍"), cw)
}

// prefillWord is what the header calls a stream that has not produced a token
// yet, and the label the prompt-progress bar hangs off.
const prefillWord = "prefill"

// prefillSeg is the header's prefill state, already fitted to the room the
// header left it: the word, the three-shade progress bar and the counts.
//
// The bar has three parts: the share of the prompt the prefix cache already
// held, the share the server has actually processed since, and what is left.
// The first two are separated because the difference between them is the whole
// question a slow first token raises — a prompt that missed the cache is being
// evaluated from scratch, and the card's cache badge and this bar are two
// views of the same fact (docs/toktape-spec.ko.md §3 M3).
type prefillSeg struct {
	// barW is the bar's width in cells, 0 when there was no room for one;
	// cacheN and procN are how many of those cells the cache held and the
	// server has processed.
	barW, cacheN, procN int
	// counts is " 176/512 · cache 128", or a shorter form, or empty.
	counts string
	// w is the display width of the whole segment. Zero means the header had
	// no room even for the word, and draws nothing.
	w int
}

// fitPrefill sizes the header's prefill segment against the columns it has.
//
// The counts are dropped a part at a time rather than cut: "cache 1" is not a
// smaller truth than "cache 128", it is a different and wrong one. What goes
// first is the cache figure, then the bar shrinks, then the counts go too, and
// the word survives on its own.
func fitPrefill(s Stream, room int) prefillSeg {
	if room < width(prefillWord) {
		return prefillSeg{}
	}
	seg := prefillSeg{w: width(prefillWord)}
	if len(s.Progress) == 0 {
		return seg
	}
	p := s.Progress[len(s.Progress)-1]
	// The bar is fitted before the counts, not after: a two-cell gauge is not
	// a small measure, it is a decoration, and at a tile's width the counts
	// will always win a straight race for the columns. Each candidate has to
	// leave room for a bar worth drawing, so what actually goes first is the
	// cache figure — which the right pane prints as a percentage anyway.
	bar := 0
	if p.Total > 0 {
		bar = 1 + prefillMinBarW
	}
	// processed counts the whole prefix the slot holds, the cached share
	// included: llama-server fills it from the slot's prompt buffer, which
	// starts at the cache length and grows as chunks are evaluated
	// (tools/server/server-context.cpp, `progress.processed =
	// slot.prompt.tokens.size()` after `keep_first(n_past)`; its README puts
	// the overall progress at processed/total and the timed one at
	// (processed-cache)/(total-cache), checked 2026-09-13). So the counts print
	// processed against total directly, and the bar draws the cached share as a
	// second shade underneath the same length.
	done := p.Processed
	for _, cand := range []string{
		fmt.Sprintf(" %d/%d · cache %d", done, p.Total, p.Cache),
		fmt.Sprintf(" %d/%d", done, p.Total),
		"",
	} {
		if seg.w+width(cand)+bar <= room {
			seg.counts = cand
			break
		}
	}
	seg.w += width(seg.counts)
	if p.Total <= 0 {
		return seg
	}
	barW := prefillBarW
	// The bar costs the space that separates it from the word as well.
	if left := room - seg.w - 1; barW > left {
		barW = left
	}
	if barW < prefillMinBarW {
		return seg
	}
	seg.barW = barW
	seg.cacheN = cells(float64(p.Cache)/float64(p.Total), barW)
	seg.procN = cells(float64(done)/float64(p.Total), barW)
	if seg.procN < seg.cacheN {
		seg.procN = seg.cacheN
	}
	seg.w += barW + 1
	return seg
}

// writePrefillSeg paints a fitted segment onto the header line.
func writePrefillSeg(l *lineBuf, th Theme, seg prefillSeg) {
	l.add(th.dim, prefillWord)
	if seg.barW > 0 {
		l.space(1)
		l.add(th.accentLow, repeat('█', seg.cacheN))
		l.add(th.accent, repeat('█', seg.procN-seg.cacheN))
		l.add(th.darkFill, repeat('█', seg.barW-seg.procN))
	}
	l.add(th.dim, seg.counts)
}

// prefillLine is the body of a stream that has not produced a token yet: the
// spinner, and what it is waiting for.
//
// The bar and the counts used to be here. They are in the header now, one row
// up, beside the word that names the state — printing them twice in one tile
// would be the same measurement competing with itself. What is left is the one
// thing the header cannot carry: something moving, so a stream stuck in
// prefill still reads as alive.
func prefillLine(th Theme, t time.Duration, cw int) string {
	l := newLine(th, cw)
	l.add(th.accent, spinnerAt(t))
	l.add(th.dim, " waiting for the first token")
	return l.String()
}

// prefillBarW is the width of the prompt-progress bar. It is the same solid
// block in the same three shades as every other gauge on the screen: the
// prefix the cache already held, what the server has processed since, and what
// is left.
const prefillBarW = 12

// prefillMinBarW is the narrowest bar the header will draw. Under it the
// three shades cannot show a prefix cache and a part-evaluated prompt as
// different lengths, which is the only thing the bar is there to say.
const prefillMinBarW = 6

// cells converts a 0..1 fraction into a whole number of bar cells.
func cells(frac float64, w int) int {
	if frac <= 0 {
		return 0
	}
	if frac >= 1 {
		return w
	}
	n := int(frac*float64(w) + 0.5)
	if n == 0 {
		n = 1
	}
	return n
}

// doneFooter is the two rows that replace the bottom of the answer pane once
// the tape has been written.
func doneFooter(m Model, th Theme, cw int) []string {
	l := newLine(th, cw)
	l.add(th.accentBold, "✓ ")
	if m.TapePath != "" {
		l.add(th.text, "tape saved ")
		l.addTrunc(th.text, m.TapePath)
	} else {
		l.add(th.text, "run complete")
	}
	if !m.Replay {
		l.add(th.dim, " — c for the card")
	}
	return []string{strings.Repeat(" ", cw), l.String()}
}

// fitRows pads lines out to exactly n rows, or cuts them to n.
func fitRows(lines []string, blank string, n int) []string {
	for len(lines) < n {
		lines = append(lines, blank)
	}
	return lines[:n]
}

// modelName is what the title bar calls the model: the GGUF's own name when it
// has one, otherwise the file name with the extension dropped — and, for a
// split set, with the "-00001-of-00009" part marker dropped too, so the bar
// names the model rather than part one of it (TTP-32). card.ModelStem is the
// same trim for a single file, so no existing title moves.
//
// The variant directory the card's MODEL line prints is deliberately not here:
// the title bar is the one place on screen where space is scarcer than
// precision, and the card beside it carries the full label.
func modelName(mi tape.ModelInfo) string {
	if mi.Name != "" {
		return mi.Name
	}
	return card.ModelStem(mi.FileName)
}

// rigSummary collapses the GPU list to "2× RTX 3090". Vendor prefixes are
// dropped because the title bar is the one place on screen where space is
// scarcer than precision; the card prints the full names.
func rigSummary(h tape.HostInfo) string {
	if len(h.GPUs) == 0 {
		return ""
	}
	var names []string
	counts := map[string]int{}
	for _, g := range h.GPUs {
		n := shortGPUName(g.Name)
		if _, ok := counts[n]; !ok {
			names = append(names, n)
		}
		counts[n]++
	}
	var parts []string
	for _, n := range names {
		if counts[n] > 1 {
			parts = append(parts, fmt.Sprintf("%d× %s", counts[n], n))
		} else {
			parts = append(parts, n)
		}
	}
	return strings.Join(parts, " + ")
}

func shortGPUName(n string) string {
	for _, p := range []string{"NVIDIA GeForce ", "NVIDIA ", "AMD ", "Intel "} {
		n = strings.TrimPrefix(n, p)
	}
	return n
}

// styleFor picks the style a flagged value wears: amber when the run is
// compromised, the plain text colour otherwise.
func styleFor(th Theme, warn bool) lipgloss.Style {
	if warn {
		return th.warn
	}
	return th.text
}
