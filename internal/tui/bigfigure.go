package tui

import "strings"

// A headline figure drawn five rows tall out of half blocks, for the result
// modal (user, 2026-09-14: the share card's numbers were unreadable at the
// size a feed shows a clip; 2026-09-15: at three rows the modal's figures
// rasterised to ~19 px on a phone-width timeline, so the face grew to five
// rows and five columns a glyph). Only what the headline figures can contain
// — digits, the decimal point and "?" — and only the two the modal leads
// with: one rate and one latency (2026-09-15), not two rates. A figure this
// size anywhere else would be shouting.
//
// Five rows and five columns a glyph. The bottom row is a full rule on every
// digit so the figure reads as seated, and the leaning strokes (the "1", the
// "7") are what keeps a five-row digit from reading as a banner.
const bigRows = 5

// bigGlyphs is the face: five rows a rune, every row bigGlyphW wide except
// the decimal point, which is one column so it does not open a gap the eye
// reads as a space.
var bigGlyphs = map[rune][bigRows]string{
	'0': {"▄▀▀▀▄", "█   █", "█   █", "█   █", "▀▀▀▀▀"},
	'1': {"  ▄█ ", "   █ ", "   █ ", "   █ ", "▀▀▀▀▀"},
	'2': {"▄▀▀▀▄", "    █", "▄▀▀▀▀", "█    ", "▀▀▀▀▀"},
	'3': {"▀▀▀▀▄", "    █", " ▀▀▀█", "    █", "▀▀▀▀▀"},
	'4': {"█   █", "█   █", "▀▀▀▀█", "    █", "    ▀"},
	'5': {"█▀▀▀▀", "█    ", "▀▀▀▀▄", "    █", "▀▀▀▀▀"},
	'6': {"▄▀▀▀▀", "█    ", "█▀▀▀▄", "█   █", "▀▀▀▀▀"},
	'7': {"▀▀▀▀█", "    █", "   ▄▀", "  █  ", "  ▀  "},
	'8': {"▄▀▀▀▄", "█   █", "█▀▀▀█", "█   █", "▀▀▀▀▀"},
	'9': {"▄▀▀▀▄", "█   █", "▀▀▀▀█", "    █", "▀▀▀▀▀"},
	'.': {" ", " ", " ", " ", "▀"},
	'?': {"▀▀▀▀▄", "    █", "  ▄▀ ", "     ", "  ▀  "},
}

// bigFigure draws s in the face, one column between glyphs. A rune the face
// does not have is drawn as "?" — the figure comes from fmtRate, who only
// produces the runes above, so this is a guard and not a path.
func bigFigure(s string) [bigRows]string {
	var rows [bigRows]strings.Builder
	for i, r := range s {
		g, ok := bigGlyphs[r]
		if !ok {
			g = bigGlyphs['?']
		}
		for k := range rows {
			if i > 0 {
				rows[k].WriteByte(' ')
			}
			rows[k].WriteString(g[k])
		}
	}
	var out [bigRows]string
	for k := range rows {
		out[k] = rows[k].String()
	}
	return out
}
