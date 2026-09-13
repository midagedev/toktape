package png

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	stdpng "image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/midagedev/toktape/internal/card"
	"github.com/midagedev/toktape/internal/tape"
)

// outDir is where the rendered cards are dropped for the lead's visual check.
// It is gitignored; the tests recreate it.
const outDir = "testdata/out"

// hangulSummary is the fallback-font fixture: a model whose name is Hangul, so
// a card rendered without D2Coding would show tofu boxes (or nothing) where
// the name goes.
func hangulSummary() *tape.RunSummary {
	s := card.Example()
	s.Model.Name = "한글 모델 이름"
	s.Model.FileName = "한글-모델-Q4_K_M.gguf"
	return s
}

// fixtures are the summaries every structural test runs over.
func fixtures() map[string]*tape.RunSummary {
	return map[string]*tape.RunSummary{
		"single":     card.Example(),
		"concurrent": card.ExampleConcurrent(),
		"unknown":    {},
		"hangul":     hangulSummary(),
		// TTP-30: three placement devices with a CPU one, a longer flags strip,
		// a bare-commit engine line and the draft clause in the hero.
		"speculative": card.ExampleSpeculative(),
		// TTP-31: six prompt rounds; the hero's first decode line is the
		// median over them.
		"rounds": card.ExampleRounds(),
	}
}

// ------------------------------------------------------------- structure ---

func TestRenderBounds(t *testing.T) {
	for name, s := range fixtures() {
		t.Run(name, func(t *testing.T) {
			img, err := Render(s)
			if err != nil {
				t.Fatalf("Render: %v", err)
			}
			want := image.Rect(0, 0, Width, Height)
			if got := img.Bounds(); got != want {
				t.Fatalf("bounds = %v, want %v", got, want)
			}
		})
	}
}

func TestRenderNilSummary(t *testing.T) {
	img, err := Render(nil)
	if err != nil {
		t.Fatalf("Render(nil): %v", err)
	}
	if got, want := img.Bounds(), image.Rect(0, 0, Width, Height); got != want {
		t.Fatalf("bounds = %v, want %v", got, want)
	}
}

// TestRenderIsDeterministic is the replay contract: the card is a pure
// function of the summary, so two renders of the same tape are byte-identical.
// A clock read or a map iteration leaking into the layout breaks this.
func TestRenderIsDeterministic(t *testing.T) {
	encode := func() []byte {
		img, err := Render(card.Example())
		if err != nil {
			t.Fatalf("Render: %v", err)
		}
		var buf bytes.Buffer
		if err := stdpng.Encode(&buf, img); err != nil {
			t.Fatalf("Encode: %v", err)
		}
		return buf.Bytes()
	}
	if a, b := encode(), encode(); !bytes.Equal(a, b) {
		t.Fatalf("two renders of the same summary differ (%d vs %d bytes)", len(a), len(b))
	}
}

// ----------------------------------------------------------- pixel probes ---

func at(img *image.RGBA, x, y int) color.RGBA {
	c := img.RGBAAt(x, y)
	return color.RGBA{R: c.R, G: c.G, B: c.B, A: c.A}
}

func TestPixelProbes(t *testing.T) {
	c, err := renderCanvas(card.Example())
	if err != nil {
		t.Fatalf("renderCanvas: %v", err)
	}

	// The canvas background outside the panel is the contract's #11111b.
	if got := at(c.img, 5, 5); got != colBase {
		t.Errorf("background at (5,5) = %v, want %v", got, colBase)
	}
	// The panel interior is #1e1e2e. Sample a spot with nothing drawn on it:
	// the gap between the hero rule and the memory label.
	if got := at(c.img, contentR-4, bandMemTop+4); got != colPanel {
		t.Errorf("panel at (%d,%d) = %v, want %v", contentR-4, bandMemTop+4, got, colPanel)
	}
	// The 1px border: the panel's left edge at mid height, where the rounded
	// corners are far away and the border is exactly one pixel of #313244.
	mid := (panelTop + panelBottom) / 2
	if got := at(c.img, panelInset, mid); got != colBorder {
		t.Errorf("border at (%d,%d) = %v, want %v", panelInset, mid, got, colBorder)
	}
	if got := at(c.img, panelInset+1, mid); got != colPanel {
		t.Errorf("inside border at (%d,%d) = %v, want %v", panelInset+1, mid, got, colPanel)
	}
	if got := at(c.img, panelInset-1, mid); got != colBase {
		t.Errorf("outside border at (%d,%d) = %v, want %v", panelInset-1, mid, got, colBase)
	}

	// The hairline rules are one pixel tall: the row is not background, the
	// rows either side of it are.
	if at(c.img, contentL+40, bandHeroTop) == colPanel {
		t.Errorf("header rule at y=%d is not drawn", bandHeroTop)
	}
	if got := at(c.img, contentL+40, bandHeroTop-2); got != colPanel {
		t.Errorf("pixel above the header rule = %v, want %v (rule is thicker than 1px)", got, colPanel)
	}
	if got := at(c.img, contentL+40, bandHeroTop+2); got != colPanel {
		t.Errorf("pixel below the header rule = %v, want %v (rule is thicker than 1px)", got, colPanel)
	}
}

// inkCount returns how many pixels in r differ from the panel colour.
func inkCount(img *image.RGBA, r image.Rectangle) int {
	n := 0
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			if at(img, x, y) != colPanel {
				n++
			}
		}
	}
	return n
}

