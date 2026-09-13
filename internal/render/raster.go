package render

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"

	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/font/sfnt"
	"golang.org/x/image/math/fixed"

	"github.com/midagedev/toktape/assets/fonts"
)

// rasteriser draws a parsed frame into an image.
//
// It is a fixed-pitch terminal: one cell is cw×ch pixels, every glyph is
// centred in its cell, and a two-column rune is centred across two. Nothing
// here reflows or measures a string — parseScreen already decided which rune
// sits in which cell, using the same width rule the TUI laid the frame out
// with, so the raster cannot disagree with the text.
//
// The faces are the PNG card's: JetBrains Mono for Latin and D2Coding for the
// Hangul the card's own note calls out (assets/fonts). The card's loader is
// unexported, so this is a second loader over the same embedded bytes — the
// font files themselves are not duplicated.
type rasteriser struct {
	size float64
	// cw and ch are one cell in pixels.
	cw, ch int
	// baseline is the glyph baseline's offset from the top of a cell.
	baseline int
	// stroke is the pen width of the geometric box and braille drawing.
	stroke int
	// pad is the margin of background around the frame.
	pad image.Point

	latin [2]font.Face // [0] regular, [1] bold
	// wide is D2Coding scaled so that a Hangul syllable advances exactly two
	// cells. The face's own Latin advance is 0.5 em against JetBrains Mono's
	// 0.6, so the two faces cannot share a size and still tile the grid.
	wide font.Face
}

// newRasteriser builds the faces for a cell size in pixels.
func newRasteriser(size float64) (*rasteriser, error) {
	if size < 6 {
		return nil, fmt.Errorf("render: cell size %.1fpx is too small to read", size)
	}
	latinSrc, err := sfnt.Parse(fonts.JetBrainsMonoRegular)
	if err != nil {
		return nil, fmt.Errorf("render: parse JetBrains Mono: %w", err)
	}
	boldSrc, err := sfnt.Parse(fonts.JetBrainsMonoBold)
	if err != nil {
		return nil, fmt.Errorf("render: parse JetBrains Mono Bold: %w", err)
	}
	wideSrc, err := sfnt.Parse(fonts.D2CodingRegular)
	if err != nil {
		return nil, fmt.Errorf("render: parse D2Coding: %w", err)
	}

	newFace := func(f *sfnt.Font, sz float64) (font.Face, error) {
		// DPI 72 makes one point one pixel, so size is a pixel height.
		return opentype.NewFace(f, &opentype.FaceOptions{Size: sz, DPI: 72, Hinting: font.HintingFull})
	}
	regular, err := newFace(latinSrc, size)
	if err != nil {
		return nil, fmt.Errorf("render: face %.1fpx: %w", size, err)
	}
	bold, err := newFace(boldSrc, size)
	if err != nil {
		return nil, fmt.Errorf("render: bold face %.1fpx: %w", size, err)
	}

	rs := &rasteriser{size: size, latin: [2]font.Face{regular, bold}}

	adv, ok := regular.GlyphAdvance('M')
	if !ok || adv <= 0 {
		return nil, fmt.Errorf("render: JetBrains Mono reports no advance for 'M'")
	}
	rs.cw = ceilFixed(adv)

	m := regular.Metrics()
	ascent, descent := m.Ascent.Ceil(), m.Descent.Ceil()
	rs.ch = m.Height.Ceil()
	if rs.ch < ascent+descent {
		rs.ch = ascent + descent
	}
	rs.baseline = ascent + (rs.ch-(ascent+descent))/2

	rs.stroke = (rs.cw + 4) / 8
	if rs.stroke < 1 {
		rs.stroke = 1
	}
	rs.pad = image.Point{X: 2 * rs.cw, Y: rs.ch}

	// Scale D2Coding so one Hangul syllable is exactly two cells wide.
	probe, err := newFace(wideSrc, size)
	if err != nil {
		return nil, fmt.Errorf("render: probe face %.1fpx: %w", size, err)
	}
	hangul, ok := probe.GlyphAdvance('가')
	_ = probe.Close()
	if !ok || hangul <= 0 {
		return nil, fmt.Errorf("render: D2Coding reports no advance for a Hangul syllable")
	}
	wide, err := newFace(wideSrc, size*float64(2*rs.cw)*64/float64(hangul))
	if err != nil {
		return nil, fmt.Errorf("render: wide face: %w", err)
	}
	rs.wide = wide
	return rs, nil
}

