package bandwidth

import (
	"math"
	"testing"

	"github.com/midagedev/toktape/internal/placement"
	"github.com/midagedev/toktape/internal/tape"
)

// fourStreamSparseMoE is the take this round was opened about, reduced to what
// this package reads: Qwen3.6-35B-A3B — 256 experts, 8 used — whole on one
// GPU, four streams at 37.6 tok/s each. The card printed "≈ 112 GB/s, 15 % of
// peak" for it, and 112 is ActiveBytesPerToken × the PER-STREAM rate, which is
// the traffic only if all four tokens picked the same 8 experts.
//
// The class bytes make every figure in the tests below exact:
//
//	attention 1.092 + output 0.540 + other 0.288   = 1.920 GB read in full
//	experts   26.624 stack + 0.218 router/shared   = 26.842 GB of ClassExperts
//	active    1.920 + 0.218 + 26.624 × 8/256      = 2.970 GB per token
//
// so ExpertsSplit solves Dense to exactly 218,000,000 (the router and the
// shared expert), the routed stack a token reads is 26.624 GB × 8/256 =
// 0.832 GB, and the device's recorded ActiveBytesPerToken (TTP-68 shape) makes
// the per-device sum the record with no gap. The one GPU peaks at 750 GB/s, a
// figure chosen so the bounds render as the spec's own example: the low end
// 111.672/750 = 14.89 % prints "15 %", the high end 205.522/750 = 27.40 %
// prints "27 %".
func fourStreamSparseMoE() *tape.RunSummary {
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
			Source: placement.SourceGGUFArgs,
			Devices: []tape.DevicePlacement{{
				Device: "GPU0",
				// TTP-68 shape: the device states its own active bytes, so the
				// split agrees with the record by construction.
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
			EffectiveBandwidthBytesPerSec: 111_672_000_000, // 2.970 GB × 37.6
		},
		Aggregate: tape.AggregateTimings{
			Streams:                     4,
			AggregatePredictedPerSecond: 149.0,
			PerStreamPredictedPerSecond: 37.6,
		},
	}
}

// TestCombinedRangeFourStreamSparseMoE is the defect itself, made a contract:
// above one stream the combined figure is a range, its low end the figure the
// card used to print, its high end the same passes with no expert shared.
func TestCombinedRangeFourStreamSparseMoE(t *testing.T) {
	s := fourStreamSparseMoE()
	low, high, ok := CombinedRange(s)
	if !ok {
		t.Fatal("CombinedRange not ok on the four-stream sparse take")
	}
	if low != 111_672_000_000 {
		t.Errorf("low = %d, want 111672000000 (2.970 GB × 37.6 passes/s)", low)
	}
	if high != 205_521_600_000 {
		t.Errorf("high = %d, want 205521600000 ((2.970 GB + 3 × 0.832 GB) × 37.6)", high)
	}
	if low >= high {
		t.Errorf("low = %d >= high = %d; a sparse MoE over 4 streams must bound", low, high)
	}
	// The low end is the recorded per-stream figure, i.e. exactly what
	// Combined hands the card today — the range widens the answer, it does not
	// move it.
	if v, ok := Combined(s); !ok || v != low {
		t.Errorf("Combined = %d, %v; the range's low end should be the figure it printed", v, low == v)
	}
}

