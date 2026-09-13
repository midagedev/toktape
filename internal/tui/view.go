package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/midagedev/toktape/internal/card"
)

// Screen geometry. The right pane is a fixed 30 columns because every figure
// in it is tabular, and a pane that reflows turns a comparison of two runs
// into a spot-the-difference puzzle; the left pane takes whatever is left.
const (
	// MinWidth and MinHeight are the smallest screen the layout is designed
	// for. Below them View draws a single message rather than a broken frame.
	MinWidth  = 100
	MinHeight = 30

	// rightW is the width of the machine pane, its side gutters included.
	rightW = 30
	// chromeH is the rows View spends on the top border, the divider, the
	// footer and the bottom border.
	chromeH = 4
)

// View renders one frame of m at clip time t into a w×h block of text.
//
// The result is exactly h lines, each exactly w display columns, measured with
// East Asian width. t only drives animation: which spinner frame is up, how
// far a bar has travelled toward its target, where the header shimmer is. The
// data comes from m, and m changes only on events — so View(m, t, w, h) is
// reproducible, which is what lets a .tape replay to the same frames
// (docs/toktape-spec.ko.md §1 decision 10).
func View(m Model, t time.Duration, w, h int) string {
	if w < MinWidth || h < MinHeight {
		return tooSmall(w, h)
	}
	if m.Mode == ModeCard && m.Err == "" {
		return cardScreen(m, w, h)
	}
	th := m.Theme
	inner := w - 2
	bodyH := h - chromeH
	leftW := inner - 1 - rightW

	var body []string
	split := m.Err == ""
	var pane paneLayout
	if split {
		pane = leftPane(m, th, t, leftW-2, bodyH)
		right := rightPane(m, th, t, rightW-2, bodyH)
		bar := th.paint(th.dim, "│")
		for i := 0; i < bodyH; i++ {
			row := pane.rows[i]
			// A tile rule runs the full width of the answer pane, gutters
			// included, and lands on the frame at both ends: ├ on the outer
			// border and ┤ where the pane separator carries on past it.
			lb, rb, gutter := bar, bar, " "
			if row.rule {
				lb = th.paint(th.dim, "├")
				rb = th.paint(th.dim, "┤")
				gutter = th.paint(th.dim, "─")
			}
			body = append(body, lb+gutter+row.text+gutter+rb+" "+right[i]+" "+bar)
		}
		if m.Mode == ModePrompt {
			overlayPrompt(m, th, body, inner)
		}
	} else {
		content := errorBody(m, th, inner-2)
		bar := th.paint(th.dim, "│")
		for i := 0; i < bodyH; i++ {
			row := strings.Repeat(" ", inner-2)
			if i < len(content) {
				row = content[i]
			}
			body = append(body, bar+" "+row+" "+bar)
		}
	}

	divider := "├" + repeat('─', leftW) + "┴" + repeat('─', rightW) + "┤"
	if !split {
		divider = "├" + repeat('─', inner) + "┤"
	} else if pane.vruleAtBottom {
		// The tile column rules end on this line. Content column c sits two
		// columns into the frame: the border, then the pane's gutter.
		for _, c := range pane.vrules {
			divider = spliceRune(divider, c+2, '┴')
		}
	}

	lines := make([]string, 0, h)
	lines = append(lines, topBorder(m, th, t, inner))
	lines = append(lines, body...)
	lines = append(lines, th.paint(th.dim, divider))
	lines = append(lines, th.paint(th.dim, "│")+" "+footerLine(m, th, inner-2, pane.page, pane.pages)+" "+th.paint(th.dim, "│"))
	lines = append(lines, th.paint(th.dim, "└"+repeat('─', inner)+"┘"))
	return strings.Join(lines, "\n")
}

// titleRole decides a title segment's colour.
type titleRole int

const (
	roleBrand titleRole = iota
	roleText
	roleDim
)

type titleSeg struct {
	text string
	role titleRole
}

func (r titleRole) style(th Theme) lipgloss.Style {
	switch r {
	case roleBrand:
		return th.accent
	case roleText:
		return th.text
	default:
		return th.dim
	}
}

