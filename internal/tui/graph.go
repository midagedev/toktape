package tui

import (
	"math"
	"strings"
)

// GraphStyle selects the glyph set. Block is the default: it reads as a solid
// measure with a lit ridge (see graphRow), where braille's dot texture read as
// a pattern and looked borrowed (user 2026-09-13: "너무 배낀거 같지는 않게").
type GraphStyle int

const (
	GraphBraille GraphStyle = iota // 2 samples per cell, 4 dot levels per row
	GraphBlock                     // 1 sample per cell, ▁▂▃▄▅▆▇█ 8 levels per row
)

// resourceGraphStyle is the one switch the lead flips to compare the two looks.
const resourceGraphStyle = GraphBlock

// brailleFill is the dot order a braille column lights in when it fills from
// the bottom: the left column's bits, then the right column's. U+2800 plus the
// OR of the lit bits is the rune. The fourth dot of each column (0x40, 0x80)
// was added to Unicode after the first six, which is why the bottom row is not
// the next power of two up.
var brailleFill = [2][4]rune{
	{0x40, 0x04, 0x02, 0x01},
	{0x80, 0x20, 0x10, 0x08},
}

// blockFill is a block column at 0..8 eighths.
var blockFill = []rune(" ▁▂▃▄▅▆▇█")

// Graph draws vals (oldest first, NaN = not observed) as an area graph `rows`
// cells tall and `w` cells wide against a FIXED ceiling, newest sample at the
// right edge. It returns rows strings, top row first, each exactly w runes.
//
// The ceiling is fixed on purpose. Sparkline scales to the window's own
// maximum, which is right for a rate whose absolute size the figure beside it
// already says, and wrong for a utilisation: a GPU sitting at a steady 40 %
// would draw as a range of mountains, and a reader would see load swinging
// where there is none (TTP-39).
//
// A measured sample always lights at least the bottom dot, so a measured zero
// draws a baseline; a NaN, and every column before the series starts, draws
// nothing at all. No sample is not a zero sample.
//
// Braille holds two samples per cell and shows the last 2w. When that window
// holds an odd number of samples the newest sits in the left dot column of the
// rightmost cell and the right column is blank, so the graph advances one cell
// every two samples rather than shuffling every cell sideways by half a cell.
func Graph(vals []float64, ceiling float64, w, rows int, style GraphStyle) []string {
	if w <= 0 || rows <= 0 {
		return make([]string, max(rows, 0))
	}
	per, levels := 2, rows*4
	if style == GraphBlock {
		per, levels = 1, rows*8
	}

	// slots[j] is the level of the j-th sample position, left to right; 0 is
	// blank.
	slots := make([]int, w*per)
	win := vals
	if len(win) > len(slots) {
		win = win[len(win)-len(slots):]
	}
	start := len(slots) - len(win)
	if per == 2 && len(win)%2 == 1 {
		start-- // the newest sample opens the rightmost cell's left column
	}
	for i, v := range win {
		slots[start+i] = graphLevel(v, ceiling, levels)
	}

	perRow := levels / rows
	out := make([]string, rows)
	for r := 0; r < rows; r++ {
		// Row r counted from the top; its first level from the bottom.
		base := (rows - 1 - r) * perRow
		var b strings.Builder
		for c := 0; c < w; c++ {
			if per == 1 {
				b.WriteRune(blockFill[clampInt(slots[c]-base, 0, 8)])
				continue
			}
			var bits rune
			for col := 0; col < 2; col++ {
				lit := clampInt(slots[c*2+col]-base, 0, 4)
				for k := 0; k < lit; k++ {
					bits |= brailleFill[col][k]
				}
			}
			if bits == 0 {
				b.WriteByte(' ')
				continue
			}
			b.WriteRune(0x2800 + bits)
		}
		out[r] = b.String()
	}
	return out
}

// graphLevel maps one sample onto 0..levels: 0 for a sample that was not
// observed, otherwise 1 + round(v/ceiling × (levels−1)) clamped to [1, levels].
// A ceiling that is not positive cannot scale anything, so it draws nothing
// rather than a guessed height.
func graphLevel(v, ceiling float64, levels int) int {
	if math.IsNaN(v) || math.IsInf(v, 0) || ceiling <= 0 {
		return 0
	}
	return clampInt(1+int(math.Round(v/ceiling*float64(levels-1))), 1, levels)
}
