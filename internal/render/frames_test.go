package render

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/midagedev/toktape/internal/tui"
)

// decodePNG reads a frame file back as an image.
func decodePNG(t *testing.T, path string) image.Image {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	return img
}

// inkFraction is the share of pixels that are not the terminal background.
func inkFraction(img image.Image) float64 {
	b := img.Bounds()
	ink := 0
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, _ := img.At(x, y).RGBA()
			c := color.RGBA{R: uint8(r >> 8), G: uint8(g >> 8), B: uint8(bl >> 8), A: 0xff}
			if c != bgColour {
				ink++
			}
		}
	}
	return float64(ink) / float64(b.Dx()*b.Dy())
}

func TestFramesWritesReadablePNGs(t *testing.T) {
	dir := t.TempDir()
	// Two frames per second for one second: the first frame, one mid-clip and
	// the last, which is three files without rendering three hundred.
	paths, err := Frames(tui.ExampleTape(), Options{FPS: 2, Duration: time.Second}, dir)
	if err != nil {
		t.Fatalf("Frames: %v", err)
	}
	if len(paths) != 3 {
		t.Fatalf("wrote %d frames, want 3", len(paths))
	}

	var bounds image.Rectangle
	for i, p := range paths {
		if want := filepath.Join(dir, "frame_0000"+string(rune('0'+i))+".png"); p != want {
			t.Errorf("frame %d is named %s, want %s", i, p, want)
		}
		img := decodePNG(t, p)
		if i == 0 {
			bounds = img.Bounds()
		} else if img.Bounds() != bounds {
			t.Errorf("frame %d is %v, want every frame at %v", i, img.Bounds(), bounds)
		}
		if got := inkFraction(img); got < 0.02 {
			t.Errorf("frame %d is %.3f%% ink — the screen came out blank", i, got*100)
		}
	}

	// yuv420p needs even dimensions; discovering that from ffmpeg's stderr
	// after three hundred frames is the failure this pins.
	if bounds.Dx()%2 != 0 || bounds.Dy()%2 != 0 {
		t.Errorf("frame is %d×%d; H.264 with chroma subsampling needs both even", bounds.Dx(), bounds.Dy())
	}
}

func TestFrameImageMatchesTheCellGrid(t *testing.T) {
	rs, err := newRasteriser(DefaultFontSize)
	if err != nil {
		t.Fatalf("newRasteriser: %v", err)
	}
	defer rs.Close()

	sched := NewSchedule(RunEnd(tui.ExampleTape()), DefaultFPS, 0)
	img, err := FrameImage(tui.ExampleTape(), Options{}, sched.Frame(sched.Count/2))
	if err != nil {
		t.Fatalf("FrameImage: %v", err)
	}
	if want := rs.bounds(DefaultWidth, DefaultHeight); img.Bounds() != want {
		t.Errorf("image is %v, want %v", img.Bounds(), want)
	}
	// The margin is background on every side: a frame drawn at the wrong
	// origin would spill into it.
	for _, p := range []image.Point{{X: 0, Y: 0}, {X: img.Bounds().Dx() - 1, Y: img.Bounds().Dy() - 1}} {
		if got := img.RGBAAt(p.X, p.Y); got != bgColour {
			t.Errorf("pixel %v = %v, want the background %v", p, got, bgColour)
		}
	}
}

func TestRasteriserCellGeometry(t *testing.T) {
	rs, err := newRasteriser(DefaultFontSize)
	if err != nil {
		t.Fatalf("newRasteriser: %v", err)
	}
	defer rs.Close()

	// JetBrains Mono's advance is 0.6 em at every weight, which is what makes
	// the card's numbers line up and what makes a 120-column frame exactly
	// 120 cells wide here.
	if want := int(DefaultFontSize * 0.6); rs.cw != want {
		t.Errorf("cell width = %d, want %d (0.6 em)", rs.cw, want)
	}
	if rs.ch <= rs.cw {
		t.Errorf("cell is %d×%d — a terminal cell is taller than it is wide", rs.cw, rs.ch)
	}
	if rs.baseline <= 0 || rs.baseline >= rs.ch {
		t.Errorf("baseline at %d is outside the %d-pixel cell", rs.baseline, rs.ch)
	}

	// A Hangul syllable must advance exactly two cells, or every glyph after
	// the first one on a Korean line sits half a cell off its column.
	adv, ok := rs.wide.GlyphAdvance('가')
	if !ok {
		t.Fatal("the fallback face has no Hangul")
	}
	if got := adv.Round(); got != 2*rs.cw {
		t.Errorf("a Hangul syllable advances %d px, want two cells = %d px", got, 2*rs.cw)
	}
}

