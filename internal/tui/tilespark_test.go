package tui

import (
	"regexp"
	"strings"
	"testing"
	"time"
)

// The sparkline contract (TTP-29, user 2026-09-13, after watching the hero
// clip: "팬의 스파크와 실제 스탯이 상하로 분리되어서 보기 힘든데 이것도
// 개선해보자. 스파크라인도 마지막 것만 밝게 하고 이전 것은 어둡게 하고. 한 라인
// 다 먹기에는 토큰 오르락내리락이 너무 적어서 별 의미 없는 듯하니 반 줄 정도로
// 줄이자").
//
// A tile used to carry its rate on row 2 and a full-width sparkline on its last
// row: two drawings of the same quantity at opposite ends of the tile, the
// larger of them nearly flat. The graph now sits inside the stat line, directly
// after the rate it belongs to, at half the tile's width, and the footer row is
// gone — the answer has it.
//
// These three tests are the mechanical half. The first pins where the graph is
// and which cell of it is lit, the second pins that nothing draws one anywhere
// else in a tile, and the third pins its width and where it gives way.

// statLineOrder is the shape of a tile's stat line once it has room for a
// graph: the rate, the sparkline, then the dim figures.
var statLineOrder = regexp.MustCompile(`^(\?|[0-9]+(\.[0-9]+)?) tok/s +[\x{2581}-\x{2588}]+( ttft .*)?$`)

// sparkRuneAt reports whether r is one of the eight sparkline levels
// (U+2581–U+2588). The stream cursor (▍ U+258D) is a block element too and is
// deliberately outside this range.
func sparkRuneAt(r rune) bool { return r >= '▁' && r <= '█' }

// paneRows renders the answer pane alone, in colour, and parses it into cells.
//
// The pane rather than the whole frame: the right pane's bars and its maj/tok
// sparkline draw the same runes on the same terminal rows, and this contract is
// about tiles (spec: "Right pane untouched").
func paneRows(t *testing.T, m Model, at time.Duration, w, h int) [][]pcell {
	t.Helper()
	m.Theme = ColourTheme()
	cw, bodyH := paneGeometry(w, h)
	lines := paneText(leftPane(m, m.Theme, at, cw, bodyH))
	return parseFrame(strings.Join(lines, "\n"), cw, len(lines))
}

// isStatLine reports whether the segment starting at column x of row y is a
// tile's stat line: the row above it, at the same column, is that tile's own
// "stream N/M" header.
func isStatLine(rows [][]pcell, y, x int) bool {
	return y > 0 && strings.HasPrefix(segmentAt(rows[y-1], x), "stream")
}

// TestTileSparklineIsInTheStatLine: the graph follows the rate on the same row,
// and exactly one of its cells — the newest, at its right-hand end — is lit.
func TestTileSparklineIsInTheStatLine(t *testing.T) {
	const w, h = 120, 36
	th := ColourTheme()
	accent, muted := styleHex(th.accent), styleHex(th.accentMuted)
	rows := paneRows(t, tileModel(t, 4, midRun, DefaultGrid, 0), midRun, w, h)

	tiles := 0
	for y, row := range rows {
		for _, seg := range segments(row) {
			if !isStatLine(rows, y, seg.from) {
				continue
			}
			tiles++
			// The order on the row: the rate, then the graph, then the dim
			// figures that qualify it. The eye reads the number and the shape
			// as one thing, which is the whole point of the move.
			if !statLineOrder.MatchString(seg.text) {
				t.Errorf("row %d: the stat line is not rate-then-sparkline-then-figures: %q", y, seg.text)
			}
			var lit, cells, last int
			last = -1
			for x := seg.from; x < seg.to; x++ {
				if !sparkRuneAt(row[x].r) {
					continue
				}
				cells++
				last = x
				switch row[x].fg {
				case accent:
					lit++
				case muted:
				default:
					t.Errorf("row %d col %d: a sparkline cell wears %s, want the muted accent %s", y, x, row[x].fg, muted)
				}
			}
			if cells == 0 {
				t.Errorf("row %d: no sparkline on the stat line: %q", y, seg.text)
				continue
			}
			if lit != 1 {
				t.Errorf("row %d: %d lit sparkline cells, want exactly one at the write head: %q", y, lit, seg.text)
			}
			if row[last].fg != accent {
				t.Errorf("row %d: the newest cell (col %d) is not the lit one: %q", y, last, seg.text)
			}
		}
	}
	if tiles != 4 {
		t.Fatalf("found %d tile stat lines, want 4", tiles)
	}
}

