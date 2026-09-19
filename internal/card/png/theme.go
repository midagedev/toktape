package png

import (
	"image/color"

	"github.com/midagedev/toktape/internal/palette"
)

// ---------------------------------------------------------------- palette ---

// The card is drawn in oxide, the TUI's palette, so a posted card and the clip
// beside it read as one product (TTP-44, 2026-09-13; it was Catppuccin Mocha
// until then). Every value comes from internal/palette, the one owner; the
// names here are the card's roles, never hues.
//
// The rules are the TUI's emphasis contract. One accent family (Accent →
// AccentHigh) carries the decode rate, the card's one lit figure, and the
// wordmark; the prefill figure beside it is Text. The GPU segments are the
// accent hue at four lightnesses, the host segment is a desaturated sand that
// is warm without reading as a warning, Warn marks a caution, Bad marks a run
// that is contended, cold or cut, and a pill that reports nothing wrong is a
// neutral. Nothing on the card may introduce another hue.
var (
	colBase    = palette.RGBA(palette.CardBase)    // canvas behind the panel
	colPanel   = palette.RGBA(palette.CardPanel)   // the card panel
	colBorder  = palette.RGBA(palette.CardBorder)  // 1px panel border and every hairline rule
	colSurface = palette.RGBA(palette.CardSurface) // inert fill: bar track, never-loaded segment

	colText  = palette.RGBA(palette.Text)      // primary text, the prefill figure
	colDim   = palette.RGBA(palette.TextMuted) // secondary text, labels, a clean pill
	colFaint = palette.RGBA(palette.Dim)       // tertiary text: run id, credits, unobserved

	colAccent     = palette.RGBA(palette.Accent)     // decode ramp start, wordmark, first GPU
	colAccentHigh = palette.RGBA(palette.AccentHigh) // decode ramp end
	colHost       = palette.RGBA(palette.CardHost)   // host memory segment
	colWarn       = palette.RGBA(palette.Warn)       // a caution pill
	colBad        = palette.RGBA(palette.Bad)        // contended / cold / cut pills
)

// gpuColors is the rotation used for GPU segments of the placement bar, in
// device-index order. Beyond four devices it repeats. The four are one hue at
// lightnesses far enough apart that adjacent segments stay distinct
// (palette_test pins the ratios).
var gpuColors = []color.RGBA{
	colAccent,
	palette.RGBA(palette.CardGPU1),
	palette.RGBA(palette.CardGPU2),
	palette.RGBA(palette.CardGPU3),
}

// Lightness steps used to subdivide a GPU segment of the placement bar.
// Weights keep the device hue; the KV cache and the compute buffers are the
// same hue mixed toward the panel, so the three read as one device rather
// than three.
const (
	shadeWeights = 0.0
	shadeKV      = 0.36
	shadeCompute = 0.60
)