// TestHeroNumbersHaveInk is the "the draw actually happened" probe: the hero
// is the whole point of the card, and a font that failed to load would leave
// the region flat.
func TestHeroNumbersHaveInk(t *testing.T) {
	for _, name := range []string{"single", "concurrent"} {
		s := fixtures()[name]
		t.Run(name, func(t *testing.T) {
			c, err := renderCanvas(s)
			if err != nil {
				t.Fatalf("renderCanvas: %v", err)
			}
			for _, id := range []string{"hero.left.number", "hero.right.number"} {
				m, ok := c.markByID(id)
				if !ok {
					t.Fatalf("%s was never drawn", id)
				}
				if got := inkCount(c.img, m.Rect); got < 400 {
					t.Errorf("%s (%q) has %d ink pixels in %v, want >= 400", id, m.Text, got, m.Rect)
				}
			}
		})
	}
}

// TestEveryBandHasInk catches a whole region silently failing to render.
func TestEveryBandHasInk(t *testing.T) {
	bands := []struct {
		name     string
		top, bot int
	}{
		{"header", bandHeaderTop, bandHeroTop},
		{"hero", bandHeroTop, bandMemTop},
		{"memory", bandMemTop, bandFooterTop},
		{"footer", bandFooterTop, bandStripTop},
		{"strip", bandStripTop, bandStripEnd},
	}
	for name, s := range fixtures() {
		t.Run(name, func(t *testing.T) {
			c, err := renderCanvas(s)
			if err != nil {
				t.Fatalf("renderCanvas: %v", err)
			}
			for _, b := range bands {
				r := image.Rect(contentL, b.top, contentR, b.bot)
				if got := inkCount(c.img, r); got < 200 {
					t.Errorf("band %s has %d ink pixels, want >= 200", b.name, got)
				}
			}
		})
	}
}

// TestHeroGradient asserts the card's single accent ramp is actually a ramp:
// the left edge of the decode number is Accent and the right edge is
// AccentHigh, the same hue 45 % toward white.
//
// 2026-09-13 (TTP-44): the ramp used to be cyan → blue, two hues sharing a red
// channel, so this test read the blue-minus-green lean. Oxide's ramp is one hue
// at two lightnesses, so the test now reads lightness: the brightest pixel of
// the right third must be clearly lighter than the brightest of the left third.
// Brightest, because antialiased edge pixels are blends with the panel and
// would dilute a mean. FAIL-first: on the cyan → blue source the right third
// is darker (blue is the lower-luminance end), so this assertion fails there.
func TestHeroGradient(t *testing.T) {
	c, err := renderCanvas(card.Example())
	if err != nil {
		t.Fatalf("renderCanvas: %v", err)
	}
	m, ok := c.markByID("hero.left.number")
	if !ok {
		t.Fatal("hero.left.number was never drawn")
	}
	brightest := func(r image.Rectangle) float64 {
		best := -1.0
		for y := r.Min.Y; y < r.Max.Y; y++ {
			for x := r.Min.X; x < r.Max.X; x++ {
				p := at(c.img, x, y)
				if l := 0.2126*float64(p.R) + 0.7152*float64(p.G) + 0.0722*float64(p.B); l > best {
					best = l
				}
			}
		}
		if best < 0 {
			t.Fatalf("no pixels in %v", r)
		}
		return best
	}
	third := m.Rect.Dx() / 3
	left := brightest(image.Rect(m.Rect.Min.X, m.Rect.Min.Y, m.Rect.Min.X+third, m.Rect.Max.Y))
	right := brightest(image.Rect(m.Rect.Max.X-third, m.Rect.Min.Y, m.Rect.Max.X, m.Rect.Max.Y))
	// Accent → AccentHigh spans ~34 levels of luma; the two thirds' extremes
	// sit about two thirds of that apart. 15 is the "visibly a ramp" floor.
	if right-left < 15 {
		t.Errorf("hero number does not ramp Accent→AccentHigh: brightest left = %.1f, right = %.1f", left, right)
	}
}

// ---------------------------------------------------------------- hangul ---

// TestHangulHasGlyphs is the font-bundling contract: the Latin face has no
// Hangul, so the fallback must supply it, and Hangul must be twice the Latin
// advance (handover lesson 5).
func TestHangulHasGlyphs(t *testing.T) {
	fs, err := newFontSet()
	if err != nil {
		t.Fatalf("newFontSet: %v", err)
	}
	defer fs.Close()
	f, err := fs.face(32, wRegular)
	if err != nil {
		t.Fatalf("face: %v", err)
	}
	if _, ok := f.latin.GlyphAdvance('한'); ok {
		t.Fatal("JetBrains Mono unexpectedly has Hangul; the fallback test proves nothing")
	}
	for _, r := range []rune{'한', '글', '모', '델'} {
		if !f.hasGlyph(r) {
			t.Errorf("no glyph for %q in either face", r)
		}
	}
	latin, _ := f.fallback.GlyphAdvance('A')
	hangul, _ := f.fallback.GlyphAdvance('한')
	if hangul != 2*latin {
		t.Errorf("fallback Hangul advance = %v, want 2× the Latin advance %v", hangul, latin)
	}
}

