package tui

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/midagedev/toktape/internal/card"
)

// boardRows is the right pane's top rows of a frame, one entry per body row,
// trailing padding trimmed — the same slice rightPaneRows takes, stopped at
// the unit row.
func boardRows(t *testing.T, frame string) []string {
	t.Helper()
	rows := rightPaneRows(frame)
	if len(rows) < bigRows+1 {
		t.Fatalf("frame has only %d pane rows", len(rows))
	}
	return rows[:bigRows+1]
}

// TestScoreboardDrawsTheDecodeFigure: the board and SPEED's decode row are the
// same measurement, because both read headlineRate. A change that reduced the
// tape twice would show up here as two different numbers on one frame.
//
// FAIL-first: with the board drawing the per-stream mean instead of the
// aggregate, every subtest below fails on the figure.
func TestScoreboardDrawsTheDecodeFigure(t *testing.T) {
	for _, at := range []time.Duration{midRun, doneAt} {
		m := goldenModel(t, at)
		rate, ok := headlineRate(m, at)
		if !ok {
			t.Fatalf("no headline rate at %v", at)
		}
		want := bigFigure(strconv.FormatFloat(rate, 'f', 1, 64))
		got := boardRows(t, View(m, at, 120, 36))
		for i, row := range want {
			if !strings.HasSuffix(strings.TrimRight(got[i], " "), strings.TrimRight(row, " ")) {
				t.Errorf("board row %d is %q, want it to end in %q", i, got[i], row)
			}
		}
		// The word under the figure is the one SPEED's row used to carry
		// (TTP-110, 2026-09-17), so a tape the card calls a sample must say
		// "sample tok/s" here and nowhere else on the screen.
		wantUnit := scoreboardUnit(rateLabel(m))
		if u := strings.TrimSpace(got[bigRows]); u != wantUnit {
			t.Errorf("the row under the face is %q, want %q", u, wantUnit)
		}
	}
}

// TestScoreboardSaysUnknownBeforeItIsMeasured: at t=0 no rate has been
// observed, and the board says so rather than drawing a zero the size of a
// hand ("never print a default you did not observe", CLAUDE.md).
//
// FAIL-first: with scoreboardFigure returning the formatted value whatever it
// is, the board reads 0.0 here and the digits below appear in the frame.
func TestScoreboardSaysUnknownBeforeItIsMeasured(t *testing.T) {
	m := goldenModel(t, 0)
	rows := boardRows(t, View(m, 0, 120, 36))
	want := bigFigure("?")
	for i, row := range want {
		if !strings.HasSuffix(strings.TrimRight(rows[i], " "), strings.TrimRight(row, " ")) {
			t.Errorf("board row %d is %q, want the %q face", i, rows[i], "?")
		}
	}
}

// TestScoreboardCanvasIsSettledBeforeTheRateArrives: a run whose settled rate
// needs more room than the default canvas has a board that wide from its first
// frame, while the live figure is still two digits.
//
// This is what keeps the answer pane from reflowing mid-clip. A board fitted
// to the figure on screen would be 28 columns at t=0 and 31 once the rate
// climbed past 999.9, and the tiles beside it would lose three columns while
// the reader was reading them.
//
// FAIL-first: with scoreboardWidth measuring the live figure instead of the
// canvas, the board is the pane's width at t=0 and this fails.
func TestScoreboardCanvasIsSettledBeforeTheRateArrives(t *testing.T) {
	m := goldenModel(t, 0)
	m.Summary.Concurrency = 4
	m.Summary.Aggregate.AggregatePredictedPerSecond = 1493.2
	want := bigFigureWidth("1493.2")
	if want <= 28 {
		t.Fatalf("the fixture no longer overflows the pane: %d columns", want)
	}
	for _, at := range []time.Duration{0, midRun, doneAt} {
		if _, got := scoreboardRows(m, PlainTheme(), at, 28); got != want {
			live, _ := headlineRate(m, at)
			t.Errorf("at %v the board is %d columns, want %d (live figure %q)",
				at, got, want, scoreboardFigure(live, true))
		}
	}
}

// TestScoreboardIntrudesLeftwardWhenItOverflows: a rate that needs more than
// the default canvas takes its columns from the answer pane rather than
// shrinking (user, 2026-09-17: "왼쪽으로 레이아웃을 침범하게 만들어줘"). The
// pane divider moves left on the board's rows, steps back under it, and the
// frame is still exactly the screen.
//
// FAIL-first: with the notch forced to zero the divider does not move and the
// step row carries no corner, so both halves below fail.
func TestScoreboardIntrudesLeftwardWhenItOverflows(t *testing.T) {
	m := goldenModel(t, doneAt)
	m.Summary.Concurrency = 4
	m.Summary.Aggregate.AggregatePredictedPerSecond = 1493.2
	const w, h = 120, 36
	frame := View(m, doneAt, w, h)
	lines := strings.Split(frame, "\n")
	if len(lines) != h {
		t.Fatalf("frame is %d rows, want %d", len(lines), h)
	}
	for i, l := range lines {
		if got := card.Width(l); got != w {
			t.Fatalf("row %d is %d columns, want %d:\n%s", i, got, w, frame)
		}
	}
	// The divider on a board row sits left of the one on a pane row.
	divider := func(y int) int {
		rs := []rune(card.StripANSI(lines[y]))
		at := -1
		for x := 0; x < len(rs)-1; x++ {
			if rs[x] == '│' || rs[x] == '┤' || rs[x] == '┐' {
				at = x
			}
		}
		return at
	}
	board, pane := divider(1), divider(h-4)
	if board >= pane {
		t.Errorf("the board's divider is at column %d and the pane's at %d; the board did not intrude\n%s", board, pane, frame)
	}
	step := card.StripANSI(lines[bigRows+2])
	if !strings.Contains(step, "└") || !strings.Contains(step, "┐") {
		t.Errorf("the row under the board carries no step from one wall to the other:\n%s", step)
	}
}

// TestScoreboardGivesWayOnTheShortestScreen: at MinHeight the board would cost
// RESOURCES its rows and leave the heading with nothing under it, so it is not
// drawn and the machine panel is whole.
//
// FAIL-first: without the fit check the pane's last section is its own title
// and this fails on the CPU row.
func TestScoreboardGivesWayOnTheShortestScreen(t *testing.T) {
	m := goldenModel(t, midRun)
	rows := rightPaneRows(View(m, midRun, MinWidth, MinHeight))
	at := -1
	for i, row := range rows {
		if f := strings.Fields(row); len(f) > 0 && f[0] == "RESOURCES" {
			at = i
		}
	}
	if at < 0 {
		t.Fatalf("no RESOURCES section:\n%s", strings.Join(rows, "\n"))
	}
	if at+1 >= len(rows) || !strings.HasPrefix(strings.TrimSpace(rows[at+1]), "CPU") {
		t.Errorf("RESOURCES has no rows under it on the shortest screen:\n%s", strings.Join(rows, "\n"))
	}
}