// Lightness steps used to subdivide the host segment into what is in RAM and
// what is not (TTP-63, 2026-09-14, user: "cpu 388g 찍혀있는데 이게 ram이랑
// nvme랑 구분이 안되나?"). The rule is the GPU segments': one hue, two
// lightnesses, so the pair reads as one device rather than two. The paged step
// is mixed further toward the panel than any GPU step, which is the point —
// those bytes are the part of the placement that is not there, and they should
// look like the bar fading into its own background.
const (
	shadeResident = 0.0
	shadePaged    = 0.48
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

// Band boundaries, top to bottom. The contract fixed the four band heights at
// 80 / 180 / 120 / 160 and the whitespace lives inside each band rather than
// between them. The first three are still exact; the identity band is 146
// rather than 160 (TTP-54, 2026-09-14 gave the footer that height, and the
// identity band kept it, 2026-09-19). Nothing above y=418 moved.
const (
	bandHeaderTop = 38
	bandHeroTop   = bandHeaderTop + 80 // 118
	bandMemTop    = bandHeroTop + 180  // 298
	bandIdentTop  = bandMemTop + 120   // 418 — was bandFooterTop until 2026-09-19
	bandStripTop  = bandIdentTop + 146 // 564
	bandStripEnd  = bandStripTop + 76  // 640
)

// Hero row.
//
// 2026-09-15: heroNumberSize grew 64 → 96. The number's digits carry no
// descenders, so what has to fit is ink, not the face's ascent box: at 96 px
// the digits' ink is 71 px tall (measured on the hinted face), and with the
// baseline at bandHeroTop+121 the ink spans 168..240 — 12 px under the
// eyebrow's ink and 12 px over the first sub-line's, splitting the air the
// 64 px number used to take 31/17 the other way. The two sub-lines did not
// move: their 22 px spacing and their 9 px clearance off the band's end were
// already the tightest the band allows, and the number's growth is paid for
// entirely out of the air above and below it. The widest real figure still
// fits the column: "11234" measures 290 px, plus the 12 px unit gap and the
// 65 px "tok/s", 367 of the column's 504.
const (
	heroSplitX     = 600 // vertical hairline between the two hero columns
	heroGutter     = 36
	heroRightX     = heroSplitX + heroGutter // 636
	heroRuleTop    = bandHeroTop + 20
	heroRuleBottom = bandMemTop - 18

	heroEyebrowBase = bandHeroTop + 38  // 156
	heroNumberBase  = bandHeroTop + 121 // 239
	heroSub1Base    = bandHeroTop + 144 // 262
	heroSub2Base    = bandHeroTop + 166 // 284

	heroNumberSize = 96
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

// Identity band (2026-09-19): three lines at 25 px — the model, the rig, the
// engine — replacing the footer grid's twelve 14 px rows. A timeline shows
// this card at about 600 px wide, and at that size the grid was grey texture:
// the conditions a rate needs in order to mean anything have to be as legible
// as the figure, or the card is an unqualified number. The twelve rows, the
// flags line and the environment line are still in the tape, on the run page,
// in -o md and in -o json — nothing is lost, it stops being printed here.
const (
	identLine1Base = bandIdentTop + 38  // 456 — model
	identSubBase   = bandIdentTop + 66  // 484 — the model's detail
	identLine2Base = bandIdentTop + 110 // 528 — rig
	identLine3Base = bandIdentTop + 154 // 572 — engine
)

// Bottom strip. One line now: the provenance pair. The rule moved down to 600
// on 2026-09-19 with the flags and environment lines gone; the credit line
// did not move.
const (
	stripRuleY    = 600
	stripFootBase = bandStripTop + 68 // 632
)

// Type sizes. Everything is a multiple of the same small scale so the card
// reads as one system.
//
// The body sizes were raised about a quarter on 2026-09-14 (TTP-54, user:
// "png카드가 너무 트위터에서 보기에 글씨가 작아"). A timeline crops this card
// to roughly half its pixel width, so the old 13 px body arrived as 6.5 px and
// the 10.5 px column labels as 5.2 px — legible only if you opened the image.
// The hero number and its unit did not move: they were already the one thing
// that read at feed size.
//
// sizeBody is 15 rather than the 16 the round asked for. It is used by the two
// hero sub-lines and nothing else, and the hero columns are 504 px wide. At
// 16 px the speculative draft clause — "draft DSpark-0.6B-Q8_0.gguf · n_max 3
// · 60% accepted", the string TestDraftClauseReplacesTheSecondDecodeLine pins
// whole — measures 520 px and is cut. Buying those pixels means moving the
// hero's centre rule right, and the rule is also the right edge of the header's
// model name, which measures 517 px against 540 on the ws tapes: the move
// would cut the model name to widen the clause under it. 15 px leaves the
// widest real sub-line (the sweep's, 486 px) 18 px of slack and moves no
// geometry at all.
const (
	sizeWordmark = 30
	sizeVersion  = 13
	sizeTitle    = 19
	sizeBody     = 15
	sizeSmall    = 14
	sizeMicro    = 13
	sizeEyebrow  = 12.5
	sizeColLabel = 12

	// The identity band's sizes (2026-09-19; 26 → 25 the same day, by
	// measurement). sizeIdent exists because a timeline crops this card to
	// roughly half its pixel width: the twelve 14 px footer rows it replaced
	// arrived at feed size as 7 px texture, and the conditions of a rate have
	// to be as legible as the figure. The advance is not the 0.6 em the
	// first cut assumed: measured on the bold face stIdent draws with, where
	// HintingFull rounds every advance to a whole pixel, it is 15 px/char at
	// 25 px (16 at 26), so a 25 px line holds 72 of the 1080 px content
	// width. 25 rather than 26 because the rig line's preferred spelling has
	// to fit the one-GPU fixtures whole, and at 26 even the sharded example's
	// fell to a fallback; the two-GPU ws rig overflows either way and its
	// chain carries the rest (rigIdent). sizeIdentSub is the detail line
	// under the model. It was 17 until 2026-09-19: at 17 it landed at about
	// 8.5 px once a timeline halved the card, which is the texture this band
	// was drawn to remove — one row of it instead of fourteen is still a row
	// nobody reads. 19 is the largest that keeps the longest fixture's line
	// whole (exl3's arch is the HF class name, and 20 truncates it), and the
	// 28 px pitch to the model baseline absorbs the two pixels.
	sizeIdent    = 25
	sizeIdentSub = 19
)

// Letter-spacing used to emulate small caps. Real small caps would need a face
// that has them; uppercasing plus tracking reads the same at these sizes.
const (
	trackEyebrow  = 1.6
	trackColLabel = 1.8
	trackRunID    = 0.8
)