// TestHangulRenders draws a Hangul model name and probes the pixels where the
// renderer said it put it. Without the fallback face the region would be blank
// or full of .notdef boxes; a blank region is the failure this catches.
func TestHangulRenders(t *testing.T) {
	c, err := renderCanvas(hangulSummary())
	if err != nil {
		t.Fatalf("renderCanvas: %v", err)
	}
	m, ok := c.markByID("footer.col0.row0")
	if !ok {
		t.Fatal("footer.col0.row0 was never drawn")
	}
	if !strings.Contains(m.Text, "한글") {
		t.Fatalf("footer model row = %q, want it to carry the Hangul name", m.Text)
	}
	if got := inkCount(c.img, m.Rect); got < 150 {
		t.Errorf("Hangul model name has %d ink pixels in %v, want >= 150", got, m.Rect)
	}
	// The header carries the Hangul file name too, on a different face size.
	h, ok := c.markByID("header.model")
	if !ok {
		t.Fatal("header.model was never drawn")
	}
	if got := inkCount(c.img, h.Rect); got < 150 {
		t.Errorf("Hangul file name has %d ink pixels in %v, want >= 150", got, h.Rect)
	}
}

// ------------------------------------------------------------- containment ---

// TestNothingLeavesTheContentBox is the layout guard. Neither the renderer nor
// this test can look at the picture, so the canvas records where it put every
// run of text and the test asserts those rectangles stay inside the panel and
// inside the content column.
func TestNothingLeavesTheContentBox(t *testing.T) {
	for name, s := range fixtures() {
		t.Run(name, func(t *testing.T) {
			c, err := renderCanvas(s)
			if err != nil {
				t.Fatalf("renderCanvas: %v", err)
			}
			for _, m := range c.marks {
				if m.Kind != "text" || m.Text == "" {
					continue
				}
				if m.Rect.Min.X < contentL {
					t.Errorf("%s (%q) starts at x=%d, left of the content box (%d)", m.ID, m.Text, m.Rect.Min.X, contentL)
				}
				if m.Rect.Max.X > contentR {
					t.Errorf("%s (%q) ends at x=%d, right of the content box (%d)", m.ID, m.Text, m.Rect.Max.X, contentR)
				}
				if m.Rect.Min.Y < panelTop || m.Rect.Max.Y > panelBottom {
					t.Errorf("%s (%q) spans y=%d..%d, outside the panel (%d..%d)", m.ID, m.Text, m.Rect.Min.Y, m.Rect.Max.Y, panelTop, panelBottom)
				}
			}
		})
	}
}

// TestFooterColumnsDoNotCollide keeps the four-column grid a grid: a long CPU
// name or a long GPU list must be truncated, never allowed to run into the
// next column.
func TestFooterColumnsDoNotCollide(t *testing.T) {
	long := card.Example()
	long.Host.CPU = "AMD Ryzen Threadripper PRO 7995WX 96-Core Processor With A Silly Name"
	long.Model.Name = "A Model Whose General Name Field Was Filled In By Somebody Enthusiastic"
	long.Server.Kind = "llama-server-with-an-unreasonably-long-kind"

	for name, s := range map[string]*tape.RunSummary{"example": card.Example(), "long": long} {
		t.Run(name, func(t *testing.T) {
			c, err := renderCanvas(s)
			if err != nil {
				t.Fatalf("renderCanvas: %v", err)
			}
			for i := 0; i < footerCols; i++ {
				left := contentL + i*footerColStep
				right := left + footerColW
				for _, part := range []string{"label", "row0", "row1", "row2", "row3"} {
					m, ok := c.markByID(colID(i, part))
					if !ok {
						t.Fatalf("%s was never drawn", colID(i, part))
					}
					if m.Rect.Min.X < left || m.Rect.Max.X > right {
						t.Errorf("%s (%q) spans x=%d..%d, outside its column (%d..%d)",
							m.ID, m.Text, m.Rect.Min.X, m.Rect.Max.X, left, right)
					}
				}
			}
		})
	}
}

// TestLongFlagsAreTruncated: the -ot group can be arbitrarily long and it is
// the last thing on the strip, so it must be the thing that gets cut.
func TestLongFlagsAreTruncated(t *testing.T) {
	s := card.Example()
	s.Server.Flags.OverrideTens = []string{strings.Repeat("blk.99.ffn_up_exps=CPU,", 40)}
	c, err := renderCanvas(s)
	if err != nil {
		t.Fatalf("renderCanvas: %v", err)
	}
	m, ok := c.markByID("strip.flags")
	if !ok {
		t.Fatal("strip.flags was never drawn")
	}
	if !strings.HasSuffix(m.Text, ellipsis) {
		t.Errorf("flags line was not truncated: %q", m.Text)
	}
	if m.Rect.Max.X > contentR {
		t.Errorf("flags line ends at x=%d, past the content box (%d)", m.Rect.Max.X, contentR)
	}
	// The five argument-starters survive the cut.
	for _, want := range []string{"-fa on", "-b 2048", "-ub 512", "-ctk q8_0", "-ctv q8_0"} {
		if !strings.Contains(m.Text, want) {
			t.Errorf("flags line lost %q: %q", want, m.Text)
		}
	}
}

// -------------------------------------------------------------- contract ---

