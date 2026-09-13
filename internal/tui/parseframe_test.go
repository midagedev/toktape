package tui

import (
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"
)

// A frame is text with escape sequences in it; a contract about colour is
// about cells. This is the smallest reader that turns one into the other: SGR
// only, no cursor movement, no scrolling, because View emits exactly h lines of
// exactly w columns and nothing else.
//
// internal/render has the same reader in its rasteriser (ansi.go). It is copied
// rather than imported: render depends on this package, so importing it back
// here would be a cycle. The copy is twenty lines and has one job.

// pcell is one parsed character cell: the rune, the foreground as the theme
// spelled it ("#7dd3fc"), and whether it was bold. The right-hand half of a
// two-column rune is a cell with a zero rune, so a column index into a row is
// a display column even after a Hangul syllable.
type pcell struct {
	r    rune
	fg   string
	bg   string
	bold bool
}

// parseFrame turns one frame into h rows of w cells.
func parseFrame(frame string, w, h int) [][]pcell {
	rows := make([][]pcell, h)
	for y := range rows {
		rows[y] = make([]pcell, w)
		for x := range rows[y] {
			rows[y][x] = pcell{r: ' '}
		}
	}
	var (
		fg   string
		bg   string
		bold bool
		x, y int
	)
	for i := 0; i < len(frame); {
		switch c := frame[i]; {
		case c == '\x1b':
			n, params, isSGR := scanSGR(frame[i:])
			if isSGR {
				fg, bg, bold = applySGR(fg, bg, bold, params)
			}
			i += n
			continue
		case c == '\n':
			x, y = 0, y+1
			i++
			continue
		}
		r, size := utf8.DecodeRuneInString(frame[i:])
		i += size
		rw := cond.RuneWidth(r)
		if rw == 0 || y >= h || x+rw > w {
			x += rw
			continue
		}
		rows[y][x] = pcell{r: r, fg: fg, bg: bg, bold: bold}
		for k := 1; k < rw; k++ {
			rows[y][x+k] = pcell{fg: fg, bg: bg, bold: bold}
		}
		x += rw
	}
	return rows
}

// scanSGR measures the escape sequence at the head of s, returning its length,
// the parameters when it is an SGR, and whether it was one.
func scanSGR(s string) (n int, params string, isSGR bool) {
	if len(s) < 2 || s[1] != '[' {
		return min(2, len(s)), "", false
	}
	for i := 2; i < len(s); i++ {
		if s[i] >= '@' && s[i] <= '~' {
			if s[i] == 'm' {
				return i + 1, s[2:i], true
			}
			return i + 1, "", false
		}
	}
	return len(s), "", false
}

// applySGR folds one parameter list into the pen. Only what lipgloss emits for
// this package's styles is interpreted: reset, bold, and a 24-bit foreground or
// background. The background is read because two contracts are about one — the
// resource graph's track and the answer's write head (TTP-47).
func applySGR(fg, bg string, bold bool, params string) (string, string, bool) {
	if params == "" {
		return "", "", false
	}
	fields := strings.Split(params, ";")
	for i := 0; i < len(fields); i++ {
		switch fields[i] {
		case "0":
			fg, bg, bold = "", "", false
		case "1":
			bold = true
		case "22":
			bold = false
		case "39":
			fg = ""
		case "49":
			bg = ""
		case "38":
			if i+4 < len(fields) && fields[i+1] == "2" {
				fg = hex(fields[i+2], fields[i+3], fields[i+4])
				i += 4
			}
		case "48":
			if i+4 < len(fields) && fields[i+1] == "2" {
				bg = hex(fields[i+2], fields[i+3], fields[i+4])
				i += 4
			}
		}
	}
	return fg, bg, bold
}

func hex(parts ...string) string {
	var b strings.Builder
	b.WriteByte('#')
	for _, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 || n > 255 {
			return ""
		}
		b.WriteString(strings.ToLower(strconv.FormatInt(int64(n)+0x100, 16)[1:]))
	}
	return b.String()
}

// styleHex is the foreground a style actually emits, read back through the same
// parser the gates use.
//
// It is not the constant the theme declares: lipgloss renders a colour through
// its own conversion and can land a unit away (#6b7280 arrives as #6b7180), so
// a test that compared against the source constant would be checking the
// palette's spelling rather than the frame's colour.
func styleHex(st lipgloss.Style) string {
	th := ColourTheme()
	rows := parseFrame(th.paint(st, "x"), 1, 1)
	return rows[0][0].fg
}

// styleBG is the background a style emits, "" when it sets none.
func styleBG(st lipgloss.Style) string {
	th := ColourTheme()
	rows := parseFrame(th.paint(st, "x"), 1, 1)
	return rows[0][0].bg
}

// TestParseFrameReadsTheThemesColours is the reader's own check: a painted
// frame has to come back with the theme's hexes on the cells that carry them,
// and stripping it has to reproduce the plain frame column for column.
func TestParseFrameReadsTheThemesColours(t *testing.T) {
	m := ModelAt(ExampleTapeN(4), midRun)
	m.Theme = ColourTheme()
	const w, h = 120, 36
	rows := parseFrame(View(m, midRun, w, h), w, h)

	plain := ModelAt(ExampleTapeN(4), midRun)
	for y, want := range strings.Split(View(plain, midRun, w, h), "\n") {
		if got := plainRow(rows[y]); got != want {
			t.Fatalf("row %d\n got %q\nwant %q", y, got, want)
		}
	}

	seen := map[string]bool{}
	for _, row := range rows {
		for _, c := range row {
			seen[c.fg] = true
		}
	}
	th := ColourTheme()
	for name, st := range map[string]lipgloss.Style{
		"accent": th.accent, "dim": th.dim, "text": th.text, "accentMuted": th.accentMuted,
	} {
		if !seen[styleHex(st)] {
			t.Errorf("no cell came back in the %s colour; the reader is not seeing the palette", name)
		}
	}
}