// TestCombinedRangeCollapsesWhenThereIsNothingToBound: one stream, a dense
// model over four streams, and a tape whose expert split will not solve all
// print one figure, because in each case a pass reads the weights once
// whatever rides it — the exact figure, not a bound.
func TestCombinedRangeCollapsesWhenThereIsNothingToBound(t *testing.T) {
	single := fourStreamSparseMoE()
	single.Concurrency = 1
	single.Aggregate = tape.AggregateTimings{}
	low, high, ok := CombinedRange(single)
	if !ok || low != high {
		t.Fatalf("one stream: CombinedRange = %d, %d, %v; want Combined's single figure", low, high, ok)
	}
	if v, _ := Combined(single); v != low {
		t.Errorf("one stream: low = %d, want Combined's %d", low, v)
	}

	// Dense at four streams: no expert counts, no experts class, the whole
	// 2.970 GB read in full every pass.
	dense := fourStreamSparseMoE()
	dense.Model = tape.ModelInfo{ActiveBytesPerToken: 2_970_000_000}
	dense.Placement.Devices[0].ActiveBytesPerToken = 2_970_000_000
	dense.Placement.Devices[0].Classes = map[tape.TensorClass]int64{
		tape.ClassAttention: 1_092_000_000,
		tape.ClassOutput:    540_000_000,
		tape.ClassOther:     1_338_000_000,
	}
	low, high, ok = CombinedRange(dense)
	if !ok || low != high {
		t.Fatalf("dense at 4 streams: CombinedRange = %d, %d, %v; want one figure", low, high, ok)
	}
	if low != 111_672_000_000 {
		t.Errorf("dense at 4 streams: low = %d, want 111672000000 (2.970 GB × 37.6)", low)
	}
}

// TestCombinedRangeKeepsCombinedsRefusals: the range may be printed exactly
// where Combined's single figure may, and nowhere else.
//
// One correction to the round's spec, verified against the code before this
// test was written: Combined refuses a nil summary, a missing recorded figure
// and an ENGINE placement — it does not refuse a mixed or a draft one. Those
// two are refused one layer up, by the card's branch order (bandwidth.RAM and
// verifyRAM print first), which this round does not touch. So a mixed GGUF
// take keeps a derivable range here, exactly as it keeps Combined's figure
// today, and the mixed test in internal/card is what pins that the card still
// prints the RAM row for it.
func TestCombinedRangeKeepsCombinedsRefusals(t *testing.T) {
	engine := fourStreamSparseMoE()
	engine.Placement.Source = placement.SourceEngine
	if low, high, ok := CombinedRange(engine); ok {
		t.Errorf("engine placement: CombinedRange = %d, %d; want no figure", low, high)
	}

	nilFig := fourStreamSparseMoE()
	nilFig.Timings.EffectiveBandwidthBytesPerSec = 0
	if _, _, ok := CombinedRange(nilFig); ok {
		t.Error("no recorded figure: want no range")
	}

	if _, _, ok := CombinedRange(nil); ok {
		t.Error("nil summary: want no range")
	}

	// A mixed placement: the range mirrors Combined (ok), because Combined is
	// ok for it — see this test's doc comment.
	mixed := fourStreamSparseMoE()
	mixed.Placement.Devices = []tape.DevicePlacement{
		{Device: "GPU0", ActiveBytesPerToken: 1_920_000_000, Classes: map[tape.TensorClass]int64{
			tape.ClassAttention: 1_092_000_000,
			tape.ClassOutput:    540_000_000,
			tape.ClassOther:     288_000_000,
		}},
		{Device: tape.DeviceCPU, ActiveBytesPerToken: 1_050_000_000, Classes: map[tape.TensorClass]int64{
			tape.ClassExperts: 26_842_000_000,
		}},
	}
	mLow, mHigh, mOK := CombinedRange(mixed)
	cFig, cOK := Combined(mixed)
	if mOK != cOK {
		t.Errorf("mixed placement: CombinedRange ok = %v, Combined ok = %v; the range must mirror the figure", mOK, cOK)
	}
	if mOK && mLow != 111_672_000_000 {
		t.Errorf("mixed placement: low = %d, want the same bounds (the buses moved, the read did not)", mLow)
	}
	if mOK && mHigh != 205_521_600_000 {
		t.Errorf("mixed placement: high = %d, want %d", mHigh, 205_521_600_000)
	}
	_ = cFig

	// A draft run: whatever Combined does today (ok, the figure stands), the
	// range does the same — the draft changes which clause the CARD prints
	// (verifyRAM), not this function's arithmetic.
	draft := fourStreamSparseMoE()
	n, accepted := 400, 260
	draft.Timings.DraftN, draft.Timings.DraftNAccepted = &n, &accepted
	dLow, dHigh, dOK := CombinedRange(draft)
	if dOK != true {
		t.Errorf("draft run: CombinedRange ok = %v, want true (Combined keeps the figure)", dOK)
	}
	if dLow != 111_672_000_000 || dHigh != 205_521_600_000 {
		t.Errorf("draft run: CombinedRange = %d, %d; want the same bounds as without the draft", dLow, dHigh)
	}
}

