package tui

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/midagedev/toktape/internal/card"
	"github.com/midagedev/toktape/internal/tape"
)

// Screen geometry. The right pane's width is a function of the screen width
// alone (rightWidth), never of the content: every figure in it is tabular, and
// a pane that reflowed with what it shows would turn a comparison of two runs
// into a spot-the-difference puzzle. Two runs at one size still line up cell
// for cell. The left pane takes whatever is left.
const (
	// MinWidth and MinHeight are the smallest screen the layout is designed
	// for. Below them View draws a single message rather than a broken frame.
	MinWidth  = 100
	MinHeight = 30

	// chromeH is the rows View spends on the top border, the divider, the
	// footer and the bottom border.
	chromeH = 4
)

// rightWidth is the machine pane's width, side gutters included, on a screen
// w columns wide: 30 up to 120 columns, then one more column for every four
// the screen gains, up to 44.
//
// 2026-09-13 (TTP-41): it used to be a fixed 30. At the mp4's 156×38 every
// extra column went to the stream tiles and the lower half of the right pane
// stayed blank, which added emptiness rather than density (user: "우리도 좀더
// 이런 조밀한 느낌이면 좋겠어"). At or below 120 columns nothing moved.
func rightWidth(w int) int { return min(44, 30+max(0, (w-120)/4)) }

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
	th := m.Theme
	inner := w - 2
	bodyH := h - chromeH
	paneW := rightWidth(w)
	leftW := inner - 1 - paneW

	var body []string
	split := m.Err == ""
	var pane paneLayout
	if split {
		// The right column is two panels, not one. The scoreboard takes the
		// top of it and is the only thing on the screen allowed to set its own
		// width; the machine panel under it keeps the tabular width every
		// figure in it is aligned to, and is built for the rows that are left
		// so its resource graphs still choose a height that fits (TTP-39).
		board, boardW := scoreboardRows(m, th, t, paneW-2)
		boardH := len(board) + 1 // the blank row that parts the two panels
		board = append(board, blankRow(boardW))
		boardDrawn := true
		if !rightPaneFits(m, th, t, paneW-2, bodyH-boardH, true) {
			// Not enough screen for both. The machine panel is the one that
			// loses a whole section when it is squeezed, so the headline is
			// the one that gives way (right.go).
			board, boardW, boardH, boardDrawn = nil, 0, 0, false
		}
		// Columns the board reaches past the pane divider into the answer
		// pane. Zero at the width the default canvas was sized for; positive
		// once the rate needs a fifth digit, which is the case the board was
		// asked to grow leftward for rather than shrink into
		// (user, 2026-09-17: "왼쪽으로 레이아웃을 침범하게 만들어줘").
		notch := 0
		if boardH > 0 {
			notch = max(0, boardW-(paneW-2))
		}

		pane = leftPane(m, th, t, leftW-2, bodyH)
		right := rightPane(m, th, t, paneW-2, bodyH-boardH, boardDrawn)
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
			text, cell := row.text, ""
			if i < boardH {
				cell = board[i]
				// The divider moves with the board, so the answer pane loses
				// exactly the columns the board gained and the frame is still
				// w columns wide.
				// pad after the clip: a cut that would land inside a wide
				// rune stops one column short of the room it was given, and
				// the frame's right border would move with it.
				text = pad(clipANSI(text, leftW-2-notch), leftW-2-notch)
				if notch > 0 && i == boardH-1 {
					// The board's last row is the blank that parts the two
					// panels, and where the wall steps back to the column the
					// rest of the frame keeps it in. Drawn as the corner it
					// is: up-and-right at the board's wall, left-and-down at
					// the pane's.
					step := th.paint(th.dim, "└"+repeat('─', notch-1)+"┐")
					body = append(body, lb+gutter+text+gutter+step+" "+blankRow(paneW-2)+" "+bar)
					continue
				}
			} else {
				cell = right[i-boardH]
			}
			body = append(body, lb+gutter+text+gutter+rb+" "+cell+" "+bar)
		}
		switch m.Mode {
		case ModePrompt:
			overlayModal(th, body, promptModal(m, th, modalWidth(inner)), inner)
		case ModeCard:
			// The result over the live screen, not in place of it
			// (2026-09-14): the clip's last frame keeps the dashboard the
			// reader was watching, and the box says what it came to.
			overlayModal(th, body, resultModal(m, th, modalWidth(inner)), inner)
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

	divider := "├" + repeat('─', leftW) + "┴" + repeat('─', paneW) + "┤"
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

func (r titleRole) style(th Theme) style {
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
	return titleBorder(th, t, inner, titleSegments(m))
}

// titleBorder is topBorder for any title: the chat screen draws the same bar
// with a title of its own (chatview.go), and one shimmer is one thing to keep
// right.
func titleBorder(th Theme, t time.Duration, inner int, segs []titleSeg) string {
	l := newLine(th, inner)
	l.add(th.dim, "─ ")
	for _, seg := range segs {
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

// titleSegments builds "toktape · <model> · <round> · <engine> · <rig>".
// The model and the rig are left out until the recorder has learned them, so
// the bar fills in as /props and the GPU probe answer. So is the engine until
// attach; after it, an engine that has not identified itself prints "?"
// (TTP-37), the word the card uses, never "unknown" or a kind nobody observed.
//
// The round segment ("round 2/6 sql-2") exists only on a rounds run (TTP-38).
// Its place is the order the bar gives segments up in when it is too narrow:
// topBorder cuts from the end, so the rig goes first, then the engine, then
// the round, and the model — the thing a reader of the bar is looking for —
// last.
func titleSegments(m Model) []titleSeg {
	s := m.Summary
	segs := []titleSeg{{text: "toktape", role: roleBrand}}
	add := func(text string, role titleRole) {
		if strings.TrimSpace(text) == "" {
			return
		}
		segs = append(segs, titleSeg{text: " · ", role: roleDim}, titleSeg{text: text, role: role})
	}
	// The model segment is card.ModelNameQuant: the model that actually ran —
	// the variant directory for a split set, the file's stem for one file —
	// never the GGUF header's general.name, which a re-quantised variant keeps
	// from the base model (2026-09-15, user: "모델이 다 실제값으로 찍혀야해").
	add(card.ModelNameQuant(s.Model), roleText)
	if m.Rounds > 1 {
		add(roundLabel(m), roleDim)
	}
	// Before attach the live model has neither a kind nor a build: nothing has
	// been observed yet, so the segment waits like the model's does. After
	// attach the recorder always stamps a kind (tape.ServerUnknown when it
	// could not tell), so the "?" appears exactly when there is an answer.
	if s.Server.Kind != "" || s.Server.Build != "" {
		add(strings.Join(nonEmpty(engineKind(s.Server.Kind), s.Server.Build), " "), roleDim)
	}
	add(rigSummary(s.Host), roleDim)
	return segs
}

// roundLabel is "round 2/6 sql-2", or "round 2/6" for a round the prompts
// file did not name. A chat tape's rounds are its turns and say so.
func roundLabel(m Model) string {
	label := fmt.Sprintf("%s %d/%d", roundNoun(m), m.Round+1, m.Rounds)
	if m.RoundName != "" {
		label += " " + m.RoundName
	}
	return label
}

// roundNoun is what the record screen calls one round: "turn" on a chat
// tape, where each round is one exchange the person typed, "round" otherwise.
// Every round wording the replay shows goes through it.
func roundNoun(m Model) string {
	if m.Summary.IsChat() {
		return "turn"
	}
	return "round"
}

// engineKind is the engine's name as the title prints it: "?" for an engine
// that has not identified itself, "" or tape.ServerUnknown alike (TTP-37).
// The build joins it the way internal/card's engineString joins them, so an
// unknown engine with a build reads "? b3650" and one with nothing is a lone
// "?". The title never carried the commit and still does not.
func engineKind(k tape.ServerKind) string {
	if k == "" || k == tape.ServerUnknown {
		return "?"
	}
	return string(k)
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
	case m.Replay:
		hints = ""
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
	base, warn, bad style
}

// writeCells appends sparkline or strip cells, painting each severity in its
// own colour. Runs of one colour are emitted as a single styled segment so a
// frame does not carry an escape sequence per column.
func writeCells(l *lineBuf, th Theme, cells []Cell, pal cellPalette) {
	styleOf := func(sev Severity) style {
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
		b.Grow(jLen(cells, i))
		j := i
		for j < len(cells) && cells[j].Sev == sev {
			if r := cells[j].R; r < utf8.RuneSelf {
				b.WriteByte(byte(r))
			} else {
				b.WriteRune(r)
			}
			j++
		}
		l.add(styleOf(sev), b.String())
		i = j
	}
}

// jLen bounds the bytes one severity run of cells can take: one byte per
// cell minimum, so the Builder never grows from zero (TTP-123).
func jLen(cells []Cell, i int) int {
	n := 0
	for j := i; j < len(cells) && cells[j].Sev == cells[i].Sev; j++ {
		n++
	}
	return n
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

// errorBody renders a fatal error in place of the two panes.
func errorBody(m Model, th Theme, cw int) []string {
	return []string{
		strings.Repeat(" ", cw),
		pad(th.paint(th.bad, clip("✗ "+m.Err, cw)), cw),
	}
}

// modalWidth is the box width both modals use: most of the screen, capped
// where a line of text stops being comfortable to read.
func modalWidth(inner int) int {
	boxW := min(inner-8, 86)
	if boxW < 40 {
		boxW = inner - 4
	}
	return boxW
}

// overlayModal replaces the middle rows of the body with a modal's rows. It
// overwrites whole rows rather than punching a hole in them: an inset that
// had to preserve the pane separator underneath would have to cut styled text
// mid-escape, and a clean full-width box is the better read anyway.
func overlayModal(th Theme, body, rows []string, inner int) {
	if len(rows) == 0 {
		return
	}
	boxW := width(rows[0])
	if len(rows) > len(body) {
		rows = rows[:len(body)]
	}
	top := (len(body) - len(rows)) / 2
	gutter := (inner - boxW) / 2
	bar := th.paint(th.dim, "│")
	for i, row := range rows {
		body[top+i] = bar + strings.Repeat(" ", gutter) + row +
			strings.Repeat(" ", inner-gutter-boxW) + bar
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
