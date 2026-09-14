package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/midagedev/toktape/internal/card"
	"github.com/midagedev/toktape/internal/tape"
)

// tileW and tileH are the size the hero clip is captured at, and therefore the
// size the tile goldens pin.
const (
	tileW = 120
	tileH = 36
)

// tileModel is a model of n streams at a chosen grid and page.
func tileModel(t *testing.T, n int, at time.Duration, g Grid, page int) Model {
	t.Helper()
	m := ModelAt(ExampleTapeN(n), at)
	m.TapePath = "~/.toktape/runs/20260913-150210-qwen3.5-35b-a3b.tape"
	m.Grid, m.Page = g, page
	return m
}

// truncatedTape is ExampleTapeN cut to the first k requests, which is how the
// counts the fixture does not generate on its own (one stream, three streams)
// are tested.
func truncatedTape(k int) *tape.Tape {
	tp := ExampleTapeN(max(k, 2))
	tp.Requests = tp.Requests[:k]
	return tp
}

// TestTileGolden pins the frames the hero clip is cut from: four streams in a
// grid, eight streams on one page of the default grid, and eight streams split
// over two pages of a 2×2 grid.
//
// 2026-09-13: stat line, user decision. Every one of these frames was
// re-baselined because a tile gained a row — the rate and the TTFT left the
// right of the header, which now carries the state and the prefill bar, and
// landed on a line of their own under it, with the token count and the median
// beside them. The footer's p50 went the same way and its label is the mean of
// the window it draws. No assertion here was weakened: the frames under them
// are different frames (user: "각 pane마다 핵심적으로 tok/s가 표시가 안 되는데,
// 표시해야 될 지표에 대해서 좀 잘 생각해 보자").
//
// 2026-09-13 TTP-28: re-baselined once more. The example is now a dense
// R1 Distill Llama 70B on two 3090s running 320-token answers, so every figure in
// these frames moved, and the emphasis contract demoted the sparklines, the
// section titles, the bars and the tile headers out of the accent (see
// TestOnlyTheRateIsAccent). A third different set of frames, not a loosened
// assertion: the gates above them were added, not relaxed.
//
// 2026-09-13 TTP-29: re-baselined again, for the sparkline. The tile's footer
// row is gone and its graph sits on the stat line, right after the rate it
// belongs to, at half the tile's width with only its newest cell lit (user,
// after watching the hero clip: "팬의 스파크와 실제 스탯이 상하로 분리되어서 보기
// 힘든데 이것도 개선해보자 … 반 줄 정도로 줄이자"). The row the footer gave up is
// answer text in every one of these frames, so each tile says one line more.
// Different frames again, and the three gates in tilespark_test.go that pin the
// graph's place, its exclusivity and its width were added, not relaxed.
//
// 2026-09-13 TTP-39: re-baselined for the right pane only. HOST became
// RESOURCES — a header and a fixed-ceiling braille utilisation graph for CPU
// and for each GPU, the load average folded into the CPU header and the
// contended tag onto the title rule (user: "cpu(ram) gpu0 gpu1 각 리소스 별로
// 스파크를 더 이쁘게 … 한 3줄로"). Every column left of the pane's border is
// byte-identical to the previous goldens (checked column by column); the
// graph contract is pinned in graph_test.go and the pane's height choice in
// resources_test.go, both added, nothing relaxed.
func TestTileGolden(t *testing.T) {
	cases := []struct {
		name string
		n    int
		grid Grid
		page int
		at   time.Duration
	}{
		{"tiles-t0", 4, DefaultGrid, 0, 0},
		{"tiles-mid", 4, DefaultGrid, 0, midRun},
		{"tiles-done", 4, DefaultGrid, 0, doneAt},
		{"tiles8-t0", 8, DefaultGrid, 0, 0},
		{"tiles8-mid", 8, DefaultGrid, 0, midRun},
		{"tiles8-done", 8, DefaultGrid, 0, doneAt},
		{"tiles8-2x2-p1", 8, Grid{2, 2}, 0, midRun},
		{"tiles8-2x2-p2", 8, Grid{2, 2}, 1, midRun},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := tileModel(t, c.n, c.at, c.grid, c.page)
			got := View(m, c.at, tileW, tileH)
			checkFrame(t, got, tileW, tileH)
			compareGolden(t, fmt.Sprintf("view-%dx%d-%s.txt", tileW, tileH, c.name), got)
		})
	}
}

