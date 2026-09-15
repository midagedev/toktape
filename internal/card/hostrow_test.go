package card

import (
	"strings"
	"testing"

	"github.com/midagedev/toktape/internal/gpu"
	"github.com/midagedev/toktape/internal/tape"
)

// The HOST row's power figure and throttle verdict, which the power-limit
// sweep redefined (lead, 2026-09-15): three runs of one model on one card
// with only the board limit changed — 281 W of 300 at 1950 MHz, 199 of 200 at
// 1710, 150 of 150 at 1335 — all reported Throttled, because sw power cap
// sets on any card boosting into its own limit. A verdict true of every
// capped board says nothing; the draw against the limit is what separates the
// machines, so that is what the row prints.

// TestHostRowPowerIsDrawAgainstLimit: with the limit read, the draw is printed
// against it; without it, the row keeps exactly the figure it always printed.
func TestHostRowPowerIsDrawAgainstLimit(t *testing.T) {
	s := Example()
	s.GPUsAtEnd[0].PowerW, s.GPUsAtEnd[0].PowerLimitW = 281, 300
	if text := Text(s); !strings.Contains(text, "GPU0 68°C 281 of 300 W") {
		t.Errorf("the HOST row does not print the draw against the limit:\n%s", text)
	}
	// No limit on the tape — a recording older than the field, or a cell the
	// device reported as "[N/A]": today's string, unchanged.
	s.GPUsAtEnd[0].PowerLimitW = 0
	if text := Text(s); !strings.Contains(text, "GPU0 68°C 281 W") {
		t.Errorf("without a limit the HOST row changed:\n%s", text)
	}
}

// TestHostRowThrottleVerdictIsTheHeldBits: the card's verdict is the narrow
// mask, not the wide one the recorder stored. The sweep's own case is the
// first one: sw power cap alone, with the wide verdict true, and the card
// must still say no.
func TestHostRowThrottleVerdictIsTheHeldBits(t *testing.T) {
	s := Example()
	s.GPUsAtEnd[0].ThrottleMask = gpu.ThrottleSWPowerCap
	s.GPUsAtEnd[0].Throttled = true
	if text := Text(s); !strings.Contains(text, "throttled: no") {
		t.Errorf("sw power cap alone still reads as throttled:\n%s", text)
	}
	s.GPUsAtEnd[0].ThrottleMask = gpu.ThrottleHWSlowdown
	if text := Text(s); !strings.Contains(text, "throttled: yes") {
		t.Errorf("a hw slowdown does not read as throttled:\n%s", text)
	}
	// A tape older than the mask field keeps the verdict the recorder wrote:
	// the bits it would need are gone, and re-deriving them would print "no"
	// for a run the recorder watched throttle.
	s.GPUsAtEnd[0].ThrottleMask = 0
	if text := Text(s); !strings.Contains(text, "throttled: yes") {
		t.Errorf("a pre-mask tape lost its stored verdict:\n%s", text)
	}
}

// TestGPUThrottledIsTheNarrowMask: the exported verdict, mask by mask, so the
// PNG's environment line — its other caller — agrees by construction rather
// than by coincidence.
func TestGPUThrottledIsTheNarrowMask(t *testing.T) {
	for _, tc := range []struct {
		mask uint64
		want bool
	}{
		{gpu.ThrottleNone, false},
		{gpu.ThrottleGPUIdle, false},
		// The operator's own numbers doing exactly what they were set to.
		{gpu.ThrottleSWPowerCap, false},
		{gpu.ThrottleApplicationsClocksSetting, false},
		{gpu.ThrottleSWPowerCap | gpu.ThrottleApplicationsClocksSetting, false},
		// The device held below what its own settings allow.
		{gpu.ThrottleHWSlowdown, true},
		{gpu.ThrottleSyncBoost, true},
		{gpu.ThrottleSWThermalSlowdown, true},
		{gpu.ThrottleHWThermalSlowdown, true},
		{gpu.ThrottleHWPowerBrakeSlowdown, true},
	} {
		if got := GPUThrottled(tape.GPUSample{
			ThrottleMask: tc.mask,
			Throttled:    gpu.Throttled(tc.mask), // what the recorder stored
		}); got != tc.want {
			t.Errorf("GPUThrottled(%#x) = %v, want %v", tc.mask, got, tc.want)
		}
	}
	// The stored verdict alone: a tape older than the mask field, which is
	// every tape recorded before today.
	if !GPUThrottled(tape.GPUSample{Throttled: true}) {
		t.Error("a pre-mask tape with a stored verdict of true must keep it")
	}
	if GPUThrottled(tape.GPUSample{}) {
		t.Error("a pre-mask tape with a stored verdict of false must keep it")
	}
}
