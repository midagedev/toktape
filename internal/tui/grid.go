package tui

import (
	"fmt"
	"strconv"
	"strings"
)

// Grid is the largest arrangement of tiles one page of the answer pane may
// use: Cols across by Rows down, so Cols×Rows streams to a page and the rest
// on the pages after it.
//
// A zero on either axis means "choose from the space available" (see resolve).
// It is a maximum, not a shape: a run with fewer streams than cells shrinks
// the grid to the streams rather than drawing empty tiles.
type Grid struct{ Cols, Rows int }

// DefaultGrid is the arrangement a run uses when nobody asked for another.
//
// Two columns by four rows is eight streams on one page at the size the clips
// are captured at, which is the concurrency an agent workload actually runs
// (docs/toktape-spec.ko.md §3). It is deliberately not the auto grid: the
// default has to be the same shape on every terminal, or two people comparing
// screenshots of the same tape would be comparing layouts.
var DefaultGrid = Grid{Cols: 2, Rows: 4}

// The caps on a user-chosen grid. Past them a tile is not a tile: three
// columns of a 120-column screen leave 26 apiece, which wraps an English
// sentence every four words, and a ninth row leaves no answer under the
// header at all.
const (
	MaxGridCols = 4
	MaxGridRows = 8
)

const (
	// autoCols2Width is the pane width at which the automatic grid stops
	// being a single column. Below it a half pane cannot hold a wrapped
	// sentence.
	autoCols2Width = 80
	// autoBodyLines is the answer budget an automatic row count guarantees
	// every tile. Fewer than four lines and a tile shows a phrase rather than
	// a reply.
	autoBodyLines = 4
)

// String renders a grid the way --grid takes it.
func (g Grid) String() string {
	if g.Cols <= 0 && g.Rows <= 0 {
		return "auto"
	}
	return fmt.Sprintf("%dx%d", g.Cols, g.Rows)
}

// resolve turns g into a concrete grid for an answer pane of cw columns and
// bodyH rows.
//
// bodyH is the pane's whole height, before the done footer comes off it. That
// is deliberate: a row count that fell by one when the run finished would
// reflow every tile at the moment nothing should move, and where the grid also
// decides the page size it would shuffle streams between pages under the
// reader's eye.
func (g Grid) resolve(cw, bodyH int) Grid {
	cols, rows := g.Cols, g.Rows
	if cols <= 0 {
		cols = 1
		if cw >= autoCols2Width {
			cols = 2
		}
	}
	if rows <= 0 {
		// A tile is a header, its answer and its sparkline footer, and every
		// row after the first also costs the rule above it.
		per := 2 + autoBodyLines
		rows = (bodyH + 1) / (per + 1)
	}
	return Grid{Cols: clampInt(cols, 1, MaxGridCols), Rows: clampInt(rows, 1, MaxGridRows)}
}

// cells is how many streams fit on one page.
func (g Grid) cells() int {
	n := g.Cols * g.Rows
	if n < 1 {
		return 1
	}
	return n
}

// ParseGrid reads the --grid argument: "2x4", "2×4" (the spelling the screen
// uses elsewhere), or "0"/"auto" for a grid chosen from the space available.
// A zero on one axis alone ("0x4") automates that axis only.
func ParseGrid(s string) (Grid, error) {
	s = strings.TrimSpace(s)
	switch s {
	case "", "auto":
		return Grid{}, nil
	case "0":
		return Grid{}, nil
	}
	rs := []rune(s)
	sep := -1
	for i, r := range rs {
		if r == 'x' || r == 'X' || r == '×' {
			sep = i
			break
		}
	}
	if sep < 0 {
		return Grid{}, fmt.Errorf("grid %q: want COLSxROWS, for example 2x4", s)
	}
	cols, err := gridAxis(s, string(rs[:sep]), "columns", MaxGridCols)
	if err != nil {
		return Grid{}, err
	}
	rows, err := gridAxis(s, string(rs[sep+1:]), "rows", MaxGridRows)
	if err != nil {
		return Grid{}, err
	}
	return Grid{Cols: cols, Rows: rows}, nil
}

// gridAxis parses one side of a COLSxROWS argument. Zero is "choose for me";
// anything else has to be a positive number within the limit, because a grid
// past it draws tiles too small to read and the user would sooner be told than
// shown.
func gridAxis(whole, part, what string, limit int) (int, error) {
	part = strings.TrimSpace(part)
	if part == "" {
		return 0, fmt.Errorf("grid %q: no %s before or after the x", whole, what)
	}
	n, err := strconv.Atoi(part)
	if err != nil {
		return 0, fmt.Errorf("grid %q: %s is not a number", whole, what)
	}
	if n < 0 {
		return 0, fmt.Errorf("grid %q: %s cannot be negative", whole, what)
	}
	if n > limit {
		return 0, fmt.Errorf("grid %q: at most %d %s", whole, limit, what)
	}
	return n, nil
}

// PageCount is how many pages of tiles this run takes on a w×h screen.
//
// It depends on the screen because an automatic grid does: the page size is
// the grid's, and the grid is chosen from the room the pane has. Callers that
// move the page (the live screen's arrow keys, the player's) ask this rather
// than keeping a count of their own, so a page can never point past the end.
func (m Model) PageCount(w, h int) int {
	cw, bodyH := paneGeometry(w, h)
	per := m.Grid.resolve(cw, bodyH).cells()
	n := len(m.Streams)
	if n <= per {
		return 1
	}
	return (n + per - 1) / per
}

// PageAfter is the page d steps from the current one on a w×h screen, clamped
// to the run's page count. It is the whole of what a key press does.
func (m Model) PageAfter(d, w, h int) int {
	return clampInt(m.Page+d, 0, m.PageCount(w, h)-1)
}

// paneGeometry is the answer pane's content width and height on a w×h screen.
// View lays the frame out with the same arithmetic; this is the copy the page
// maths needs, and the two are pinned together by TestPaneGeometryMatchesView.
func paneGeometry(w, h int) (cw, bodyH int) {
	if w < MinWidth {
		w = MinWidth
	}
	if h < MinHeight {
		h = MinHeight
	}
	inner := w - 2
	return inner - 1 - rightW - 2, h - chromeH
}

func clampInt(v, lo, hi int) int {
	if hi < lo {
		hi = lo
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