// TestTileFrameIsExactlyWide is the width contract of the track: a grid splits
// the pane into tiles and draws its own rules across it, so every one of those
// columns is a chance to be one off. Every ANSI-stripped row of every tile
// frame is exactly the frame width, coloured or not.
func TestTileFrameIsExactlyWide(t *testing.T) {
	for _, n := range []int{4, 8} {
		for _, at := range []time.Duration{0, 500 * time.Millisecond, midRun, 3 * time.Second, doneAt} {
			m := tileModel(t, n, at, DefaultGrid, 0)
			plain := View(m, at, tileW, tileH)
			checkFrame(t, plain, tileW, tileH)

			m.Theme = ColourTheme()
			coloured := View(m, at, tileW, tileH)
			for i, line := range strings.Split(coloured, "\n") {
				if got := card.Width(line); got != tileW {
					t.Errorf("n=%d at=%v coloured row %d is %d columns, want %d: %q",
						n, at, i, got, tileW, card.StripANSI(line))
				}
			}
			if got := card.StripANSI(coloured); got != plain {
				t.Errorf("n=%d at=%v: stripping the palette does not reproduce the plain frame", n, at)
				diffLines(t, plain, got)
			}
		}
	}
}

// TestTileCountsRender: every stream count from one to eight has its own grid
// shape — a full-width tile, a single split row, a split row over a spanning
// tile, a 2×2, and on up to pages — and all of them have to come out as a whole
// frame at every size the layout supports. The odd widths are deliberate: an
// uneven column split is exactly where a rounding mistake hides.
func TestTileCountsRender(t *testing.T) {
	sizes := []struct{ w, h int }{
		{100, 30}, {101, 31}, {120, 36}, {140, 40}, {160, 50}, {199, 33},
	}
	grids := []Grid{DefaultGrid, {}, {1, 1}, {2, 2}, {1, 4}, {4, 8}}
	for n := 1; n <= 8; n++ {
		tp := truncatedTape(n)
		for _, sz := range sizes {
			for _, g := range grids {
				for _, at := range []time.Duration{0, midRun, doneAt} {
					name := fmt.Sprintf("n%d-%dx%d-%s-%v", n, sz.w, sz.h, g, at)
					t.Run(name, func(t *testing.T) {
						m := ModelAt(tp, at)
						m.Grid = g
						if got := len(m.Streams); got != n {
							t.Fatalf("the fixture has %d streams, want %d", got, n)
						}
						for page := 0; page < m.PageCount(sz.w, sz.h); page++ {
							m.Page = page
							checkFrame(t, View(m, at, sz.w, sz.h), sz.w, sz.h)
						}
					})
				}
			}
		}
	}
}

// TestPagesCoverEveryStreamOnce is the contract pagination rests on: across
// the pages every stream is drawn exactly once, and a tile names its stream by
// its place in the run, not by its place on the page.
func TestPagesCoverEveryStreamOnce(t *testing.T) {
	for _, g := range []Grid{DefaultGrid, {2, 2}, {1, 3}, {4, 1}} {
		m := tileModel(t, 8, midRun, g, 0)
		pages := m.PageCount(tileW, tileH)
		seen := map[int]int{}
		for page := 0; page < pages; page++ {
			m.Page = page
			frame := card.StripANSI(View(m, midRun, tileW, tileH))
			for i := 1; i <= len(m.Streams); i++ {
				// The index is global — the sixth stream is "stream 6"
				// wherever it is drawn — so a reader can say which slot a
				// tile belongs to without counting pages. The "/8" after it
				// is dropped on a tile too narrow to hold it, which is why
				// this counts the name alone.
				seen[i] += headerCount(frame, i)
			}
		}
		for i := 1; i <= 8; i++ {
			if seen[i] != 1 {
				t.Errorf("grid %s: stream %d appears on %d pages, want exactly 1", g, i, seen[i])
			}
		}
		if want := (8 + g.cells() - 1) / g.cells(); pages != want {
			t.Errorf("grid %s: %d pages, want %d", g, pages, want)
		}
	}

	// The second page of a 2×2 grid is the back half of the run, by name.
	m := tileModel(t, 8, midRun, Grid{2, 2}, 1)
	frame := card.StripANSI(View(m, midRun, tileW, tileH))
	for _, want := range []string{"stream 5/8", "stream 6/8", "stream 7/8", "stream 8/8"} {
		if !strings.Contains(frame, want) {
			t.Errorf("page 2 of a 2x2 grid does not show %q:\n%s", want, frame)
		}
	}
	for _, unwanted := range []string{"stream 1/8", "stream 4/8"} {
		if strings.Contains(frame, unwanted) {
			t.Errorf("page 2 of a 2x2 grid still shows %q", unwanted)
		}
	}
}