// TestUnknownSummaryNeverInventsANumber: the all-unknown card must print "?"
// everywhere, and must not show a plausible-looking zero for anything it did
// not measure (repo rule; handover lesson 3).
func TestUnknownSummaryNeverInventsANumber(t *testing.T) {
	c, err := renderCanvas(&tape.RunSummary{})
	if err != nil {
		t.Fatalf("renderCanvas: %v", err)
	}
	want := map[string]string{
		"hero.left.number":  unknown,
		"hero.right.number": unknown,
		"header.model":      unknown,
		"header.runid":      unknown,
		"footer.col0.row0":  unknown,
		"footer.col1.row0":  unknown,
		"footer.col2.row0":  unknown,
	}
	for id, w := range want {
		m, ok := c.markByID(id)
		if !ok {
			t.Fatalf("%s was never drawn", id)
		}
		if m.Text != w {
			t.Errorf("%s = %q, want %q", id, m.Text, w)
		}
	}
	// Page faults were not observed, so the pill says "?" rather than "0.0".
	m, ok := c.markByID("memory.pill0")
	if !ok {
		t.Fatal("memory.pill0 was never drawn")
	}
	if m.Text != "maj/tok "+unknown {
		t.Errorf("maj-fault pill = %q, want %q", m.Text, "maj/tok "+unknown)
	}
	// The contention label is always printed (lesson 6), but "no" would be a
	// claim about a machine nobody read, so an empty summary gets "?".
	if m, ok := c.markByID("memory.pill2"); !ok || m.Text != "contended "+unknown {
		t.Errorf("contended pill = %q (drawn=%v), want %q", m.Text, ok, "contended "+unknown)
	}
	if m, ok := c.markByID("footer.col1.row3"); !ok || m.Text != unknown {
		t.Errorf("RAM cell = %q (drawn=%v), want %q — an unread size is not %q", m.Text, ok, unknown, "? GB")
	}
	// 2026-09-14 (TTP-54): the environment column became the strip's second
	// line, so the assertion that used to read footer.col3.row0 reads the line
	// that carries those fields now. The rule is the one it always was: the
	// verdicts are labelled and keep their "?", and the fields nobody read are
	// dropped rather than strung together as bare question marks.
	if m, ok := c.markByID("strip.env"); !ok || m.Text != "throttled ? · contended ?" {
		t.Errorf("environment line = %q (drawn=%v), want %q", m.Text, ok, "throttled ? · contended ?")
	}
}

// TestContentionReadingIsPrinted is the other half: once anything was read,
// the verdict is a measurement and prints as yes/no.
func TestContentionReadingIsPrinted(t *testing.T) {
	for _, tc := range []struct {
		name string
		ci   tape.ContentionInfo
		want string
	}{
		{"loadavg only", tape.ContentionInfo{LoadAvg1: 1.2}, "contended no"},
		{"other gpu procs", tape.ContentionInfo{OtherGPUProcs: 2}, "contended no"},
		{"flagged", tape.ContentionInfo{Contended: true, LoadAvg1: 12.3, Reasons: []string{"loadavg 12.3 > cores 8"}}, "contended yes"},
		{"nothing read", tape.ContentionInfo{}, "contended " + unknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := card.Example()
			s.Contention = tc.ci
			c, err := renderCanvas(s)
			if err != nil {
				t.Fatalf("renderCanvas: %v", err)
			}
			if m, _ := c.markByID("memory.pill2"); m.Text != tc.want {
				t.Errorf("contended pill = %q, want %q", m.Text, tc.want)
			}
		})
	}
}

// TestObservedZeroIsPrinted is the other half of the rule: a run that really
// did read /proc and really did see no faults prints 0.0, not "?".
func TestObservedZeroIsPrinted(t *testing.T) {
	c, err := renderCanvas(card.Example())
	if err != nil {
		t.Fatalf("renderCanvas: %v", err)
	}
	m, ok := c.markByID("memory.pill0")
	if !ok {
		t.Fatal("memory.pill0 was never drawn")
	}
	if m.Text != "maj/tok 0.0" {
		t.Errorf("maj-fault pill = %q, want %q", m.Text, "maj/tok 0.0")
	}
}

// TestConcurrentHeroShowsBothFigures: the aggregate is the headline and the
// per-stream rate must stay next to it, because "96.8 tok/s" alone would be a
// different claim (spec §4, track contract).
func TestConcurrentHeroShowsBothFigures(t *testing.T) {
	c, err := renderCanvas(card.ExampleConcurrent())
	if err != nil {
		t.Fatalf("renderCanvas: %v", err)
	}
	checks := map[string]string{
		"hero.left.number":   "72.9",
		"hero.left.sub1":     "8 × 9.1 tok/s per stream",
		"hero.left.eyebrow":  "AGGREGATE DECODE",
		"hero.right.number":  "2927",
		"hero.right.sub1":    "610 tok/s per stream", // 2026-09-13: no "N ×" for prefill, it is not a product
		"hero.right.eyebrow": "AGGREGATE PREFILL",
	}
	for id, want := range checks {
		m, ok := c.markByID(id)
		if !ok {
			t.Fatalf("%s was never drawn", id)
		}
		if m.Text != want {
			t.Errorf("%s = %q, want %q", id, m.Text, want)
		}
	}
	// The TTFT spread stays on the prefill side; it is the figure a concurrent
	// run is actually judged on.
	if m, _ := c.markByID("hero.right.sub2"); !strings.Contains(m.Text, "p95 1050 ms") {
		t.Errorf("prefill sub-line = %q, want the p50/p95 TTFT spread", m.Text)
	}
	// The cache pill names the verdict and the hit ratio rather than leaving a
	// bare percentage to be read as a speed.
	if m, _ := c.markByID("memory.pill1"); !strings.Contains(m.Text, "warm") || !strings.Contains(m.Text, "25%") {
		t.Errorf("cache pill = %q, want the label and the hit ratio", m.Text)
	}
	// And a cold run still says so in words, not as a silent 0%. The example
	// rig fits in VRAM and never goes cold (2026-09-13, TTP-28), so the cold
	// case is built here instead of taken from the fixture.
	cold := card.ExampleConcurrent()
	cold.Cache = tape.CacheSummary{PromptTotal: 512, Label: tape.CacheCold}
	cc, err := renderCanvas(cold)
	if err != nil {
		t.Fatalf("renderCanvas: %v", err)
	}
	if m, _ := cc.markByID("memory.pill1"); !strings.Contains(m.Text, "cold") {
		t.Errorf("a cold run's cache pill = %q, want it to say cold", m.Text)
	}
}

