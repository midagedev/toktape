package tui

import (
	"io"
	"strings"
	"sync"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// The palette. One accent hue, one warm hue, one bad hue, and neutrals —
// docs/toktape-spec.ko.md §1 decision 10 and the track contract. Nothing else
// gets a colour; restraint is the look.
const (
	colAccent = "#7dd3fc" // cyan: bars, active stream, the live numbers
	colWarn   = "#fbbf24" // amber: contended, cold cache, throttled
	colBad    = "#f87171" // red: a maj/tok spike, a failed stream
	colText   = "#e5e7eb" // primary text
	colDim    = "#6b7280" // labels and chrome, and nothing else
	// colDarkFill is the unfilled remainder of a bar: the accent hue at the
	// bottom of its lightness range, drawn with the same solid block as the
	// filled part. A hatch glyph (░) was noisy in most terminal fonts and read
	// as texture rather than as an empty measure.
	colDarkFill = "#1e293b"
)

// Three shades of the accent, used only where something breathes: the stream
// cursor and the header shimmer. They are the same hue at three lightnesses,
// never three different hues.
const (
	colAccentLow  = "#38607a"
	colAccentMid  = "#5aa3c8"
	colAccentHigh = "#bde8ff"
)

// Theme carries the styles View paints with. The zero Theme is plain: every
// paint call returns its argument unchanged, which is what the golden tests
// render. Colour comes from ColourTheme.
//
// Two renderings of the same Model must differ only in escape sequences, so
// no style here may set a width, a padding, a border or an alignment —
// lipgloss measures with its own width function, and this package owns
// layout. Styles set a foreground, and at most the bold attribute, which
// changes no widths. TestColourMatchesPlain pins that invariant.
type Theme struct {
	colour bool

	accent lipgloss.Style
	warn   lipgloss.Style
	bad    lipgloss.Style
	text   lipgloss.Style
	dim    lipgloss.Style

	// accentBold carries the hierarchy: section titles, the active stream and
	// the three headline figures wear it, and nothing else does.
	accentBold lipgloss.Style
	darkFill   lipgloss.Style

	accentLow  lipgloss.Style
	accentMid  lipgloss.Style
	accentHigh lipgloss.Style
}

// PlainTheme returns the theme that emits no escape sequences. It is the zero
// value, named so callers can say what they mean.
func PlainTheme() Theme { return Theme{} }

var (
	colourOnce  sync.Once
	colourTheme Theme
)

// ColourTheme returns the true-colour theme.
//
// The renderer is explicit rather than lipgloss's package default: the default
// sniffs the attached terminal, so a test would silently render plain while a
// capture rendered coloured, and the two could drift without anything failing.
// Writing to io.Discard is deliberate — this renderer is only ever asked to
// produce strings, never to write them.
func ColourTheme() Theme {
	colourOnce.Do(func() {
		r := lipgloss.NewRenderer(io.Discard)
		r.SetColorProfile(termenv.TrueColor)
		r.SetHasDarkBackground(true)
		fg := func(hex string) lipgloss.Style {
			return r.NewStyle().Foreground(lipgloss.Color(hex))
		}
		colourTheme = Theme{
			colour:     true,
			accent:     fg(colAccent),
			accentBold: fg(colAccent).Bold(true),
			darkFill:   fg(colDarkFill),
			warn:       fg(colWarn),
			bad:        fg(colBad),
			text:       fg(colText),
			dim:        fg(colDim),
			accentLow:  fg(colAccentLow),
			accentMid:  fg(colAccentMid),
			accentHigh: fg(colAccentHigh),
		}
	})
	return colourTheme
}

// paint applies st to s, or returns s untouched on a plain theme.
func (th Theme) paint(st lipgloss.Style, s string) string {
	if !th.colour || s == "" {
		return s
	}
	return st.Render(s)
}

// lineBuf assembles one screen line to an exact column count.
//
// Every segment is truncated to the space that is left *before* it is painted,
// so an escape sequence can never be cut in half and the painted and plain
// renderings always agree on where the text ends. String pads the result to
// the full width.
type lineBuf struct {
	th   Theme
	w    int
	used int
	b    strings.Builder
}

func newLine(th Theme, w int) *lineBuf { return &lineBuf{th: th, w: w} }

// left returns the columns still free on the line.
func (l *lineBuf) left() int { return l.w - l.used }

// add appends s painted with st, clipped to what is left.
func (l *lineBuf) add(st lipgloss.Style, s string) *lineBuf {
	s = clip(s, l.left())
	if s == "" {
		return l
	}
	l.used += cond.StringWidth(s)
	l.b.WriteString(l.th.paint(st, s))
	return l
}

// addTrunc is add with an ellipsis when the text does not fit.
func (l *lineBuf) addTrunc(st lipgloss.Style, s string) *lineBuf {
	return l.addRaw(st, truncate(s, l.left()))
}

// addRaw appends s painted with st without clipping. The caller must already
// have measured it; used where a segment was fitted by the layout.
func (l *lineBuf) addRaw(st lipgloss.Style, s string) *lineBuf {
	if s == "" {
		return l
	}
	l.used += cond.StringWidth(s)
	l.b.WriteString(l.th.paint(st, s))
	return l
}

// space appends n blank columns, never more than are left.
func (l *lineBuf) space(n int) *lineBuf {
	if n > l.left() {
		n = l.left()
	}
	if n <= 0 {
		return l
	}
	l.used += n
	l.b.WriteString(strings.Repeat(" ", n))
	return l
}

// gapTo leaves blanks so that the next segment of width n ends at column w.
func (l *lineBuf) gapTo(n int) *lineBuf { return l.space(l.left() - n) }

// String returns the line padded with spaces to exactly w columns.
func (l *lineBuf) String() string {
	return l.b.String() + strings.Repeat(" ", max(0, l.left()))
}
