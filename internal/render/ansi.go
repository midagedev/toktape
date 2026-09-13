package render

import (
	"image/color"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/mattn/go-runewidth"
	"github.com/midagedev/toktape/internal/tui"
)

// The terminal the frames are rasterised for: a dark, unthemed one. These two
// are the only colours not carried by an escape sequence.
var (
	// bgColour is the terminal background: the palette's ground, a near-black
	// with a green cast rather than #000, against which every luminance step
	// of the theme was measured (TTP-40).
	bgColour = hexColour(tui.ThemePalette().Ground)
	// fgColour is the default foreground, used for any cell the frame did not
	// paint. It is tui's text colour, read from its palette (TTP-40).
	fgColour = hexColour(tui.ThemePalette().Text)
)

// hexColour parses a "#rrggbb" palette entry. The palette is tui's, fixed at
// compile time and pinned by its tests, so a malformed entry is a programming
// error and panics at start-up rather than rasterising a wrong colour.
func hexColour(hex string) color.RGBA {
	v, err := strconv.ParseUint(strings.TrimPrefix(hex, "#"), 16, 32)
	if err != nil || len(hex) != 7 {
		panic("render: palette colour " + strconv.Quote(hex) + " is not #rrggbb")
	}
	return color.RGBA{R: uint8(v >> 16), G: uint8(v >> 8), B: uint8(v), A: 0xff}
}

// cellWidth measures runes exactly as the TUI laid them out.
//
// Same condition as internal/card: the locale is pinned off, so an ambiguous
// rune (the box set, "·", "×", "…") is one column on every machine, and a
// Hangul syllable is still two because it is Wide, not Ambiguous. A
// rasteriser that measured differently from the layout would slide every cell
// after the first Hangul syllable.
var cellWidth = &runewidth.Condition{EastAsianWidth: false, StrictEmojiNeutral: true}

// cell is one character cell of a parsed frame.
type cell struct {
	r     rune
	fg    color.RGBA
	bg    color.RGBA
	hasBG bool
	bold  bool
	// cont marks the right-hand half of a two-column rune. Its r is zero: the
	// glyph was drawn from the cell to its left.
	cont bool
}

// screen is a parsed frame: w×h cells in row-major order.
type screen struct {
	w, h  int
	cells []cell
}

func newScreen(w, h int) screen {
	s := screen{w: w, h: h, cells: make([]cell, w*h)}
	for i := range s.cells {
		s.cells[i] = cell{r: ' ', fg: fgColour}
	}
	return s
}

func (s screen) at(x, y int) cell { return s.cells[y*s.w+x] }

// sgrState is the pen: what the next character will be drawn with.
type sgrState struct {
	fg    color.RGBA
	bg    color.RGBA
	hasBG bool
	bold  bool
	dim   bool
}

func defaultPen() sgrState { return sgrState{fg: fgColour} }

// parseScreen turns one frame of tui.View output into a w×h cell grid.
//
// It is a terminal emulator with everything the frames do not need left out:
// there is no cursor addressing, no scrolling and no wrapping, because
// tui.View emits exactly h lines of exactly w columns and nothing else. SGR is
// interpreted; any other escape sequence is skipped, so a frame that arrived
// with a leading "\x1b[H" from the asciicast path rasterises the same as the
// bare view.
//
// A line short of w columns leaves the remaining cells at the background, and
// anything past column w is dropped: a rasteriser is the wrong place to
// discover a layout bug, and the width tests are the right one.
func parseScreen(text string, w, h int) screen {
	sc := newScreen(w, h)
	pen := defaultPen()
	x, y := 0, 0

	for i := 0; i < len(text); {
		c := text[i]
		switch {
		case c == '\x1b':
			n, params, isSGR := scanEscape(text[i:])
			if isSGR {
				pen.applySGR(params)
			}
			i += n
			continue
		case c == '\n':
			x, y = 0, y+1
			i++
			continue
		case c == '\r':
			x = 0
			i++
			continue
		}

		r, size := decodeRune(text[i:])
		i += size
		if y >= h {
			continue
		}
		rw := cellWidth.RuneWidth(r)
		if rw == 0 {
			// Combining marks and other zero-width runes have no cell of
			// their own. Dropping them is a deliberate limit of the grid
			// model, not an oversight: the TUI emits none.
			continue
		}
		if x+rw > w {
			x += rw
			continue
		}
		sc.cells[y*w+x] = cell{r: r, fg: pen.paint(), bg: pen.bg, hasBG: pen.hasBG, bold: pen.bold}
		for k := 1; k < rw; k++ {
			sc.cells[y*w+x+k] = cell{cont: true, bg: pen.bg, hasBG: pen.hasBG}
		}
		x += rw
	}
	return sc
}

// paint returns the pen's foreground with the dim attribute folded in. A
// terminal renders SGR 2 by darkening, and the frames use it nowhere, but a
// frame that did use it should not come out at full strength.
func (p sgrState) paint() color.RGBA {
	if !p.dim {
		return p.fg
	}
	return color.RGBA{R: p.fg.R * 3 / 5, G: p.fg.G * 3 / 5, B: p.fg.B * 3 / 5, A: 0xff}
}