// TestSingleStreamPrefillIsNotAggregate: only a concurrent run gets the
// "aggregate" framing; a one-stream run says plain PREFILL.
func TestSingleStreamPrefillIsNotAggregate(t *testing.T) {
	c, err := renderCanvas(card.Example())
	if err != nil {
		t.Fatalf("renderCanvas: %v", err)
	}
	if m, _ := c.markByID("hero.right.eyebrow"); m.Text != "PREFILL" {
		t.Errorf("prefill eyebrow = %q, want %q", m.Text, "PREFILL")
	}
	if m, _ := c.markByID("hero.right.number"); m.Text != "610" {
		t.Errorf("prefill number = %q, want %q", m.Text, "610")
	}
	if m, _ := c.markByID("hero.left.eyebrow"); m.Text != "DECODE" {
		t.Errorf("decode eyebrow = %q, want %q", m.Text, "DECODE")
	}
}

// TestSampleLabel: below tape.MinDecodeTokens the summary carries
// DecodeLabel "sample", and the card must not call it decode (lesson 2).
func TestSampleLabel(t *testing.T) {
	s := card.Example()
	s.Timings.PredictedN = 19
	s.Timings.DecodeLabel = "sample"
	c, err := renderCanvas(s)
	if err != nil {
		t.Fatalf("renderCanvas: %v", err)
	}
	m, _ := c.markByID("hero.left.eyebrow")
	if m.Text != "SAMPLE" {
		t.Errorf("decode eyebrow = %q, want %q", m.Text, "SAMPLE")
	}
}

// TestPlacementBarSegments: one segment per device plus never-loaded, laid out
// left to right in placement order and filling the bar exactly.
func TestPlacementBarSegments(t *testing.T) {
	s := card.Example()
	s.Placement.NeverLoadedBytes = 4 * gib
	c, err := renderCanvas(s)
	if err != nil {
		t.Fatalf("renderCanvas: %v", err)
	}
	want := len(s.Placement.Devices) + 1
	var segs []mark
	for i := 0; i < want+2; i++ {
		if m, ok := c.markByID(segmentID(i)); ok {
			segs = append(segs, m)
		}
	}
	if len(segs) != want {
		t.Fatalf("drew %d bar segments, want %d", len(segs), want)
	}
	if segs[0].Rect.Min.X != contentL {
		t.Errorf("first segment starts at x=%d, want %d", segs[0].Rect.Min.X, contentL)
	}
	if last := segs[len(segs)-1]; last.Rect.Max.X != contentR {
		t.Errorf("last segment ends at x=%d, want %d", last.Rect.Max.X, contentR)
	}
	for i := 1; i < len(segs); i++ {
		if segs[i].Rect.Min.X != segs[i-1].Rect.Max.X {
			t.Errorf("gap between segment %d and %d: %d != %d", i-1, i, segs[i-1].Rect.Max.X, segs[i].Rect.Min.X)
		}
	}
	// The legend is searched rather than indexed: how many entries precede the
	// never-loaded one depends on how many devices the placement has, and a
	// fixed index would make this test quietly check a different entry the
	// next time the example's rig changes.
	if !hasLegendEntry(c, "never loaded") {
		t.Errorf("never-loaded legend is missing: %v", legendEntries(c))
	}
}

// legendEntries is every memory-legend label the canvas drew, in order.
func legendEntries(c *canvas) []string {
	var out []string
	for i := 0; ; i++ {
		m, ok := c.markByID(fmt.Sprintf("memory.legend.text%d", i))
		if !ok {
			return out
		}
		out = append(out, m.Text)
	}
}

// hasLegendEntry reports whether any legend entry starts with prefix.
func hasLegendEntry(c *canvas, prefix string) bool {
	for _, e := range legendEntries(c) {
		if strings.HasPrefix(e, prefix) {
			return true
		}
	}
	return false
}

