package bandwidth

import (
	"strings"
	"testing"

	"github.com/midagedev/toktape/internal/placement"
	"github.com/midagedev/toktape/internal/tape"
)

// q6kSummary is the real q6k tape's placement shape (lead, 2026-09-15): a
// one-GPU run whose placement ESTIMATE — source "gguf+args" — split the model
// across both cards, because the server was started under
// CUDA_VISIBLE_DEVICES, which the argument parser cannot see. GPU1 was
// estimated to hold 14.04 GB and held one MiB, and the ceiling built on that
// split printed "46 % of peak" against a card this run never touched.
func q6kSummary() *tape.RunSummary {
	return &tape.RunSummary{
		Model: tape.ModelInfo{ActiveBytesPerToken: 2969684480},
		Host: tape.HostInfo{GPUs: []tape.GPUInfo{
			{Index: 0, Name: "NVIDIA RTX A6000", PeakBandwidthBytesPerSec: 768_000_000_000},
			{Index: 1, Name: "NVIDIA GeForce RTX 3090", PeakBandwidthBytesPerSec: 936_200_000_000},
		}},
		Placement: tape.PlacementSummary{
			Source: placement.SourceGGUFArgs,
			Devices: []tape.DevicePlacement{
				{Device: tape.DeviceCPU, Bytes: 540344320},
				{Device: "GPU0", Bytes: 14713174016, ActiveBytesPerToken: 1274034176},
				{Device: "GPU1", Bytes: 14043812352, ActiveBytesPerToken: 1695650304},
			},
		},
		GPUsAtEnd: []tape.GPUSample{
			{Index: 0, UsedBytes: 30438064128, ProcBytes: 30427578368},
			{Index: 1, UsedBytes: 1048576},
		},
		Timings: tape.TimingsSummary{EffectiveBandwidthBytesPerSec: 391804799788},
	}
}

// TestPlacementContradicted: the predicate's five cases. Only an estimated
// placement can be contradicted, only a device the reading actually saw, and
// only an estimate big enough to be a split worth defending.
func TestPlacementContradicted(t *testing.T) {
	// The real shape: GPU1 is estimated to hold 14.04 GB and held one MiB.
	s := q6kSummary()
	dev, ok := PlacementContradicted(s)
	if !ok || dev != 1 {
		t.Fatalf("PlacementContradicted = GPU%d, %v; want GPU1, true", dev, ok)
	}

	// A placement the measurement agrees with is not contradicted: GPU1
	// holds what the estimate says it holds.
	s = q6kSummary()
	s.GPUsAtEnd[1] = tape.GPUSample{Index: 1, UsedBytes: 14043812352, ProcBytes: 14000000000}
	if _, ok := PlacementContradicted(s); ok {
		t.Error("PlacementContradicted fired on a placement the measurement agrees with")
	}

	// An engine placement is the engine's own report, not an estimate to
	// check against the box: never contradicted, whatever the readings say.
	s = q6kSummary()
	s.Placement.Source = placement.SourceEngine
	if _, ok := PlacementContradicted(s); ok {
		t.Error("PlacementContradicted fired on an engine placement")
	}

	// A device the end reading never saw is a missing view, not a
	// contradiction — GPUsAtEnd may simply have been unreadable.
	s = q6kSummary()
	s.GPUsAtEnd = s.GPUsAtEnd[:1]
	if _, ok := PlacementContradicted(s); ok {
		t.Error("PlacementContradicted fired on a device missing from GPUsAtEnd")
	}

	// 900 MiB is under the 1 GiB floor: an estimate that small is not a
	// split worth defending, and one MiB held against it is not a
	// contradiction.
	s = q6kSummary()
	s.Placement.Devices[2].Bytes = 943718400 // 900 MiB
	if _, ok := PlacementContradicted(s); ok {
		t.Error("PlacementContradicted fired on a device estimated under 1 GiB")
	}

	// ProcBytes is the measured hold when the server's own VRAM was read,
	// UsedBytes otherwise: a card holding the weights in another process
	// still agrees with the estimate.
	s = q6kSummary()
	s.GPUsAtEnd[1] = tape.GPUSample{Index: 1, UsedBytes: 14043812352}
	if _, ok := PlacementContradicted(s); ok {
		t.Error("PlacementContradicted ignored UsedBytes when ProcBytes was not read")
	}
}

// TestContradictedPlacementHasNoCeiling: the derived figures stand down and
// the measured one does not. Combined keeps the recorded figure — it is the
// run's own measurement — while Ceiling and OfPeak, which are built from the
// split, report nothing.
func TestContradictedPlacementHasNoCeiling(t *testing.T) {
	s := q6kSummary()
	if _, ok := Ceiling(s); ok {
		t.Error("Ceiling derived a figure from a contradicted placement")
	}
	if _, ok := OfPeak(s); ok {
		t.Error("OfPeak derived a ratio from a contradicted placement")
	}
	if v, ok := Combined(s); !ok || v != 391804799788 {
		t.Errorf("Combined = %d, %v; want the measured 391804799788, true", v, ok)
	}

	e := Explain(s)
	if e.CeilingKnown || e.OfPeakKnown {
		t.Errorf("Explain still knows a ceiling (%d, %.1f %%): the listing and the card would disagree", e.CeilingBytesPerSec, e.OfPeak*100)
	}
	// The listing's line names the device and the two figures.
	text := e.String()
	if !strings.Contains(text, "GPU1") {
		t.Errorf("the listing does not name the contradicted device:\n%s", text)
	}
	for _, fig := range []string{"14.044 GB", "0.001 GB"} {
		if !strings.Contains(text, fig) {
			t.Errorf("the listing does not carry the figure %s:\n%s", fig, text)
		}
	}
	if i, j := strings.Index(text, "placement ?"), strings.Index(text, "ceiling ?"); i < 0 || j < 0 || i > j {
		t.Errorf("the contradiction line is not in front of the ceiling line:\n%s", text)
	}

	// The fixtures that already pass keep their ceilings: none of them
	// carries a reading that contradicts its placement, so the guard stands
	// aside and TestCeiling's own statuses are unchanged.
	for name, fn := range map[string]func() *tape.RunSummary{
		"ws":       wsSummary,
		"wsLegacy": wsLegacySummary,
		"hero":     heroSummary,
		"dense":    denseTwoGPU,
		"offload":  offloadedMoE,
	} {
		if c := Contradiction(fn()); c != nil {
			t.Errorf("Contradiction fired on the %s fixture: GPU%d placed %d, held %d",
				name, c.Device, c.PlacedBytes, c.MeasuredBytes)
		}
	}
}
