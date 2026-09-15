package tape

import "testing"

// TestResidencyRefusesAnEnginePlacement (2026-09-15, lead review): an engine
// placement is the engine's accounting, not a mapping of the model file, so
// RSSFile cannot say which of its CPU bytes are in RAM. The llama.cpp path
// keeps its split. FAIL-first: before the guard this returned Ok with 5.8 GiB
// "paged" for a placement the engine holds in its own memory.
func TestResidencyRefusesAnEnginePlacement(t *testing.T) {
	cpu := []DevicePlacement{{Device: DeviceCPU, Bytes: 105151541665}}
	mem := MemSample{RSSBytes: 103079215104, RSSFileBytes: 98876291584}

	if r := Residency(PlacementSummary{Devices: cpu, Source: "engine"}, mem); r.Ok {
		t.Errorf("engine placement: Residency = %+v, want Ok false", r)
	}
	if r := Residency(PlacementSummary{Devices: cpu, Source: "gguf+args"}, mem); !r.Ok || r.Paged == 0 {
		t.Errorf("gguf placement: Residency = %+v, want the split as before", r)
	}
}
