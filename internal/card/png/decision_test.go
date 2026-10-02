package png

import (
	"bytes"
	"image"
	"image/color"
	stdpng "image/png"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/midagedev/toktape/internal/palette"
	"github.com/midagedev/toktape/internal/tape"
)

// decisionExample reads the synthetic decision tape the card is developed
// against (TTP-192, 2026-10-02).
func decisionExample(t *testing.T) *tape.RunSummary {
	t.Helper()
	tp, err := tape.Read(filepath.Join("..", "..", "tape", "testdata", "decision-example.tape"))
	if err != nil {
		t.Fatalf("read the decision example tape: %v", err)
	}
	if !tp.Summary.IsDecision() {
		t.Fatal("the example tape is not a decision tape")
	}
	return &tp.Summary
}

// fullDecision is the example with every optional row and a client clock.
func fullDecision(t *testing.T) *tape.RunSummary {
	s := *decisionExample(t)
	d := *s.Decision
	d.TimingSource = tape.DecisionTimingClient
	d.CacheHits = 12
	d.Reference = &tape.DecisionAgreement{File: "official-bf16.json", Questions: 40, MaxAbsDeltaP: 0.012, TopFlips: 1, BrierDelta: 0.0004}
	s.Decision = &d
	return &s
}

func markText(t *testing.T, c *canvas, id string) string {
	t.Helper()
	m, ok := c.markByID(id)
	if !ok {
		t.Fatalf("%s was never drawn", id)
	}
	return m.Text
}

// TestDecisionCardIsNotTheTokenCard: a decision tape takes the decision
// layout (the eyebrow says so) and every figure comes from the summary.
func TestDecisionCardIsNotTheTokenCard(t *testing.T) {
	c, err := renderCanvas(decisionExample(t))
	if err != nil {
		t.Fatalf("renderCanvas: %v", err)
	}
	for id, want := range map[string]string{
		"hero.left.eyebrow":  "DECISION",
		"hero.left.number":   "37.6",
		"hero.left.unit":     "ms",
		"hero.right.eyebrow": "COLD",
		"hero.right.number":  "640",
		"stat.short.value":   "37.4",
		"stat.long.value":    "417",
		"stat.p95.value":     "417",
		"stat.prefill.value": "5.2k",
	} {
		if got := markText(t, c, id); got != want {
			t.Errorf("%s = %q, want %q", id, got, want)
		}
	}
	if got := markText(t, c, "hero.left.sub1"); !strings.Contains(got, "engine") {
		t.Errorf("hero sub1 = %q, want the timing word engine", got)
	}
	for _, m := range c.marks {
		if strings.Contains(strings.ToLower(m.Text), "decode") || strings.Contains(m.Text, "38.8") {
			t.Errorf("%s = %q: a token-card word or Cloudflare's figure on the decision card", m.ID, m.Text)
		}
	}
}

// TestDecisionNoteShows: the synthetic tape says SYNTHETIC on the card, whole.
func TestDecisionNoteShows(t *testing.T) {
	s := decisionExample(t)
	c, err := renderCanvas(s)
	if err != nil {
		t.Fatalf("renderCanvas: %v", err)
	}
	var got []string
	for i := 0; i < 3; i++ {
		if m, ok := c.markByID("strip.line" + strconv.Itoa(i)); ok {
			got = append(got, m.Text)
		}
	}
	if joined := strings.Join(got, " "); joined != s.Note {
		t.Errorf("the strip carries %q, want the whole note %q", joined, s.Note)
	}
	if _, ok := c.markByID("strip.verified"); ok {
		t.Error("a tape with a note still says 'verified by toktape'")
	}
}

// TestDecisionOneLitFigure pins the emphasis contract: the warm median is the
// card's one accent figure; the cold figure beside it and every stat cell are
// Text, never the accent.
func TestDecisionOneLitFigure(t *testing.T) {
	c, err := renderCanvas(decisionExample(t))
	if err != nil {
		t.Fatalf("renderCanvas: %v", err)
	}
	accent, high, text := palette.RGBA(palette.Accent), palette.RGBA(palette.AccentHigh), palette.RGBA(palette.Text)
	const minPixels = 200

	left, _ := c.markByID("hero.left.number")
	ramp := hGradient{x0: left.Rect.Min.X, x1: left.Rect.Max.X, c0: accent, c1: high}
	if n := countPixels(c.img, left.Rect, func(x, _ int, p color.RGBA) bool { return p == ramp.At(x, 0) }); n < minPixels {
		t.Errorf("warm median: %d pixels on the accent ramp, want >= %d", n, minPixels)
	}

	notAccent := func(id string) {
		m, ok := c.markByID(id)
		if !ok {
			t.Fatalf("%s was never drawn", id)
		}
		r := hGradient{x0: m.Rect.Min.X, x1: m.Rect.Max.X, c0: accent, c1: high}
		if n := countPixels(c.img, m.Rect, func(x, _ int, p color.RGBA) bool {
			return p == accent || p == high || p == r.At(x, 0)
		}); n > 0 {
			t.Errorf("%s: %d accent pixels, want 0", id, n)
		}
	}
	right, _ := c.markByID("hero.right.number")
	if n := countPixels(c.img, right.Rect, func(_, _ int, p color.RGBA) bool { return p == text }); n < minPixels {
		t.Errorf("cold figure: %d pixels in Text, want >= %d", n, minPixels)
	}
	notAccent("hero.right.number")
	for _, k := range []string{"short", "long", "p95", "prefill", "throughput"} {
		notAccent("stat." + k + ".value")
	}
}

