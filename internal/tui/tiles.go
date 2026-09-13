package tui

import (
	"strings"
	"time"
)

// This file lays the answer pane out as a grid of tiles. It is the only
// layout: the list of stacked stream blocks it replaced is gone (user,
// 2026-09-13, "리스트 레이아웃 아예 버리고 싶어, 다 카드로 하고").
//
// A list reads downward, which is the right shape for a queue and the wrong
// one for a dashboard. Four or eight streams are not a queue — the reader is
// comparing them, and comparison wants them side by side. What a list bought,
// a page of tiles buys back: more streams than fit are paginated rather than
// squeezed, so a tile never shrinks below a readable answer however many
// streams the run has.
//
// Everything here is a pure function of (m, t): the page is model state, so a
// replayed frame reproduces the live one (docs/toktape-spec.ko.md decision 10).

// tileGutter is what a rule between two tiles costs in columns: the rule and
// one gutter on each side of it. Without the gutters the left tile's
// right-aligned rate butts against the rule and the right tile's answer starts
// on it; the frame spends a column on the same gutter for the same reason.
const tileGutter = 3

// tileChromeRows is what a full tile spends on something other than the
// answer: the header and the stat line under it. The automatic grid budgets
// with it (grid.go), so the two cannot drift.
//
// 2026-09-13 (TTP-29): three rows, until the sparkline footer moved into the
// stat line and its row went to the answer. The degradation rule that used to
// drop that footer before the body went with it — there is nothing left in a
// tile to drop.
const tileChromeRows = 2

// tilePane draws one page of m.Streams as a grid and returns exactly rows
// lines of cw columns, footer included.
//
// g is already resolved to a concrete grid; the caller resolves it from the
// pane's full height, before the done footer comes off, so that finishing a
// run does not reflow the page.
func tilePane(m Model, th Theme, t time.Duration, g Grid, cw, rows int, footer []string) paneLayout {
	blank := strings.Repeat(" ", cw)
	n := len(m.Streams)
	per := g.cells()
	pages := 1
	if n > per {
		pages = (n + per - 1) / per
	}
	page := clampInt(m.Page, 0, pages-1)
	first := page * per
	last := first + per
	if last > n {
		last = n
	}
	shown := m.Streams[first:last]

	gridRows := (len(shown) + g.Cols - 1) / g.Cols
	if gridRows < 1 {
		gridRows = 1
	}
	// One row per horizontal rule comes off the top; what is left is shared
	// out over the grid rows, the odd row going to the top.
	heights := tileHeights(rows-(gridRows-1), gridRows)
	active := m.activeStream()
	bar := th.paint(th.dim, "│")

	out := make([]paneRow, 0, rows)
	var firstRules, prevRules []int
	for r := 0; r < gridRows; r++ {
		lo := r * g.Cols
		hi := lo + g.Cols
		if hi > len(shown) {
			hi = len(shown)
		}
		// A row with fewer streams than columns spreads them over the whole
		// pane. Half a row of white space beside a lone answer reads as a
		// missing stream rather than as a layout.
		widths, rules := tileWidths(cw, hi-lo)
		if r > 0 {
			out = append(out, paneRow{text: tileRule(th, cw, prevRules, rules), rule: true})
		} else {
			firstRules = rules
		}
		prevRules = rules

		h := heights[r]
		cols := make([][]string, 0, hi-lo)
		for i := lo; i < hi; i++ {
			cols = append(cols, tile(m, th, t, shown[i], widths[i-lo], h, active))
		}
		for i := 0; i < h; i++ {
			var b strings.Builder
			for c, lines := range cols {
				if c > 0 {
					b.WriteString(" " + bar + " ")
				}
				b.WriteString(lines[i])
			}
			out = append(out, paneRow{text: b.String()})
		}
	}

	for len(out) < rows {
		out = append(out, paneRow{text: blank})
	}
	out = out[:rows]
	for _, line := range footer {
		out = append(out, paneRow{text: line})
	}
	return paneLayout{
		rows: out,
		// The divider under the pane closes the column rules only when they
		// run its whole height. A bottom row that spreads its streams — an
		// odd count, or a part-full last page — has no rule there to close,
		// and the done footer puts two rows between the grid and the divider.
		vrules: prevRules,
		vruleAtBottom: len(footer) == 0 && len(prevRules) > 0 &&
			sameInts(prevRules, firstRules),
		page:  page,
		pages: pages,
	}
}

