package tui

import (
	"math"
	"strings"
	"testing"
	"unicode/utf8"
)

var nan = math.NaN()

// brailleCell composes a cell from the number of lit dots in its left and
// right columns, counted from the bottom — written out independently of
// Graph's own table so the two have to agree.
func brailleCell(left, right int) rune {
	lbits := []rune{0x40, 0x04, 0x02, 0x01}
	rbits := []rune{0x80, 0x20, 0x10, 0x08}
	var bits rune
	for i := 0; i < left; i++ {
		bits |= lbits[i]
	}
	for i := 0; i < right; i++ {
		bits |= rbits[i]
	}
	if bits == 0 {
		return ' '
	}
	return 0x2800 + bits
}

// columnLevels reads a graph back into one level per sample position, left to
// right, by counting lit dots (or eighths) down each column.
func columnLevels(t *testing.T, rows []string, style GraphStyle) []int {
	t.Helper()
	if len(rows) == 0 {
		return nil
	}
	w := utf8.RuneCountInString(rows[0])
	per := 2
	if style == GraphBlock {
		per = 1
	}
	out := make([]int, w*per)
	for _, row := range rows {
		for c, r := range []rune(row) {
			if style == GraphBlock {
				eighths := -1
				for i, g := range blockFill {
					if g == r {
						eighths = i
					}
				}
				if eighths < 0 {
					t.Fatalf("rune %q is neither a block element nor a space", r)
				}
				out[c] += eighths
				continue
			}
			if r == ' ' {
				continue
			}
			if r < 0x2800 || r > 0x28ff {
				t.Fatalf("rune %q is neither a braille cell nor a space", r)
			}
			bits := r - 0x2800
			for col := 0; col < 2; col++ {
				for _, b := range brailleFill[col] {
					if bits&b != 0 {
						out[c*2+col]++
					}
				}
			}
		}
	}
	return out
}

func TestGraphShape(t *testing.T) {
	for _, style := range []GraphStyle{GraphBraille, GraphBlock} {
		for _, rows := range []int{1, 2, 3} {
			got := Graph([]float64{10, 50, 90, nan, 0}, 100, 7, rows, style)
			if len(got) != rows {
				t.Fatalf("style %d rows %d: got %d rows", style, rows, len(got))
			}
			for i, row := range got {
				if n := utf8.RuneCountInString(row); n != 7 {
					t.Errorf("style %d rows %d: row %d is %d runes, want 7: %q", style, rows, i, n, row)
				}
				if strings.ContainsRune(row, 0x2800) {
					t.Errorf("style %d: row %d uses the empty braille cell U+2800; a blank must be a space", style, i)
				}
			}
		}
	}
}

// TestGraphBrailleRunes pins the four runes the spec names, so a bit table
// that was mirrored or shifted by one dot cannot pass on shape alone.
func TestGraphBrailleRunes(t *testing.T) {
	const top = 100.0
	for _, c := range []struct {
		name string
		vals []float64
		want string
	}{
		// One row, four levels: 100 % lights all four dots of a column, and a
		// measured 0 lights the bottom one.
		{"both columns full", []float64{100, 100}, "⣿"},
		{"left bottom dot only", []float64{0, nan}, "⡀"},
		{"right bottom dot only", []float64{nan, 0}, "⢀"},
		{"both bottom dots", []float64{0, 0}, "⣀"},
		{"empty cell", []float64{nan, nan}, " "},
		// An odd window: the newest sample opens the rightmost cell's left
		// column and its right column is blank.
		{"odd count", []float64{0}, "⡀"},
		{"odd count, three", []float64{100, 100, 0}, "⣿⡀"},
	} {
		got := Graph(c.vals, top, utf8.RuneCountInString(c.want), 1, GraphBraille)
		if got[0] != c.want {
			t.Errorf("%s: Graph(%v) = %q, want %q", c.name, c.vals, got[0], c.want)
		}
	}
	if got, want := brailleCell(4, 4), '⣿'; got != want {
		t.Errorf("brailleCell(4,4) = %q, want %q", got, want)
	}
}

// TestGraphLevels checks level = 1 + round(v/ceiling × (L−1)) on both styles
// and every height, read back column by column.
func TestGraphLevels(t *testing.T) {
	vals := []float64{0, 12.5, 33, 50, 66, 87.5, 100, 140, -5}
	for _, style := range []GraphStyle{GraphBraille, GraphBlock} {
		for _, rows := range []int{1, 2, 3} {
			levels := rows * 4
			per := 2
			if style == GraphBlock {
				levels, per = rows*8, 1
			}
			w := (len(vals) + per - 1) / per
			got := columnLevels(t, Graph(vals, 100, w, rows, style), style)
			start := len(got) - len(vals)
			if per == 2 && len(vals)%2 == 1 {
				start--
			}
			for i, v := range vals {
				want := 1 + int(math.Round(v/100*float64(levels-1)))
				want = max(1, min(levels, want)) // 140 clamps to the top, -5 to the baseline
				if got[start+i] != want {
					t.Errorf("style %d rows %d: sample %v drew level %d, want %d", style, rows, v, got[start+i], want)
				}
			}
		}
	}
}

