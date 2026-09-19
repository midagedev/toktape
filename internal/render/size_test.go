package render

import (
	"image"
	"path/filepath"
	"testing"
	"time"

	"github.com/midagedev/toktape/internal/tui"
)

// TestFramesDefaultIs1080p pins the video size (TTP-41): the frame sequence
// that becomes the mp4 is exactly 1920×1080 when nobody asked for a size, so
// X, Reddit and YouTube show it without scaling.
func TestFramesDefaultIs1080p(t *testing.T) {
	paths, err := Frames(tui.ExampleTape(), Options{FPS: 1, Duration: time.Second}, t.TempDir())
	if err != nil {
		t.Fatalf("Frames: %v", err)
	}
	if len(paths) == 0 {
		t.Fatal("Frames wrote nothing")
	}
	want := image.Rect(0, 0, 1920, 1080)
	for _, p := range []string{paths[0], paths[len(paths)-1]} {
		if got := decodePNG(t, p).Bounds(); got != want {
			t.Errorf("%s is %v, want %v", filepath.Base(p), got, want)
		}
	}
}

// TestGIFDefaultsToTheVideoShape is the other half: the GIF a person gets for
// their own run is the same shape as the card they post beside it.
//
// 2026-09-19 (user: "png 요약카드가 toktape 재생 크기랑 다른것도 신경쓰여 딱
// 맞아야하는데"). This test used to be TestGIFKeepsTheInlineSize and pinned
// 120×36, on two stated grounds. Both were re-examined rather than argued
// away:
//
//   - The 1.5 MB budget. Measured on assets/hero.tape at GIFFontSize, one
//     grid against the other with nothing else changed: 120×36 is 1,818,343
//     bytes and 156×38 is 1,756,292. The wider grid is the cheaper file, not
//     the dearer one — a GIF stores the rectangle that changed, and the
//     narrower layout packs more change into each frame. The ground this
//     rested on was not there.
//   - Legibility on a phone. This one is real and is being paid: the cell is
//     the same pixel size on both grids, so a glyph only shrinks where a feed
//     scales the wider frame down to its column — about 23 % on a 390 px
//     screen. It is paid for the reason the hero already paid it
//     (internal/render/cmd/hero): at that width the clip is a picture and the
//     card above it is what a phone reader actually reads (spec §9).
//
// What is left is that every artifact of one run — the card, the hero, the
// mp4 and now the GIF — is 16:9. The asciicast keeps DefaultWidth×
// DefaultHeight: it is replayed in a terminal, where 16:9 means nothing.
func TestGIFDefaultsToTheVideoShape(t *testing.T) {
	out := filepath.Join(t.TempDir(), "run.gif")
	if err := GIF(tui.ExampleTape(), Options{FPS: 1, Duration: time.Second}, out); err != nil {
		t.Fatalf("GIF: %v", err)
	}
	rs, err := newRasteriser(GIFFontSize)
	if err != nil {
		t.Fatalf("rasteriser: %v", err)
	}
	defer rs.Close()
	want := rs.bounds(VideoWidth, VideoHeight)
	g := decodeGIF(t, out)
	if g.Config.Width != want.Dx() || g.Config.Height != want.Dy() {
		t.Errorf("GIF is %d×%d px, want %d×%d (%d×%d cells at %v px)",
			g.Config.Width, g.Config.Height, want.Dx(), want.Dy(), VideoWidth, VideoHeight, GIFFontSize)
	}
	// The shape, not just the numbers: a ratio is what a reader sees when the
	// card and the clip sit in one post.
	if r := float64(g.Config.Width) / float64(g.Config.Height); r < 1.77 || r > 1.785 {
		t.Errorf("GIF aspect = %.4f, want 16:9 (1.7778) — the card is 1200×675", r)
	}
	// The cell did not grow with the grid. A GIF is still the inline artifact
	// and GIFFontSize is still what decides its glyph.
	if GIFFontSize >= DefaultFontSize {
		t.Errorf("GIFFontSize = %v, want smaller than DefaultFontSize %v", GIFFontSize, DefaultFontSize)
	}
}

// TestAsciicastKeepsTheTerminalShape pins the one output that must not follow:
// an asciicast is replayed in somebody's terminal, so it stays the shape the
// TUI's panes were laid out in.
func TestAsciicastKeepsTheTerminalShape(t *testing.T) {
	if DefaultWidth != 120 || DefaultHeight != 36 {
		t.Errorf("DefaultWidth×DefaultHeight = %d×%d, want 120×36", DefaultWidth, DefaultHeight)
	}
	o := Options{FPS: 1, Duration: time.Second}.withDefaults()
	if o.Width != DefaultWidth || o.Height != DefaultHeight {
		t.Errorf("withDefaults gives %d×%d, want the terminal shape %d×%d",
			o.Width, o.Height, DefaultWidth, DefaultHeight)
	}
}
