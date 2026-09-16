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
//	CardGPU1    Accent 15 % toward Ground                   6.41:1
//	CardGPU2    Accent 30 % toward white                   10.63:1
//	CardGPU3    = AccentMuted (Accent 45 % toward Ground)  3.32:1
//	CardHost    desaturated sand 20 % toward Ground, below
//	            the Accent's luminance, never Warn           5.10:1
//
// The GPU steps darken from the first device before they brighten, so the
// order on the bar reads as device order, and each clears the legend's kv and
// compute swatches as well as the other steps (lead 2026-09-13, after the first oxide card showed GPU1
// and kv as one colour), and the host sand is dimmed so a mostly-offloaded
// bar does not outshine the decode figure.
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

// The code hues (user 2026-09-16: "코드 하일라이팅이 너무 안 예쁘다, 우리
// 팔레트를 살짝 확장하는게 어떨까", then "좀 더 밝게 가도 될 것 같아, 좀더
// vscode스럽게 그리고 기왕 의존성 추가한거 하일라이팅 할 수 있는거 최대한
// 활용하자").
//
// A fenced block used to be shaped and never coloured — keywords bold, the
// rest walking the body's grey ladder — which left a function reading as prose
// with two bold words in it. These are the roles a reader of code already
// knows from an editor, at Dark+'s assignments (blue keywords, yellow calls,
// salmon strings, green numbers and comments, purple types) with the
// saturation pulled toward oxide's mute so a tile of code still belongs to the
// same screen as the rates beside it.
//
// They sit ABOVE the body ladder on purpose: code is the one part of an answer
// a reader stops to read, and the old rule that a class may only darken a rune
// kept it under the prose around it. The write head still wins over every one
// of them, so "this token just landed" survives.
//
// Teal is not among them. The accent is the rates and the active stream, and a
// type wearing it would be the one colour on the screen that means two things.
const (
	// CodeKeyword is a keyword, an operator word and control flow: Dark+'s
	// #569cd6 lifted out of its saturation.
	CodeKeyword = "#8ab6de"
	// CodeFunc is what is called or defined: a function, a method, a
	// decorator. Dark+'s #dcdcaa, warmed a shade toward the palette's text.
	CodeFunc = "#d8cd92"
	// CodeType is a class, a type name or a builtin: Dark+ spends its purple
	// on control flow, which oxide gives to the keyword blue, so the purple
	// carries the types instead — the one hue no other role in this palette
	// claims.
	CodeType = "#bda9d8"
	// CodeString is a string or a character literal.
	CodeString = "#d4a288"
	// CodeNumber is a number, a boolean, nil and the other built-in
	// constants.
	CodeNumber = "#b9cf9f"
	// CodeComment is a comment, and the only code hue under the body's own
	// luminance: a comment is the one thing in a block a reader may skip.
	CodeComment = "#7e9a6b"
	// CodeVar is a plain identifier — a variable, a field, a parameter — and
	// the quietest of the set, a step off the neutral text rather than a hue
	// of its own. Dark+ paints these too (#9cdcfe); at four tiles of code on
	// one screen a fully saturated one would be the loudest thing on it.
	CodeVar = "#a9c9e6"
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
	CardGPU1 = "#74a89c"
	// CardGPU2 is the third GPU segment.
	CardGPU2 = "#aad4ca"
	// CardGPU3 is the fourth GPU segment.
	CardGPU3 = "#50746c"
	// CardHost is the host / CPU memory segment. It is warm like Warn and sits
	// a few degrees from it in hue, so it is kept from reading as a warning by
	// saturation: 0.25 against Warn's 0.59.
	CardHost = "#968b71"
)

// GleamPeak is the core of the one pass of light the result modal's figures
// take when the card appears (user 2026-09-16: "메인숫자에 애니메이션 준게 너무
// 티가 안나더라고"). The sweep used to run between Accent and AccentHigh, a
// third of a stop apart, which on a 30 fps clip read as noise in the glyphs
// rather than as a light crossing them. This is the top of the accent's own
// ramp — the hue washed almost to white — so the band has somewhere to go.
const GleamPeak = "#eaf6f2"

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
