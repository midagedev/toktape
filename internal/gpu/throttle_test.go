package gpu

import (
	"reflect"
	"testing"
)

func TestThrottled(t *testing.T) {
	tests := []struct {
		name string
		mask uint64
		want bool
	}{
		{"none", ThrottleNone, false},
		{"gpu idle only", ThrottleGPUIdle, false},
		{"sw power cap", ThrottleSWPowerCap, true},
		{"hw thermal slowdown", ThrottleHWThermalSlowdown, true},
		{"idle plus thermal", ThrottleGPUIdle | ThrottleSWThermalSlowdown, true},
		{"applications clocks setting", ThrottleApplicationsClocksSetting, true},
		{"unknown high bit", 1 << 20, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Throttled(tc.mask); got != tc.want {
				t.Errorf("Throttled(%#x) = %v, want %v", tc.mask, got, tc.want)
			}
		})
	}
}

func TestThrottleReasons(t *testing.T) {
	tests := []struct {
		mask uint64
		want []string
	}{
		{ThrottleNone, nil},
		{ThrottleGPUIdle, []string{"gpu idle"}},
		{ThrottleSWPowerCap | ThrottleHWThermalSlowdown, []string{"sw power cap", "hw thermal slowdown"}},
		{1 << 20, []string{"unknown bit 0x100000"}},
	}
	for _, tc := range tests {
		if got := ThrottleReasons(tc.mask); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("ThrottleReasons(%#x) = %q, want %q", tc.mask, got, tc.want)
		}
	}
}

// TestThrottleHeldBelowSettingsMask is the card's verdict, mask by mask
// (lead, 2026-09-15). The wide Throttled above stays as it is for the tape
// and -o json; this is the narrower question a card answers — was the device
// held below what its own settings allow — and the sweep is why the two part:
// one A6000, one model, only the board power limit changed, drew 281 W of
// 300 at 1950 MHz, 199 of 200 at 1710 and 150 of 150 at 1335 (141, 133 and
// 120 tok/s), and every run carried sw power cap and nothing else. A verdict
// fed from that bit says yes of all three machines; the draw against the
// limit is the figure that tells them apart.
func TestThrottleHeldBelowSettingsMask(t *testing.T) {
	tests := []struct {
		name string
		mask uint64
		want bool
	}{
		{"none", ThrottleNone, false},
		{"gpu idle only", ThrottleGPUIdle, false},
		// The operator's own numbers doing exactly what they were set to.
		{"sw power cap — the sweep's own mask", ThrottleSWPowerCap, false},
		{"applications clocks pinned with -ac", ThrottleApplicationsClocksSetting, false},
		{"both settings", ThrottleSWPowerCap | ThrottleApplicationsClocksSetting, false},
		// The device held below what its own settings allow.
		{"hw slowdown", ThrottleHWSlowdown, true},
		{"sync boost", ThrottleSyncBoost, true},
		{"sw thermal slowdown", ThrottleSWThermalSlowdown, true},
		{"hw thermal slowdown", ThrottleHWThermalSlowdown, true},
		{"hw power brake slowdown", ThrottleHWPowerBrakeSlowdown, true},
		// A bit past the vocabulary is not asserted either way here: the
		// narrow mask is the five held-below bits and no more.
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.mask&ThrottleHeldBelowSettingsMask != 0; got != tc.want {
				t.Errorf("mask %#x held below settings = %v, want %v", tc.mask, got, tc.want)
			}
		})
	}
	// The sweep's mask is the wide verdict and not the narrow one, which is
	// the whole distinction.
	m := ThrottleSWPowerCap
	if !Throttled(m) || m&ThrottleHeldBelowSettingsMask != 0 {
		t.Errorf("sw power cap: wide %v, narrow %v; want true, false", Throttled(m), m&ThrottleHeldBelowSettingsMask != 0)
	}
}
