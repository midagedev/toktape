package png

import "image/color"

// ---------------------------------------------------------------- palette ---

// The colour contract is fixed by the track spec and
// docs/research/02-sharing-artifacts.md §6.2 (Catppuccin Mocha). Hues are
// rationed on purpose: one cool accent family (cyan → blue) carries decode,
// one warm hue (amber) carries memory, green means "clean/verified", red means
// "this run is contended or cold", and everything else is a neutral. Nothing
// on the card may introduce a sixth hue.
var (
	colBase    = rgb(0x11111b) // canvas behind the panel
	colPanel   = rgb(0x1e1e2e) // the card panel
	colBorder  = rgb(0x313244) // 1px panel border and every hairline rule
	colSurface = rgb(0x45475a) // inert fill: bar track, never-loaded segment

	colText  = rgb(0xcdd6f4) // primary text
	colDim   = rgb(0xa6adc8) // secondary text, labels
	colFaint = rgb(0x6c7086) // tertiary text: run id, credits, unobserved

	colCyan     = rgb(0x89dceb) // decode accent, gradient start
	colBlue     = rgb(0x89b4fa) // gradient end, second GPU
	colSapphire = rgb(0x74c7ec) // third GPU
	colTeal     = rgb(0x94e2d5) // fourth GPU
	colGreen    = rgb(0xa6e3a1) // prefill / TTFT, "clean" pills
	colAmber    = rgb(0xf9e2af) // host memory
	colRed      = rgb(0xf38ba8) // contended / cold
)

// gpuColors is the rotation used for GPU segments of the placement bar, in
// device-index order. Beyond four devices it repeats.
var gpuColors = []color.RGBA{colCyan, colBlue, colSapphire, colTeal}

// Lightness steps used to subdivide a GPU segment of the placement bar.
// Weights keep the device hue; the KV cache and the compute buffers are the
// same hue mixed toward the panel, so the three read as one device rather
// than three.
const (
	shadeWeights = 0.0
	shadeKV      = 0.36
	shadeCompute = 0.60
)

// devicePrefixGPU is how tape names GPU devices: "GPU0", "GPU1", ...
const devicePrefixGPU = "GPU"

// shade mixes c toward the panel colour by t (0 = c, 1 = the panel).
func shade(c color.RGBA, t float64) color.RGBA {
	mix := func(a, b uint8) uint8 { return uint8(float64(a) + (float64(b)-float64(a))*t + 0.5) }
	return color.RGBA{R: mix(c.R, colPanel.R), G: mix(c.G, colPanel.G), B: mix(c.B, colPanel.B), A: 0xff}
}

func rgb(v uint32) color.RGBA {
	return color.RGBA{R: uint8(v >> 16), G: uint8(v >> 8), B: uint8(v), A: 0xff}
}

// alpha returns c at the given opacity, pre-multiplied so it can be used
// directly as a draw.Over source. Pill backgrounds are the only translucent
// fill on the card.
func alpha(c color.RGBA, a uint8) color.RGBA {
	f := func(v uint8) uint8 { return uint8((uint32(v) * uint32(a)) / 0xff) }
	return color.RGBA{R: f(c.R), G: f(c.G), B: f(c.B), A: a}
}

// --------------------------------------------------------------- geometry ---

// Canvas size. 1200×675 is exact 16:9 and is what X and Reddit crop an
// OpenGraph large image to (docs/toktape-spec.ko.md §4, research §6.2).
const (
	// Width is the card's pixel width.
	Width = 1200
	// Height is the card's pixel height.
	Height = 675
)

// The panel and its content box.
const (
	panelInset  = 20 // canvas margin around the panel
	panelRadius = 18
	panelPad    = 40 // panel edge → content box

	contentL = panelInset + panelPad // 60
	contentR = Width - contentL      // 1140
	contentW = contentR - contentL   // 1080

	panelTop    = panelInset          // 20
	panelBottom = Height - panelInset // 655
)

// Band boundaries, top to bottom. The contract fixes the four band heights at
// 80 / 180 / 120 / 160; they are kept exactly, and the whitespace lives inside
// each band rather than between them. The bottom strip takes the remainder.
const (
	bandHeaderTop = 38
	bandHeroTop   = bandHeaderTop + 80  // 118
	bandMemTop    = bandHeroTop + 180   // 298
	bandFooterTop = bandMemTop + 120    // 418
	bandStripTop  = bandFooterTop + 160 // 578
	bandStripEnd  = bandStripTop + 44   // 622
)

// Hero row.
const (
	heroSplitX     = 600 // vertical hairline between the two hero columns
	heroGutter     = 36
	heroRightX     = heroSplitX + heroGutter // 636
	heroRuleTop    = bandHeroTop + 20
	heroRuleBottom = bandMemTop - 18

	heroEyebrowBase = bandHeroTop + 38  // 156
	heroNumberBase  = bandHeroTop + 116 // 234
	heroSub1Base    = bandHeroTop + 144 // 262
	heroSub2Base    = bandHeroTop + 166 // 284

	heroNumberSize = 64
	heroUnitSize   = 22
	heroUnitGap    = 12
)

// Memory row.
const (
	memLabelBase  = bandMemTop + 26 // 324
	memPillTop    = bandMemTop + 12 // 310
	memPillHeight = 26

	memBarTop    = bandMemTop + 50 // 348
	memBarHeight = 26
	memBarRadius = 6

	memLegendBase    = bandMemTop + 102 // 400
	memLegendSwatch  = 10
	memLegendGap     = 9  // swatch → label
	memLegendSpacing = 22 // entry → entry
)

// Footer grid.
const (
	footerCols     = 4
	footerColW     = 255
	footerColStep  = 275                // colW + 20 gutter; 4 columns + 3 gutters == contentW
	footerLabelBas = bandFooterTop + 36 // 454
	footerRuleY    = bandFooterTop + 46 // 464
	footerRow0Base = bandFooterTop + 72 // 490
	footerRowStep  = 22
	footerRows     = 4
)

// Bottom strip.
const (
	stripRuleY    = bandStripTop + 2  // 580
	stripFlagBase = bandStripTop + 26 // 604
	stripFootBase = bandStripTop + 46 // 624
)

// Type sizes. Everything is a multiple of the same small scale so the card
// reads as one system.
const (
	sizeWordmark = 30
	sizeVersion  = 13
	sizeTitle    = 16
	sizeBody     = 13
	sizeSmall    = 12
	sizeMicro    = 11.5
	sizeEyebrow  = 11
	sizeColLabel = 10.5
)

// Letter-spacing used to emulate small caps. Real small caps would need a face
// that has them; uppercasing plus tracking reads the same at these sizes.
const (
	trackEyebrow  = 1.6
	trackColLabel = 1.8
	trackRunID    = 0.8
)
