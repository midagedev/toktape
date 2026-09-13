package render

import (
	"image"
	"image/color"
	"sort"
)

// MaxGIFColours is the size of the GIF palette.
//
// Sixty-four, not the ninety-six the track spec allows, for a mechanical
// reason: image/gif pads a colour table to a power of two, so a 96-entry
// palette is written — and decoded — as 128 entries, and "≤ 96" becomes a
// claim nobody can check from the file. Sixty-four is exact both ways and is
// already generous: a frame holds one background, eleven foregrounds and the
// antialiasing fringes between them.
const MaxGIFColours = 64

// quantise builds a palette for a set of frames and a mapper onto it.
//
// One palette for the whole clip, not one per frame. A per-frame palette would
// track each frame's colours more tightly and would also make the GIF flicker
// as the table shifted under a static background, and would cost a local
// colour table in every frame.
type quantiser struct {
	// palette is what a pixel is mapped onto: opaque colours only.
	palette color.Palette
	// out is the palette the produced images carry. The GIF path sets it to
	// palette plus a transparent entry, which pixels are never mapped to but
	// which the encoder needs to see in the table. Nil means palette itself.
	out   color.Palette
	cache map[color.RGBA]uint8
}

// imagePalette is the table the produced images carry.
func (q *quantiser) imagePalette() color.Palette {
	if q.out != nil {
		return q.out
	}
	return q.palette
}

// histogram counts the colours of an image.
func histogram(img *image.RGBA, into map[color.RGBA]int) {
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		row := img.Pix[img.PixOffset(b.Min.X, y):img.PixOffset(b.Max.X, y)]
		for x := 0; x+3 < len(row); x += 4 {
			into[color.RGBA{R: row[x], G: row[x+1], B: row[x+2], A: 0xff}]++
		}
	}
}

// newQuantiser reduces a colour histogram to at most MaxGIFColours entries by
// median cut.
func newQuantiser(hist map[color.RGBA]int, max int) *quantiser {
	if max < 2 {
		max = 2
	}
	if max > 256 {
		max = 256
	}
	return &quantiser{palette: medianCut(hist, max), cache: make(map[color.RGBA]uint8, 4096)}
}

// index returns the palette entry nearest to c, memoised. A frame is a million
// pixels drawn from a few thousand distinct colours, so the cache turns the
// per-pixel search into a map lookup.
func (q *quantiser) index(c color.RGBA) uint8 {
	if i, ok := q.cache[c]; ok {
		return i
	}
	best, bestD := 0, 1<<62
	for i, p := range q.palette {
		pr, pg, pb, _ := p.RGBA()
		dr := int(c.R) - int(pr>>8)
		dg := int(c.G) - int(pg>>8)
		db := int(c.B) - int(pb>>8)
		// Squared distance in plain RGB. The palette is built from the same
		// pixels it maps, so the cells are small and a perceptual metric
		// would move no pixel to a different entry.
		if d := dr*dr + dg*dg + db*db; d < bestD {
			best, bestD = i, d
		}
	}
	q.cache[c] = uint8(best)
	return uint8(best)
}

// paletted maps an image onto the palette.
func (q *quantiser) paletted(img *image.RGBA) *image.Paletted {
	b := img.Bounds()
	out := image.NewPaletted(b, q.imagePalette())
	for y := b.Min.Y; y < b.Max.Y; y++ {
		row := img.Pix[img.PixOffset(b.Min.X, y):img.PixOffset(b.Max.X, y)]
		dst := out.Pix[out.PixOffset(b.Min.X, y) : out.PixOffset(b.Min.X, y)+b.Dx()]
		for x := 0; x+3 < len(row); x += 4 {
			dst[x/4] = q.index(color.RGBA{R: row[x], G: row[x+1], B: row[x+2], A: 0xff})
		}
	}
	return out
}

// entry is one distinct colour and how many pixels wore it.
type entry struct {
	c color.RGBA
	n int
}

// box is a region of colour space holding a slice of the entry list.
type box struct {
	items []entry
	// n is the pixel count in the box, which is what decides who splits next.
	n int
	// lo and hi are the box's extent per channel, kept so the longest axis is
	// found once per split rather than per comparison.
	lo, hi [3]uint8
}

