package tape

import "testing"

// TestLayoutNamesWhereTheWeightsLive (2026-09-14). The three shapes are three
// different machines, and the figures on a card only mean something once the
// reader knows which one produced them.
func TestLayoutNamesWhereTheWeightsLive(t *testing.T) {
	const gib = 1 << 30
	gpu := DevicePlacement{Device: "GPU0", Bytes: 40 * gib}
	gpu1 := DevicePlacement{Device: "GPU1", Bytes: 20 * gib}
	cpu := DevicePlacement{Device: DeviceCPU, Bytes: 388 * gib}

	for name, tc := range map[string]struct {
		devices []DevicePlacement
		want    MemoryShape
		share   float64
	}{
		"all on the cards":  {[]DevicePlacement{gpu, gpu1}, ShapeVRAM, 1},
		"all in host RAM":   {[]DevicePlacement{cpu}, ShapeHost, 0},
		"experts offloaded": {[]DevicePlacement{gpu, gpu1, cpu}, ShapeOffload, 60.0 / 448.0},
		"nothing derived":   {nil, ShapeUnknown, 0},
		"named but empty":   {[]DevicePlacement{{Device: "GPU0"}}, ShapeUnknown, 0},
	} {
		got := Layout(PlacementSummary{Devices: tc.devices})
		if got.Shape != tc.want {
			t.Errorf("%s: shape %q, want %q", name, got.Shape, tc.want)
		}
		if d := got.VRAMShare - tc.share; d > 1e-9 || d < -1e-9 {
			t.Errorf("%s: VRAM share %.4f, want %.4f", name, got.VRAMShare, tc.share)
		}
	}

	// The split is of what was placed, so the two sides always add up.
	got := Layout(PlacementSummary{Devices: []DevicePlacement{gpu, gpu1, cpu}})
	if got.VRAMBytes != 60*gib || got.HostBytes != 388*gib {
		t.Errorf("split is %d / %d bytes", got.VRAMBytes, got.HostBytes)
	}
}
