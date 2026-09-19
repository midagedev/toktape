package render

import (
	"image"
	"image/color"
	"image/draw"
	"image/gif"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/midagedev/toktape/internal/tui"
)

// stripe builds a test frame: a dark field with a white bar at column i.
// Two colours, so the palette is exact and a composite can be compared
// pixel for pixel.
func stripe(w, h, i int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(img, img.Bounds(), image.NewUniform(bgColour), image.Point{}, draw.Src)
	white := color.RGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}
	x := (i * 7) % (w - 4)
	draw.Draw(img, image.Rect(x, 0, x+4, h), image.NewUniform(white), image.Point{}, draw.Src)
	return img
}

func decodeGIF(t *testing.T, path string) *gif.GIF {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer f.Close()
	g, err := gif.DecodeAll(f)
	if err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	return g
}

func fileSize(t *testing.T, path string) int64 {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	return fi.Size()
}

func TestGIFOfTenFrames(t *testing.T) {
	const (
		w, h  = 240, 120
		count = 10
	)
	out := filepath.Join(t.TempDir(), "clip.gif")
	buf := image.NewRGBA(image.Rect(0, 0, w, h))
	src := frameSource{
		count: count,
		at: func(i int) *image.RGBA {
			draw.Draw(buf, buf.Bounds(), stripe(w, h, i), image.Point{}, draw.Src)
			return buf
		},
	}
	if err := writeGIF(src, DefaultFPS, out); err != nil {
		t.Fatalf("writeGIF: %v", err)
	}

	g := decodeGIF(t, out)
	if len(g.Image) != count {
		t.Errorf("the GIF holds %d frames, want %d", len(g.Image), count)
	}
	if g.Config.Width != w || g.Config.Height != h {
		t.Errorf("logical screen is %d×%d, want %d×%d", g.Config.Width, g.Config.Height, w, h)
	}
	for i, img := range g.Image {
		if n := len(img.Palette); n > MaxGIFColours {
			t.Fatalf("frame %d carries %d colours, want at most %d", i, n, MaxGIFColours)
		}
	}
	if got := fileSize(t, out); got > 1_500_000 {
		t.Errorf("the GIF is %d bytes, want under 1.5 MB", got)
	}

	// Composite the decoded frames the way a player does — paint each one over
	// the canvas, honouring the transparent index — and compare with the
	// source. This is what catches a wrong dirty rectangle or a mis-masked
	// pixel: either decodes fine and draws the bar in the wrong place.
	canvas := image.NewRGBA(image.Rect(0, 0, w, h))
	for i, frame := range g.Image {
		draw.Draw(canvas, frame.Bounds(), frame, frame.Bounds().Min, draw.Over)
		want := stripe(w, h, i)
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				if canvas.RGBAAt(x, y) != want.RGBAAt(x, y) {
					t.Fatalf("frame %d differs at (%d,%d): got %v, want %v",
						i, x, y, canvas.RGBAAt(x, y), want.RGBAAt(x, y))
				}
			}
		}
	}
}

func TestGIFStoresAStillSceneOnce(t *testing.T) {
	// The three-second card hold is ninety identical frames. Storing them is
	// ninety copies of the same screen; storing one and lengthening its delay
	// is the same clip and a fraction of the file.
	const count = 10
	out := filepath.Join(t.TempDir(), "still.gif")
	still := stripe(120, 60, 3)
	src := frameSource{count: count, at: func(int) *image.RGBA { return still }}
	if err := writeGIF(src, DefaultFPS, out); err != nil {
		t.Fatalf("writeGIF: %v", err)
	}

	g := decodeGIF(t, out)
	if len(g.Image) != 1 {
		t.Errorf("a still scene stored %d frames, want 1", len(g.Image))
	}
	total := 0
	for _, d := range g.Delay {
		total += d
	}
	// Ten frames at 30 fps is a third of a second, in hundredths.
	if want := 100 * count / DefaultFPS; total < want-1 || total > want+1 {
		t.Errorf("the clip lasts %d cs, want %d ± 1", total, want)
	}
}

func TestDelaysDoNotDrift(t *testing.T) {
	// A GIF delay is a whole centisecond and 30 fps is 3⅓ of them. Rounding
	// each frame to 3 would run a twelve-second clip eight seconds fast, so
	// the delays alternate and the total is what matters.
	for _, fps := range []int{10, 12, 24, 25, 30, 50, 60} {
		total := 0
		for i := 0; i < fps; i++ {
			total += delayFor(i, fps)
		}
		if total != 100 {
			t.Errorf("fps %d: one second of frames lasts %d cs, want 100", fps, total)
		}
	}
}

