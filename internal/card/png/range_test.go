package png

import (
	"testing"

	"github.com/midagedev/toktape/internal/tape"
)

// concurrentSparseMoE is the four-stream sparse take of internal/bandwidth's
// fourStreamSparseMoE, rebuilt here a third time (internal/card, its test
// files and this package each need their own copy; the derivation comment
// lives on the bandwidth one). 2.970 GB active a token, 0.832 GB of it the
// routed stack, 4 streams at 37.6 tok/s, one 750 GB/s GPU: bounds 112 and
// 206 GB/s, 15 and 27 % of peak.
func concurrentSparseMoE() *tape.RunSummary {
	return &tape.RunSummary{
		Model: tape.ModelInfo{
			NExperts:            256,
			NExpertsUsed:        8,
			ActiveBytesPerToken: 2_970_000_000,
		},
		Host: tape.HostInfo{
			GPUs: []tape.GPUInfo{{Index: 0, PeakBandwidthBytesPerSec: 750_000_000_000}},
		},
		Placement: tape.PlacementSummary{
			Devices: []tape.DevicePlacement{{
				Device:              "GPU0",
				ActiveBytesPerToken: 2_970_000_000,
				Classes: map[tape.TensorClass]int64{
					tape.ClassAttention: 1_092_000_000,
					tape.ClassOutput:    540_000_000,
					tape.ClassOther:     288_000_000,
					tape.ClassExperts:   26_842_000_000,
				},
			}},
		},
		Concurrency: 4,
		Timings: tape.TimingsSummary{
			PredictedPerSecond:            37.6,
			EffectiveBandwidthBytesPerSec: 111_672_000_000,
		},
		Aggregate: tape.AggregateTimings{
			Streams:                     4,
			AggregatePredictedPerSecond: 149.0,
			PerStreamPredictedPerSecond: 37.6,
		},
	}
}

// TestBandwidthRangeAgreesWithTheTextCard (2026-09-16): wherever this card
// renders the bandwidth clause it must print the same bounds the text card
// prints — the wording is this card's ("effective", "·"), the figures are
// neither's, they are internal/bandwidth's.
//
// Where that is: bandwidthString feeds the single-stream hero's sub1 line
// (buildHero); a concurrent hero carries no bandwidth clause at all, before
// this round and after it, because its two sub-rows are spoken for by the
// stream arithmetic and the token count. The renderer still computes through
// CombinedRange, so the clause it would print cannot disagree with the text
// card, and this test holds it to that on a concurrent summary directly.
func TestBandwidthRangeAgreesWithTheTextCard(t *testing.T) {
	got := bandwidthString(concurrentSparseMoE())
	if want := "≈ 112–206 GB/s effective · 15–27% of peak"; got != want {
		t.Errorf("bandwidthString = %q, want %q", got, want)
	}

	// The en dash the range prints must have a glyph in the faces this card
	// draws with: a visible tofu box is a wrong card (font.go), and a missing
	// rune is exactly that.
	fs, err := newFontSet()
	if err != nil {
		t.Fatalf("newFontSet: %v", err)
	}
	defer fs.Close()
	f, err := fs.face(32, wRegular)
	if err != nil {
		t.Fatalf("face: %v", err)
	}
	for _, r := range []rune{'–'} {
		if !f.hasGlyph(r) {
			t.Errorf("no glyph for %q in either face; the range would draw tofu", r)
		}
	}
}
