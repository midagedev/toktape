package bandwidth

// The over-ceiling refusals (2026-09-16, the lookup round). The qwen38
// Qwen3.8-Flash-Next tapes printed "≈ 1495 GB/s from RAM, 1033% of peak" and
// its siblings for a box whose host bus measures 144.8 GB/s: the CPU's active
// bytes carried a 26.8 GiB per-layer embedding table that is read by row, so
// the "streamed per token" model of the placement was wrong, not the bus. A
// figure that exceeds the bus it must have been carried on is not a reading
// of anything, and the honest output is no figure — these tests pin that
// refusal on the host side (RAM) and against Ceiling (Combined).
//
// The verify-step view takes no refusal, on purpose: its figure is an upper
// bound by construction and host-independent, so clearing the wall is as
// often a loose bound or a low DMI derivation as a wrong split. A draft of
// this round refused it too; TestRAMWithinSlackStands is the fixture that
// showed why not (the same step figure that reads 1.14x beside one ruler
// reads 0.94x beside another), and the refusal was dropped with its test.

import (
	"math"
	"testing"

	"github.com/midagedev/toktape/internal/tape"
)

// lookupDefectSummary is the defect's shape at its real size: the ws
// recording's placement with the CPU's active bytes inflated to 29.06 GB by
// a per-layer embedding table, on a host whose bus measures 115.8 GB/s.
func lookupDefectSummary() *tape.RunSummary {
	s := wsSummary()
	s.Host.RAMBytesPerSec = wsSTREAMBytesPerSec
	s.Host.RAMSource = tape.RAMSourceMeasured
	s.Placement.Devices[0].ActiveBytesPerToken = 29_056_138_240
	return s
}

// TestRAMRefusedWhenOverTheHostBus: the host-side figure at 5.1x the
// measured bus is refused, and the refusal names itself, the figure and the
// bus it exceeded.
func TestRAMRefusedWhenOverTheHostBus(t *testing.T) {
	s := lookupDefectSummary()
	if r, ok := RAM(s); ok {
		t.Fatalf("RAM ok at %s from a %s bus: a figure over the bus is not a reading of anything",
			gbps(r.BytesPerSec), gbps(wsSTREAMBytesPerSec))
	}
	ref := RefusedFigure(s)
	if ref == nil || ref.Which != RefusedRAM {
		t.Fatalf("RefusedFigure = %+v, want the ram refusal", ref)
	}
	want := int64(math.Round(29_056_138_240 * wsDecodeRate))
	if ref.BytesPerSec != want {
		t.Errorf("refusal figure = %d, want the side it refused (%d)", ref.BytesPerSec, want)
	}
	if ref.LimitBytesPerSec != wsSTREAMBytesPerSec {
		t.Errorf("refusal limit = %d, want the measured host bus %d", ref.LimitBytesPerSec, wsSTREAMBytesPerSec)
	}
}

// TestRAMWithinSlackStands: the slack exists for rounding and for a host
// figure derived from DDR speed x channels rather than measured, so a figure
// at 1.03x the bus is a figure, not a refusal.
func TestRAMWithinSlackStands(t *testing.T) {
	s := wsSummary()
	s.Host.RAMBytesPerSec = wsSTREAMBytesPerSec
	s.Host.RAMSource = tape.RAMSourceMeasured
	// The CPU active bytes that put the host figure at 1.03x the bus.
	cpuActive := int64(math.Round(1.03 * wsSTREAMBytesPerSec / wsDecodeRate))
	s.Placement.Devices[0].ActiveBytesPerToken = cpuActive
	r, ok := RAM(s)
	if !ok {
		t.Fatal("RAM refused at 1.03x the host bus; the slack is 5%")
	}
	if r.OfPeak < 1.02 || r.OfPeak > 1.04 {
		t.Errorf("OfPeak = %v, want ~1.03 carried as measured", r.OfPeak)
	}
	if ref := RefusedFigure(s); ref != nil {
		t.Errorf("RefusedFigure = %+v inside the slack, want none", ref)
	}
}

// TestCombinedRefusedWhenOverTheCeiling: the recorded figure at 10x the
// ceiling this placement allows is refused wherever the card would print it —
// Combined, CombinedRange, OfPeak — because a clause refused beside a ratio
// kept would be two clauses disagreeing about one run.
func TestCombinedRefusedWhenOverTheCeiling(t *testing.T) {
	s := wsSummary()
	s.Host.RAMBytesPerSec = wsSTREAMBytesPerSec
	s.Host.RAMSource = tape.RAMSourceMeasured
	ceiling, ok := Ceiling(s)
	if !ok {
		t.Fatal("the fixture must derive a ceiling for this test")
	}
	s.Timings.EffectiveBandwidthBytesPerSec = int64(10 * ceiling)
	if _, ok := Combined(s); ok {
		t.Fatal("Combined ok at 10x its ceiling")
	}
	if _, _, ok := CombinedRange(s); ok {
		t.Fatal("CombinedRange ok at 10x its ceiling")
	}
	if _, ok := OfPeak(s); ok {
		t.Fatal("OfPeak ok beside a figure refused over the ceiling")
	}
	ref := RefusedFigure(s)
	if ref == nil || ref.Which != RefusedCombined {
		t.Fatalf("RefusedFigure = %+v, want the combined refusal (the RAM side stands at 65.5 of 115.8)", ref)
	}
	if ref.LimitBytesPerSec != ceiling {
		t.Errorf("refusal limit = %d, want the ceiling %d", ref.LimitBytesPerSec, ceiling)
	}

	// Inside the slack the figure stands, over 1.0 and all: a small overshoot
	// is cache-hit territory and OfPeak's own contract returns it as
	// measured rather than clamped.
	s.Timings.EffectiveBandwidthBytesPerSec = int64(math.Round(1.04 * float64(ceiling)))
	if v, ok := Combined(s); !ok || v != s.Timings.EffectiveBandwidthBytesPerSec {
		t.Error("Combined refused inside the slack")
	}
	if r, ok := OfPeak(s); !ok || r < 1.03 || r > 1.05 {
		t.Errorf("OfPeak = %v ok %v, want ~1.04 as measured", r, ok)
	}
}
