package tape

import "testing"

// TestResidencySplitsCPUPlacementByTheSample (TTP-63, 2026-09-14). The
// figures are the ws DeepSeek tape's: 388 GiB placed on CPU, 195 GiB of
// file-backed RSS, so 193 GiB are paged.
func TestResidencySplitsCPUPlacementByTheSample(t *testing.T) {
	const gib = 1 << 30
	p := PlacementSummary{Devices: []DevicePlacement{
		{Device: "GPU0", Bytes: 32 * gib},
		{Device: DeviceCPU, Bytes: 388 * gib},
	}}
	r := Residency(p, MemSample{RSSBytes: 196 * gib, RSSFileBytes: 195 * gib})
	if !r.Ok || r.Placed != 388*gib || r.Resident != 195*gib || r.Paged != 193*gib {
		t.Fatalf("got %+v", r)
	}

	// Everything placed is resident: nothing is paged, and the split is
	// still a reading.
	r = Residency(p, MemSample{RSSBytes: 400 * gib, RSSFileBytes: 399 * gib})
	if !r.Ok || r.Resident != 388*gib || r.Paged != 0 {
		t.Errorf("fully resident: got %+v", r)
	}

	// No /proc sample (a remote server): placed is known, the split is not.
	r = Residency(p, MemSample{})
	if r.Ok || r.Placed != 388*gib || r.Resident != 0 || r.Paged != 0 {
		t.Errorf("no sample: got %+v", r)
	}

	// Nothing on the CPU: nothing to split.
	r = Residency(PlacementSummary{Devices: p.Devices[:1]}, MemSample{RSSBytes: gib, RSSFileBytes: gib})
	if r.Ok || r.Placed != 0 {
		t.Errorf("no CPU placement: got %+v", r)
	}
}
