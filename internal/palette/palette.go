// Package palette is the single owner of toktape's colours: the TUI, the
// frames internal/render rasterises and the PNG share card all read them from
// here (TTP-44, 2026-09-13). Before it, the TUI and the card each carried a
// set, and the card was still Catppuccin Mocha after the TUI became oxide, so
// a posted card and the clip beside it looked like two different products.
//
// One accent hue, one warm hue, one bad hue, and neutrals. Nothing else gets a
// colour; restraint is the look.
//
// "oxide" (TTP-40, user 2026-09-13: "색상은 oxide로 가자", after "전반적으로
// mute되었지만 그래도 특정 색상 경향은 있는 유니크한 팔레트", and "너무 배낀거
// 같지는 않게" about the btop/gruvbox reference): a sage-teal accent on a
// green-black ground with warm off-white text. It replaced a Tailwind
// sky/amber/red set whose accent sat at 0.95 saturation and was the first thing
// the eye found; oxide's is 0.33. The derived shades are blends of the base
// colours (Ground, Text, Dim, Accent, Warn, Bad), so a future palette changes
// the six and re-derives the rest:
//
//	TextMuted   Text blended 30 % toward Ground
//	TextMid     midpoint of Text and TextMuted; DimMid of TextMuted and Dim
//	AccentHigh  Accent 45 % toward white
//	AccentMid   Accent 25 % toward Ground
//	AccentMuted Accent 45 % toward Ground
//	AccentLow   Accent 62 % toward Ground
//	DarkFill    Accent 86 % toward Ground
//
// The share card needs a few roles a terminal does not: a canvas, a panel, an
// inert fill, four GPU segments and a host-memory segment. They are derived
// from the same base (contrast is against CardPanel):
//
//	CardBase    Ground 25 % toward black                    1.12:1
//	CardPanel   Ground 4.5 % toward Accent                  —
//	CardBorder  = DarkFill                                  1.20:1
//	CardSurface bar track, never-loaded segment             1.39:1
//	CardGPU1    Accent 30 % toward Ground                   4.68:1
//	CardGPU2    Accent 30 % toward white                   10.63:1
//	CardGPU3    Accent 48 % toward Ground                   3.10:1
//	CardHost    a desaturated sand, deliberately not Warn   7.36:1
//
// Every colour is a "#rrggbb" string, because lipgloss takes strings; RGBA
// converts one for the image renderers.
package palette

import (
	"fmt"
	"image/color"
	"strconv"
)

// The oxide base colours.
const (
	// Ground is the background the palette is designed on. The TUI never
	// paints it — a terminal brings its own — but internal/render rasterises
	// frames on it, and every luminance step is measured against it.
	Ground = "#0f1514"
	// Text is primary text.
	Text = "#e6e2d8"
	// Dim is labels and chrome, and nothing else.
	Dim = "#6f7872"
	// Accent is sage teal: the rates, the active stream, the cursor.
	Accent = "#86c2b4"
	// Warn is ochre: contended, cold cache, throttled.
	Warn = "#d6a760"
	// Bad is clay red: a maj/tok spike, a failed stream.
	Bad = "#d47e70"
)

// The derived shades (see the package doc for each blend).
const (
	// TextMid is the midpoint of Text and TextMuted.
	TextMid = "#c6c3ba"
	// TextMuted is Text blended 30 % toward Ground: body text.
	TextMuted = "#a6a49d"
	// DimMid is the midpoint of TextMuted and Dim.
	DimMid = "#8a8e88"
	// AccentHigh is Accent 45 % toward white.
	AccentHigh = "#bcddd6"
	// AccentMid is Accent 25 % toward Ground.
	AccentMid = "#68978c"
	// AccentMuted is Accent 45 % toward Ground.
	AccentMuted = "#50746c"
	// AccentLow is Accent 62 % toward Ground.
	AccentLow = "#3c5751"
	// DarkFill is Accent 86 % toward Ground: the unfilled remainder of a bar.
	DarkFill = "#202d2a"
)

// The share card's extra roles.
const (
	// CardBase is the canvas behind the card panel.
	CardBase = "#0b100f"
	// CardPanel is the card panel.
	CardPanel = "#141d1b"
	// CardBorder is the panel's 1 px border and every hairline rule.
	CardBorder = DarkFill
	// CardSurface is the inert fill: the bar track, the never-loaded segment.
	CardSurface = "#273834"
	// CardGPU1 is the second GPU segment.
	CardGPU1 = "#628e84"
	// CardGPU2 is the third GPU segment.
	CardGPU2 = "#aad4ca"
	// CardGPU3 is the fourth GPU segment.
	CardGPU3 = "#4d6f67"
	// CardHost is the host / CPU memory segment. It is warm like Warn and sits
	// a few degrees from it in hue, so it is kept from reading as a warning by
	// saturation: 0.25 against Warn's 0.59.
	CardHost = "#b8a888"
)

// RGBA parses a "#rrggbb" palette entry into an opaque colour. It panics on a
// malformed entry: the palette is fixed at compile time, so one is a
// programming error, and palette_test parses every entry.
func RGBA(hex string) color.RGBA {
	if len(hex) != 7 || hex[0] != '#' {
		panic(fmt.Sprintf("palette: %q is not #rrggbb", hex))
	}
	v, err := strconv.ParseUint(hex[1:], 16, 32)
	if err != nil {
		panic(fmt.Sprintf("palette: %q is not #rrggbb: %v", hex, err))
	}
	return color.RGBA{R: uint8(v >> 16), G: uint8(v >> 8), B: uint8(v), A: 0xff}
}
