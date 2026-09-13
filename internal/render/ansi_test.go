package render

import (
	"image/color"
	"strings"
	"testing"
	"time"

	"github.com/midagedev/toktape/internal/tui"
)

func TestParseScreenSGR(t *testing.T) {
	cyan := color.RGBA{R: 0x7d, G: 0xd3, B: 0xfc, A: 0xff}

	tests := []struct {
		name  string
		text  string
		at    [2]int // x, y
		wantR rune
		wantC color.RGBA
		bold  bool
	}{
		{
			name:  "plain text keeps the default foreground",
			text:  "ab",
			at:    [2]int{1, 0},
			wantR: 'b',
			wantC: fgColour,
		},
		{
			name:  "a true-colour foreground is read exactly",
			text:  "\x1b[38;2;125;211;252mX",
			at:    [2]int{0, 0},
			wantR: 'X',
			wantC: cyan,
		},
		{
			name:  "bold is carried on the cell",
			text:  "\x1b[1;38;2;125;211;252mX",
			at:    [2]int{0, 0},
			wantR: 'X',
			wantC: cyan,
			bold:  true,
		},
		{
			name:  "a reset puts the pen back",
			text:  "\x1b[1;38;2;125;211;252mX\x1b[0mY",
			at:    [2]int{1, 0},
			wantR: 'Y',
			wantC: fgColour,
		},
		{
			name:  "a 256-colour index resolves through the cube",
			text:  "\x1b[38;5;196mX",
			at:    [2]int{0, 0},
			wantR: 'X',
			wantC: color.RGBA{R: 255, G: 0, B: 0, A: 0xff},
		},
		{
			name:  "a basic colour resolves through the sixteen",
			text:  "\x1b[31mX",
			at:    [2]int{0, 0},
			wantR: 'X',
			wantC: ansi16[1],
		},
		{
			name:  "a cursor-movement sequence is skipped, not printed",
			text:  "\x1b[H\x1b[2JX",
			at:    [2]int{0, 0},
			wantR: 'X',
			wantC: fgColour,
		},
		{
			name:  "an OSC title is swallowed whole",
			text:  "\x1b]0;a title\x07X",
			at:    [2]int{0, 0},
			wantR: 'X',
			wantC: fgColour,
		},
		{
			name:  "the second line starts at column zero",
			text:  "abc\ndef",
			at:    [2]int{0, 1},
			wantR: 'd',
			wantC: fgColour,
		},
		{
			name:  "CRLF does not print a stray column",
			text:  "abc\r\ndef",
			at:    [2]int{0, 1},
			wantR: 'd',
			wantC: fgColour,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sc := parseScreen(tc.text, 10, 3)
			got := sc.at(tc.at[0], tc.at[1])
			if got.r != tc.wantR {
				t.Errorf("cell (%d,%d) holds %q, want %q", tc.at[0], tc.at[1], got.r, tc.wantR)
			}
			if got.fg != tc.wantC {
				t.Errorf("cell (%d,%d) foreground = %v, want %v", tc.at[0], tc.at[1], got.fg, tc.wantC)
			}
			if got.bold != tc.bold {
				t.Errorf("cell (%d,%d) bold = %v, want %v", tc.at[0], tc.at[1], got.bold, tc.bold)
			}
		})
	}
}

func TestParseScreenWideRunesTakeTwoCells(t *testing.T) {
	// Hangul is two columns (handover lesson 5) and the grid has to agree with
	// the layout that produced it, or every cell after the syllable slides.
	sc := parseScreen("가나a", 10, 1)
	if got := sc.at(0, 0); got.r != '가' || got.cont {
		t.Errorf("cell 0 = %+v, want the syllable itself", got)
	}
	if got := sc.at(1, 0); !got.cont || got.r != 0 {
		t.Errorf("cell 1 = %+v, want the syllable's second half", got)
	}
	if got := sc.at(2, 0); got.r != '나' {
		t.Errorf("cell 2 holds %q, want 나", got.r)
	}
	if got := sc.at(4, 0); got.r != 'a' {
		t.Errorf("cell 4 holds %q, want a", got.r)
	}
}

func TestParseScreenIgnoresOverflow(t *testing.T) {
	sc := parseScreen("abcdef\nghi", 3, 2)
	if got := sc.at(2, 0); got.r != 'c' {
		t.Errorf("cell (2,0) holds %q, want c", got.r)
	}
	if got := sc.at(0, 1); got.r != 'g' {
		t.Errorf("cell (0,1) holds %q, want g — the overflow must not wrap", got.r)
	}
}

func TestParseScreenReadsTheRealTheme(t *testing.T) {
	// The colours the TUI actually emits, through lipgloss, must come back as
	// the hexes theme.go names. A renderer that guessed at the terminal profile
	// would silently render the whole clip in the sixteen-colour set.
	//
	// Within one count per channel, not exactly: lipgloss round-trips a hex
	// through its colour library and emits #6b7280 as 107;113;128 — one short
	// on green. That is the sequence a real terminal receives too, so the
	// parser is right to report what it was sent; the tolerance is here to say
	// that the shift is lipgloss's and is known.
	// A contended run, so the frame carries the warm hue as well: the example
	// rig runs on a quiet machine and shows no amber at all (2026-09-13,
	// TTP-28), and a palette check needs every colour it names to be on screen.
	tp := tui.ExampleTape()
	tp.Summary.Contention.Contended = true
	tp.Summary.Contention.Reasons = []string{"2 other GPU procs"}
	m := tui.ModelAt(tp, 3*time.Second)
	m.Theme = tui.ColourTheme()
	frame := tui.View(m, 3*time.Second, DefaultWidth, DefaultHeight)
	if !strings.Contains(frame, "\x1b[38;2;") {
		t.Fatal("the coloured frame carries no true-colour escape; the theme is not on")
	}

	sc := parseScreen(frame, DefaultWidth, DefaultHeight)
	for _, want := range []struct {
		name string
		col  color.RGBA
	}{
		// The palette's own entries (TTP-40): the check is that every role
		// survives the parse, whatever the palette currently is.
		{"accent", hexColour(tui.ThemePalette().Accent)},
		{"dim", hexColour(tui.ThemePalette().Dim)},
		{"text", hexColour(tui.ThemePalette().Text)},
		{"warn", hexColour(tui.ThemePalette().Warn)},
	} {
		found := 0
		for _, c := range sc.cells {
			if near(c.fg, want.col, 1) {
				found++
			}
		}
		if found == 0 {
			t.Errorf("no cell wears the %s colour %v; the palette did not survive the parse", want.name, want.col)
		}
	}
}

// near reports whether two colours are within tol counts on every channel.
func near(a, b color.RGBA, tol int) bool {
	d := func(x, y uint8) int {
		if x > y {
			return int(x - y)
		}
		return int(y - x)
	}
	return d(a.R, b.R) <= tol && d(a.G, b.G) <= tol && d(a.B, b.B) <= tol
}