// Close releases the faces.
func (rs *rasteriser) Close() {
	for _, f := range rs.latin {
		if f != nil {
			_ = f.Close()
		}
	}
	if rs.wide != nil {
		_ = rs.wide.Close()
	}
}

// bounds is the image size for a w×h frame.
//
// Both dimensions are rounded up to an even number of pixels: H.264 with
// yuv420p chroma subsampling rejects an odd width or height, and discovering
// that from ffmpeg's stderr three hundred frames later is a poor trade against
// one spare column of background.
func (rs *rasteriser) bounds(w, h int) image.Rectangle {
	px := 2*rs.pad.X + w*rs.cw
	py := 2*rs.pad.Y + h*rs.ch
	return image.Rect(0, 0, px+px%2, py+py%2)
}

// cellRect is the pixel rectangle of the cell at (x, y), spanning span columns.
func (rs *rasteriser) cellRect(x, y, span int) image.Rectangle {
	x0 := rs.pad.X + x*rs.cw
	y0 := rs.pad.Y + y*rs.ch
	return image.Rect(x0, y0, x0+span*rs.cw, y0+rs.ch)
}

// draw rasterises one parsed frame.
func (rs *rasteriser) draw(sc screen) *image.RGBA {
	img := image.NewRGBA(rs.bounds(sc.w, sc.h))
	draw.Draw(img, img.Bounds(), image.NewUniform(bgColour), image.Point{}, draw.Src)
	rs.drawInto(img, sc)
	return img
}

// drawInto paints a frame over an existing image, which is how the GIF path
// reuses one buffer for every frame.
func (rs *rasteriser) drawInto(img *image.RGBA, sc screen) {
	draw.Draw(img, img.Bounds(), image.NewUniform(bgColour), image.Point{}, draw.Src)
	for y := 0; y < sc.h; y++ {
		for x := 0; x < sc.w; x++ {
			c := sc.at(x, y)
			if c.hasBG {
				fillRect(img, rs.cellRect(x, y, 1), c.bg)
			}
			if c.cont || c.r == 0 || c.r == ' ' {
				continue
			}
			span := 1
			if x+1 < sc.w && sc.at(x+1, y).cont {
				span = 2
			}
			cell := rs.cellRect(x, y, span)
			if rs.blockGlyph(img, cell, c.r, c.fg) {
				continue
			}
			rs.drawGlyph(img, cell, c.r, c.fg, c.bold)
		}
	}
}

// faceFor picks the face that has a glyph for r.
//
// Latin first, so digits and punctuation keep JetBrains Mono's tabular
// advance; anything it does not cover falls through to D2Coding. A rune
// neither face has comes back to the Latin face, which draws .notdef — a
// visible tofu box is a bug report, a silently dropped character is a wrong
// frame. (The one family that would have hit that path, braille, is drawn as
// geometry in glyph.go.)
func (rs *rasteriser) faceFor(r rune, bold bool) font.Face {
	f := rs.latin[0]
	if bold {
		f = rs.latin[1]
	}
	if _, ok := f.GlyphAdvance(r); ok {
		return f
	}
	if _, ok := rs.wide.GlyphAdvance(r); ok {
		return rs.wide
	}
	return f
}

// drawGlyph centres one glyph in its cell span.
func (rs *rasteriser) drawGlyph(dst *image.RGBA, cell image.Rectangle, r rune, col color.RGBA, bold bool) {
	f := rs.faceFor(r, bold)
	adv, ok := f.GlyphAdvance(r)
	if !ok {
		adv = fixed.I(rs.cw)
	}
	dx := (cell.Dx() - ceilFixed(adv)) / 2
	if dx < 0 {
		dx = 0
	}
	dot := fixed.P(cell.Min.X+dx, cell.Min.Y+rs.baseline)
	dr, mask, mp, _, ok := f.Glyph(dot, r)
	if !ok || mask == nil {
		return
	}
	draw.DrawMask(dst, dr, image.NewUniform(col), image.Point{}, mask, mp, draw.Over)
}

// hasGlyph reports whether r can be drawn at all: as geometry, or by one of
// the faces. The coverage test walks the example frames through this.
func (rs *rasteriser) hasGlyph(r rune) bool {
	switch {
	case r >= 0x2500 && r <= 0x259f, r >= 0x2800 && r <= 0x28ff:
		return true
	}
	if _, ok := rs.latin[0].GlyphAdvance(r); ok {
		return true
	}
	_, ok := rs.wide.GlyphAdvance(r)
	return ok
}

func ceilFixed(v fixed.Int26_6) int { return v.Ceil() }
