package render

import (
	"fmt"
	"image"
	"image/color"
	"image/gif"
	"os"

	"github.com/midagedev/toktape/internal/tape"
)

// histFrames is how many frames the palette is built from.
//
// Not all of them: the clip's colours are the theme's eleven foregrounds over
// one background plus the antialiasing between them, and that set is complete
// after a handful of frames. Two dozen spread across the clip catch the card
// screen and the warn/bad colours a stall introduces, and cost two dozen
// rasterisations instead of a second full pass over three hundred.
const histFrames = 24

// GIF renders the clip as an animated GIF at out.
//
// Three things keep the file postable. The palette is one global table of at
// most MaxGIFColours entries, built from the clip's own colours. Each frame
// after the first is stored as only the rectangle that changed, which on a
// screen whose border and right pane are static is a fraction of the canvas.
// And a frame identical to the one before it is not stored at all — its time
// is added to the previous frame's delay, so the three-second card hold is one
// frame, not ninety.
//
// Options.FontSize defaults to GIFFontSize here rather than DefaultFontSize:
// see the constant.
func GIF(tp *tape.Tape, opts Options, out string) error {
	if opts.FontSize <= 0 {
		opts.FontSize = GIFFontSize
	}
	o, sched, err := prepare(tp, opts)
	if err != nil {
		return err
	}
	rs, err := newRasteriser(o.FontSize)
	if err != nil {
		return err
	}
	defer rs.Close()

	buf := image.NewRGBA(rs.bounds(o.Width, o.Height))
	src := frameSource{
		count: sched.Count,
		at: func(i int) *image.RGBA {
			rs.drawInto(buf, parseScreen(FrameText(tp, o, sched.Frame(i)), o.Width, o.Height))
			return buf
		},
	}
	return writeGIF(src, o.FPS, out)
}

// frameSource is a clip as the GIF encoder sees it: a frame count and a
// function that draws frame i. at may return the same buffer every call —
// writeGIF consumes each frame before asking for the next.
type frameSource struct {
	count int
	at    func(i int) *image.RGBA
}