// headerCount is how many stream headers in frame name stream i, matching the
// index exactly so that "stream 1" never counts "stream 18".
func headerCount(frame string, i int) int {
	want := fmt.Sprintf("stream %d", i)
	n := 0
	for at := 0; ; {
		j := strings.Index(frame[at:], want)
		if j < 0 {
			return n
		}
		j += at
		rest := frame[j+len(want):]
		if rest == "" || rest[0] < '0' || rest[0] > '9' {
			n++
		}
		at = j + len(want)
	}
}

// TestPageIsClampedAndReversible: the keys can never step off either end, and
// a page beyond the count renders the last page rather than an empty pane.
func TestPageIsClampedAndReversible(t *testing.T) {
	m := tileModel(t, 8, midRun, Grid{2, 2}, 0)
	if got := m.PageCount(tileW, tileH); got != 2 {
		t.Fatalf("page count = %d, want 2", got)
	}
	if got := m.PageAfter(-1, tileW, tileH); got != 0 {
		t.Errorf("stepping back from page 1 gave %d, want 0", got)
	}
	if got := m.PageAfter(1, tileW, tileH); got != 1 {
		t.Errorf("stepping forward gave %d, want 1", got)
	}
	m.Page = 1
	if got := m.PageAfter(1, tileW, tileH); got != 1 {
		t.Errorf("stepping past the last page gave %d, want 1", got)
	}

	// A page out of range is drawn as the last one, not as nothing.
	m.Page = 99
	frame := card.StripANSI(View(m, midRun, tileW, tileH))
	if !strings.Contains(frame, "stream 8/8") {
		t.Errorf("a page beyond the end did not fall back to the last page:\n%s", frame)
	}
	if !strings.Contains(frame, "page 2/2") {
		t.Errorf("the footer does not name the clamped page:\n%s", frame)
	}
}

// TestFooterNamesThePage: a paginated run says so on the footer, and a run
// that fits on one page does not spend the room.
func TestFooterNamesThePage(t *testing.T) {
	one := View(tileModel(t, 8, midRun, DefaultGrid, 0), midRun, tileW, tileH)
	if strings.Contains(one, "page 1/1") {
		t.Error("a single-page run still printed a page indicator")
	}
	two := card.StripANSI(View(tileModel(t, 8, midRun, Grid{2, 2}, 0), midRun, tileW, tileH))
	if !strings.Contains(two, "page 1/2 · ←/→") {
		t.Errorf("no page indicator on a two-page run:\n%s", two)
	}
	// The strip beside it keeps a usable width even at the smallest screen
	// with the longest hints.
	m := tileModel(t, 8, doneAt, Grid{2, 2}, 0)
	small := card.StripANSI(View(m, doneAt, MinWidth, MinHeight))
	if !strings.Contains(small, "page 1/2") {
		t.Errorf("no page indicator at the minimum screen:\n%s", small)
	}
	if !strings.Contains(small, "c card · p prompt · q quit") {
		t.Errorf("the key hints were squeezed out at the minimum screen:\n%s", small)
	}
}

// TestTileRulesJoinTheFrame: the horizontal rule between two grid rows reaches
// the frame at both ends, and the column rule crosses it with a junction. A
// dangling ┼ at an outer edge, or a rule that stops a column short of the
// border, is the flaw the track contract names.
func TestTileRulesJoinTheFrame(t *testing.T) {
	m := tileModel(t, 4, midRun, DefaultGrid, 0)
	frame := View(m, midRun, tileW, tileH)
	lines := strings.Split(frame, "\n")

	rules := 0
	for i, line := range lines[1 : len(lines)-3] {
		if !strings.Contains(line, "┼") {
			continue
		}
		rules++
		rs := []rune(line)
		if rs[0] != '├' {
			t.Errorf("body row %d starts with %q, want ├", i+1, string(rs[0]))
		}
		if !strings.Contains(line, "┤") {
			t.Errorf("body row %d does not meet the pane separator with ┤: %q", i+1, line)
		}
		if rs[len(rs)-1] != '│' {
			t.Errorf("body row %d ends with %q, want │", i+1, string(rs[len(rs)-1]))
		}
	}
	if rules != 1 {
		t.Errorf("%d rule rows in a four-stream frame, want 1", rules)
	}

	// Four streams put a column rule in the bottom grid row, so the divider
	// under the pane closes it with a ┴ rather than letting it dangle.
	divider := lines[len(lines)-3]
	if got := strings.Count(divider, "┴"); got != 2 {
		t.Errorf("the divider has %d ┴ junctions, want 2 (the pane split and the tile column): %q",
			got, divider)
	}

	// Three streams spread the bottom row over the whole pane, so there is no
	// column rule to close there and the divider keeps the pane split alone.
	odd := ModelAt(truncatedTape(3), midRun)
	oddLines := strings.Split(View(odd, midRun, tileW, tileH), "\n")
	if got := strings.Count(oddLines[len(oddLines)-3], "┴"); got != 1 {
		t.Errorf("the divider under a spanning bottom tile has %d ┴ junctions, want 1", got)
	}
	// Where that spanning row meets the row above it, the rule closes the
	// column above with ┴ and starts nothing below.
	joined := false
	for _, line := range oddLines {
		if strings.Contains(line, "┴") && strings.HasPrefix(line, "├") {
			joined = true
		}
	}
	if !joined {
		t.Error("the rule above a spanning tile does not close the column above it")
	}
}