func TestRasteriserCoversEveryRuneTheTUIDraws(t *testing.T) {
	// The one class of defect a width test cannot see: a frame that lays out
	// correctly and rasterises as a row of tofu boxes. Neither embedded face
	// has braille, which is exactly how the prefill spinner would have gone
	// missing.
	rs, err := newRasteriser(DefaultFontSize)
	if err != nil {
		t.Fatalf("newRasteriser: %v", err)
	}
	defer rs.Close()

	tp := tui.ExampleTape()
	sched := NewSchedule(RunEnd(tp), DefaultFPS, 0)
	seen := map[rune]bool{}
	for _, i := range sampleIndices(sched.Count, 40) {
		sc := parseScreen(FrameText(tp, Options{}.withDefaults(), sched.Frame(i)), DefaultWidth, DefaultHeight)
		for _, c := range sc.cells {
			if c.r != 0 && c.r != ' ' {
				seen[c.r] = true
			}
		}
	}
	if len(seen) < 40 {
		t.Fatalf("only %d distinct runes across the clip; the frames are not being parsed", len(seen))
	}
	for r := range seen {
		if !rs.hasGlyph(r) {
			t.Errorf("no glyph and no geometry for U+%04X %q", r, r)
		}
	}
	if !seen['⠋'] && !seen['⠙'] && !seen['⠹'] {
		t.Error("no spinner frame appeared in the clip; the prefill phase is not being drawn")
	}
}

func TestBlockRunesAreDrawnToTheCellEdges(t *testing.T) {
	// A full block drawn from the font leaves a gap between rows, which turns
	// the TUI's border into a dashed line and its bars into floating tiles.
	// Geometry, not typography: the cell is filled corner to corner.
	rs, err := newRasteriser(DefaultFontSize)
	if err != nil {
		t.Fatalf("newRasteriser: %v", err)
	}
	defer rs.Close()

	sc := newScreen(2, 2)
	white := color.RGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}
	for i := range sc.cells {
		sc.cells[i] = cell{r: '█', fg: white}
	}
	img := rs.draw(sc)

	// Every pixel of the 2×2 cell block, the seam between the two rows
	// included, must be ink.
	area := image.Rect(rs.pad.X, rs.pad.Y, rs.pad.X+2*rs.cw, rs.pad.Y+2*rs.ch)
	for y := area.Min.Y; y < area.Max.Y; y++ {
		for x := area.Min.X; x < area.Max.X; x++ {
			if got := img.RGBAAt(x, y); got != white {
				t.Fatalf("pixel (%d,%d) = %v, want the block filled to %v", x, y, got, white)
			}
		}
	}
}

func TestVerticalRuleIsContinuous(t *testing.T) {
	rs, err := newRasteriser(DefaultFontSize)
	if err != nil {
		t.Fatalf("newRasteriser: %v", err)
	}
	defer rs.Close()

	sc := newScreen(1, 3)
	white := color.RGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}
	for i := range sc.cells {
		sc.cells[i] = cell{r: '│', fg: white}
	}
	img := rs.draw(sc)

	x := rs.pad.X + (rs.cw-rs.stroke)/2
	for y := rs.pad.Y; y < rs.pad.Y+3*rs.ch; y++ {
		if got := img.RGBAAt(x, y); got != white {
			t.Fatalf("the border breaks at row %d: %v", y-rs.pad.Y, got)
		}
	}
}

