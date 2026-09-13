package render

import (
	"image"
	"testing"

	"github.com/midagedev/toktape/internal/tui"
)

// TestAFillDoesNotEraseAWideGlyph is TTP-47's renderer half.
//
// A cell background used to be filled in the same pass that drew the glyphs,
// cell by cell in reading order. A two-column rune is drawn once, spanning its
// own cell and the continuation cell to its right, so the continuation cell's
// own background — filled one iteration later — painted over the right half of
// the glyph that had just been drawn. Nothing showed it while backgrounds
// appeared only under spaces and block elements (the resource graph's track,
// TTP-39); the write head's fill put one behind text, and a Korean answer came
// out as half-glyphs.
//
// The frame is built here rather than taken from the TUI so the case is one
// line and cannot drift: a Hangul syllable with a 24-bit background, nothing
// else on the screen.
func TestAFillDoesNotEraseAWideGlyph(t *testing.T) {
	rs, err := newRasteriser(DefaultFontSize)
	if err != nil {
		t.Skipf("fonts unavailable: %v", err)
	}
	defer rs.Close()

	const (
		bg   = "\x1b[48;2;32;45;42m"
		fg   = "\x1b[38;2;230;226;216m"
		off  = "\x1b[0m"
		text = "글"
	)
	sc := parseScreen(fg+bg+text+off+"  ", 4, 1)
	img := rs.draw(sc)

	// The right half of the syllable is the continuation cell. Count the
	// pixels in it that are neither the frame's ground nor the fill: those are
	// ink, and a syllable that survived has some.
	ink := func(c image.Rectangle) int {
		n := 0
		for y := c.Min.Y; y < c.Max.Y; y++ {
			for x := c.Min.X; x < c.Max.X; x++ {
				r, g, b, _ := img.At(x, y).RGBA()
				got := [3]uint32{r >> 8, g >> 8, b >> 8}
				if got == [3]uint32{uint32(bgColour.R), uint32(bgColour.G), uint32(bgColour.B)} {
					continue
				}
				if got == [3]uint32{32, 45, 42} {
					continue
				}
				n++
			}
		}
		return n
	}
	left, right := ink(rs.cellRect(0, 0, 1)), ink(rs.cellRect(1, 0, 1))
	if left == 0 {
		t.Fatalf("the syllable drew no ink at all in its own cell")
	}
	if right == 0 {
		t.Errorf("the right half of %q was erased by the continuation cell's fill (left half has %d ink pixels)", text, left)
	}
}

// TestTheWriteHeadReachesTheClip renders the real frame the user looks at and
// checks the fill arrived there: at least one cell of a streaming answer
// carries the theme's dark fill behind a rune that is not a space.
func TestTheWriteHeadReachesTheClip(t *testing.T) {
	tp := tui.ExampleTapeN(4)
	at := tui.ExampleMidRun
	frame := FrameText(tp, Options{}.withDefaults(), Frame{At: at, Anim: at})
	sc := parseScreen(frame, DefaultWidth, DefaultHeight)
	n := 0
	for y := 0; y < sc.h; y++ {
		for x := 0; x < sc.w; x++ {
			if c := sc.at(x, y); c.hasBG && c.r != ' ' && c.r != 0 && !c.cont && !isBlock(c.r) {
				n++
			}
		}
	}
	if n == 0 {
		t.Errorf("no text cell in the live frame carries a fill; the write head is not painted")
	}
}

// isBlock reports whether r is one of the block elements the resource graph
// draws its bars with, which carry a fill for their own reason.
func isBlock(r rune) bool { return r >= 0x2580 && r <= 0x259f }
