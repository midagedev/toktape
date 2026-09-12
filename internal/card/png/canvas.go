package png

import (
	"image"
	"image/color"
	"image/draw"
	"math"

	"golang.org/x/image/math/fixed"
	"golang.org/x/image/vector"
)

// mark records where the renderer put something.
//
// Nothing here can look at the finished image, and neither can the tests, so
// the canvas keeps a ledger of every rectangle it drew into. The tests assert
// on the ledger — that a run id never leaves the content box, that a footer
// cell never crosses into the next column — which catches the "silently drawn
// off the edge" class that a pixel probe alone would miss.
type mark struct {
	ID   string          // "hero.left.number", "footer.col2.row1", ...
	Kind string          // "text" | "rect" | "rule" | "pill" | "swatch"
	Rect image.Rectangle // advance box for text, fill box for shapes
	Text string          // the string actually drawn, after truncation
}

// canvas is an RGBA image plus the fonts and the ledger.
type canvas struct {
	img   *image.RGBA
	fonts *fontSet
	marks []mark
}

func newCanvas(fs *fontSet) *canvas {
	return &canvas{img: image.NewRGBA(image.Rect(0, 0, Width, Height)), fonts: fs}
}

func (c *canvas) record(id, kind string, r image.Rectangle, text string) image.Rectangle {
	c.marks = append(c.marks, mark{ID: id, Kind: kind, Rect: r, Text: text})
	return r
}

// markByID returns the first recorded mark with that id.
func (c *canvas) markByID(id string) (mark, bool) {
	for _, m := range c.marks {
		if m.ID == id {
			return m, true
		}
	}
	return mark{}, false
}

// ------------------------------------------------------------- primitives ---

// fill paints r with a solid colour, replacing whatever was there.
func (c *canvas) fill(r image.Rectangle, col color.RGBA) {
	draw.Draw(c.img, r, image.NewUniform(col), image.Point{}, draw.Src)
}

// over composites a (possibly translucent) colour onto r.
func (c *canvas) over(r image.Rectangle, col color.RGBA) {
	draw.Draw(c.img, r, image.NewUniform(col), image.Point{}, draw.Over)
}

// hairline draws a 1px horizontal rule.
//
// Rules go through draw.Draw on an integer rectangle rather than through the
// rasteriser: an antialiased 1px line spreads its ink over two rows and reads
// as a smudge at this scale.
func (c *canvas) hairline(id string, x0, x1, y int, col color.RGBA) image.Rectangle {
	r := image.Rect(x0, y, x1, y+1)
	c.over(r, col)
	return c.record(id, "rule", r, "")
}

// vrule draws a 1px vertical rule.
func (c *canvas) vrule(id string, x, y0, y1 int, col color.RGBA) image.Rectangle {
	r := image.Rect(x, y0, x+1, y1)
	c.over(r, col)
	return c.record(id, "rule", r, "")
}

// roundRectMask rasterises a rounded rectangle into a coverage mask the size
// of r. Corners are cubic approximations of a quarter circle.
func roundRectMask(r image.Rectangle, radius float32) *image.Alpha {
	w, h := float32(r.Dx()), float32(r.Dy())
	if radius > w/2 {
		radius = w / 2
	}
	if radius > h/2 {
		radius = h / 2
	}
	const kappa = 0.5522847498 // circle → cubic Bézier constant
	k := radius * kappa

	z := vector.NewRasterizer(r.Dx(), r.Dy())
	z.MoveTo(radius, 0)
	z.LineTo(w-radius, 0)
	z.CubeTo(w-radius+k, 0, w, radius-k, w, radius)
	z.LineTo(w, h-radius)
	z.CubeTo(w, h-radius+k, w-radius+k, h, w-radius, h)
	z.LineTo(radius, h)
	z.CubeTo(radius-k, h, 0, h-radius+k, 0, h-radius)
	z.LineTo(0, radius)
	z.CubeTo(0, radius-k, radius-k, 0, radius, 0)
	z.ClosePath()

	m := image.NewAlpha(image.Rect(0, 0, r.Dx(), r.Dy()))
	z.Draw(m, m.Bounds(), image.NewUniform(color.Alpha{A: 0xff}), image.Point{})
	return m
}

