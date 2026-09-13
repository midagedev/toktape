package render

import (
	"image"
	"image/color"
	"image/draw"
)

// Three rune families are drawn as geometry rather than as glyphs.
//
// A terminal draws U+2500's box set and U+2580's block set to the cell box, so
// a column of "│" is one unbroken rule and a row of "█" is one unbroken bar.
// A font does not: JetBrains Mono's line height here is 27 px and its box
// glyphs are 20, which turns the TUI's border into a dashed line and its bars
// into a row of floating tiles. Drawing them as rectangles at cell size is not
// a stylistic liberty — it is what makes the raster match the terminal.
//
// Braille is the other reason: neither embedded face has U+2800–U+28FF at all,
// so the prefill spinner would rasterise as a tofu box. It is drawn as the dot
// grid it is.
//
// Everything else — letters, digits, punctuation, "·", "✓", "≈", Hangul —
// goes through the faces, where the type designer's answer beats ours.

// blockGlyph reports whether r is drawn geometrically, and draws it into the
// cell rectangle if so.
func (rs *rasteriser) blockGlyph(dst *image.RGBA, cellR image.Rectangle, r rune, col color.RGBA) bool {
	switch {
	case r >= 0x2500 && r <= 0x257f:
		return rs.drawBox(dst, cellR, r, col)
	case r >= 0x2580 && r <= 0x259f:
		return rs.drawBlock(dst, cellR, r, col)
	case r >= 0x2800 && r <= 0x28ff:
		rs.drawBraille(dst, cellR, r, col)
		return true
	}
	return false
}

// fillRect paints an integer rectangle, clipped to the cell.
func fillRect(dst *image.RGBA, r image.Rectangle, col color.RGBA) {
	if r.Empty() {
		return
	}
	draw.Draw(dst, r, image.NewUniform(col), image.Point{}, draw.Over)
}

// blendRect paints a rectangle at fractional coverage, which is how the three
// shade blocks (░▒▓) are drawn.
func blendRect(dst *image.RGBA, r image.Rectangle, col color.RGBA, alpha float64) {
	if r.Empty() || alpha <= 0 {
		return
	}
	a := uint8(alpha*255 + 0.5)
	// Premultiplied: draw.Over expects the source scaled by its own alpha.
	c := color.RGBA{
		R: uint8(float64(col.R) * alpha),
		G: uint8(float64(col.G) * alpha),
		B: uint8(float64(col.B) * alpha),
		A: a,
	}
	draw.Draw(dst, r, image.NewUniform(c), image.Point{}, draw.Over)
}

// drawBox draws one rune of the box-drawing block as a stroke of the cell's
// centre lines. Only the single-width forms the TUI uses are implemented; the
// rounded corners map to the square ones, which are indistinguishable at a
// cell this size. An unimplemented form returns false and falls back to the
// font, where the double and heavy strokes do exist.
func (rs *rasteriser) drawBox(dst *image.RGBA, c image.Rectangle, r rune, col color.RGBA) bool {
	lw := rs.stroke
	// The centre lines. x0/y0 are the near edge of the stroke, x1/y1 the far
	// one, so a junction's arms start and stop flush with the stroke.
	x0 := c.Min.X + (c.Dx()-lw)/2
	x1 := x0 + lw
	y0 := c.Min.Y + (c.Dy()-lw)/2
	y1 := y0 + lw

	// The four arms of a junction, each from the cell edge to the far side of
	// the centre stroke.
	left := image.Rect(c.Min.X, y0, x1, y1)
	right := image.Rect(x0, y0, c.Max.X, y1)
	up := image.Rect(x0, c.Min.Y, x1, y1)
	down := image.Rect(x0, y0, x1, c.Max.Y)
	horiz := image.Rect(c.Min.X, y0, c.Max.X, y1)
	vert := image.Rect(x0, c.Min.Y, x1, c.Max.Y)

	var parts []image.Rectangle
	switch r {
	case '─':
		parts = []image.Rectangle{horiz}
	case '│':
		parts = []image.Rectangle{vert}
	case '┌', '╭':
		parts = []image.Rectangle{right, down}
	case '┐', '╮':
		parts = []image.Rectangle{left, down}
	case '└', '╰':
		parts = []image.Rectangle{right, up}
	case '┘', '╯':
		parts = []image.Rectangle{left, up}
	case '├':
		parts = []image.Rectangle{vert, right}
	case '┤':
		parts = []image.Rectangle{vert, left}
	case '┬':
		parts = []image.Rectangle{horiz, down}
	case '┴':
		parts = []image.Rectangle{horiz, up}
	case '┼':
		parts = []image.Rectangle{horiz, vert}
	default:
		return false
	}
	for _, p := range parts {
		fillRect(dst, p, col)
	}
	return true
}