// TestTileRateIsAlive: every tile reports its own decode rate and draws its
// own graph of it, which is what makes a grid a comparison rather than four
// copies of the same screen.
//
// 2026-09-13 (TTP-29): the graph used to be a full-width footer with the
// window's mean beside it, on the tile's last row. Both rows are now one row:
// the footer is gone and the sparkline follows the rate on the stat line (user:
// "팬의 스파크와 실제 스탯이 상하로 분리되어서 보기 힘든데"). The assertions name
// the stat line; tilespark_test.go pins the graph itself.
func TestTileRateIsAlive(t *testing.T) {
	at := 3 * time.Second
	m := tileModel(t, 4, at, DefaultGrid, 0)
	frame := View(m, at, tileW, tileH)

	if got := strings.Count(frame, " tok/s"); got < len(m.Streams) {
		t.Errorf("%d tok/s figures in the frame, want at least one per tile (%d)", got, len(m.Streams))
	}
	if strings.Contains(frame, " avg") {
		t.Error("a tile still carries the footer's mean label; the footer row is gone")
	}
	if !strings.ContainsAny(frame, string(sparkRunes)) {
		t.Error("no sparkline glyph in a mid-run tile frame")
	}

	// A stream with no token yet has nothing to plot and prints no rate
	// rather than a zero it never measured (CLAUDE.md).
	if got := tileStatLine(Model{}, PlainTheme(), 0, Stream{}, 41); !strings.HasPrefix(got, unknown+" tok/s") {
		t.Errorf("an unstarted stream's stat line = %q, want an unknown rate", got)
	}
	if got := tileStatLine(Model{}, PlainTheme(), 0, Stream{}, 41); strings.ContainsAny(got, string(sparkRunes)) {
		t.Errorf("an unstarted stream's stat line draws a graph of nothing: %q", got)
	}
}

// TestTileSpendsEveryRowPastItsChromeOnTheAnswer: a tile is a header, a stat
// line and answer all the way down. There is nothing else left in it to drop.
func TestTileSpendsEveryRowPastItsChromeOnTheAnswer(t *testing.T) {
	m := tileModel(t, 4, midRun, DefaultGrid, 0)
	s := m.Streams[0]
	th := PlainTheme()

	// 2026-09-13 (TTP-29): this used to assert the opposite — that a tile
	// with fewer than tileMinBody lines of answer gave up its footer. The
	// footer and the rule went together, and the row the rule saved is the
	// row the body gained at every size.
	for rows := 1; rows <= 8; rows++ {
		got := tile(m, th, midRun, s, 41, rows, 0)
		if len(got) != rows {
			t.Fatalf("a %d-row tile came back %d rows", rows, len(got))
		}
		if !strings.Contains(got[0], "stream 1") {
			t.Errorf("a %d-row tile dropped its header: %q", rows, got[0])
		}
		if strings.Contains(strings.Join(got, "\n"), " avg") {
			t.Errorf("a %d-row tile drew a footer:\n%s", rows, strings.Join(got, "\n"))
		}
		// The rate outlives everything but the name: a tile reporting nothing
		// is not a smaller tile, it is a different one.
		if rows >= 2 && !strings.Contains(got[1], "tok/s") {
			t.Errorf("a %d-row tile dropped its stat line: %q", rows, got[1])
		}
		// Every row past the two of chrome is answer: the tail of the
		// stream's own wrapped text, line for line. (The gutter used to be
		// the mark of an answer row; it went in TTP-50, and the rows are now
		// checked against the text itself, which is the stronger claim.)
		if rows > tileChromeRows {
			want := tail(streamTextLines(s, 41-2), rows-tileChromeRows)
			for i := tileChromeRows; i < rows; i++ {
				k := i - tileChromeRows - (rows - tileChromeRows - len(want))
				if k < 0 || !strings.HasPrefix(got[i], want[k].text) {
					t.Errorf("a %d-row tile's row %d is not answer: %q", rows, i, got[i])
				}
			}
		}
	}
}

