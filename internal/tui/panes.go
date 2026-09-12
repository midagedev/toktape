package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/midagedev/toktape/internal/tape"
)

// leftPane renders the answers: one block per concurrent stream, each with its
// own rate and the last few lines of what it has said.
//
// It always returns exactly rows lines of exactly cw columns.
func leftPane(m Model, th Theme, t time.Duration, cw, rows int) []string {
	out := make([]string, 0, rows)
	blank := strings.Repeat(" ", cw)

	if len(m.Streams) == 0 {
		out = append(out, blank)
		l := newLine(th, cw)
		l.add(th.accent, spinnerAt(t)+" ")
		l.add(th.dim, "waiting for the first request")
		out = append(out, l.String())
		return fitRows(out, blank, rows)
	}

	avail := rows
	var footer []string
	if m.Done {
		footer = doneFooter(m, th, cw)
		avail -= len(footer)
	}

	n := len(m.Streams)
	shown, bodyLines, gap := streamLayout(n, avail, m.Done)
	active := m.activeStream()

	for i := 0; i < shown; i++ {
		s := m.Streams[i]
		if i > 0 && gap > 0 {
			out = append(out, blank)
		}
		out = append(out, streamHeader(m, th, s, cw, i == 0 || n > 1, s.Index == active))
		out = append(out, streamBody(m, th, t, s, cw, bodyLines[i], s.Index == active)...)
	}
	if shown < n {
		l := newLine(th, cw)
		l.add(th.dim, fmt.Sprintf("  +%d more streams", n-shown))
		out = append(out, l.String())
	}
	out = fitRows(out, blank, avail)
	return append(out, footer...)
}

// streamLayout decides how many streams to show and how many answer lines each
// one gets.
//
// While the run is live every stream gets the same budget — three lines, then
// two, then one — because a block that grew as its neighbour finished would
// make the text jump around under the reader's eye. Once the run is over
// nothing moves again, so the rows are shared out instead and the pane fills
// with the tail of every answer, the odd rows going to the streams at the top.
func streamLayout(n, avail int, done bool) (shown int, bodyLines []int, gap int) {
	switch {
	case n < 1:
		return 0, nil, 0
	case n == 1:
		return 1, []int{max(1, avail-1)}, 0
	}
	// One header per stream comes off the top whatever the state.
	if body := avail - n; done && body >= n {
		lines := make([]int, n)
		base, extra := body/n, body%n
		for i := range lines {
			lines[i] = base
			if i < extra {
				lines[i]++
			}
		}
		return n, lines, 0
	}
	// Live, or a pane too short to give every finished stream a line: answer
	// lines are worth more than breathing room, so the per-stream budget is
	// the outer choice and the blank line between blocks is given up first.
	for k := 3; k >= 1; k-- {
		for _, g := range []int{1, 0} {
			if n*(1+k)+(n-1)*g <= avail {
				return n, uniform(n, k), g
			}
		}
	}
	shown = avail / 2
	if shown < 1 {
		shown = 1
	}
	if shown > n {
		shown = n
	}
	if shown < n {
		shown-- // leave a row for the "+N more" line
		if shown < 1 {
			shown = 1
		}
	}
	return shown, uniform(shown, 1), 0
}

// uniform is the same line budget for every stream.
func uniform(n, k int) []int {
	out := make([]int, n)
	for i := range out {
		out[i] = k
	}
	return out
}

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
	if showIndex {
		l.add(label, fmt.Sprintf("stream %d", s.Index+1))
		l.add(th.dim, fmt.Sprintf("/%d", len(m.Streams)))
	} else {
		l.add(label, "stream")
	}

	var plain, kind string
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
		plain, kind = rate+" tok/s · ttft "+ttft, "rate"
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
	lines := wrap(s.Text, cw-indent-2)
	for _, line := range tail(lines, rows) {
		l := newLine(th, cw)
		gutter(l, th, active)
		l.add(th.text, line)
		out = append(out, l.String())
	}
	if !s.Done && len(out) > 0 {
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
	tail := fmt.Sprintf(" %d/%d · cache %d", p.Processed, p.Total, p.Cache)
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