// drawBlock draws one rune of the block-elements range.
//
// The two runs go in opposite directions and are easy to transpose:
// U+2581–U+2587 are the LOWER one-eighth through seven-eighths, growing
// upward from the bottom edge; U+2589–U+258F are the LEFT seven-eighths down
// to one-eighth, shrinking from the left edge. U+2580 is the upper half and
// U+2588 the full block.
func (rs *rasteriser) drawBlock(dst *image.RGBA, c image.Rectangle, r rune, col color.RGBA) bool {
	w, h := c.Dx(), c.Dy()
	// eighths rounds to whole pixels so that two adjacent cells of the same
	// level agree on where the edge is.
	eighth := func(total, n int) int { return (total*n + 4) / 8 }

	switch {
	case r == '▀': // upper half
		fillRect(dst, image.Rect(c.Min.X, c.Min.Y, c.Max.X, c.Min.Y+h/2), col)
	case r >= '▁' && r <= '▇': // lower 1/8 … 7/8
		n := int(r - '▀') // 1..7
		fillRect(dst, image.Rect(c.Min.X, c.Max.Y-eighth(h, n), c.Max.X, c.Max.Y), col)
	case r == '█':
		fillRect(dst, c, col)
	case r >= '▉' && r <= '▏': // left 7/8 … 1/8
		n := 8 - int(r-'█') // ▉ → 7 … ▏ → 1
		fillRect(dst, image.Rect(c.Min.X, c.Min.Y, c.Min.X+eighth(w, n), c.Max.Y), col)
	case r == '▐': // right half
		fillRect(dst, image.Rect(c.Max.X-w/2, c.Min.Y, c.Max.X, c.Max.Y), col)
	case r == '░':
		blendRect(dst, c, col, 0.25)
	case r == '▒':
		blendRect(dst, c, col, 0.5)
	case r == '▓':
		blendRect(dst, c, col, 0.75)
	default:
		return false
	}
	return true
}

// brailleDots maps a braille bit to its (column, row) in the 2×4 grid. Bits
// 0-2 are the left column's top three rows, 3-5 the right column's, and bits
// 6 and 7 are the fourth row — the order the Unicode block was assigned in,
// not the order a reader would guess.
var brailleDots = [8][2]int{
	{0, 0}, {0, 1}, {0, 2},
	{1, 0}, {1, 1}, {1, 2},
	{0, 3}, {1, 3},
}

// drawBraille draws a braille pattern as its dots. The dots sit on a 2×4 grid
// inset from the cell edges, so a spinner does not touch the text beside it.
func (rs *rasteriser) drawBraille(dst *image.RGBA, c image.Rectangle, r rune, col color.RGBA) {
	bits := uint8(r - 0x2800)
	dot := rs.stroke + 1
	if dot < 2 {
		dot = 2
	}
	insetX := (c.Dx() - 2*dot) / 3
	insetY := (c.Dy() - 4*dot) / 5
	if insetX < 0 {
		insetX = 0
	}
	if insetY < 0 {
		insetY = 0
	}
	stepX := dot + insetX
	stepY := dot + insetY
	originX := c.Min.X + (c.Dx()-(2*stepX-insetX))/2
	originY := c.Min.Y + (c.Dy()-(4*stepY-insetY))/2

	for i, pos := range brailleDots {
		if bits&(1<<uint(i)) == 0 {
			continue
		}
		x := originX + pos[0]*stepX
		y := originY + pos[1]*stepY
		fillRect(dst, image.Rect(x, y, x+dot, y+dot), col)
	}
}
