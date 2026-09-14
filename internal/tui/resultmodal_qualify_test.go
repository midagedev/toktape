package tui

import (
	"strings"
	"testing"

	"github.com/midagedev/toktape/internal/card"
	"github.com/midagedev/toktape/internal/tape"
)

// The result modal is the third rendering of a run's two rates, after the text
// card and the share image, and it must qualify them the same way (TTP-65 and
// TTP-74, 2026-09-14). Before this it read the recorder's stored decode label
// directly and said nothing at all about a prompt too short to be a prefill
// measurement.
func TestTheModalQualifiesTheSameFiguresTheCardDoes(t *testing.T) {
	base := card.ExampleConcurrent()

	t.Run("a clean run is unqualified", func(t *testing.T) {
		dec, pre := heroFigures(*base)
		if strings.Contains(dec.caption, "sample") || strings.Contains(pre.caption, "short prompt") {
			t.Errorf("a clean run is qualified anyway: %q / %q", dec.caption, pre.caption)
		}
	})

	t.Run("a short prompt", func(t *testing.T) {
		s := *base
		s.Cache.PromptTotal, s.Cache.HitTokens = 63, 0
		s.Timings.PromptN, s.Timings.CacheN = 63, 0
		_, pre := heroFigures(s)
		if !strings.Contains(pre.caption, "short prompt") {
			t.Errorf("prefill caption = %q, want the short-prompt qualification", pre.caption)
		}
	})

	t.Run("a short generation the recorder still called decode", func(t *testing.T) {
		s := *base
		s.Timings.PredictedN, s.Timings.DecodeLabel = tape.MinDecodeTokens-1, "decode"
		dec, _ := heroFigures(s)
		if !strings.Contains(dec.caption, "sample") {
			t.Errorf("decode caption = %q, want the sample label the count earns", dec.caption)
		}
	})

	// TTP-83 (2026-09-14): a 10-token stream beside healthy ones keeps the
	// decode label — the aggregate is a rate — and says so beside it, as the
	// card's short_stream caveat and the image's eyebrow do.
	t.Run("a short stream inside a healthy mean", func(t *testing.T) {
		s := *base
		s.Aggregate.MinPredictedN = 10
		dec, _ := heroFigures(s)
		if !strings.Contains(dec.caption, "decode · short stream") || strings.Contains(dec.caption, "sample") {
			t.Errorf("decode caption = %q, want decode qualified by short stream", dec.caption)
		}
	})
}
