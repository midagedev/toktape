package bandwidth

import (
	"testing"

	"github.com/midagedev/toktape/internal/placement"
	"github.com/midagedev/toktape/internal/tape"
)

// engineDeviceFixture is one CPU device an engine might report (2026-09-15,
// ExLlamaV3): a full expert stack in host RAM, no per-device active bytes —
// the normal case, because an engine that swaps experts between devices has
// no static answer to what one device is read for.
func engineDeviceFixture() tape.DevicePlacement {
	return tape.DevicePlacement{
		Device:  tape.DeviceCPU,
		Bytes:   105_151_541_665,
		Classes: map[tape.TensorClass]int64{tape.ClassExperts: 105_151_541_665},
	}
}

// engineModelFixture pairs it with the model figure the engine reported.
func engineModelFixture() tape.ModelInfo {
	return tape.ModelInfo{
		ActiveBytesPerToken: 7_000_000_000,
		NExperts:            288,
		NExpertsUsed:        8,
	}
}

// TestActiveBytesOnEngineSource: the class-proportion fallback does not run
// for an engine placement. The same device figures under gguf+args estimate
// from the class totals; under engine they read as 0, so no RAM or verify
// line is ever computed from a split nobody measured. A per-device figure the
// engine did send still wins — that one is a report, not an estimate.
func TestActiveBytesOnEngineSource(t *testing.T) {
	mk := func(source string) *tape.RunSummary {
		return &tape.RunSummary{
			Model:     engineModelFixture(),
			Placement: tape.PlacementSummary{Source: source, Devices: []tape.DevicePlacement{engineDeviceFixture()}},
		}
	}
	gguf := mk(placement.SourceGGUFArgs)
	if got := activeBytesOn(gguf.Placement.Devices[0], gguf, false); got == 0 {
		t.Errorf("activeBytesOn(gguf+args, experts 105 GB x 8/288) = 0, want the class estimate")
	}
	engine := mk(placement.SourceEngine)
	if got := activeBytesOn(engine.Placement.Devices[0], engine, false); got != 0 {
		t.Errorf("activeBytesOn(engine, no per-device figure) = %d, want 0 — the fallback must not run", got)
	}

	// The engine that DID answer: the recorded figure is used verbatim,
	// whatever the source.
	reported := engineDeviceFixture()
	reported.ActiveBytesPerToken = 2_500_000_000
	engine.Placement.Devices[0] = reported
	if got := activeBytesOn(reported, engine, false); got != 2_500_000_000 {
		t.Errorf("activeBytesOn(engine, reported 2.5 GB) = %d, want 2500000000", got)
	}
}

// TestSpeculativeEngineGuard: without the guard, an engine placement with no
// CPU active bytes left the verify arithmetic running on class totals alone,
// drove cpuOncePerStep negative, and printed a per-step RAM figure nobody
// measured. With it, Speculative reports nothing — absent, not "?".
func TestSpeculativeEngineGuard(t *testing.T) {
	accepted, draft := 300, 400
	s := &tape.RunSummary{
		Model:     engineModelFixture(),
		Placement: tape.PlacementSummary{Source: placement.SourceEngine, Devices: []tape.DevicePlacement{engineDeviceFixture()}},
		Timings: tape.TimingsSummary{
			DraftN:         &draft,
			DraftNAccepted: &accepted,
		},
		Host: tape.HostInfo{RAMBytesPerSec: 230_000_000_000},
	}
	// The pieces Speculative needs to get past its early returns: a decode
	// window and an aggregate count. Without them it gives up for other
	// reasons and the guard is not what is being tested.
	streams := 2
	s.Concurrency = streams
	s.Aggregate = tape.AggregateTimings{
		Streams:                     streams,
		TotalPredictedN:             480,
		AggregatePredictedPerSecond: 39.6,
	}
	if _, ok := Speculative(s); ok {
		t.Error("Speculative(engine, no per-device active bytes) = ok, want not ok")
	}
	// RAM equally: the engine's silence is not a mixed placement to split.
	if _, ok := RAM(s); ok {
		t.Error("RAM(engine, no per-device active bytes) = ok, want not ok")
	}

	// The engine that DID send a consistent split gets both lines: they are
	// built from its own figures.
	devs := []tape.DevicePlacement{
		{Device: "GPU0", Bytes: 40_000_000_000, Classes: map[tape.TensorClass]int64{tape.ClassAttention: 8_000_000_000, tape.ClassExperts: 30_000_000_000, tape.ClassEmbed: 2_000_000_000}, ActiveBytesPerToken: 3_000_000_000},
		{Device: "GPU1", Bytes: 20_000_000_000, Classes: map[tape.TensorClass]int64{tape.ClassExperts: 19_000_000_000, tape.ClassOutput: 1_000_000_000}, ActiveBytesPerToken: 1_500_000_000},
		{Device: tape.DeviceCPU, Bytes: 105_151_541_665, Classes: map[tape.TensorClass]int64{tape.ClassExperts: 105_151_541_665}, ActiveBytesPerToken: 2_500_000_000},
	}
	s.Placement.Devices = devs
	s.Host.GPUs = []tape.GPUInfo{
		{Index: 0, Name: "RTX A6000", PeakBandwidthBytesPerSec: 768_000_000_000},
		{Index: 1, Name: "RTX 3090", PeakBandwidthBytesPerSec: 936_200_000_000},
	}
	ram, ok := RAM(s)
	if !ok {
		t.Fatal("RAM(engine with a consistent split) = not ok, want ok")
	}
	if ram.ActiveBytesPerToken != 2_500_000_000 {
		t.Errorf("RAM active = %d, want the engine's 2500000000", ram.ActiveBytesPerToken)
	}
	if !ram.Exact {
		t.Error("RAM.Exact = false, want true — the engine's own figure is the record")
	}
}
