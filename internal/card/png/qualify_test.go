package png

import (
	"strings"
	"testing"

	"github.com/midagedev/toktape/internal/card"
	"github.com/midagedev/toktape/internal/tape"
)

// The image half of "a qualified figure cannot be rendered without its
// qualification" (TTP-74, 2026-09-14). internal/card/caveat_test.go asserts it
// for the text card; these assert that the share image, which is the thing
// most people actually see, says the same.

// TestTheShortPromptQualificationReachesTheEyebrow (TTP-65, 2026-09-14).
//
// The decode column's eyebrow already flips "decode" to "sample" when the
// generation was too short to be a rate, and it is the only place on the image
// that says so. A prompt too short to be a prefill measurement is the same
// defect one column over, and it is answered in the same slot.
func TestTheShortPromptQualificationReachesTheEyebrow(t *testing.T) {
	for name, mk := range map[string]func() *tape.RunSummary{
		"concurrent": card.ExampleConcurrent,
		"single":     card.Example,
	} {
		t.Run(name, func(t *testing.T) {
			long := mk()
			if card.ShortPrompt(long) {
				t.Fatalf("the fixture's prompt is already short; this test cannot show the change")
			}
			if m := eyebrowOf(t, long); strings.Contains(m, "SHORT PROMPT") {
				t.Errorf("a long prompt is qualified anyway: %q", m)
			}

			short := mk()
			short.Cache.PromptTotal, short.Cache.HitTokens = 63, 0
			short.Timings.PromptN, short.Timings.CacheN = 63, 0
			got := eyebrowOf(t, short)
			if !strings.Contains(got, "SHORT PROMPT") {
				t.Errorf("prefill eyebrow = %q, want the short-prompt qualification", got)
			}
			// And it stays inside its column, the way every hero string must.
			c := mustCanvas(t, short)
			m, _ := c.markByID("hero.right.eyebrow")
			if m.Rect.Max.X > contentR {
				t.Errorf("the eyebrow ends at x=%d, past the content box (%d)", m.Rect.Max.X, contentR)
			}
		})
	}
}

func mustCanvas(t *testing.T, s *tape.RunSummary) *canvas {
	t.Helper()
	c, err := renderCanvas(s)
	if err != nil {
		t.Fatalf("renderCanvas: %v", err)
	}
	return c
}

func eyebrowOf(t *testing.T, s *tape.RunSummary) string {
	t.Helper()
	m, ok := mustCanvas(t, s).markByID("hero.right.eyebrow")
	if !ok {
		t.Fatal("hero.right.eyebrow was never drawn")
	}
	return m.Text
}

// TestTheDraftClauseGivesUpItsLeastValuableElementFirst is TTP-69
// (2026-09-14): the hero column is 504 px and the ws draft model's file name
// is 46 characters, so something has to go. What went before this was whatever
// happened to be past the 504th pixel — on that card, the tail of the file
// name AND the block size AND the acceptance rate.
//
// The order is the one draftString documents: the model's name first, because
// it is printed whole on the FLAGS strip and on the text card; then the "of
// peak" ratio, which only means anything beside this box's own ceiling; then
// the bandwidth, which is on the text card's Decode row. The block size and
// the acceptance rate are the last two standing.
func TestTheDraftClauseGivesUpItsLeastValuableElementFirst(t *testing.T) {
	s := card.ExampleSpeculative()
	clause, fallbacks := draftString(s)
	if len(fallbacks) != 3 {
		t.Fatalf("the clause has %d fallbacks, want the three draftString documents", len(fallbacks))
	}
	for i, want := range [][]string{
		{"DSpark", "n_max 3", "60% accepted", "RAM/step", "of peak"},
		{"n_max 3", "60% accepted", "RAM/step", "of peak"},
		{"n_max 3", "60% accepted", "RAM/step"},
		{"n_max 3", "60% accepted"},
	} {
		got := clause
		if i > 0 {
			got = fallbacks[i-1]
		}
		for _, w := range want {
			if !strings.Contains(got, w) {
				t.Errorf("rung %d = %q, which lost %q", i, got, w)
			}
		}
	}
	if strings.Contains(fallbacks[0], "DSpark") {
		t.Errorf("the first fallback still names the model: %q", fallbacks[0])
	}
	if strings.Contains(fallbacks[1], "of peak") {
		t.Errorf("the second fallback still names the ratio: %q", fallbacks[1])
	}
	if strings.Contains(fallbacks[2], "GB/s") {
		t.Errorf("the last fallback still names a bandwidth: %q", fallbacks[2])
	}
}