func TestLowerBlocksGrowUpwardAndLeftBlocksShrinkLeftward(t *testing.T) {
	// The two runs of the block-elements range go in opposite directions and
	// transposing them is the classic bug: the sparkline would read upside
	// down and the placement bar would fill from the wrong end.
	rs, err := newRasteriser(DefaultFontSize)
	if err != nil {
		t.Fatalf("newRasteriser: %v", err)
	}
	defer rs.Close()
	white := color.RGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}

	// inkBox is the bounding box of the ink inside a single cell, in cell
	// coordinates.
	inkBox := func(r rune) image.Rectangle {
		sc := newScreen(1, 1)
		sc.cells[0] = cell{r: r, fg: white}
		img := rs.draw(sc)
		box := image.Rectangle{Min: image.Pt(rs.cw, rs.ch)}
		for y := 0; y < rs.ch; y++ {
			for x := 0; x < rs.cw; x++ {
				if img.RGBAAt(rs.pad.X+x, rs.pad.Y+y) != white {
					continue
				}
				if x < box.Min.X {
					box.Min.X = x
				}
				if y < box.Min.Y {
					box.Min.Y = y
				}
				if x+1 > box.Max.X {
					box.Max.X = x + 1
				}
				if y+1 > box.Max.Y {
					box.Max.Y = y + 1
				}
			}
		}
		if box.Min.X >= box.Max.X {
			return image.Rectangle{}
		}
		return box
	}

	tests := []struct {
		r            rune
		wantW, wantH int
		anchorBottom bool
		anchorLeft   bool
	}{
		{r: '█', wantW: rs.cw, wantH: rs.ch, anchorBottom: true, anchorLeft: true},
		{r: '▁', wantW: rs.cw, wantH: (rs.ch + 4) / 8, anchorBottom: true, anchorLeft: true},
		{r: '▄', wantW: rs.cw, wantH: (rs.ch*4 + 4) / 8, anchorBottom: true, anchorLeft: true},
		{r: '▇', wantW: rs.cw, wantH: (rs.ch*7 + 4) / 8, anchorBottom: true, anchorLeft: true},
		{r: '▏', wantW: (rs.cw + 4) / 8, wantH: rs.ch, anchorBottom: true, anchorLeft: true},
		{r: '▌', wantW: (rs.cw*4 + 4) / 8, wantH: rs.ch, anchorBottom: true, anchorLeft: true},
		{r: '▉', wantW: (rs.cw*7 + 4) / 8, wantH: rs.ch, anchorBottom: true, anchorLeft: true},
	}
	for _, tc := range tests {
		box := inkBox(tc.r)
		if box.Dx() != tc.wantW || box.Dy() != tc.wantH {
			t.Errorf("%q covers %d×%d px, want %d×%d in a %d×%d cell",
				tc.r, box.Dx(), box.Dy(), tc.wantW, tc.wantH, rs.cw, rs.ch)
			continue
		}
		if tc.anchorBottom && box.Max.Y != rs.ch {
			t.Errorf("%q ends %d px above the cell's foot; the lower blocks grow upward from the bottom",
				tc.r, rs.ch-box.Max.Y)
		}
		if tc.anchorLeft && box.Min.X != 0 {
			t.Errorf("%q starts %d px in from the cell's left edge; the left blocks grow rightward from the left",
				tc.r, box.Min.X)
		}
	}
}

func TestBrailleIsDrawnAsDots(t *testing.T) {
	rs, err := newRasteriser(DefaultFontSize)
	if err != nil {
		t.Fatalf("newRasteriser: %v", err)
	}
	defer rs.Close()
	white := color.RGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}

	ink := func(r rune) int {
		sc := newScreen(1, 1)
		sc.cells[0] = cell{r: r, fg: white}
		img := rs.draw(sc)
		n := 0
		for y := 0; y < rs.ch; y++ {
			for x := 0; x < rs.cw; x++ {
				if img.RGBAAt(rs.pad.X+x, rs.pad.Y+y) == white {
					n++
				}
			}
		}
		return n
	}

	// U+2800 is the blank pattern and must draw nothing; U+28FF has all eight
	// dots and must draw the most; the spinner frames sit in between.
	if got := ink('⠀'); got != 0 {
		t.Errorf("the blank braille pattern drew %d pixels, want none", got)
	}
	full := ink('⣿')
	if full == 0 {
		t.Fatal("the full braille pattern drew nothing; the spinner would be invisible")
	}
	if spin := ink('⠋'); spin == 0 || spin >= full {
		t.Errorf("spinner frame ⠋ drew %d pixels, want between 1 and the full pattern's %d", spin, full)
	}
}