// topBorder draws the title bar: the identity of the run, then a rule that a
// six-cell highlight travels along once every four seconds. The shimmer is the
// only motion in the chrome; everything else up there is static by design.
func topBorder(m Model, th Theme, t time.Duration, inner int) string {
	l := newLine(th, inner)
	l.add(th.dim, "─ ")
	for _, seg := range titleSegments(m) {
		if l.left() <= 1 {
			break
		}
		l.addTrunc(seg.role.style(th), seg.text)
	}
	l.space(1)

	// The highlight sweeps the rule, not the whole border: the title covers
	// most of the bar's width, and a shimmer that spent two thirds of its
	// cycle hidden underneath it would read as a flicker rather than a sweep.
	fillW := l.left()
	if fillW > 0 {
		start := shimmerStart(t, fillW)
		bright := func(c int) bool { return ((c-start)%fillW+fillW)%fillW < shimmerW }
		for i := 0; i < fillW; {
			on := bright(i)
			n := 1
			for i+n < fillW && bright(i+n) == on {
				n++
			}
			st := th.dim
			if on {
				st = th.accentHigh
			}
			l.addRaw(st, repeat('─', n))
			i += n
		}
	}
	return th.paint(th.dim, "┌") + l.String() + th.paint(th.dim, "┐")
}

// titleSegments builds "toktape · <model> · <engine> · <rig>". Anything the
// recorder has not learned yet is left out rather than guessed, so the bar
// fills in as /props and the GPU probe answer.
func titleSegments(m Model) []titleSeg {
	s := m.Summary
	segs := []titleSeg{{text: "toktape", role: roleBrand}}
	add := func(text string, role titleRole) {
		if strings.TrimSpace(text) == "" {
			return
		}
		segs = append(segs, titleSeg{text: " · ", role: roleDim}, titleSeg{text: text, role: role})
	}
	add(strings.Join(nonEmpty(modelName(s.Model), s.Model.Quant), " "), roleText)
	add(strings.Join(nonEmpty(string(s.Server.Kind), s.Server.Build), " "), roleDim)
	add(rigSummary(s.Host), roleDim)
	return segs
}

// footerLine is the token latency strip and the key hints.
//
// Each token is one glyph whose width is its inter-token latency against the
// window's p95 (spec §3.2 S3), so a stall reads as a thick block among
// hairlines. The p50 of the same window is printed beside it: the strip says
// how even the stream is, and that number says what "even" costs.
func footerLine(m Model, th Theme, w, page, pages int) string {
	l := newLine(th, w)
	hints := "q quit · p prompt"
	switch {
	case m.Mode == ModePrompt:
		hints = "p close · q quit"
	case m.Mode == ModeCard:
		hints = "c live · q quit"
	case m.Done:
		hints = "c card · p prompt · q quit"
	}
	hintW := width(hints)

	// Where the run takes more than one page of tiles, the footer says which
	// page is up and how to move. Its room is reserved whenever there is more
	// than one page, not only when the hint happens to be drawn, so the strip
	// beside it keeps one width for the whole run.
	paging := ""
	if pages > 1 {
		paging = fmt.Sprintf("page %d/%d · ←/→", page+1, pages)
	}

	l.add(th.dim, "token latency ")
	series := m.latencySeries(l.left())
	median := "p50 " + fmtMs(percentile(series, 0.5))
	pagingW := 0
	if paging != "" {
		pagingW = width(paging) + 2
	}
	stripW := l.left() - width(median) - pagingW - hintW - 4
	if stripW < minStripW && paging != "" {
		// Too tight for the keys; the page number is the half that says
		// something the reader cannot work out for themselves.
		paging = fmt.Sprintf("page %d/%d", page+1, pages)
		stripW += pagingW - (width(paging) + 2)
		pagingW = width(paging) + 2
	}
	cells := LatencyStrip(m.latencySeries(stripW), stripW)
	// The newest token is pinned to the right-hand end, so the strip scrolls
	// left under a fixed edge instead of growing out of the label.
	//
	// The strip runs along the bottom of the screen for its whole width, which
	// makes it the largest series on it, so it is drawn in the chrome's own
	// dim and the tokens past the window's p95 are lifted to plain text rather
	// than to amber (TTP-28: the strip's one amber block was competing with
	// the figures, and an even run has no outliers worth an alarm). A stall at
	// three times the median is a real event and keeps the warm hue.
	l.space(stripW - len(cells))
	writeCells(l, th, cells, cellPalette{base: th.dim, warn: th.text, bad: th.warn})
	l.space(2)
	l.add(th.dim, median)
	if paging != "" {
		l.space(2)
		l.add(th.dim, paging)
	}
	l.gapTo(hintW)
	l.add(th.dim, hints)
	return l.String()
}

// minStripW is the narrowest token-latency strip worth drawing. Below it the
// strip stops being a shape and becomes a smudge.
const minStripW = 20