// TestTileThinkingStates mirrors TestExampleTapeShowsEveryThinkingState for the
// grid: the badge and the answer marker have to survive the move from a
// full-width block to a tile, because the hero clip is the four-stream run.
func TestTileThinkingStates(t *testing.T) {
	mid := tileModel(t, 4, midRun, DefaultGrid, 0)
	var thinkingNow, crossed int
	for _, s := range mid.Streams {
		if thinkingBadge(s) == "thinking" {
			thinkingNow++
		}
		for _, bl := range streamTextLines(s, 38) {
			if bl.marker {
				crossed++
				break
			}
		}
	}
	if thinkingNow == 0 {
		t.Errorf("no stream is thinking at %v", midRun)
	}
	if crossed == 0 {
		t.Errorf("no stream has crossed the answer marker at %v", midRun)
	}

	frame := View(mid, midRun, tileW, tileH)
	if !strings.Contains(frame, "· thinking") {
		t.Errorf("no thinking badge in the tile frame at %v:\n%s", midRun, frame)
	}
	if !strings.Contains(frame, answerMarker) {
		t.Errorf("no %q marker in the tile frame at %v:\n%s", answerMarker, midRun, frame)
	}

	done := tileModel(t, 4, doneAt, DefaultGrid, 0)
	var cut int
	for _, s := range done.Streams {
		if thinkingBadge(s) == "thinking · cut" {
			cut++
		}
	}
	if cut != 1 {
		t.Errorf("%d streams read \"thinking · cut\" at %v, want 1", cut, doneAt)
	}
	if got := View(done, doneAt, tileW, tileH); !strings.Contains(got, "thinking · cut") {
		t.Errorf("no cut badge in the final tile frame:\n%s", got)
	}
}

// TestTileHeaderNamesTheState: the header is identity and state, and nothing
// else.
//
// 2026-09-13: it used to carry the rate and the TTFT on its right, and the
// test that pinned their degradation order stood here. Both figures moved one
// row down into the stat line (user decision: "각 pane마다 핵심적으로 tok/s가
// 표시가 안 되는데"), so the order they degrade in is pinned by
// TestStatLineDegradesInOrder instead, and what is left to pin here is that
// the header reports the state and never the rate.
func TestTileHeaderNamesTheState(t *testing.T) {
	m := tileModel(t, 4, midRun, DefaultGrid, 0)
	th := PlainTheme()

	for _, w := range []int{31, 41, 85} {
		got := streamHeader(m, th, m.Streams[0], w, true, false)
		if strings.Contains(got, "tok/s") || strings.Contains(got, "ttft") {
			t.Errorf("the header at width %d still carries a figure: %q", w, got)
		}
		if !strings.Contains(got, "answer") {
			t.Errorf("the header at width %d does not name the state: %q", w, got)
		}
	}

	// A stream whose badge already says what it is doing does not say it
	// twice: "stream 4/4 · thinking" needs no second "thinking".
	last := m.Streams[len(m.Streams)-1]
	if got := thinkingBadge(last); got != "thinking" {
		t.Fatalf("the fixture's last stream reads %q at %v, want a thinking badge", got, midRun)
	}
	badged := streamHeader(m, th, last, 41, true, false)
	if !strings.Contains(badged, "· thinking") {
		t.Errorf("the badged header lost its badge: %q", badged)
	}
	if strings.Count(badged, "thinking") != 1 {
		t.Errorf("the header names the same state twice: %q", badged)
	}

	// The states a tile has to be able to report, each from a stream that is
	// actually in it.
	done := tileModel(t, 4, doneAt, DefaultGrid, 0)
	for _, tc := range []struct {
		what  string
		s     Stream
		state string
	}{
		{"a fresh stream", Stream{}, prefillWord},
		{"a failed stream", Stream{Err: "connection reset"}, "failed"},
		{"a finished stream", done.Streams[0], "done"},
		{"an all-reasoning finish", done.Streams[len(done.Streams)-1], "cut"},
	} {
		if got := streamState(tc.s); got != tc.state {
			t.Errorf("%s reads %q, want %q", tc.what, got, tc.state)
		}
	}

	// Nothing may be half-printed at any width the header can be asked for,
	// at any point in the run.
	for _, at := range []time.Duration{0, 500 * time.Millisecond, midRun, doneAt} {
		frame := tileModel(t, 4, at, DefaultGrid, 0)
		for _, s := range frame.Streams {
			for w := 12; w <= 85; w++ {
				got := streamHeader(frame, th, s, w, true, false)
				if width(got) != w {
					t.Fatalf("header at width %d is %d columns: %q", w, width(got), got)
				}
				if strings.Contains(got, "prefil") && !strings.Contains(got, prefillWord) {
					t.Errorf("header at width %d printed a cut state: %q", w, got)
				}
			}
		}
	}
}