func TestGIFOfARun(t *testing.T) {
	out := filepath.Join(t.TempDir(), "run.gif")
	if err := GIF(tui.ExampleTape(), Options{FPS: 4, Duration: 2 * time.Second}, out); err != nil {
		t.Fatalf("GIF: %v", err)
	}

	g := decodeGIF(t, out)
	if len(g.Image) < 2 {
		t.Fatalf("the clip stored %d frames; a streaming run is not a still", len(g.Image))
	}
	if n := len(g.Image[0].Palette); n > MaxGIFColours {
		t.Errorf("palette holds %d colours, want at most %d", n, MaxGIFColours)
	}
	if !hasTransparent(g.Image[0].Palette) {
		t.Error("the palette has no transparent entry; unchanged pixels cannot be skipped")
	}
	// The first frame must cover the whole screen; every later one is a patch
	// composited over it.
	if got := g.Image[0].Bounds(); got.Dx() != g.Config.Width || got.Dy() != g.Config.Height {
		t.Errorf("the first frame is %v, want the full %d×%d screen", got, g.Config.Width, g.Config.Height)
	}
	if got := fileSize(t, out); got > 1_500_000 {
		t.Errorf("the GIF is %d bytes, want under 1.5 MB", got)
	}
}

func TestDirtyRect(t *testing.T) {
	pal := color.Palette{bgColour, color.RGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}}
	mk := func(set ...image.Point) *image.Paletted {
		p := image.NewPaletted(image.Rect(0, 0, 8, 8), pal)
		for _, pt := range set {
			p.SetColorIndex(pt.X, pt.Y, 1)
		}
		return p
	}
	if got := dirtyRect(mk(), mk()); !got.Empty() {
		t.Errorf("two identical frames differ over %v, want nothing", got)
	}
	if got, want := dirtyRect(mk(), mk(image.Pt(3, 5))), image.Rect(3, 5, 4, 6); got != want {
		t.Errorf("one changed pixel gives %v, want %v", got, want)
	}
	got := dirtyRect(mk(), mk(image.Pt(1, 2), image.Pt(6, 4)))
	if want := image.Rect(1, 2, 7, 5); got != want {
		t.Errorf("two changed pixels give %v, want the box %v around them", got, want)
	}
}

// hasTransparent reports whether a palette carries the zero-alpha entry the
// encoder turns into the transparent index.
func hasTransparent(pal color.Palette) bool {
	for _, c := range pal {
		if _, _, _, a := c.RGBA(); a == 0 {
			return true
		}
	}
	return false
}

func TestGIFOfARunReplaysTheRenderedFrames(t *testing.T) {
	// End to end: decode the GIF, composite it the way a player does, and
	// compare every frame with the image the rasteriser produced. The
	// synthetic test above pins the masking arithmetic; this one pins that the
	// whole pipeline — schedule, raster, quantise, mask, encode — puts the
	// right screen on the canvas at the right time.
	//
	// The tolerance is quantisation error and nothing else. Measured
	// 2026-09-13 on this clip: mean absolute error 0.09–0.45 counts per
	// channel, with 0.00–0.18% of pixels past 32 counts (antialiasing fringes
	// of the rarest colours). The gates are set at 2 and 0.5%.
	tp := tui.ExampleTape()
	opts := Options{FPS: 4, Duration: 2 * time.Second}
	out := filepath.Join(t.TempDir(), "run.gif")
	if err := GIF(tp, opts, out); err != nil {
		t.Fatalf("GIF: %v", err)
	}

	// The GIF's own resolution of the defaults, not a copy of it: this used to
	// spell out "GIFFontSize, then withDefaults", which silently stopped
	// describing GIF() when the grid moved to the video's (2026-09-19).
	want := opts.withGIFDefaults()
	// The renderer's own schedule, poster frame included (2026-09-14).
	_, sched, err := prepare(tp, want)
	if err != nil {
		t.Fatal(err)
	}

	g := decodeGIF(t, out)
	canvas := image.NewRGBA(image.Rect(0, 0, g.Config.Width, g.Config.Height))
	for i, frame := range g.Image {
		draw.Draw(canvas, frame.Bounds(), frame, frame.Bounds().Min, draw.Over)
		ref, err := FrameImage(tp, want, sched.Frame(i))
		if err != nil {
			t.Fatalf("FrameImage: %v", err)
		}
		if ref.Bounds() != canvas.Bounds() {
			t.Fatalf("frame %d: the GIF canvas is %v and the raster is %v", i, canvas.Bounds(), ref.Bounds())
		}
		var sum, off int
		b := canvas.Bounds()
		for y := b.Min.Y; y < b.Max.Y; y++ {
			for x := b.Min.X; x < b.Max.X; x++ {
				d := channelDistance(canvas.RGBAAt(x, y), ref.RGBAAt(x, y))
				sum += d
				if d > 32 {
					off++
				}
			}
		}
		px := b.Dx() * b.Dy()
		if mean := float64(sum) / float64(px); mean > 2 {
			t.Errorf("frame %d: mean error %.2f counts per channel, want at most 2", i, mean)
		}
		if share := 100 * float64(off) / float64(px); share > 0.5 {
			t.Errorf("frame %d: %.3f%% of pixels are more than 32 counts off, want at most 0.5%%", i, share)
		}
	}
}

