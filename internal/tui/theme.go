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

// colAccentMuted is the accent blended 45% toward the terminal background
// (#11111b, the ground internal/render rasterises frames on): 0x7d→0x4c,
// 0xd3→0x7c, 0xfc→0x97.
//
// It exists because of the emphasis contract (TTP-28, user 2026-09-13: "화면에
// 너무 많은 요소들이 강조되어 있다"). The shapes that used to be painted in the
// accent itself — every tile's sparkline, the fault sparkline, the placement
// bars — are the largest lit areas on the screen, so they were the first thing
// the eye found and the figures they qualify were the last. They keep the hue,
// which is what says they belong to the same reading, and give up the
// lightness, which is what says they are not the point.
//
// It is not colAccentLow: that shade already means "the third segment of a
// gauge" in the vram legend, and it is dark enough that a whole sparkline in it
// stops reading as a line.
const colAccentMuted = "#4c7c97"

// The body-text ladder (TTP-28, user 2026-09-13: "토큰 내용 자체는 한 톤 내리는
// 게 맞겠어", then "방금 막 나온 토큰 정도만 조금 밝게 해서 속도감은 살리자").
//
// The text a model generated is the largest area on the screen, so drawing it
// at colText made the body the brightest thing in every tile and left the
// header and the rate to compete with it. The body drops one tone, and the
// tokens that have just landed keep the header's tone for a moment, so the
// write head reads as motion rather than the whole paragraph reading as new.
//
// Five stops, measured as WCAG relative luminance on this theme's ground:
//
//	colText       #e5e7eb  0.798   headers, labels, right-pane values
//	colTextMid    #d6d8dc  0.686   an answer token 150–500 ms old
//	colTextMuted  #c6c8cc  0.577   settled answer text, and a fresh thought
//	colDimMid     #989da6  0.335   a reasoning token 150–500 ms old
//	colDim        #6b7280  0.167   settled reasoning, chrome, labels
//
// colTextMuted is colText blended toward the ground until its luminance is
// 72 % of colText's; the measured ratio is 0.723. colTextMid is the sRGB
// midpoint of colText and colTextMuted, colDimMid the midpoint of colTextMuted
// and colDim, so the reasoning ramp is the answer ramp shifted two stops down
// and the two never collide at the same age.
//
// colDim is unchanged: the contract asks that reasoning sit at or below 80 %
// of colTextMuted's luminance, and 0.167/0.577 = 0.29 clears it with room.
const (
	colTextMid   = "#d6d8dc"
	colTextMuted = "#c6c8cc"
	colDimMid    = "#989da6"
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
	// accentMuted is what the shapes wear: sparklines and placement bars. It
	// is the accent with its lightness spent (see colAccentMuted), so a bar
	// still reads as one of the screen's measures without competing with the
	// figure it qualifies.
	accentMuted lipgloss.Style

	// textMid, textMuted and dimMid are the body-text ladder above. Nothing
	// but a stream's generated text wears them.
	textMid   lipgloss.Style
	textMuted lipgloss.Style
	dimMid    lipgloss.Style
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
			colour:      true,
			accent:      fg(colAccent),
			accentBold:  fg(colAccent).Bold(true),
			darkFill:    fg(colDarkFill),
			warn:        fg(colWarn),
			bad:         fg(colBad),
			text:        fg(colText),
			dim:         fg(colDim),
			accentLow:   fg(colAccentLow),
			accentMid:   fg(colAccentMid),
			accentHigh:  fg(colAccentHigh),
			accentMuted: fg(colAccentMuted),
			textMid:     fg(colTextMid),
			textMuted:   fg(colTextMuted),
			dimMid:      fg(colDimMid),
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