// TestNoSparkRuneOutsideTheStatLine: the footer row is gone, so a tile draws
// its sparkline runes on the stat line and nowhere else.
func TestNoSparkRuneOutsideTheStatLine(t *testing.T) {
	for _, sz := range []struct{ w, h int }{{120, 36}, {160, 48}} {
		for _, n := range []int{4, 8} {
			rows := paneRows(t, tileModel(t, n, midRun, DefaultGrid, 0), midRun, sz.w, sz.h)
			for y, row := range rows {
				for _, seg := range segments(row) {
					if isStatLine(rows, y, seg.from) {
						continue
					}
					if strings.Contains(seg.text, prefillWord) {
						// A prompt-progress bar is solid blocks by design and
						// is gone the moment a token arrives.
						continue
					}
					for _, r := range seg.text {
						if sparkRuneAt(r) {
							t.Errorf("%dx%d n=%d row %d: a sparkline rune %q outside the stat line: %q",
								sz.w, sz.h, n, y, r, seg.text)
							break
						}
					}
				}
			}
		}
	}
}

// TestStatLineSparkIsTheStreamsOwnWindow: the cells drawn are this stream's
// own instantaneous rates over the window the graph is wide, newest at the
// right, and they scroll as the run goes on.
//
// It replaces the footer's "11.8 avg" test (TTP-29). The label the footer
// carried is gone — the live rate to the graph's left is the number now — so
// what is left to pin is that the shape is the stream's own series and not
// some other window's.
func TestStatLineSparkIsTheStreamsOwnWindow(t *testing.T) {
	const cw = 41
	sw := tileSparkW(cw)
	if sw == 0 {
		t.Fatalf("a %d-column tile draws no sparkline; this test measures nothing", cw)
	}
	for _, at := range []time.Duration{3 * time.Second, midRun, doneAt} {
		for i, s := range ModelAt(ExampleTapeN(4), at).Streams {
			line := tileStatLine(PlainTheme(), s, cw)
			var drawn []rune
			for _, r := range line {
				if sparkRuneAt(r) {
					drawn = append(drawn, r)
				}
			}
			var want []rune
			for _, c := range Sparkline(streamRates(s, sw), sw, 0) {
				want = append(want, c.R)
			}
			if string(drawn) != string(want) {
				t.Errorf("at %v stream %d drew %q, want the stream's own window %q", at, i, string(drawn), string(want))
			}
		}
	}
	// And it scrolls: the same stream two seconds later is a different line.
	early := tileStatLine(PlainTheme(), ModelAt(ExampleTapeN(4), 3*time.Second).Streams[0], cw)
	later := tileStatLine(PlainTheme(), ModelAt(ExampleTapeN(4), 5*time.Second).Streams[0], cw)
	if early == later {
		t.Errorf("a tile's stat line is identical at 3 s and at 5 s; the sparkline does not scroll:\n%q", early)
	}
}

// TestTileSparkWidth: the graph is half the tile minus the rate field, capped,
// and it gives way before the TTFT does.
//
// The widths in the table are the tile widths the layout actually produces —
// 41 columns on a 120-column screen and 61 on a 160-column one, both in two
// columns (tileWidths over paneGeometry) — plus the narrow sizes where the
// graph has to go.
func TestTileSparkWidth(t *testing.T) {
	tests := []struct {
		cw   int
		want int // sparkline cells, 0 when the tile has no room for one
	}{
		{41, 8},  // two tiles on a 120-column screen: the floor, exactly
		{61, 18}, // two tiles on a 160-column screen
		{58, 17}, // cw/2 − tileRateW − 1
		{85, 24}, // one tile on a 120-column screen: the cap
		{31, 0},  // two tiles on a 100-column screen: under the floor
		{28, 0},
		{18, 0},
	}
	s := ModelAt(ExampleTapeN(4), midRun).Streams[0]
	for _, tc := range tests {
		if got := tileSparkW(tc.cw); got != tc.want {
			t.Errorf("tileSparkW(%d) = %d, want %d", tc.cw, got, tc.want)
		}
		line := tileStatLine(PlainTheme(), s, tc.cw)
		drawn := 0
		for _, r := range line {
			if sparkRuneAt(r) {
				drawn++
			}
		}
		if drawn != tc.want {
			t.Errorf("a %d-column stat line drew %d sparkline cells, want %d: %q", tc.cw, drawn, tc.want, line)
		}
		// The rate never goes, and the TTFT outlives the graph: a tile that
		// cannot hold both reports the measurement, not the shape.
		if !strings.Contains(line, "tok/s") {
			t.Errorf("a %d-column stat line dropped its rate: %q", tc.cw, line)
		}
		// At cw = 18 there is no room for a TTFT either: the rate field is
		// eleven columns and "ttft 630 ms" is another eleven. The narrowest
		// width where the order is observable is 28.
		if tc.want == 0 && tc.cw >= 28 && !strings.Contains(line, "ttft") {
			t.Errorf("a %d-column stat line dropped the ttft before the sparkline: %q", tc.cw, line)
		}
	}
}
