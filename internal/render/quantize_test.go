package render

import (
	"image/color"
	"math/rand"
	"testing"
)

func TestMedianCutKeepsASmallPaletteWhole(t *testing.T) {
	// Fewer distinct colours than entries: every one must survive untouched.
	// A quantiser that averaged here would shift the theme's accent by a
	// count or two on a frame that needed no quantising at all.
	hist := map[color.RGBA]int{
		bgColour:                             10000,
		{R: 0x7d, G: 0xd3, B: 0xfc, A: 0xff}: 400,
		{R: 0xfb, G: 0xbf, B: 0x24, A: 0xff}: 30,
		{R: 0xf8, G: 0x71, B: 0x71, A: 0xff}: 5,
	}
	pal := medianCut(hist, MaxGIFColours)
	if len(pal) != len(hist) {
		t.Fatalf("palette has %d entries, want %d", len(pal), len(hist))
	}
	for c := range hist {
		found := false
		for _, p := range pal {
			if p.(color.RGBA) == c {
				found = true
			}
		}
		if !found {
			t.Errorf("%v was dropped from the palette", c)
		}
	}
}

func TestMedianCutRespectsTheLimit(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	hist := make(map[color.RGBA]int, 5000)
	for i := 0; i < 5000; i++ {
		hist[color.RGBA{R: uint8(rng.Intn(256)), G: uint8(rng.Intn(256)), B: uint8(rng.Intn(256)), A: 0xff}] = rng.Intn(100) + 1
	}
	pal := medianCut(hist, MaxGIFColours)
	if len(pal) != MaxGIFColours {
		t.Errorf("palette has %d entries, want exactly %d", len(pal), MaxGIFColours)
	}
}

func TestMedianCutIsDeterministic(t *testing.T) {
	// The histogram is a map, and iterating one is randomised. A palette built
	// straight off that iteration order would change between two renders of
	// the same tape, and so would every byte of the GIF.
	rng := rand.New(rand.NewSource(7))
	hist := make(map[color.RGBA]int, 2000)
	for i := 0; i < 2000; i++ {
		hist[color.RGBA{R: uint8(rng.Intn(64)) * 4, G: uint8(rng.Intn(64)) * 4, B: uint8(rng.Intn(64)) * 4, A: 0xff}] += rng.Intn(50) + 1
	}
	first := medianCut(hist, MaxGIFColours)
	for run := 0; run < 5; run++ {
		again := medianCut(hist, MaxGIFColours)
		if len(again) != len(first) {
			t.Fatalf("run %d: %d entries, want %d", run, len(again), len(first))
		}
		for i := range first {
			if first[i] != again[i] {
				t.Fatalf("run %d: entry %d is %v, want %v", run, i, again[i], first[i])
			}
		}
	}
}

func TestMedianCutKeepsTheDominantColoursExact(t *testing.T) {
	// The background owns most of a frame and the accent owns most of the
	// rest. Splitting by population, not by box count, is what keeps them as
	// entries of their own instead of averaging them with the antialiasing
	// fringe around their edges.
	hist := map[color.RGBA]int{
		bgColour:                             1_000_000,
		{R: 0x7d, G: 0xd3, B: 0xfc, A: 0xff}: 100_000,
	}
	// A long tail of blends between the two, one pixel each.
	for i := 1; i < 255; i++ {
		f := float64(i) / 255
		hist[color.RGBA{
			R: uint8(float64(bgColour.R) + (0x7d-float64(bgColour.R))*f),
			G: uint8(float64(bgColour.G) + (0xd3-float64(bgColour.G))*f),
			B: uint8(float64(bgColour.B) + (0xfc-float64(bgColour.B))*f),
			A: 0xff,
		}] = 1
	}
	pal := medianCut(hist, MaxGIFColours)
	q := &quantiser{palette: pal, cache: map[color.RGBA]uint8{}}
	for _, want := range []color.RGBA{bgColour, {R: 0x7d, G: 0xd3, B: 0xfc, A: 0xff}} {
		got := pal[q.index(want)].(color.RGBA)
		if !near(got, want, 2) {
			t.Errorf("%v maps to %v, want it within two counts of itself", want, got)
		}
	}
}

func TestQuantiserMapsToTheNearestEntry(t *testing.T) {
	pal := color.Palette{
		color.RGBA{R: 0, G: 0, B: 0, A: 0xff},
		color.RGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff},
		color.RGBA{R: 0xff, G: 0, B: 0, A: 0xff},
	}
	q := &quantiser{palette: pal, cache: map[color.RGBA]uint8{}}
	tests := []struct {
		in   color.RGBA
		want int
	}{
		{color.RGBA{R: 0x10, G: 0x10, B: 0x10, A: 0xff}, 0},
		{color.RGBA{R: 0xf0, G: 0xf0, B: 0xf0, A: 0xff}, 1},
		{color.RGBA{R: 0xe0, G: 0x10, B: 0x10, A: 0xff}, 2},
	}
	for _, tc := range tests {
		if got := int(q.index(tc.in)); got != tc.want {
			t.Errorf("%v mapped to entry %d (%v), want %d", tc.in, got, pal[got], tc.want)
		}
	}
	// The cache must not change the answer.
	for _, tc := range tests {
		if got := int(q.index(tc.in)); got != tc.want {
			t.Errorf("%v mapped to entry %d on the second call, want %d", tc.in, got, tc.want)
		}
	}
}