// roundRect fills a rounded rectangle with a solid colour.
func (c *canvas) roundRect(id string, r image.Rectangle, radius float32, col color.RGBA) image.Rectangle {
	m := roundRectMask(r, radius)
	draw.DrawMask(c.img, r, image.NewUniform(col), image.Point{}, m, image.Point{}, draw.Over)
	return c.record(id, "rect", r, "")
}

// maskedFill paints part of an already-rasterised mask. sub must lie inside r,
// the rectangle the mask was built for. This is how the placement bar gets
// rounded outer ends and square internal divisions from one shape.
func (c *canvas) maskedFill(id string, r, sub image.Rectangle, m *image.Alpha, col color.RGBA) image.Rectangle {
	sub = sub.Intersect(r)
	if sub.Empty() {
		return c.record(id, "rect", sub, "")
	}
	draw.DrawMask(c.img, sub, image.NewUniform(col), image.Point{}, m, sub.Min.Sub(r.Min), draw.Over)
	return c.record(id, "rect", sub, "")
}

// ------------------------------------------------------------- gradients ---

// hGradient is a horizontal linear ramp addressed in canvas coordinates. It is
// the card's single gradient: cyan → blue across the decode number, the one
// place the spec allows an accent ramp.
type hGradient struct {
	x0, x1 int
	c0, c1 color.RGBA
}

func (g hGradient) ColorModel() color.Model { return color.RGBAModel }

// Bounds is deliberately unbounded-ish: draw.DrawMask clips the source to
// these bounds, and the gradient must cover whatever glyph box it is asked for.
func (g hGradient) Bounds() image.Rectangle {
	return image.Rect(math.MinInt32/2, math.MinInt32/2, math.MaxInt32/2, math.MaxInt32/2)
}

func (g hGradient) At(x, _ int) color.Color {
	span := g.x1 - g.x0
	if span <= 0 {
		return g.c0
	}
	t := float64(x-g.x0) / float64(span)
	if t < 0 {
		t = 0
	}
	if t > 1 {
		t = 1
	}
	lerp := func(a, b uint8) uint8 { return uint8(float64(a) + (float64(b)-float64(a))*t + 0.5) }
	return color.RGBA{R: lerp(g.c0.R, g.c1.R), G: lerp(g.c0.G, g.c1.G), B: lerp(g.c0.B, g.c1.B), A: 0xff}
}

// ------------------------------------------------------------------ text ---

type align int

const (
	alignLeft align = iota
	alignRight
)

// textStyle is one typographic voice on the card.
type textStyle struct {
	size     float64
	weight   weight
	tracking float64 // extra pixels between runes; small-caps emulation
	upper    bool    // uppercase before drawing
}

var (
	stWordmark = textStyle{size: sizeWordmark, weight: wExtraBold}
	stVersion  = textStyle{size: sizeVersion, weight: wRegular}
	stRunID    = textStyle{size: sizeMicro, weight: wRegular, tracking: trackRunID}
	stTitle    = textStyle{size: sizeTitle, weight: wBold}
	stEyebrow  = textStyle{size: sizeEyebrow, weight: wBold, tracking: trackEyebrow, upper: true}
	// stMeta is the eyebrow's voice without the case transform, for strings
	// whose own casing carries meaning: a quant sub-type is "UD-Q4_K_M" and a
	// size is "19.83 GiB", never "19.83 GIB".
	stMeta     = textStyle{size: sizeEyebrow, weight: wRegular, tracking: 0.5}
	stHero     = textStyle{size: heroNumberSize, weight: wExtraBold}
	stHeroUnit = textStyle{size: heroUnitSize, weight: wBold}
	stBody     = textStyle{size: sizeBody, weight: wRegular}
	stSmall    = textStyle{size: sizeSmall, weight: wRegular}
	stSmallB   = textStyle{size: sizeSmall, weight: wBold}
	stMicro    = textStyle{size: sizeMicro, weight: wRegular}
	stPill     = textStyle{size: sizeEyebrow, weight: wBold}
	stColLabel = textStyle{size: sizeColLabel, weight: wBold, tracking: trackColLabel, upper: true}
)

