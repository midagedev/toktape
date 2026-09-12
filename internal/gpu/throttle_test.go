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