// TestGraphFixedCeiling is the reason Graph exists beside Sparkline.
//
// A near-constant utilisation must draw flat. Sparkline scales to the window's
// own maximum and draws the series below as mountains — 39.8 against a max of
// 40.4 is its bottom glyph, 40.4 its top — and a GPU holding a steady 40 %
// would read as load swinging from idle to full.
func TestGraphFixedCeiling(t *testing.T) {
	vals := []float64{40.0, 40.4, 39.8, 40.1}
	for _, style := range []GraphStyle{GraphBraille, GraphBlock} {
		got := columnLevels(t, Graph(vals, 100, 4, 3, style), style)
		lo, hi := math.MaxInt, 0
		for _, l := range got {
			if l == 0 {
				continue
			}
			lo, hi = min(lo, l), max(hi, l)
		}
		if hi-lo > 1 {
			t.Errorf("style %d: a near-constant series spans levels %d..%d, want one level ±1: %v", style, lo, hi, got)
		}
	}
	// Above the ceiling clamps to the top rather than wrapping or overflowing
	// into a row that does not exist.
	full := Graph([]float64{250, 250}, 100, 1, 3, GraphBraille)
	for i, row := range full {
		if row != "⣿" {
			t.Errorf("a value past the ceiling: row %d is %q, want ⣿", i, row)
		}
	}
}

// TestGraphNotObservedIsBlank: a NaN sample and the columns before the series
// starts draw nothing, while a measured 0 draws the baseline.
func TestGraphNotObservedIsBlank(t *testing.T) {
	rows := Graph([]float64{nan, 0, nan, nan}, 100, 6, 2, GraphBraille)
	want := []string{"      ", "    ⢀ "}
	for i := range want {
		if rows[i] != want[i] {
			t.Errorf("row %d = %q, want %q", i, rows[i], want[i])
		}
	}
	block := Graph([]float64{nan, 0, nan}, 100, 5, 1, GraphBlock)
	if block[0] != "   ▁ " {
		t.Errorf("block: %q, want %q", block[0], "   ▁ ")
	}
	if got := Graph(nil, 100, 3, 2, GraphBraille); got[0] != "   " || got[1] != "   " {
		t.Errorf("no samples at all: %q, want blank rows", got)
	}
	if got := Graph([]float64{50}, 0, 1, 1, GraphBraille); got[0] != " " {
		t.Errorf("a zero ceiling scales nothing and must draw nothing, got %q", got[0])
	}
}

// TestGraphFillsFromTheBottom: a column at level k lights the bottom k levels
// across the rows, so the bottom row is full before the one above it lights.
func TestGraphFillsFromTheBottom(t *testing.T) {
	// Three rows of braille, 12 levels. 50 % is level 1+round(5.5) = 7:
	// the bottom row full (4), the middle row three dots, the top row empty.
	rows := Graph([]float64{50, 50}, 100, 1, 3, GraphBraille)
	want := []string{" ", string(brailleCell(3, 3)), "⣿"}
	for i := range want {
		if rows[i] != want[i] {
			t.Errorf("braille row %d = %q, want %q", i, rows[i], want[i])
		}
	}
	// Block, two rows of 16 levels: 50 % is 1+round(7.5) = 9, a full bottom
	// cell and one eighth above.
	rows = Graph([]float64{50}, 100, 1, 2, GraphBlock)
	if rows[0] != "▁" || rows[1] != "█" {
		t.Errorf("block rows = %q, want [▁ █]", rows)
	}
}

// TestGraphWindow: braille shows the last 2w samples and block the last w,
// newest at the right edge.
func TestGraphWindow(t *testing.T) {
	var vals []float64
	for i := 0; i < 20; i++ {
		vals = append(vals, 0)
	}
	vals = append(vals, 100, 100, 100, 100)
	// Two braille cells hold the four newest samples, all full.
	if got := Graph(vals, 100, 2, 1, GraphBraille)[0]; got != "⣿⣿" {
		t.Errorf("braille window = %q, want ⣿⣿", got)
	}
	// Three cells: one of baseline pairs, then the four full samples.
	if got := Graph(vals, 100, 3, 1, GraphBraille)[0]; got != "⣀⣿⣿" {
		t.Errorf("braille window = %q, want ⣀⣿⣿", got)
	}
	if got := Graph(vals, 100, 5, 1, GraphBlock)[0]; got != "▁████" {
		t.Errorf("block window = %q, want ▁████", got)
	}
}