// TestGPUSegmentsAreSubdivided: when the placement reports the three-way VRAM
// split, each GPU segment carries weights | kv | compute as three lightness
// steps of its own hue, they tile the segment exactly, and the legend names
// the three recorded totals.
func TestGPUSegmentsAreSubdivided(t *testing.T) {
	s := card.Example()
	// A device on the host as well, so the "VRAM only" check below has a CPU
	// segment to look at. The example rig is fully offloaded and has none
	// (2026-09-13, TTP-28).
	s.Placement.Devices = append(s.Placement.Devices, tape.DevicePlacement{
		Device:  tape.DeviceCPU,
		Bytes:   4 * gib,
		Classes: map[tape.TensorClass]int64{tape.ClassFFN: 4 * gib},
	})
	c, err := renderCanvas(s)
	if err != nil {
		t.Fatalf("renderCanvas: %v", err)
	}
	for _, seg := range []int{0, 1} { // GPU0, GPU1
		outer, ok := c.markByID(segmentID(seg))
		if !ok {
			t.Fatalf("%s was never drawn", segmentID(seg))
		}
		var parts []mark
		for p := 0; p < 3; p++ {
			m, ok := c.markByID(fmt.Sprintf("%s.part%d", segmentID(seg), p))
			if !ok {
				t.Fatalf("%s.part%d was never drawn", segmentID(seg), p)
			}
			parts = append(parts, m)
		}
		if parts[0].Rect.Min.X != outer.Rect.Min.X || parts[2].Rect.Max.X != outer.Rect.Max.X {
			t.Errorf("segment %d parts span %d..%d, want %d..%d", seg,
				parts[0].Rect.Min.X, parts[2].Rect.Max.X, outer.Rect.Min.X, outer.Rect.Max.X)
		}
		for i := 1; i < 3; i++ {
			if parts[i].Rect.Min.X != parts[i-1].Rect.Max.X {
				t.Errorf("segment %d has a gap between part%d and part%d", seg, i-1, i)
			}
			if _, ok := c.markByID(fmt.Sprintf("%s.div%d", segmentID(seg), i)); !ok {
				t.Errorf("segment %d is missing the divider before part%d", seg, i)
			}
		}
	}
	// The host gets no VRAM breakdown: the three-way split is a GPU one. It
	// has a split of its own since 2026-09-14 (TTP-63) — in RAM, and not — so
	// the assertion is that the CPU segment has two parts and never a third,
	// rather than that it has none. The example's 4 GiB CPU placement against
	// a 0.8 GiB file-backed RSS is exactly the shortfall case.
	if _, ok := c.markByID(segmentID(2) + ".part1"); !ok {
		t.Error("the CPU segment was not split into what is in RAM and what is not")
	}
	if _, ok := c.markByID(segmentID(2) + ".part2"); ok {
		t.Error("the CPU segment got a third part; the three-way split is VRAM only")
	}
	for _, want := range []string{"weights 42.5 GiB", "kv 2.6 GiB", "compute 1.5 GiB"} {
		if !hasLegendEntry(c, want) {
			t.Errorf("the legend does not name %q: %v", want, legendEntries(c))
		}
	}
}

// TestUnsplitPlacementDrawsWholeSegments: a placement with no KV or compute
// figures is not invented into a three-way split.
func TestUnsplitPlacementDrawsWholeSegments(t *testing.T) {
	s := card.Example()
	s.Placement.VRAMKVBytes = 0
	s.Placement.VRAMComputeBytes = 0
	c, err := renderCanvas(s)
	if err != nil {
		t.Fatalf("renderCanvas: %v", err)
	}
	if _, ok := c.markByID(segmentID(0) + ".part0"); ok {
		t.Error("GPU0 was subdivided without a reported VRAM split")
	}
	if _, ok := c.markByID("memory.legend.text3"); ok {
		t.Error("a breakdown legend entry was drawn without a reported VRAM split")
	}
}

// TestUnknownPlacementDrawsTrackOnly: with no placement the bar is an empty
// track and the legend says so, rather than a full bar of invented bytes.
func TestUnknownPlacementDrawsTrackOnly(t *testing.T) {
	c, err := renderCanvas(&tape.RunSummary{})
	if err != nil {
		t.Fatalf("renderCanvas: %v", err)
	}
	for _, m := range c.marks {
		if strings.HasPrefix(m.ID, "memory.bar.seg") {
			t.Fatalf("drew a placement segment for an unobserved placement: %s", m.ID)
		}
	}
	if m, ok := c.markByID("memory.legend.none"); !ok || m.Text != "no placement observed" {
		t.Errorf("legend = %q (drawn=%v), want %q", m.Text, ok, "no placement observed")
	}
	if m, ok := c.markByID("memory.bar.track"); !ok || m.Rect.Dx() != contentW {
		t.Errorf("bar track = %v (drawn=%v), want width %d", m.Rect, ok, contentW)
	}
}

// ----------------------------------------------------------------- write ---

// TestWriteCards writes the three contract cards to testdata/out for the
// lead's visual check and asserts each one decodes back at the right size.
func TestWriteCards(t *testing.T) {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", outDir, err)
	}
	cards := []struct {
		file string
		s    *tape.RunSummary
	}{
		{"card-single.png", card.Example()},
		{"card-concurrent.png", card.ExampleConcurrent()},
		{"card-unknown.png", &tape.RunSummary{}},
		{"card-hangul.png", hangulSummary()},
	}
	for _, c := range cards {
		path := filepath.Join(outDir, c.file)
		if err := Write(path, c.s); err != nil {
			t.Fatalf("Write(%s): %v", path, err)
		}
		f, err := os.Open(path)
		if err != nil {
			t.Fatalf("open %s: %v", path, err)
		}
		img, err := stdpng.Decode(f)
		f.Close()
		if err != nil {
			t.Fatalf("decode %s: %v", path, err)
		}
		if got, want := img.Bounds(), image.Rect(0, 0, Width, Height); got != want {
			t.Errorf("%s bounds = %v, want %v", c.file, got, want)
		}
		abs, _ := filepath.Abs(path)
		t.Logf("wrote %s", abs)
	}
}

