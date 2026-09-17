package tui

import (
	"strconv"
	"strings"
	"time"
)

// The scoreboard is the top panel of the right column: the run's headline rate
// in the big face, so it survives being watched on a phone.
//
// It exists because the figure that settles the argument was the smallest
// thing on the screen. A clip posted to Twitter plays a few hundred pixels
// wide, where a 13-px "148.7 tok/s" is a smudge and the reader has nothing to
// take away (user, 2026-09-17: "실시간tps가 너무 작아서 읽히지 않거든"). The
// number comes from headlineRate, drawn five rows tall.
//
// It is the only place that figure appears while the board is up (TTP-110):
// SPEED's decode row draws it when the screen is too short for the board, and
// not otherwise.
//
// Five rows and not three, in the face the result modal already uses
// (bigfigure.go). That file records the measurement: at three rows the modal's
// figures rasterised to ~19 px on a phone-width timeline, which is the exact
// complaint this panel answers, so a three-row board would have shipped the
// defect a second time. The face is shared rather than copied for the usual
// reason — two faces are two things to keep in agreement.

// scoreboardUnit is the label under the figure: the unit, and the word that
// says which of the three speeds this is.
//
// The word moved here from SPEED's first row (TTP-110, user 2026-09-17: "tps가
// 두군디 같은 숫자가 보이는거"). The board and that row carried the same figure
// from headlineRate five rows apart, which is not a disagreement — it is the
// same sentence printed twice in two sizes, and the second one cost a row the
// pane could not spare (see buildRightPane). What the row had and the board did
// not was the word: whether the figure is a decode rate or a sample, which
// card.IsSample decides. So the word comes up here and the row goes.
func scoreboardUnit(label string) string { return label + " tok/s" }

// scoreboardRows is the board's height: the face, and the unit under it.
//
// The unit sits on a row of its own rather than beside the bottom row the way
// the modal writes it. Inline, "999.9 tok/s" needs 31 columns and the pane has
// 28 at the width the GIF is recorded in, so the board would reach into the
// answer pane on every run rather than only on the fast ones — and intruding
// is the exception the user asked for, not the resting state.
const scoreboardHeight = bigRows + 1

// bigFigureWidth is the columns bigFigure draws s in.
func bigFigureWidth(s string) int {
	w := 0
	for i, r := range s {
		if i > 0 {
			w++
		}
		g, ok := bigGlyphs[r]
		if !ok {
			g = bigGlyphs['?']
		}
		w += width(g[0])
	}
	return w
}

// scoreboardFigure formats the headline rate the way the board draws it: three
// integer digits and one decimal, which is what the user asked the default
// canvas to hold ("기본적으로 3자리와 소숫점 하나", 2026-09-17), and "?" until
// a rate has actually been measured — a board reading 0.0 in half-metre digits
// would be the loudest possible "never print a default you did not observe"
// violation (CLAUDE.md).
func scoreboardFigure(v float64, ok bool) string {
	if !ok || v <= 0 {
		return "?"
	}
	return strconv.FormatFloat(v, 'f', 1, 64)
}

// scoreboardCanvas is the widest figure the board must hold for the whole run:
// "999.9" — three integer digits and one decimal, the default the user set —
// or the run's own final rate when that is wider.
//
// Sizing to the figure on screen would be a mistake the reader sees. The rate
// climbs through the first seconds of every run, so a board fitted to the
// moment would jump a digit wider as it crossed 999.9 and the answer pane
// beside it would reflow mid-clip. A replayed tape carries the whole run from
// its first frame, so the final rate is known at t=0 and the canvas can be
// settled before anything is drawn. Reading the summary here is not the thing
// speedRows refuses to do: nothing unobserved is printed, only counted, and
// what is drawn in that space is still whatever has been measured by t.
//
// A live run has no summary yet, so its board can widen once, when the rate
// first passes the default canvas. That is the one place this can be seen
// moving, and a clip never replays it.
func scoreboardCanvas(m Model) string {
	fig := "999.9"
	final := m.Summary.Aggregate.PerStreamPredictedPerSecond
	if m.Summary.Concurrency > 1 {
		final = m.Summary.Aggregate.AggregatePredictedPerSecond
	}
	if final > 0 {
		if f := scoreboardFigure(final, true); bigFigureWidth(f) > bigFigureWidth(fig) {
			fig = f
		}
	}
	return fig
}

// scoreboardWidth is the board's canvas in columns: the figure it must hold,
// and never less than the pane it sits on top of.
//
// Past the default canvas the board keeps growing rather than shrinking its
// digits — that is the case the user asked it to intrude leftward for
// ("왼쪽으로 레이아웃을 침범하게 만들어줘", 2026-09-17), since a rate that
// needs a fifth character is exactly the rate worth reading from across a
// room.
func scoreboardWidth(canvas, fig string, cw int) int {
	w := bigFigureWidth(canvas)
	if n := bigFigureWidth(fig); n > w {
		w = n
	}
	if w < cw {
		w = cw
	}
	return w
}

// scoreboardRows draws the board and reports the width it needs. The rows are
// exactly that many columns wide; the caller decides what to do when it is
// more than the right pane has (view.go).
func scoreboardRows(m Model, th Theme, t time.Duration, cw int) ([]string, int) {
	rate, ok := headlineRate(m, t)
	fig := scoreboardFigure(rate, ok)
	boardW := scoreboardWidth(scoreboardCanvas(m), fig, cw)

	// A number that grows leftward from a fixed right edge is a scoreboard;
	// one that grows rightward from a fixed left edge is a form field. So the
	// figure is right-aligned in the canvas and the unit sits under its right
	// end, where every other unit in this pane sits.
	glyphs := bigFigure(fig)
	out := make([]string, 0, scoreboardHeight)
	for _, row := range glyphs {
		l := newLine(th, boardW)
		l.space(boardW - width(row))
		l.add(th.accentBold, row)
		out = append(out, l.String())
	}
	unit := scoreboardUnit(rateLabel(m))
	l := newLine(th, boardW)
	l.space(boardW - width(unit))
	l.add(th.dim, unit)
	return append(out, l.String()), boardW
}

// blankRow is n columns of nothing, the row that parts the two panels of the
// right column.
func blankRow(n int) string { return strings.Repeat(" ", n) }
