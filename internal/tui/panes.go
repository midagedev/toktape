package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
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

// streamBlock is one stream as both layouts draw it: the header, then rows
// lines of what it has said.
//
// It is the unit the list and the grid are each built from — the list stacks
// blocks down the pane, the grid puts one inside each tile — so the two can
// never drift apart on what a stream looks like.
func streamBlock(m Model, th Theme, t time.Duration, s Stream, cw, rows int, showIndex, active bool) []string {
	out := make([]string, 0, rows+1)
	out = append(out, streamHeader(m, th, s, cw, showIndex, active))
	if rows > 0 {
		out = append(out, streamBody(m, th, t, s, cw, rows, active)...)
	}
	return out
}

// maxBadgeW is the width of the widest thinkingBadge, separator included. The
// header reserves it whatever the stream is doing, so its shape does not
// change when a model starts or stops thinking.
var maxBadgeW = width(" · thinking · cut")

// streamHeader is "stream 3/8" on the left and this stream's own rate on the
// right. The per-stream rate is the point of the concurrent view: an aggregate
// alone hides a slot that is starving.
func streamHeader(m Model, th Theme, s Stream, cw int, showIndex, active bool) string {
	l := newLine(th, cw)
	// The stream that received the newest token is the one the eye should land
	// on, so it alone is accent and bold; the others are ordinary text. Dim is
	// reserved for labels and chrome.
	label := th.text
	if active {
		label = th.accentBold
	}
	name, total := "stream", ""
	if showIndex {
		name = fmt.Sprintf("stream %d", s.Index+1)
		total = fmt.Sprintf("/%d", len(m.Streams))
	}
	// The badge sits with the stream's name rather than with its rate: it says
	// what the stream is doing, not how fast. Dim, because it is a state
	// label, and absent the moment an answer token arrives.
	badge := thinkingBadge(s)
	if badge != "" {
		badge = " · " + badge
	}

	// plain is the figure on the right of the header; short is the same figure
	// with everything but the rate dropped, for a header too narrow to hold
	// both (a tile is half the width of the list pane).
	var plain, short, kind string
	switch {
	case s.Err != "":
		plain, kind = "failed", "bad"
	case len(s.Tokens) == 0:
		plain, kind = "prefill", "dim"
	default:
		// Server figures are the record, but only once the stream has
		// finished: a tape being replayed carries the final timings from the
		// first frame, and printing them beside a half-written answer would
		// put the header at odds with the live figure in the speed panel.
		live := streamRate(s)
		rate := fmtRate(live)
		if s.Done && s.Timings.PredictedPerSecond > 0 {
			rate = fmtRate(s.Timings.PredictedPerSecond)
		} else if live <= 0 && s.Timings.PredictedPerSecond > 0 {
			rate = fmtRate(s.Timings.PredictedPerSecond)
		}
		ttft := fmtMs(s.Timings.TTFTMs)
		if s.Timings.TTFTMs <= 0 && len(s.Tokens) > 0 {
			ttft = fmtMs(msOf(s.Tokens[0].T - s.StartedAt))
		}
		plain, short, kind = rate+" tok/s · ttft "+ttft, rate+" tok/s", "rate"
	}
	// What the header gives up when it runs out of room, in order: the TTFT,
	// then the stream count, then the figure itself. Nothing is ever cut in
	// half — a clipped number reads as a wrong number — and the stream's own
	// name always survives, because it is the only thing naming the block.
	//
	// The TTFT decision is taken against the widest badge this stream could
	// ever wear, not the one it wears now. Otherwise a tile would reflow the
	// instant its model started to think, and four tiles of one run would
	// print the same figure in two different shapes.
	if kind == "rate" && width(name)+width(total)+maxBadgeW+1+width(plain) > cw {
		plain = short
	}
	if total != "" && width(name)+width(total)+width(badge)+1+width(plain) > cw {
		total = ""
	}
	if width(name)+width(badge)+1+width(plain) > cw {
		plain = ""
	}

	l.add(label, name)
	l.add(th.dim, total)
	l.add(th.dim, badge)
	if plain == "" {
		return l.String()
	}
	l.gapTo(width(plain) + 1)
	l.space(1)
	switch kind {
	case "bad":
		l.add(th.bad, plain)
	case "dim":
		l.add(th.dim, plain)
	default:
		parts := strings.SplitN(plain, " ", 2)
		st := th.text
		if active {
			st = th.accentBold
		}
		l.add(st, parts[0])
		l.add(th.dim, " "+parts[1])
	}
	return l.String()
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
	const indent = 2

	if len(s.Tokens) == 0 {
		out = append(out, prefillLine(th, t, s, cw, indent, active))
		return fitRows(out, blank, rows)
	}

	// Two columns are held back so the cursor never forces a re-wrap when it
	// appears at the end of the last line.
	lines := streamTextLines(s, cw-indent-2)
	lastIsText := true
	for _, bl := range tail(lines, rows) {
		l := newLine(th, cw)
		gutter(l, th, active)
		l.add(bodyStyle(th, bl), bl.text)
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

// gutter draws the one-cell rule that runs down the left of a stream's answer.
// It separates stacked blocks in the dense layout without spending a row on a
// blank line, and on the active stream it is the accent, which is the cheapest
// way to say "this one is talking".
func gutter(l *lineBuf, th Theme, active bool) {
	st := th.dim
	if active {
		st = th.accent
	}
	l.add(st, "▏")
	l.space(1)
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

// prefillLine is the spinner, the prompt-progress bar and the token counts of
// a stream that has not produced a token yet.
//
// The bar has three parts: the share of the prompt the prefix cache already
// held, the share the server has actually processed since, and what is left.
// The first two are separated because the difference between them is the whole
// question a slow first token raises — a prompt that missed the cache is being
// evaluated from scratch, and the card's cache badge and this bar are two
// views of the same fact (docs/toktape-spec.ko.md §3 M3).
func prefillLine(th Theme, t time.Duration, s Stream, cw, indent int, active bool) string {
	l := newLine(th, cw)
	gutter(l, th, active)
	l.add(th.accent, spinnerAt(t))
	l.add(th.dim, " prefill  ")

	if len(s.Progress) == 0 {
		l.add(th.dim, "waiting for the first token")
		return l.String()
	}
	p := s.Progress[len(s.Progress)-1]
	// The counts are dropped a part at a time rather than cut: "cache 1" is
	// not a smaller truth than "cache 128", it is a different and wrong one.
	// A tile is a fraction of the pane, so this line has to survive widths the
	// full-pane layout never asked it for.
	tail := ""
	for _, cand := range []string{
		fmt.Sprintf(" %d/%d · cache %d", p.Processed, p.Total, p.Cache),
		fmt.Sprintf(" %d/%d", p.Processed, p.Total),
	} {
		if width(cand) <= l.left() {
			tail = cand
			break
		}
	}
	barW := prefillBarW
	if room := l.left() - width(tail); barW > room {
		barW = room
	}
	if barW > 0 && p.Total > 0 {
		cacheN := cells(float64(p.Cache)/float64(p.Total), barW)
		procN := cells(float64(p.Processed)/float64(p.Total), barW)
		if procN < cacheN {
			procN = cacheN
		}
		l.add(th.accentLow, repeat('█', cacheN))
		l.add(th.accent, repeat('█', procN-cacheN))
		l.add(th.darkFill, repeat('█', barW-procN))
	}
	l.add(th.dim, tail)
	return l.String()
}

// prefillBarW is the width of the prompt-progress bar. It is the same solid
// block in the same three shades as every other gauge on the screen: the
// prefix the cache already held, what the server has processed since, and what
// is left.
const prefillBarW = 12

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
	l.add(th.dim, " — c for the card")
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
// has one, otherwise the file name with the extension dropped.
func modelName(mi tape.ModelInfo) string {
	if mi.Name != "" {
		return mi.Name
	}
	return strings.TrimSuffix(mi.FileName, ".gguf")
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