// TestDecisionInsideTheContentBox: no drawn text leaves the content box or is
// cut, and the strip rule stays above the strip's lines, on the example, on a
// variant with every optional row and a client clock, and on an empty tape.
func TestDecisionInsideTheContentBox(t *testing.T) {
	for name, s := range map[string]*tape.RunSummary{
		"example": decisionExample(t),
		"full":    fullDecision(t),
		"empty":   {Mode: tape.ModeDecision},
	} {
		t.Run(name, func(t *testing.T) {
			c, err := renderCanvas(s)
			if err != nil {
				t.Fatalf("renderCanvas: %v", err)
			}
			box := image.Rect(contentL, 0, contentR, Height)
			for _, m := range c.marks {
				if m.Kind != "text" || m.Text == "" {
					continue
				}
				if !m.Rect.In(box) {
					t.Errorf("%s %q at %v leaves the content box", m.ID, m.Text, m.Rect)
				}
				if strings.HasSuffix(m.Text, "…") {
					t.Errorf("%s was truncated: %q", m.ID, m.Text)
				}
			}
			rule, _ := c.markByID("rule.strip")
			for _, m := range c.marks {
				if strings.HasPrefix(m.ID, "strip.") && m.Kind == "text" && m.Rect.Min.Y <= rule.Rect.Max.Y {
					t.Errorf("%s at %v touches the strip rule at %v", m.ID, m.Rect, rule.Rect)
				}
			}
		})
	}
}

// TestDecisionClientTimedCard: a client-timed tape says so beside the figure
// and in the strip; Cache and Reference appear only when the tape has them.
func TestDecisionClientTimedCard(t *testing.T) {
	c, _ := renderCanvas(decisionExample(t))
	if _, ok := c.markByID("stat.extra.cache"); ok {
		t.Error("a cache line without a cache hit")
	}
	if _, ok := c.markByID("stat.extra.reference"); ok {
		t.Error("a reference line without a reference file")
	}
	for _, m := range c.marks {
		if strings.Contains(m.Text, "network and JSON") {
			t.Errorf("%s: an engine-timed tape carries the client-timing caveat", m.ID)
		}
	}

	c, err := renderCanvas(fullDecision(t))
	if err != nil {
		t.Fatalf("renderCanvas: %v", err)
	}
	if got := markText(t, c, "hero.left.sub1"); !strings.Contains(got, "client, end to end") {
		t.Errorf("hero sub1 = %q, want the client timing word", got)
	}
	if got := markText(t, c, "strip.line0"); !strings.Contains(got, "network and JSON are inside every latency") {
		t.Errorf("strip line 0 = %q, want the client-timing caveat", got)
	}
	if got := markText(t, c, "stat.extra.cache"); !strings.Contains(got, "12 hits") {
		t.Errorf("cache line = %q", got)
	}
	if got := markText(t, c, "stat.extra.reference"); !strings.Contains(got, "0.012") || !strings.Contains(got, "official-bf16.json") {
		t.Errorf("reference line = %q", got)
	}
}

// TestDecisionNoteless: without a note and with engine timing the strip is the
// token card's own "verified by toktape".
func TestDecisionNoteless(t *testing.T) {
	s := *decisionExample(t)
	s.Note = ""
	c, _ := renderCanvas(&s)
	if got := markText(t, c, "strip.verified"); !strings.EqualFold(got, "verified by toktape") {
		t.Errorf("strip = %q", got)
	}
}

// TestDecisionRenderIsDeterministic: the replay contract holds for this card
// too — two renders of one tape are byte-identical.
func TestDecisionRenderIsDeterministic(t *testing.T) {
	encode := func() []byte {
		img, err := Render(decisionExample(t))
		if err != nil {
			t.Fatalf("Render: %v", err)
		}
		var buf bytes.Buffer
		if err := stdpng.Encode(&buf, img); err != nil {
			t.Fatalf("Encode: %v", err)
		}
		return buf.Bytes()
	}
	if !bytes.Equal(encode(), encode()) {
		t.Error("two renders of the same decision tape differ")
	}
}

// TestDecisionWithoutSummaryDoesNotPanic: ModeDecision with no DecisionSummary
// draws every figure as "?".
func TestDecisionWithoutSummaryDoesNotPanic(t *testing.T) {
	c, err := renderCanvas(&tape.RunSummary{Mode: tape.ModeDecision})
	if err != nil {
		t.Fatalf("renderCanvas: %v", err)
	}
	if got := markText(t, c, "hero.left.number"); got != "?" {
		t.Errorf("hero number = %q, want ?", got)
	}
}