// writeGIF quantises a frame source to one global palette and encodes it.
//
// Two things shrink the file, and the second matters far more than the first.
// A frame is stored only over the rectangle that changed — but on this screen
// the header shimmer touches the top row and the latency strip touches the
// bottom one in the same frame, so that rectangle is usually the whole canvas.
// What actually pays is the second: inside that rectangle, every pixel equal to
// the frame before it is written as the palette's transparent entry, and
// because the screen is mostly still, those become the long identical runs LZW
// was built for. The last palette slot is spent on that transparency, which is
// why the quantiser only gets MaxGIFColours-1 colours.
func writeGIF(src frameSource, fps int, out string) error {
	if src.count <= 0 {
		return fmt.Errorf("render: gif: no frames to encode")
	}

	hist := make(map[color.RGBA]int, 8192)
	for _, i := range sampleIndices(src.count, histFrames) {
		histogram(src.at(i), hist)
	}
	q := newQuantiser(hist, MaxGIFColours-1)

	// The image palette is the quantiser's plus one fully transparent entry at
	// the end. Go's encoder picks the first zero-alpha entry as the transparent
	// index, and the quantiser never maps a pixel there because it searches
	// only its own opaque palette.
	transparent := uint8(len(q.palette))
	q.out = make(color.Palette, len(q.palette), len(q.palette)+1)
	copy(q.out, q.palette)
	q.out = append(q.out, color.RGBA{})

	bounds := src.at(0).Bounds()
	g := &gif.GIF{
		Config: image.Config{
			ColorModel: q.out,
			Width:      bounds.Dx(),
			Height:     bounds.Dy(),
		},
	}
	var prev *image.Paletted
	for i := 0; i < src.count; i++ {
		full := q.paletted(src.at(i))
		cur := full
		delay := delayFor(i, fps)

		if prev != nil {
			dirty := dirtyRect(prev, full)
			if dirty.Empty() {
				g.Delay[len(g.Delay)-1] += delay
				continue
			}
			cur = maskUnchanged(prev, full, dirty, transparent)
		}
		g.Image = append(g.Image, cur)
		g.Delay = append(g.Delay, delay)
		// DisposalNone: every frame paints over what is already on screen,
		// which is what makes a transparent pixel mean "unchanged".
		g.Disposal = append(g.Disposal, gif.DisposalNone)
		prev = full
	}

	f, err := os.Create(out)
	if err != nil {
		return fmt.Errorf("render: create %s: %w", out, err)
	}
	if err := gif.EncodeAll(f, g); err != nil {
		f.Close()
		return fmt.Errorf("render: encode %s: %w", out, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("render: close %s: %w", out, err)
	}
	return nil
}

// maskUnchanged returns the part of cur inside r, with every pixel that is
// unchanged from prev replaced by the transparent index.
func maskUnchanged(prev, cur *image.Paletted, r image.Rectangle, transparent uint8) *image.Paletted {
	out := image.NewPaletted(r, cur.Palette)
	for y := r.Min.Y; y < r.Max.Y; y++ {
		p := prev.Pix[prev.PixOffset(r.Min.X, y) : prev.PixOffset(r.Min.X, y)+r.Dx()]
		c := cur.Pix[cur.PixOffset(r.Min.X, y) : cur.PixOffset(r.Min.X, y)+r.Dx()]
		d := out.Pix[out.PixOffset(r.Min.X, y) : out.PixOffset(r.Min.X, y)+r.Dx()]
		for x := range c {
			if c[x] == p[x] {
				d[x] = transparent
			} else {
				d[x] = c[x]
			}
		}
	}
	return out
}

// delayFor is frame i's on-screen time in centiseconds.
//
// GIF measures delay in hundredths of a second, which does not divide 30 fps.
// Rounding each frame to 3 cs would run the clip 10% fast, so the delay is the
// difference between two rounded cumulative times: the frames alternate 3, 4,
// 3, 3, 4 … and the clip's total length is exact.
func delayFor(i, fps int) int {
	if fps <= 0 {
		fps = DefaultFPS
	}
	at := func(n int) int { return (n*100 + fps/2) / fps }
	d := at(i+1) - at(i)
	if d < 1 {
		d = 1
	}
	return d
}

// sampleIndices returns at most n indices spread evenly over count frames,
// both ends included.
func sampleIndices(count, n int) []int {
	if count <= 0 {
		return nil
	}
	if count <= n {
		out := make([]int, count)
		for i := range out {
			out[i] = i
		}
		return out
	}
	out := make([]int, n)
	for i := range out {
		out[i] = i * (count - 1) / (n - 1)
	}
	return out
}

// dirtyRect is the smallest rectangle covering every pixel where cur differs
// from prev. An empty rectangle means the two frames are identical.
//
// Both images must have the same bounds and the same palette, which they do:
// they come from the same buffer and the same quantiser.
func dirtyRect(prev, cur *image.Paletted) image.Rectangle {
	b := cur.Bounds()
	minX, minY := b.Max.X, b.Max.Y
	maxX, maxY := b.Min.X, b.Min.Y
	for y := b.Min.Y; y < b.Max.Y; y++ {
		p := prev.Pix[prev.PixOffset(b.Min.X, y) : prev.PixOffset(b.Min.X, y)+b.Dx()]
		c := cur.Pix[cur.PixOffset(b.Min.X, y) : cur.PixOffset(b.Min.X, y)+b.Dx()]
		first, last := -1, -1
		for x := 0; x < len(c); x++ {
			if p[x] != c[x] {
				if first < 0 {
					first = x
				}
				last = x
			}
		}
		if first < 0 {
			continue
		}
		if y < minY {
			minY = y
		}
		if y >= maxY {
			maxY = y + 1
		}
		if b.Min.X+first < minX {
			minX = b.Min.X + first
		}
		if b.Min.X+last+1 > maxX {
			maxX = b.Min.X + last + 1
		}
	}
	if minX >= maxX || minY >= maxY {
		return image.Rectangle{}
	}
	return image.Rect(minX, minY, maxX, maxY)
}