// prepare applies the style's case transform.
func (st textStyle) prepare(s string) string {
	if !st.upper {
		return s
	}
	return upper(s)
}

// measure returns the advance width in pixels of s in this style.
func (c *canvas) measure(s string, st textStyle) int {
	f, err := c.fonts.face(st.size, st.weight)
	if err != nil {
		return 0
	}
	return measureWith(f, st.prepare(s), st.tracking)
}

func measureWith(f *faceSet, s string, tracking float64) int {
	var total fixed.Int26_6
	n := 0
	for _, r := range s {
		adv, ok := f.faceFor(r).GlyphAdvance(r)
		if !ok {
			adv, _ = f.latin.GlyphAdvance(' ')
		}
		total += adv
		n++
	}
	w := total.Ceil()
	if n > 1 && tracking != 0 {
		w += int(math.Round(tracking * float64(n-1)))
	}
	return w
}

// fit shortens s until it measures at most maxW, appending "…" when anything
// was cut. maxW <= 0 means no limit.
func (c *canvas) fit(s string, st textStyle, maxW int) string {
	if maxW <= 0 {
		return s
	}
	s = st.prepare(s)
	if c.measureRaw(s, st) <= maxW {
		return s
	}
	rs := []rune(s)
	for len(rs) > 0 {
		rs = rs[:len(rs)-1]
		cand := string(rs) + ellipsis
		if c.measureRaw(cand, st) <= maxW {
			return cand
		}
	}
	return ""
}

// measureRaw measures without re-applying the case transform, so fit can call
// it on already-prepared text.
func (c *canvas) measureRaw(s string, st textStyle) int {
	f, err := c.fonts.face(st.size, st.weight)
	if err != nil {
		return 0
	}
	return measureWith(f, s, st.tracking)
}

// ellipsis marks a truncation. One rune, present in both faces.
const ellipsis = "…"

// textOpts is one call to draw a string.
type textOpts struct {
	id       string
	s        string
	x        int // left edge, or right edge when align is alignRight
	baseline int
	style    textStyle
	src      image.Image // usually image.Uniform; hGradient for the hero number
	align    align
	maxW     int // 0 = no truncation
}

// text draws a run of text and records its advance box.
//
// One rune loop does everything: per-rune face fallback, tracking, and an
// arbitrary source image. font.Drawer is not used because it re-anchors the
// source at every glyph, which would restart the hero gradient on each digit.
func (c *canvas) text(o textOpts) image.Rectangle {
	f, err := c.fonts.face(o.style.size, o.style.weight)
	if err != nil {
		return image.Rectangle{}
	}
	s := c.fit(o.s, o.style, o.maxW)
	if o.maxW <= 0 {
		s = o.style.prepare(s)
	}
	w := measureWith(f, s, o.style.tracking)

	x := o.x
	if o.align == alignRight {
		x = o.x - w
	}

	ascent, descent := f.metrics()
	box := image.Rect(x, o.baseline-ascent, x+w, o.baseline+descent)
	if s == "" {
		return c.record(o.id, "text", box, "")
	}

	dot := fixed.Point26_6{X: fixed.I(x), Y: fixed.I(o.baseline)}
	track := fixed.Int26_6(math.Round(o.style.tracking * 64))
	for _, r := range s {
		face := f.faceFor(r)
		dr, gmask, gp, adv, ok := face.Glyph(dot, r)
		if ok && gmask != nil {
			draw.DrawMask(c.img, dr, o.src, dr.Min, gmask, gp, draw.Over)
		}
		if !ok {
			adv, _ = f.latin.GlyphAdvance(' ')
		}
		dot.X += adv + track
	}
	return c.record(o.id, "text", box, s)
}

// solid wraps a colour as a draw source.
func solid(c color.RGBA) image.Image { return image.NewUniform(c) }
