package tui

import (
	"testing"
	"time"
)

func TestParseGrid(t *testing.T) {
	ok := []struct {
		in   string
		want Grid
	}{
		{"", Grid{}},
		{"auto", Grid{}},
		{"0", Grid{}},
		{"0x0", Grid{}},
		{"2x4", Grid{2, 4}},
		{"2X4", Grid{2, 4}},
		{"2×4", Grid{2, 4}},
		{" 1x2 ", Grid{1, 2}},
		{"0x4", Grid{0, 4}},
		{"2x0", Grid{2, 0}},
		{"4x8", Grid{4, 8}},
	}
	for _, tc := range ok {
		got, err := ParseGrid(tc.in)
		if err != nil {
			t.Errorf("ParseGrid(%q) = error %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("ParseGrid(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}

	bad := []string{"24", "x2", "2x", "-1x2", "2x-1", "5x4", "2x9", "axb", "2x4x6"}
	for _, in := range bad {
		if got, err := ParseGrid(in); err == nil {
			t.Errorf("ParseGrid(%q) = %v, want an error", in, got)
		}
	}
}

func TestGridString(t *testing.T) {
	if got := DefaultGrid.String(); got != "2x4" {
		t.Errorf("DefaultGrid.String() = %q, want 2x4", got)
	}
	if got := (Grid{}).String(); got != "auto" {
		t.Errorf("the zero grid renders as %q, want auto", got)
	}
	// Whatever String prints, ParseGrid must read back.
	for _, g := range []Grid{DefaultGrid, {}, {1, 1}, {4, 8}, {2, 2}} {
		back, err := ParseGrid(g.String())
		if err != nil {
			t.Fatalf("ParseGrid(%q) = error %v", g.String(), err)
		}
		if back != g {
			t.Errorf("%v printed as %q read back as %v", g, g.String(), back)
		}
	}
}

// TestAutoGridFitsThePane: an automatic grid gives every tile at least
// autoBodyLines of answer, and it never asks for more columns than the pane
// can hold a sentence in.
func TestAutoGridFitsThePane(t *testing.T) {
	sizes := []struct{ w, h int }{
		{100, 30}, {101, 31}, {120, 36}, {140, 40}, {160, 50}, {199, 33}, {200, 60},
	}
	for _, sz := range sizes {
		cw, bodyH := paneGeometry(sz.w, sz.h)
		g := Grid{}.resolve(cw, bodyH)
		if g.Cols < 1 || g.Cols > MaxGridCols || g.Rows < 1 || g.Rows > MaxGridRows {
			t.Fatalf("%dx%d: auto grid %v is out of range", sz.w, sz.h, g)
		}
		if cw >= autoCols2Width && g.Cols != 2 {
			t.Errorf("%dx%d: a %d-column pane chose %d tile columns, want 2", sz.w, sz.h, cw, g.Cols)
		}
		if cw < autoCols2Width && g.Cols != 1 {
			t.Errorf("%dx%d: a %d-column pane chose %d tile columns, want 1", sz.w, sz.h, cw, g.Cols)
		}
		// Every row gets a header, a stat line, four lines of answer and a
		// footer, plus the rule above it.
		//
		// 2026-09-13: the chrome went from two rows to three when the stat
		// line was added (user decision). The budget is tightened, not
		// loosened: an automatic grid now has to leave room for the extra row
		// as well, and the constant is tileChromeRows so this and resolve
		// cannot drift apart.
		if used := g.Rows*(tileChromeRows+autoBodyLines) + g.Rows - 1; used > bodyH {
			t.Errorf("%dx%d: auto grid %v needs %d rows of %d", sz.w, sz.h, g, used, bodyH)
		}
	}
}

// TestAutoGridDoesNotMoveWhenTheRunEnds is why resolve reads the pane's full
// height rather than what is left after the done footer: a grid that shrank by
// a row at the end would reflow every tile, and where it also decides the page
// size it would shuffle streams between pages at the moment nothing should
// move.
func TestAutoGridDoesNotMoveWhenTheRunEnds(t *testing.T) {
	for _, sz := range []struct{ w, h int }{{100, 30}, {120, 36}, {140, 40}} {
		live := ModelAt(ExampleTapeN(8), midRun)
		live.Grid = Grid{}
		done := ModelAt(ExampleTapeN(8), doneAt)
		done.Grid = Grid{}
		if !done.Done {
			t.Fatal("the fixture is not finished at the done offset")
		}
		if a, b := live.PageCount(sz.w, sz.h), done.PageCount(sz.w, sz.h); a != b {
			t.Errorf("%dx%d: the auto grid pages %d while live and %d when done", sz.w, sz.h, a, b)
		}
	}
}

// TestPaneGeometryMatchesView pins the one duplicated piece of arithmetic in
// the package: the page maths works out the pane's size on its own, and if it
// ever disagreed with View the footer would name a page the pane never drew.
func TestPaneGeometryMatchesView(t *testing.T) {
	for _, sz := range []struct{ w, h int }{{100, 30}, {101, 31}, {120, 36}, {140, 40}, {199, 33}} {
		cw, bodyH := paneGeometry(sz.w, sz.h)
		// The same expressions View uses, spelled out so a change there fails
		// here rather than silently splitting the two.
		inner := sz.w - 2
		wantH := sz.h - chromeH
		wantW := inner - 1 - rightW - 2
		if cw != wantW || bodyH != wantH {
			t.Errorf("%dx%d: paneGeometry = %dx%d, View lays out %dx%d", sz.w, sz.h, cw, bodyH, wantW, wantH)
		}
		// And the pane really does return that many rows of that many columns.
		m := ModelAt(ExampleTapeN(8), midRun)
		pane := leftPane(m, PlainTheme(), midRun, cw, bodyH)
		if len(pane.rows) != bodyH {
			t.Errorf("%dx%d: the pane returned %d rows, want %d", sz.w, sz.h, len(pane.rows), bodyH)
		}
		for i, row := range pane.rows {
			if got := width(row.text); got != cw {
				t.Errorf("%dx%d: pane row %d is %d columns, want %d", sz.w, sz.h, i, got, cw)
			}
		}
	}
}

// TestGridIsCarriedThroughDone: finishing a run must not throw away the layout
// or the page the viewer chose. EventDone rebuilds the model from the tape, so
// this is the one place those two fields could be lost.
func TestGridIsCarriedThroughDone(t *testing.T) {
	tp := ExampleTapeN(8)
	m := ModelAt(tp, midRun)
	m.Grid, m.Page = Grid{2, 2}, 1
	got := m.Apply(Event{Kind: EventDone, T: doneAt, Stream: -1, Tape: tp, TapePath: "/tmp/x.tape"})
	if got.Grid != (Grid{2, 2}) {
		t.Errorf("the grid became %v when the run finished, want 2x2", got.Grid)
	}
	if got.Page != 1 {
		t.Errorf("the page became %d when the run finished, want 1", got.Page)
	}
	if !got.Done {
		t.Error("the run did not report Done")
	}
}

// TestModelAtUsesTheDefaultGrid: a replayed tape lays out the way the run did,
// not the way the reader's terminal happens to fit.
func TestModelAtUsesTheDefaultGrid(t *testing.T) {
	if got := ModelAt(ExampleTapeN(8), midRun).Grid; got != DefaultGrid {
		t.Errorf("ModelAt gave grid %v, want %v", got, DefaultGrid)
	}
	if got := ModelAt(nil, 0).Grid; got != DefaultGrid {
		t.Errorf("ModelAt(nil) gave grid %v, want %v", got, DefaultGrid)
	}
	_ = time.Second
}