// TestNoHeroSubLineIsEverCut: whatever the run, both sub-lines of both hero
// columns end inside their column and carry no ellipsis.
//
// This is the assertion the ws card would have failed. The fallback ladder is
// what makes it passable rather than the sizes, and it is checked against the
// longest strings the fixtures produce plus the one that caused the ticket: a
// 46-character draft model name.
func TestNoHeroSubLineIsEverCut(t *testing.T) {
	longDraft := card.ExampleSpeculative()
	longDraft.Server.Flags.DraftModel = "DeepSeek-V4.1-Flash-Fp8-128x742M-MXFP4_MOE.tl37.gguf"
	shortPrompt := card.ExampleSpeculative()
	shortPrompt.Cache.PromptTotal, shortPrompt.Timings.PromptN = 30, 30

	for name, s := range map[string]*tape.RunSummary{
		"example":        card.Example(),
		"concurrent":     card.ExampleConcurrent(),
		"speculative":    card.ExampleSpeculative(),
		"rounds":         card.ExampleRounds(),
		"sweep":          card.ExampleSweep(),
		"sharded":        card.ExampleSharded(),
		"long draft":     longDraft,
		"short prompt":   shortPrompt,
		"nothing at all": {},
	} {
		t.Run(name, func(t *testing.T) {
			c := mustCanvas(t, s)
			for id, right := range map[string]int{
				"hero.left.sub1":  heroSplitX - heroGutter,
				"hero.left.sub2":  heroSplitX - heroGutter,
				"hero.right.sub1": contentR,
				"hero.right.sub2": contentR,
			} {
				m, ok := c.markByID(id)
				if !ok {
					t.Fatalf("%s was never drawn", id)
				}
				if strings.Contains(m.Text, ellipsis) {
					t.Errorf("%s is cut: %q", id, m.Text)
				}
				if m.Rect.Max.X > right {
					t.Errorf("%s = %q ends at x=%d, past %d", id, m.Text, m.Rect.Max.X, right)
				}
			}
		})
	}
}

// TestTheVerifyStepBandwidthReplacesThePerAcceptedTokenOne is TTP-67
// (measured 2026-09-14).
//
// With a draft model the engine reads the weights once per verify step over a
// batch of (1 + drafted) tokens, so bytes per ACCEPTED token is not a rate the
// hardware experienced — it is the real rate divided by the acceptance. On the
// ws prose run that is 65.5 GB/s against 109.9, which reads as headroom where
// there is none. The image must not carry both, because two RAM bandwidths
// differing by the acceptance rate, with nothing to say why, is worse than one
// that is true.
func TestTheVerifyStepBandwidthReplacesThePerAcceptedTokenOne(t *testing.T) {
	s := card.ExampleSpeculative()
	bps, ofPeak, ok := card.VerifyRAM(s)
	if !ok {
		t.Fatal("the speculative fixture derives no verify-step figure")
	}
	if ofPeak <= 0 {
		t.Fatal("the speculative fixture derives no ratio; this test cannot check one")
	}
	got := bandwidthString(s)
	if !strings.Contains(got, "per verify step") {
		t.Errorf("bandwidth clause = %q, want the verify-step qualification", got)
	}
	if !strings.Contains(got, formatGBs(bps)) {
		t.Errorf("bandwidth clause = %q, want %s", got, formatGBs(bps))
	}
	if !strings.Contains(got, formatPct(ofPeak)) {
		t.Errorf("bandwidth clause = %q, want %s of peak", got, formatPct(ofPeak))
	}
	// The per-accepted-token figure is not printed beside it.
	if strings.Contains(got, formatGBs(s.Timings.EffectiveBandwidthBytesPerSec)) {
		t.Errorf("bandwidth clause = %q, which still carries the per-accepted-token figure", got)
	}
	// A run with no draft is untouched.
	if plain := bandwidthString(card.Example()); strings.Contains(plain, "verify") {
		t.Errorf("a run without a draft says %q", plain)
	}
}
