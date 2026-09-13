package tui

import (
	"io"
	"strings"
	"sync"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/midagedev/toktape/internal/palette"
)

// The palette. One accent hue, one warm hue, one bad hue, and neutrals.
// Nothing else gets a colour; restraint is the look. The values, their blends
// and the user's words that chose them live in internal/palette, their one
// owner (TTP-44); the names here are the TUI's.
const (
	colAccent = palette.Accent // sage teal: the rates, the active stream, the cursor
	colWarn   = palette.Warn   // ochre: contended, cold cache, throttled
	colBad    = palette.Bad    // clay red: a maj/tok spike, a failed stream
	colText   = palette.Text   // primary text
	colDim    = palette.Dim    // labels and chrome, and nothing else
	// colDarkFill is the unfilled remainder of a bar: the accent hue at the
	// bottom of its lightness range, drawn with the same solid block as the
	// filled part. A hatch glyph (░) was noisy in most terminal fonts and read
	// as texture rather than as an empty measure.
	colDarkFill = palette.DarkFill
)

// colGround is the terminal background the palette is designed on. The TUI
// never paints it — a terminal brings its own — but internal/render rasterises
// frames on it, and every luminance step in this file is measured against it.
const colGround = palette.Ground

// Palette is the part of the theme the renderers outside this package draw
// with directly: the ground a frame is rasterised on, the default foreground,
// and the roles the cold open uses. It is the single owner of those colours
// (TTP-40, 2026-09-13): internal/render used to carry its own copies, and a
// palette change would have left them behind.
type Palette struct {
	Ground, Text, Dim, Accent, Warn string
}

// ThemePalette returns the palette as hex strings, "#rrggbb".
func ThemePalette() Palette {
	return Palette{Ground: colGround, Text: colText, Dim: colDim, Accent: colAccent, Warn: colWarn}
}

// Three shades of the accent, used only where something breathes: the stream
// cursor and the header shimmer. They are the same hue at three lightnesses,
// never three different hues.
const (
	colAccentLow  = palette.AccentLow
	colAccentMid  = palette.AccentMid
	colAccentHigh = palette.AccentHigh
)

// colAccentMuted is the accent blended 45% toward the terminal background
// (colGround, the ground internal/render rasterises frames on).
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
const colAccentMuted = palette.AccentMuted

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
//	colText       #e6e2d8  0.762   headers, labels, right-pane values
//	colTextMid    #c6c3ba  0.546   an answer token 150–500 ms old
//	colTextMuted  #a6a49d  0.371   settled answer text, and a fresh thought
//	colDimMid     #8a8e88  0.265   a reasoning token 150–500 ms old
//	colDim        #6f7872  0.180   settled reasoning, chrome, labels
//
// (Values for the oxide palette, TTP-40; the ratios below were set on the
// palette before it and hold on this one: 0.487 and 0.485.)
//
// colTextMuted is colText blended 30 % toward the ground, which puts the body
// at 0.48 of the header's luminance. The first cut (2026-09-13) used
// 0.72, and the user watched the clip and could see the write-head glow on the
// reasoning text but not on the answers: a 0.72 step is one the eye reads as
// the same tone. The reasoning ramp (0.29 of the body) was visible, so the
// answer body drops until its step is of the same order. colTextMid is the sRGB
// midpoint of colText and colTextMuted, colDimMid the midpoint of colTextMuted
// and colDim, so the reasoning ramp is the answer ramp shifted two stops down
// and the two never collide at the same age.
//
// The contract asks that reasoning sit at or below 80 % of colTextMuted's
// luminance, and colDim at 0.180/0.371 = 0.49 clears it.
const (
	colTextMid   = palette.TextMid
	colTextMuted = palette.TextMuted
	colDimMid    = palette.DimMid
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
//
// The one exception is the resource graph (TTP-39, 2026-09-13): graphTrack and
// graphRidge also set a background, colDarkFill, because a partial block's
// unlit part shows the cell background and a ridge drawn on the terminal's
// ground floated free of its own track. A background changes no width either,
// and the track itself is spaces, so a plain rendering shows no slab where a
// graph is empty.
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

	// graphTrack is the unlit part of a resource graph's measured columns,
	// painted as spaces; graphRidge is a column's topmost lit cell. Both sit
	// on colDarkFill (see the exception above).
	graphTrack lipgloss.Style
	graphRidge lipgloss.Style
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
			graphTrack:  r.NewStyle().Background(lipgloss.Color(colDarkFill)),
			graphRidge:  fg(colAccentMid).Background(lipgloss.Color(colDarkFill)),
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
