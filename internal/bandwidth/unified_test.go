package bandwidth

import (
	"testing"

	"github.com/midagedev/toktape/internal/tape"
)

// TestUnifiedHostHasOneLeg (TTP-178, 2026-10-11). On one memory pool a "GPU0"
// device reads the memory the CPU reads, so its ceiling is the host's — not a
// figure stamped for the part's name, and "?" (0) when the host's own bandwidth
// was never observed. A GB10 reports no memory.used either, so an unread hold
// of 0 must not read as a contradiction of the placement estimate.
func TestUnifiedHostHasOneLeg(t *testing.T) {
	const gib = 1 << 30
	s := &tape.RunSummary{
		Host: tape.HostInfo{
			GPUs: []tape.GPUInfo{{Index: 0, Name: "NVIDIA GB10", PeakBandwidthBytesPerSec: 999_000_000_000}},
		},
		Placement: tape.PlacementSummary{Devices: []tape.DevicePlacement{
			{Device: "GPU0", Bytes: 40 * gib},
			{Device: tape.DeviceCPU, Bytes: 10 * gib},
		}},
		GPUsAtEnd: []tape.GPUSample{{Index: 0}},
	}
	if got := DeviceBytesPerSec(s, "GPU0"); got != 0 {
		t.Errorf("no host bandwidth observed: GPU0 ceiling %d, want 0 (unknown)", got)
	}
	s.Host.RAMBytesPerSec, s.Host.RAMSource = 273_000_000_000, tape.RAMSourceMeasured
	if got, want := DeviceBytesPerSec(s, "GPU0"), int64(273_000_000_000); got != want {
		t.Errorf("GPU0 ceiling %d, want the host's %d", got, want)
	}
	if got := DeviceBytesPerSec(s, tape.DeviceCPU); got != 273_000_000_000 {
		t.Errorf("CPU ceiling %d", got)
	}
	if c := Contradiction(s); c != nil {
		t.Errorf("an unread GB10 hold contradicts the placement: %+v", c)
	}
	// The same tape on a discrete card is still contradicted: the guard is the
	// part's name and nothing wider.
	s.Host.GPUs[0].Name = "NVIDIA GeForce RTX 3090"
	if c := Contradiction(s); c == nil {
		t.Error("a discrete card holding 0 of 40 GiB is no longer a contradiction")
	}
}
