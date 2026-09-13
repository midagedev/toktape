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

// TestGIFKeepsTheInlineSize is the other half: the GIF is posted inline, has a
// 1.5 MB budget and must stay legible on a phone, so it keeps 120×36 cells at
// GIFFontSize however large the video grows.
func TestGIFKeepsTheInlineSize(t *testing.T) {
	if DefaultWidth != 120 || DefaultHeight != 36 {
		t.Errorf("DefaultWidth×DefaultHeight = %d×%d, want 120×36", DefaultWidth, DefaultHeight)
	}
	out := filepath.Join(t.TempDir(), "run.gif")
	if err := GIF(tui.ExampleTape(), Options{FPS: 1, Duration: time.Second}, out); err != nil {
		t.Fatalf("GIF: %v", err)
	}
	rs, err := newRasteriser(GIFFontSize)
	if err != nil {
		t.Fatalf("rasteriser: %v", err)
	}
	defer rs.Close()
	want := rs.bounds(120, 36)
	g := decodeGIF(t, out)
	if g.Config.Width != want.Dx() || g.Config.Height != want.Dy() {
		t.Errorf("GIF is %d×%d px, want %d×%d (120×36 cells at %v px)",
			g.Config.Width, g.Config.Height, want.Dx(), want.Dy(), GIFFontSize)
	}
}