// medianCut reduces a histogram to at most max colours.
//
// The classic algorithm: start with every colour in one box, repeatedly split
// the box holding the most pixels along its longest channel at the population
// median, and finish by averaging each box. Splitting by population rather
// than by box count is what keeps the background and the accent — two colours
// that between them own most of the frame — as entries of their own instead of
// being averaged with the fringes around them.
//
// The result is deterministic: the entry list is sorted before the first split
// and every tie is broken by colour order, never by map iteration.
func medianCut(hist map[color.RGBA]int, max int) color.Palette {
	items := make([]entry, 0, len(hist))
	for c, n := range hist {
		items = append(items, entry{c: c, n: n})
	}
	sort.Slice(items, func(i, j int) bool { return lessColour(items[i].c, items[j].c) })

	if len(items) == 0 {
		return color.Palette{bgColour}
	}
	if len(items) <= max {
		out := make(color.Palette, len(items))
		for i, it := range items {
			out[i] = it.c
		}
		return out
	}

	boxes := []*box{newBox(items)}
	for len(boxes) < max {
		i := splittable(boxes)
		if i < 0 {
			break
		}
		a, b := boxes[i].split()
		if a == nil || b == nil {
			break
		}
		boxes[i] = a
		boxes = append(boxes, b)
	}

	out := make(color.Palette, 0, len(boxes))
	for _, bx := range boxes {
		out = append(out, bx.mean())
	}
	sort.Slice(out, func(i, j int) bool {
		return lessColour(out[i].(color.RGBA), out[j].(color.RGBA))
	})
	return out
}

func lessColour(a, b color.RGBA) bool {
	if a.R != b.R {
		return a.R < b.R
	}
	if a.G != b.G {
		return a.G < b.G
	}
	return a.B < b.B
}

func newBox(items []entry) *box {
	bx := &box{items: items, lo: [3]uint8{255, 255, 255}}
	for _, it := range items {
		bx.n += it.n
		for ch, v := range [3]uint8{it.c.R, it.c.G, it.c.B} {
			if v < bx.lo[ch] {
				bx.lo[ch] = v
			}
			if v > bx.hi[ch] {
				bx.hi[ch] = v
			}
		}
	}
	return bx
}

// splittable returns the index of the box that should be split next: the one
// holding the most pixels among those that can still be divided.
func splittable(boxes []*box) int {
	best, bestN := -1, -1
	for i, bx := range boxes {
		if len(bx.items) < 2 {
			continue
		}
		if bx.n > bestN {
			best, bestN = i, bx.n
		}
	}
	return best
}

// longestAxis is the channel with the widest spread, ties going to red then
// green so the choice does not depend on iteration order.
func (bx *box) longestAxis() int {
	axis, span := 0, -1
	for ch := 0; ch < 3; ch++ {
		if s := int(bx.hi[ch]) - int(bx.lo[ch]); s > span {
			axis, span = ch, s
		}
	}
	return axis
}

// split divides the box at its population median along the longest axis.
func (bx *box) split() (*box, *box) {
	axis := bx.longestAxis()
	items := make([]entry, len(bx.items))
	copy(items, bx.items)
	sort.SliceStable(items, func(i, j int) bool {
		a, b := channel(items[i].c, axis), channel(items[j].c, axis)
		if a != b {
			return a < b
		}
		return lessColour(items[i].c, items[j].c)
	})

	half, run := bx.n/2, 0
	cut := 0
	for i, it := range items {
		run += it.n
		if run >= half {
			cut = i + 1
			break
		}
	}
	// Both halves must be non-empty: a box whose whole population sits in one
	// entry would otherwise split into itself and nothing, and the loop above
	// would never terminate.
	if cut <= 0 {
		cut = 1
	}
	if cut >= len(items) {
		cut = len(items) - 1
	}
	return newBox(items[:cut]), newBox(items[cut:])
}

func channel(c color.RGBA, axis int) uint8 {
	switch axis {
	case 0:
		return c.R
	case 1:
		return c.G
	default:
		return c.B
	}
}

// mean is the box's pixel-weighted average colour.
func (bx *box) mean() color.RGBA {
	var r, g, b, n int64
	for _, it := range bx.items {
		w := int64(it.n)
		r += int64(it.c.R) * w
		g += int64(it.c.G) * w
		b += int64(it.c.B) * w
		n += w
	}
	if n == 0 {
		return bgColour
	}
	return color.RGBA{R: uint8(r / n), G: uint8(g / n), B: uint8(b / n), A: 0xff}
}