func TestGIFOfTheWholeClipStaysPostable(t *testing.T) {
	// The product gate. A GIF nobody can attach is not a money shot, and the
	// three things that keep this one small — one global palette, the dirty
	// rectangle, and transparency for unchanged pixels — are each easy to
	// undo by accident. Without the transparency pass this same clip is
	// 19.3 MB (measured 2026-09-13). It runs unconditionally — a few seconds
	// is worth paying on every gate for the one number that decides whether
	// the clip can be posted at all.
	//
	// The budget stayed at 1.5 MB when the cold open nearly doubled the
	// clip's length, 11.5 s to 20 s (2026-09-13). It could have been raised
	// and was not: measured on this fixture the clip went 1.09 MB → 1.22 MB,
	// and the hero's own four-stream shape is 0.87 MB. A static opening is
	// almost free here — every frame of it is one dirty rectangle of a few
	// cells — so a looser gate would have bought headroom nobody needs and
	// given up the one that caught a 7× regression before.
	tp := tui.ExampleTape()
	out := filepath.Join(t.TempDir(), "clip.gif")
	if err := GIF(tp, Options{}, out); err != nil {
		t.Fatalf("GIF: %v", err)
	}

	// The budget is per *streaming* second, not a fixed total and not per
	// clip second (TTP-27, 2026-09-13). Since the clip plays the run at 1:1
	// its length now follows the tape, so a fixed total would have to be
	// re-pinned every time the fixture changes — which is exactly the kind of
	// gate that gets loosened until it stops catching anything.
	//
	// Streaming seconds is the denominator that holds still. The cold open,
	// the intro and the card hold are nearly free: every frame of them is one
	// small dirty rectangle, and the card folds into a single stored frame.
	// So essentially all of the file is the streaming phase, and dividing by
	// the whole clip would make the number drift with the hold fraction.
	// Measured 2026-09-13 at GIFFontSize, 30 fps, on this fixture and on a
	// synthetic twenty-five-second lengthening of it (repeat the token
	// cadence until the run lasts 25 s):
	//
	//	streams  run      clip    stream   bytes      kB/clip-s  kB/stream-s
	//	8        7.47 s   19.5 s   7.50 s  1,540,971       77.2        200.6
	//	4        7.31 s   19.3 s   7.33 s  1,041,309       52.6        138.7
	//	8       24.98 s   37.0 s  25.00 s  5,381,963      142.0        210.2
	//	4       24.98 s   37.0 s  25.00 s  5,232,166      138.1        204.4
	//
	// 250 kB per streaming second is that 210 plus a quarter of headroom. It
	// is tighter than the 2.0 MB flat cap it replaces (1.88 MB on this
	// fixture), so nothing was loosened; it is the same gate expressed in the
	// one unit that survives a longer run. What it still catches is the thing
	// it was written for: without the transparency pass the earlier
	// list-layout clip measured 19.3 MB, an order of magnitude over.
	//
	// The absolute size is now the lead's problem rather than this test's: a
	// four-stream twenty-five-second run lands near 5 MB, which is past the
	// 1.5 MB the README hero is capped at in cmd/toktape. Reducing it is a
	// cell-size or frame-rate decision, not a density one.
	const perStreamSecond = 250_000
	s := NewSchedule(0, RunEnd(tp), DefaultFPS, 0, false)
	budget := int64(perStreamSecond * s.Stream.Seconds())
	if got := fileSize(t, out); got > budget {
		t.Errorf("the clip is %d bytes over a %v stream, %.1f kB per streaming second; want at most %d (%d kB/s)",
			got, s.Stream, float64(got)/1024/s.Stream.Seconds(), budget, perStreamSecond/1000)
	}
}

// channelDistance is the largest per-channel difference between two colours.
func channelDistance(a, b color.RGBA) int {
	d := func(x, y uint8) int {
		if x > y {
			return int(x - y)
		}
		return int(y - x)
	}
	m := d(a.R, b.R)
	if v := d(a.G, b.G); v > m {
		m = v
	}
	if v := d(a.B, b.B); v > m {
		m = v
	}
	return m
}