// tileWidths splits cw columns between k tiles, leaving tileGutter columns
// between neighbours, and returns the tile widths with the display column of
// each rule.
//
// The remainder of an uneven split goes to the tiles on the right: the eye
// tracks the left edge of the pane, which makes a ragged right tile the
// cheaper flaw.
func tileWidths(cw, k int) (widths, rules []int) {
	if k < 1 {
		return nil, nil
	}
	content := cw - tileGutter*(k-1)
	if content < k {
		content = k
	}
	base, extra := content/k, content%k
	widths = make([]int, k)
	for i := range widths {
		widths[i] = base
		if i >= k-extra {
			widths[i]++
		}
	}
	at := 0
	for i := 0; i < k-1; i++ {
		at += widths[i]
		rules = append(rules, at+1)
		at += tileGutter
	}
	return widths, rules
}

// tileHeights shares avail rows out over n tiles, the remainder going to the
// top rows. Every tile in a row keeps the same width, so the only thing that
// can differ between grid rows is a line of answer, and the reader's eye
// starts at the top.
func tileHeights(avail, n int) []int {
	if n <= 0 {
		return nil
	}
	if avail < 0 {
		avail = 0
	}
	out := make([]int, n)
	base, extra := avail/n, avail%n
	for i := range out {
		out[i] = base
		if i < extra {
			out[i]++
		}
	}
	return out
}

// tileRule is the horizontal line between two grid rows: cw columns of the
// chrome's own rule, with a junction wherever a column rule meets it.
//
// above and below are the rule columns of the grid rows on either side, which
// is what decides each junction: ┼ where a rule runs through, ┴ or ┬ where one
// stops here. View draws the ends, because they land on the frame rather than
// inside the pane.
func tileRule(th Theme, cw int, above, below []int) string {
	line := []rune(repeat('─', cw))
	mark := func(cols []int, solo, joined rune) {
		for _, c := range cols {
			if c < 0 || c >= len(line) {
				continue
			}
			if line[c] == '─' {
				line[c] = solo
				continue
			}
			line[c] = joined
		}
	}
	mark(above, '┴', '┼')
	mark(below, '┬', '┼')
	return th.paint(th.dim, string(line))
}

// tile draws one stream inside its own rectangle: the stream header, the stat
// line that reports how fast it is decoding, and the answer.
//
// It always returns exactly rows lines of cw columns. Every row past the two
// of chrome is answer: a tile has nothing else to spend them on since the
// sparkline joined the stat line (TTP-29).
//
// active is the index of the stream whose tile is highlighted, not a flag: the
// grid hands the same value to every tile.
func tile(m Model, th Theme, t time.Duration, s Stream, cw, rows, active int) []string {
	if rows <= 0 || cw <= 0 {
		return nil
	}
	blank := strings.Repeat(" ", cw)
	on := s.Index == active
	if rows == 1 {
		return []string{streamHeader(m, th, s, cw, true, on)}
	}
	// Two rows is identity and the figure. Below that a tile is a label, not
	// a tile, but the rows it can afford are the ones that report something.
	return fitRows(streamBlock(m, th, t, s, cw, rows-tileChromeRows, true, on), blank, rows)
}

// spliceRune replaces the i-th rune of s with r. s must carry no escape
// sequences; every caller builds it out of the chrome's own single-column
// glyphs.
func spliceRune(s string, i int, r rune) string {
	rs := []rune(s)
	if i < 0 || i >= len(rs) {
		return s
	}
	rs[i] = r
	return string(rs)
}

// sameInts reports whether two rule-column lists are identical.
func sameInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