// TestTileWidthsFillThePane: the tile widths and the rules between them account
// for every column of the pane, at every column count and every width.
func TestTileWidthsFillThePane(t *testing.T) {
	for cw := 20; cw <= 140; cw++ {
		for k := 1; k <= MaxGridCols; k++ {
			widths, rules := tileWidths(cw, k)
			if len(widths) != k {
				t.Fatalf("tileWidths(%d, %d) gave %d widths", cw, k, len(widths))
			}
			if len(rules) != k-1 {
				t.Fatalf("tileWidths(%d, %d) gave %d rules, want %d", cw, k, len(rules), k-1)
			}
			total := tileGutter * (k - 1)
			for _, w := range widths {
				if w < 1 {
					t.Fatalf("tileWidths(%d, %d) gave a tile of %d columns", cw, k, w)
				}
				total += w
			}
			if total != cw && cw >= k+tileGutter*(k-1) {
				t.Errorf("tileWidths(%d, %d) covers %d columns", cw, k, total)
			}
			// Each rule sits one gutter past the tile before it.
			at := 0
			for i, col := range rules {
				at += widths[i]
				if col != at+1 {
					t.Errorf("tileWidths(%d, %d) rule %d at column %d, want %d", cw, k, i, col, at+1)
				}
				at += tileGutter
			}
		}
	}
}

// TestTileHeightsShareTheRemainder: the odd row goes to the top, and the grid
// spends every row it was given.
func TestTileHeightsShareTheRemainder(t *testing.T) {
	tests := []struct {
		avail, n int
		want     []int
	}{
		{30, 2, []int{15, 15}},
		{31, 2, []int{16, 15}},
		{10, 3, []int{4, 3, 3}},
		{0, 2, []int{0, 0}},
	}
	for _, tc := range tests {
		got := tileHeights(tc.avail, tc.n)
		if len(got) != len(tc.want) {
			t.Fatalf("tileHeights(%d, %d) = %v, want %v", tc.avail, tc.n, got, tc.want)
		}
		for i := range tc.want {
			if got[i] != tc.want[i] {
				t.Errorf("tileHeights(%d, %d) = %v, want %v", tc.avail, tc.n, got, tc.want)
				break
			}
		}
	}
}

// TestStreamRatesSkipTheFirstToken: a stream's own sparkline is built from its
// inter-token gaps, and the gap before the first token is the TTFT — a
// different measurement, and counting it would draw the prefill as a slow
// decode (handover lesson 1).
func TestStreamRatesSkipTheFirstToken(t *testing.T) {
	s := Stream{Tokens: []Token{
		{T: 500 * time.Millisecond, ITL: 0},
		{T: 600 * time.Millisecond, ITL: 100 * time.Millisecond},
		{T: 650 * time.Millisecond, ITL: 50 * time.Millisecond},
	}}
	if got := streamITLs(s); len(got) != 2 || got[0] != 100 || got[1] != 50 {
		t.Errorf("streamITLs = %v, want [100 50]", got)
	}
	got := streamRates(s, 8)
	if len(got) != 2 || got[0] != 10 || got[1] != 20 {
		t.Errorf("streamRates = %v, want [10 20] tok/s", got)
	}
	if got := streamRates(s, 1); len(got) != 1 || got[0] != 20 {
		t.Errorf("streamRates(w=1) = %v, want the newest gap alone", got)
	}
	if got := streamRates(Stream{}, 8); len(got) != 0 {
		t.Errorf("streamRates of an unstarted stream = %v, want none", got)
	}
}
