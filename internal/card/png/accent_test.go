package png

import (
	"image"
	"image/color"
	"testing"

	"github.com/midagedev/toktape/internal/card"
	"github.com/midagedev/toktape/internal/palette"
)

// TestDecodeIsTheOneAccentFigure pins the emphasis contract on the share card
// (TTP-44, 2026-09-13): the decode hero number is painted in the accent ramp,
// Accent → AccentHigh, and the prefill hero number is painted in Text, never
// the accent. It reads the painted pixels inside each number's advance box: a
// fully covered glyph pixel carries its source colour exactly, so counting
// exact matches says which source painted the figure without judging the
// image by eye.
func TestDecodeIsTheOneAccentFigure(t *testing.T) {
	c, err := renderCanvas(card.Example())
	if err != nil {
		t.Fatalf("renderCanvas: %v", err)
	}
	accent, high, text := palette.RGBA(palette.Accent), palette.RGBA(palette.AccentHigh), palette.RGBA(palette.Text)

	// A glyph stroke at the hero size covers thousands of pixels; a few
	// hundred exact matches is far above anything antialiasing could produce
	// by chance.
	const minPixels = 200

	left, ok := c.markByID("hero.left.number")
	if !ok {
		t.Fatal("hero.left.number was never drawn")
	}
	ramp := hGradient{x0: left.Rect.Min.X, x1: left.Rect.Max.X, c0: accent, c1: high}
	if n := countPixels(c.img, left.Rect, func(x, _ int, p color.RGBA) bool { return p == ramp.At(x, 0) }); n < minPixels {
		t.Errorf("decode number: %d pixels on the Accent→AccentHigh ramp, want ≥ %d", n, minPixels)
	}

	right, ok := c.markByID("hero.right.number")
	if !ok {
		t.Fatal("hero.right.number was never drawn")
	}
	if n := countPixels(c.img, right.Rect, func(_, _ int, p color.RGBA) bool { return p == text }); n < minPixels {
		t.Errorf("prefill number: %d pixels in Text, want ≥ %d", n, minPixels)
	}
	rampR := hGradient{x0: right.Rect.Min.X, x1: right.Rect.Max.X, c0: accent, c1: high}
	if n := countPixels(c.img, right.Rect, func(x, _ int, p color.RGBA) bool {
		return p == accent || p == high || p == rampR.At(x, 0)
	}); n > 0 {
		t.Errorf("prefill number: %d pixels in the accent, want 0", n)
	}
}

func countPixels(img *image.RGBA, r image.Rectangle, match func(x, y int, p color.RGBA) bool) int {
	n := 0
	r = r.Intersect(img.Bounds())
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			if match(x, y, img.RGBAAt(x, y)) {
				n++
			}
		}
	}
	return n
}
