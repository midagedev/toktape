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
		got := Layout(PlacementSummary{Devices: tc.devices}, HostInfo{})
		if got.Shape != tc.want {
			t.Errorf("%s: shape %q, want %q", name, got.Shape, tc.want)
		}
		if d := got.VRAMShare - tc.share; d > 1e-9 || d < -1e-9 {
			t.Errorf("%s: VRAM share %.4f, want %.4f", name, got.VRAMShare, tc.share)
		}
	}

	// The split is of what was placed, so the two sides always add up.
	got := Layout(PlacementSummary{Devices: []DevicePlacement{gpu, gpu1, cpu}}, HostInfo{})
	if got.VRAMBytes != 60*gib || got.HostBytes != 388*gib {
		t.Errorf("split is %d / %d bytes", got.VRAMBytes, got.HostBytes)
	}
}

// TestLayoutOnAUnifiedHost (TTP-178). The shapes ask whether reading a token
// crosses a slow bus. On one pool there is no second bus, so a split between
// "GPU0" and "CPU" is bookkeeping and the shape is unified; the split is still
// reported.
func TestLayoutOnAUnifiedHost(t *testing.T) {
	const gib = 1 << 30
	split := PlacementSummary{Devices: []DevicePlacement{
		{Device: "GPU0", Bytes: 40 * gib},
		{Device: DeviceCPU, Bytes: 10 * gib},
	}}
	spark := HostInfo{GPUs: []GPUInfo{{Name: "NVIDIA GB10"}}}
	mac := HostInfo{CPU: "Apple M1 Pro"}
	for name, h := range map[string]HostInfo{"spark": spark, "mac": mac} {
		got := Layout(split, h)
		if got.Shape != ShapeUnified {
			t.Errorf("%s: shape %q, want %q", name, got.Shape, ShapeUnified)
		}
		if got.VRAMBytes != 40*gib || got.HostBytes != 10*gib || got.VRAMShare != 0.8 {
			t.Errorf("%s: split %d / %d share %.2f", name, got.VRAMBytes, got.HostBytes, got.VRAMShare)
		}
	}
	// All on one side is unified too: a Mac model "all in host RAM" ran on Metal.
	hostOnly := PlacementSummary{Devices: []DevicePlacement{{Device: DeviceCPU, Bytes: 10 * gib}}}
	if got := Layout(hostOnly, mac); got.Shape != ShapeUnified {
		t.Errorf("mac, cpu-only placement: shape %q", got.Shape)
	}
	// No placement stays unknown whatever the host is.
	if got := Layout(PlacementSummary{}, spark); got.Shape != ShapeUnknown {
		t.Errorf("no placement: shape %q", got.Shape)
	}
}