// cellPalette is the three colours a series of cells is painted in: the
// ordinary value, the one past its first threshold, and the one past its
// second.
//
// It is a parameter rather than the theme's warn and bad because the emphasis
// contract gives each series its own answer to "how loud is an outlier here"
// (TTP-28). A latency strip marks its slowest tokens by making them plain text
// among dim ones — the shape is the message and a coloured block in the middle
// of it reads as an alarm the run did not raise. A fault sparkline does raise
// one, and goes warm.
type cellPalette struct {
	base, warn, bad lipgloss.Style
}

// writeCells appends sparkline or strip cells, painting each severity in its
// own colour. Runs of one colour are emitted as a single styled segment so a
// frame does not carry an escape sequence per column.
func writeCells(l *lineBuf, th Theme, cells []Cell, pal cellPalette) {
	style := func(sev Severity) lipgloss.Style {
		switch sev {
		case SevWarn:
			return pal.warn
		case SevBad:
			return pal.bad
		default:
			return pal.base
		}
	}
	for i := 0; i < len(cells); {
		sev := cells[i].Sev
		var b strings.Builder
		j := i
		for j < len(cells) && cells[j].Sev == sev {
			b.WriteRune(cells[j].R)
			j++
		}
		l.add(style(sev), b.String())
		i = j
	}
}

// tooSmall renders the "make the window bigger" frame. It still returns
// exactly w×h so a caller that blits the result into a fixed region is safe.
func tooSmall(w, h int) string {
	if w <= 0 || h <= 0 {
		return ""
	}
	msg := "toktape needs at least 100×30"
	lines := make([]string, h)
	for i := range lines {
		if i == h/2 {
			lines[i] = pad(clip(center(msg, w), w), w)
		} else {
			lines[i] = strings.Repeat(" ", w)
		}
	}
	return strings.Join(lines, "\n")
}

// cardScreen replaces the entire screen with the result card.
//
// "c" drops the chrome rather than framing the card inside it. The card is
// card.CardWidth (72) columns and about thirty rows; nested inside the live
// screen's border it would not fit an 80×24-descended terminal, and a card
// reflowed or clipped to fit is no longer the fixed layout that makes it worth
// posting (docs/toktape-spec.ko.md §4). This is the moment the screen stops
// being a monitor and becomes the artifact.
func cardScreen(m Model, w, h int) string {
	th := m.Theme
	summary := m.Summary
	body := strings.Split(strings.TrimRight(card.Text(&summary), "\n"), "\n")

	foot := newLine(th, w)
	hint := "c live · q quit"
	if m.TapePath != "" {
		foot.add(th.accent, "✓ ")
		foot.add(th.dim, "saved ")
		foot.addTrunc(th.text, m.TapePath)
	}
	foot.gapTo(width(hint) + 2)
	foot.space(2)
	foot.add(th.dim, hint)

	lines := make([]string, 0, h)
	top := (h - 1 - len(body)) / 2
	if top < 0 {
		top = 0
	}
	for i := 0; i < top; i++ {
		lines = append(lines, strings.Repeat(" ", w))
	}
	for _, line := range body {
		if len(lines) >= h-1 {
			break
		}
		lines = append(lines, pad(th.paint(th.text, clip(center(line, w), w)), w))
	}
	for len(lines) < h-1 {
		lines = append(lines, strings.Repeat(" ", w))
	}
	lines = append(lines, foot.String())
	return strings.Join(lines, "\n")
}

// errorBody renders a fatal error in place of the two panes.
func errorBody(m Model, th Theme, cw int) []string {
	return []string{
		strings.Repeat(" ", cw),
		pad(th.paint(th.bad, clip("✗ "+m.Err, cw)), cw),
	}
}

// overlayPrompt replaces the middle rows of the body with the rendered-prompt
// modal. It overwrites whole rows rather than punching a hole in them: an
// inset that had to preserve the pane separator underneath would have to cut
// styled text mid-escape, and a clean full-width card is the better read
// anyway.
func overlayPrompt(m Model, th Theme, body []string, inner int) {
	boxW := min(inner-8, 86)
	if boxW < 40 {
		boxW = inner - 4
	}
	rows := promptModal(m, th, boxW)
	if len(rows) > len(body) {
		rows = rows[:len(body)]
	}
	top := (len(body) - len(rows)) / 2
	gutter := (inner - boxW) / 2
	for i, row := range rows {
		body[top+i] = th.paint(th.dim, "│") +
			strings.Repeat(" ", gutter) + row +
			strings.Repeat(" ", inner-gutter-boxW) + th.paint(th.dim, "│")
	}
}

func nonEmpty(vals ...string) []string {
	out := make([]string, 0, len(vals))
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			out = append(out, v)
		}
	}
	return out
}