// TestOfPeakRange: the ratio pair over the same Ceiling OfPeak divides by,
// low ratio from low bytes, and nothing wherever that ceiling is not known.
func TestOfPeakRange(t *testing.T) {
	s := fourStreamSparseMoE()
	low, high, ok := OfPeakRange(s)
	if !ok {
		t.Fatal("OfPeakRange not ok on the four-stream sparse take")
	}
	if math.Abs(low-0.148896) > 1e-9 {
		t.Errorf("low ratio = %.6f, want 0.148896 (111.672 GB/s of 750 GB/s)", low)
	}
	if math.Abs(high-0.2740288) > 1e-9 {
		t.Errorf("high ratio = %.6f, want 0.2740288 (205.522 GB/s of 750 GB/s)", high)
	}

	// The same order CombinedRange gives: low from low bytes, high from high.
	bLow, bHigh, _ := CombinedRange(s)
	ceiling, _ := Ceiling(s)
	if math.Abs(low-float64(bLow)/float64(ceiling)) > 1e-12 || math.Abs(high-float64(bHigh)/float64(ceiling)) > 1e-12 {
		t.Errorf("OfPeakRange = %.6f, %.6f; want each bound over the ceiling %d", low, high, ceiling)
	}

	// No ceiling: an unread GPU peak means no ratio pair.
	noPeak := fourStreamSparseMoE()
	noPeak.Host.GPUs[0].PeakBandwidthBytesPerSec = 0
	if _, _, ok := OfPeakRange(noPeak); ok {
		t.Error("unknown ceiling: want no ratio pair")
	}

	// Where CombinedRange refuses, the ratio refuses with it.
	engine := fourStreamSparseMoE()
	engine.Placement.Source = placement.SourceEngine
	if _, _, ok := OfPeakRange(engine); ok {
		t.Error("engine placement: want no ratio pair")
	}
	if _, _, ok := OfPeakRange(nil); ok {
		t.Error("nil summary: want no ratio pair")
	}

	// One stream: the pair collapses to OfPeak's single ratio.
	single := fourStreamSparseMoE()
	single.Concurrency = 1
	single.Aggregate = tape.AggregateTimings{}
	low, high, ok = OfPeakRange(single)
	singleRatio, singleOK := OfPeak(single)
	if !ok || !singleOK || math.Abs(low-singleRatio) > 1e-12 || math.Abs(high-singleRatio) > 1e-12 {
		t.Errorf("one stream: OfPeakRange = %.6f, %.6f (%v), OfPeak = %.6f (%v); want the same single ratio",
			low, high, ok, singleRatio, singleOK)
	}

	// Dense at four streams: exact figure, exact ratio, still no bound.
	dense := fourStreamSparseMoE()
	dense.Model = tape.ModelInfo{ActiveBytesPerToken: 2_970_000_000}
	dense.Placement.Devices[0].ActiveBytesPerToken = 2_970_000_000
	dense.Placement.Devices[0].Classes = map[tape.TensorClass]int64{
		tape.ClassAttention: 1_092_000_000,
		tape.ClassOutput:    540_000_000,
		tape.ClassOther:     1_338_000_000,
	}
	low, high, ok = OfPeakRange(dense)
	if !ok || low != high {
		t.Fatalf("dense at 4 streams: OfPeakRange = %.6f, %.6f, %v; want one ratio", low, high, ok)
	}
}