// scanEscape measures the escape sequence at the head of s. It returns the
// byte length, the parameter string when the sequence is an SGR ("...m"), and
// whether it was one.
func scanEscape(s string) (n int, params string, isSGR bool) {
	if len(s) < 2 {
		return len(s), "", false
	}
	switch s[1] {
	case '[':
		for i := 2; i < len(s); i++ {
			if s[i] >= '@' && s[i] <= '~' {
				if s[i] == 'm' {
					return i + 1, s[2:i], true
				}
				return i + 1, "", false
			}
		}
		return len(s), "", false
	case ']':
		// OSC runs to BEL or to ST (ESC \).
		for i := 2; i < len(s); i++ {
			if s[i] == '\x07' {
				return i + 1, "", false
			}
			if s[i] == '\x1b' && i+1 < len(s) && s[i+1] == '\\' {
				return i + 2, "", false
			}
		}
		return len(s), "", false
	default:
		return 2, "", false
	}
}

// applySGR folds one SGR parameter list into the pen.
func (p *sgrState) applySGR(params string) {
	if params == "" {
		*p = defaultPen()
		return
	}
	fields := strings.Split(params, ";")
	nums := make([]int, len(fields))
	for i, f := range fields {
		n, err := strconv.Atoi(f)
		if err != nil {
			n = 0
		}
		nums[i] = n
	}
	for i := 0; i < len(nums); i++ {
		switch n := nums[i]; {
		case n == 0:
			*p = defaultPen()
		case n == 1:
			p.bold = true
		case n == 2:
			p.dim = true
		case n == 22:
			p.bold, p.dim = false, false
		case n == 39:
			p.fg = fgColour
		case n == 49:
			p.hasBG = false
		case n >= 30 && n <= 37:
			p.fg = ansi16[n-30]
		case n >= 90 && n <= 97:
			p.fg = ansi16[n-90+8]
		case n >= 40 && n <= 47:
			p.bg, p.hasBG = ansi16[n-40], true
		case n >= 100 && n <= 107:
			p.bg, p.hasBG = ansi16[n-100+8], true
		case n == 38 || n == 48:
			col, used, ok := extendedColour(nums[i+1:])
			i += used
			if !ok {
				continue
			}
			if n == 38 {
				p.fg = col
			} else {
				p.bg, p.hasBG = col, true
			}
		}
	}
}

// extendedColour reads the tail of a 38/48 parameter: ";2;r;g;b" or ";5;n".
// It returns the colour and how many parameters it consumed.
func extendedColour(rest []int) (col color.RGBA, used int, ok bool) {
	if len(rest) == 0 {
		return col, 0, false
	}
	switch rest[0] {
	case 2:
		if len(rest) < 4 {
			return col, len(rest), false
		}
		return color.RGBA{R: clamp8(rest[1]), G: clamp8(rest[2]), B: clamp8(rest[3]), A: 0xff}, 4, true
	case 5:
		if len(rest) < 2 {
			return col, len(rest), false
		}
		return xterm256(rest[1]), 2, true
	default:
		return col, 1, false
	}
}

func clamp8(v int) uint8 {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return uint8(v)
}

// ansi16 is the sixteen-colour set, in the shades a modern terminal ships. The
// TUI asks for true colour and never reaches these; they exist so a frame from
// somewhere else still rasterises.
var ansi16 = [16]color.RGBA{
	{0x00, 0x00, 0x00, 0xff}, {0xcd, 0x31, 0x31, 0xff}, {0x0d, 0xbc, 0x79, 0xff}, {0xe5, 0xe5, 0x10, 0xff},
	{0x24, 0x72, 0xc8, 0xff}, {0xbc, 0x3f, 0xbc, 0xff}, {0x11, 0xa8, 0xcd, 0xff}, {0xe5, 0xe5, 0xe5, 0xff},
	{0x66, 0x66, 0x66, 0xff}, {0xf1, 0x4c, 0x4c, 0xff}, {0x23, 0xd1, 0x8b, 0xff}, {0xf5, 0xf5, 0x43, 0xff},
	{0x3b, 0x8e, 0xea, 0xff}, {0xd6, 0x70, 0xd6, 0xff}, {0x29, 0xb8, 0xdb, 0xff}, {0xff, 0xff, 0xff, 0xff},
}

// xterm256 resolves a 256-colour index: 0-15 the basic set, 16-231 a 6×6×6
// cube, 232-255 a 24-step grey ramp.
func xterm256(n int) color.RGBA {
	switch {
	case n < 0 || n > 255:
		return fgColour
	case n < 16:
		return ansi16[n]
	case n < 232:
		n -= 16
		level := func(v int) uint8 {
			if v == 0 {
				return 0
			}
			return uint8(55 + v*40)
		}
		return color.RGBA{R: level(n / 36), G: level((n / 6) % 6), B: level(n % 6), A: 0xff}
	default:
		v := uint8(8 + (n-232)*10)
		return color.RGBA{R: v, G: v, B: v, A: 0xff}
	}
}

// decodeRune reads one UTF-8 rune. Invalid bytes come back as the replacement
// rune, one byte at a time, so a malformed frame still produces a grid instead
// of an infinite loop.
func decodeRune(s string) (rune, int) {
	return utf8.DecodeRuneInString(s)
}