// TestWriteLeavesNoTempFile: Write renames a temp file into place, and a
// failure must not leave a half-encoded card behind.
func TestWriteLeavesNoTempFile(t *testing.T) {
	dir := t.TempDir()
	if err := Write(filepath.Join(dir, "nested", "card.png"), card.Example()); err != nil {
		t.Fatalf("Write: %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(dir, "nested"))
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	if len(entries) == 1 && entries[0].Name() == "card.png" {
		// The card is meant to be handed to other people, so it must not
		// inherit os.CreateTemp's owner-only mode.
		info, err := entries[0].Info()
		if err != nil {
			t.Fatalf("stat: %v", err)
		}
		if got := info.Mode().Perm(); got != 0o644 {
			t.Errorf("card mode = %v, want %v", got, os.FileMode(0o644))
		}
	}
	if len(entries) != 1 || entries[0].Name() != "card.png" {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("directory holds %v, want just card.png", names)
	}
}

// TestUnsetFlagPrintsDefaultOnceTheArgvWasRead: the PNG's ENGINE column obeys
// the text card's rule (internal/card/format.go flagValue). "?" is "nobody
// looked"; a flag missing from an argv the recorder read is observed-as-absent
// and the server's own default is in effect.
func TestUnsetFlagPrintsDefaultOnceTheArgvWasRead(t *testing.T) {
	read := &tape.RunSummary{Server: tape.ServerInfo{
		PID:   4242,
		Args:  []string{"/usr/local/bin/llama-server", "-b", "2048"},
		Flags: tape.ServerFlags{Batch: "2048"},
	}}
	c, err := renderCanvas(read)
	if err != nil {
		t.Fatalf("renderCanvas: %v", err)
	}
	for id, want := range map[string]string{
		"footer.col2.row1": "fa · ctk · ctv at defaults",
		"footer.col2.row2": "b 2048 · ub default · ngl ?",
	} {
		m, ok := c.markByID(id)
		if !ok {
			t.Fatalf("%s was never drawn", id)
		}
		if m.Text != want {
			t.Errorf("%s = %q, want %q", id, m.Text, want)
		}
	}

	// No argv was read: every one of the five is genuinely unobserved.
	c, err = renderCanvas(&tape.RunSummary{Server: tape.ServerInfo{PID: 4242}})
	if err != nil {
		t.Fatalf("renderCanvas: %v", err)
	}
	for id, want := range map[string]string{
		"footer.col2.row1": "fa ? · ctk ? · ctv ?",
		"footer.col2.row2": "b ? · ub ? · ngl ?",
	} {
		m, ok := c.markByID(id)
		if !ok {
			t.Fatalf("%s was never drawn", id)
		}
		if m.Text != want {
			t.Errorf("%s = %q, want %q — a default was claimed from an argv nobody read", id, m.Text, want)
		}
	}
}

// TestAllDefaultFlagsStripFits: the worst case of the observed-as-absent rule
// is a server started with nothing but a model path, where all five
// argument-starters read "default". That strip must still fit the content box
// uncut — the five are exactly the fields the card exists to settle.
func TestAllDefaultFlagsStripFits(t *testing.T) {
	s := &tape.RunSummary{Server: tape.ServerInfo{
		Args: []string{"/usr/local/bin/llama-server", "-m", "/models/model.gguf"},
	}}
	c, err := renderCanvas(s)
	if err != nil {
		t.Fatalf("renderCanvas: %v", err)
	}
	m, ok := c.markByID("strip.flags")
	if !ok {
		t.Fatal("strip.flags was never drawn")
	}
	if strings.HasSuffix(m.Text, ellipsis) {
		t.Errorf("the all-default flag strip was truncated: %q", m.Text)
	}
	for _, want := range []string{"-fa default", "-b default", "-ub default", "-ctk default", "-ctv default"} {
		if !strings.Contains(m.Text, want) {
			t.Errorf("flags strip is missing %q: %q", want, m.Text)
		}
	}
	if m.Rect.Max.X > contentR {
		t.Errorf("flags line ends at x=%d, past the content box (%d)", m.Rect.Max.X, contentR)
	}
}

// TestQueuedStreamsAreNamed: the PNG says the same thing the text card does
// about a run that asked for more streams than the server has slots. The
// per-stream figure it sits under is the one the queue explains, so the note
// goes on that line.
func TestQueuedStreamsAreNamed(t *testing.T) {
	s := card.ExampleConcurrent()
	s.Server.NSlots = 4
	s.Aggregate.SlotsBusyMax = 4
	c, err := renderCanvas(s)
	if err != nil {
		t.Fatalf("renderCanvas: %v", err)
	}
	m, ok := c.markByID("hero.left.sub1")
	if !ok {
		t.Fatal("hero.left.sub1 was never drawn")
	}
	if !strings.Contains(m.Text, "4 slots, 4 queued") {
		t.Errorf("hero.left.sub1 = %q, want it to name the queue", m.Text)
	}
	if !strings.Contains(m.Text, "per stream") {
		t.Errorf("hero.left.sub1 lost the per-stream rate: %q", m.Text)
	}
	if strings.HasSuffix(m.Text, ellipsis) {
		t.Errorf("the queue note pushed the per-stream line past its column: %q", m.Text)
	}

	// Streams that all fit, a server whose slot count was never read, and a
	// single-stream run each report no queue: two of them have nothing to
	// report and the third would be guessing.
	for name, s := range map[string]*tape.RunSummary{
		"all fit":    card.ExampleConcurrent(),
		"no /props":  func() *tape.RunSummary { x := card.ExampleConcurrent(); x.Server.NSlots = 0; return x }(),
		"one stream": func() *tape.RunSummary { x := card.Example(); x.Server.NSlots = 0; return x }(),
	} {
		t.Run(name, func(t *testing.T) {
			c, err := renderCanvas(s)
			if err != nil {
				t.Fatalf("renderCanvas: %v", err)
			}
			m, ok := c.markByID("hero.left.sub1")
			if ok && strings.Contains(m.Text, "queued") {
				t.Errorf("hero.left.sub1 = %q, want no queue", m.Text)
			}
		})
	}
}

// TestAnswerCutPillFitsTheCard: a run that thought until it ran out of budget
// gets a fourth pill, and the memory band still fits inside the content box
// with it (TTP-20, 2026-09-13).
func TestAnswerCutPillFitsTheCard(t *testing.T) {
	s := *card.Example()
	s.Timings.PredictedN = 128
	s.Timings.ReasoningN = 128

	c := build(&s)
	var texts []string
	for _, p := range c.pills {
		texts = append(texts, p.text)
	}
	if got, want := len(c.pills), 4; got != want {
		t.Fatalf("pills = %d %v, want %d", got, texts, want)
	}
	if got := c.pills[3].text; got != "answer cut" {
		t.Errorf("last pill = %q, want %q", got, "answer cut")
	}

	// And it is absent when the run answered.
	s.Timings.ReasoningN = 96
	if got, want := len(build(&s).pills), 3; got != want {
		t.Errorf("pills = %d, want %d for a run that answered", got, want)
	}

	// The layout guard: nothing may leave the content box with the extra pill.
	s.Timings.ReasoningN = 128
	cv, err := renderCanvas(&s)
	if err != nil {
		t.Fatalf("renderCanvas: %v", err)
	}
	for _, m := range cv.marks {
		if m.Kind != "text" || m.Text == "" {
			continue
		}
		if m.Rect.Min.X < contentL || m.Rect.Max.X > contentR {
			t.Errorf("%s (%q) spans x=%d..%d, outside the content box (%d..%d)",
				m.ID, m.Text, m.Rect.Min.X, m.Rect.Max.X, contentL, contentR)
		}
	}
}

// TestEngineStringIKLlamaHasNoBuildNumber is internal/card's test of the same
// name, run against this package's copy of the rule: the two renderings of one
// summary must never disagree about how a version is spelled (TTP-33).
func TestEngineStringIKLlamaHasNoBuildNumber(t *testing.T) {
	cases := []struct {
		name string
		srv  tape.ServerInfo
		want string
	}{
		{"ik with a bare commit", tape.ServerInfo{Kind: tape.ServerIKLlama, Commit: "7b79b229"}, "ik_llama.cpp 7b79b229"},
		{"ik that did stamp a build keeps the pair", tape.ServerInfo{Kind: tape.ServerIKLlama, Build: "b3650", Commit: "7b79b229"}, "ik_llama.cpp b3650 (7b79b229)"},
		// 2026-09-13 TTP-37: the card's case for an ik attach that reported
		// neither a build nor a commit, run against this package's copy.
		{"ik with neither", tape.ServerInfo{Kind: tape.ServerIKLlama}, "ik_llama.cpp ?"},
		{"mainline with a bare commit keeps the brackets", tape.ServerInfo{Kind: tape.ServerLlamaCPP, Commit: "abcdef12"}, "llama-server (abcdef12)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := engineString(tc.srv); got != tc.want {
				t.Errorf("engineString = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestDraftClauseReplacesTheSecondDecodeLine pins TTP-30 on the share image.
// On a speculative run the decode column's second sub-line is the draft
// clause, whole and uncut, inside the column. On any other run it is what it
// was.
func TestDraftClauseReplacesTheSecondDecodeLine(t *testing.T) {
	const want = "draft DSpark-0.6B-Q8_0.gguf · n_max 3 · 60% accepted"
	single := card.ExampleSpeculative()
	single.Concurrency = 1
	for name, s := range map[string]*tape.RunSummary{
		"concurrent": card.ExampleSpeculative(),
		"single":     single,
	} {
		t.Run(name, func(t *testing.T) {
			c, err := renderCanvas(s)
			if err != nil {
				t.Fatalf("renderCanvas: %v", err)
			}
			m, ok := c.markByID("hero.left.sub2")
			if !ok {
				t.Fatal("hero.left.sub2 was never drawn")
			}
			if m.Text != want {
				t.Errorf("hero.left.sub2 = %q, want %q", m.Text, want)
			}
			if m.Rect.Max.X > heroSplitX-heroGutter {
				t.Errorf("the draft clause ends at x=%d, past the decode column (%d)", m.Rect.Max.X, heroSplitX-heroGutter)
			}
		})
	}

	t.Run("a run without a draft keeps its ITL", func(t *testing.T) {
		c, err := renderCanvas(card.Example())
		if err != nil {
			t.Fatalf("renderCanvas: %v", err)
		}
		if m, _ := c.markByID("hero.left.sub2"); !strings.Contains(m.Text, "ITL p50") || strings.Contains(m.Text, "draft") {
			t.Errorf("hero.left.sub2 = %q, want the ITL clause and no draft", m.Text)
		}
	})

	t.Run("zero drafted and an unread argv", func(t *testing.T) {
		s := card.ExampleSpeculative()
		zero := 0
		s.Timings.DraftN, s.Timings.DraftNAccepted = &zero, &zero
		s.Server.Flags.DraftModel, s.Server.Flags.DraftMax = "", ""
		c, err := renderCanvas(s)
		if err != nil {
			t.Fatalf("renderCanvas: %v", err)
		}
		if m, _ := c.markByID("hero.left.sub2"); m.Text != "draft ? · n_max ? · 0 drafted" {
			t.Errorf("hero.left.sub2 = %q", m.Text)
		}
	})
}
