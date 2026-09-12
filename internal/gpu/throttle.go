package gpu

import "strings"

// Throttle reason bits, as reported by nvidia-smi's
// clocks_throttle_reasons.active and NVML's
// nvmlDeviceGetCurrentClocksThrottleReasons. The values are NVML's
// nvmlClocksThrottleReason* constants.
const (
	ThrottleNone                      uint64 = 0x0000000000000000
	ThrottleGPUIdle                   uint64 = 0x0000000000000001
	ThrottleApplicationsClocksSetting uint64 = 0x0000000000000002
	ThrottleSWPowerCap                uint64 = 0x0000000000000004
	ThrottleHWSlowdown                uint64 = 0x0000000000000008
	ThrottleSyncBoost                 uint64 = 0x0000000000000010
	ThrottleSWThermalSlowdown         uint64 = 0x0000000000000020
	ThrottleHWThermalSlowdown         uint64 = 0x0000000000000040
	ThrottleHWPowerBrakeSlowdown      uint64 = 0x0000000000000080
	ThrottleDisplayClockSetting       uint64 = 0x0000000000000100
)

// ThrottleIgnoreMask are the bits that do not count as throttling.
// "None" is the empty mask and GPUIdle only says the device had nothing to
// do. Every other bit sets GPUSample.Throttled.
//
// Note for the card: ApplicationsClocksSetting (0x2) is set on any device
// whose clocks were pinned with `nvidia-smi -ac`, which is common on A100 /
// H100 hosts and is a deliberate setting rather than a fault. It is counted
// as throttled here because the rate it produces is still not the device's
// peak. Widen this mask if that reads wrong on the card.
const ThrottleIgnoreMask = ThrottleGPUIdle

// Throttled reports whether a throttle bitmask means the device was held
// below its peak clocks.
func Throttled(mask uint64) bool {
	return mask&^ThrottleIgnoreMask != 0
}

var throttleNames = []struct {
	bit  uint64
	name string
}{
	{ThrottleGPUIdle, "gpu idle"},
	{ThrottleApplicationsClocksSetting, "applications clocks setting"},
	{ThrottleSWPowerCap, "sw power cap"},
	{ThrottleHWSlowdown, "hw slowdown"},
	{ThrottleSyncBoost, "sync boost"},
	{ThrottleSWThermalSlowdown, "sw thermal slowdown"},
	{ThrottleHWThermalSlowdown, "hw thermal slowdown"},
	{ThrottleHWPowerBrakeSlowdown, "hw power brake slowdown"},
	{ThrottleDisplayClockSetting, "display clock setting"},
}

// ThrottleReasons names every set bit of mask, in bit order. Unknown bits
// are rendered as "unknown bit 0x...". An empty mask yields nil. It exists
// so a surprising Throttled verdict can be explained from a log line.
func ThrottleReasons(mask uint64) []string {
	if mask == ThrottleNone {
		return nil
	}
	var out []string
	rest := mask
	for _, t := range throttleNames {
		if mask&t.bit != 0 {
			out = append(out, t.name)
			rest &^= t.bit
		}
	}
	for bit := uint64(1); bit != 0 && rest != 0; bit <<= 1 {
		if rest&bit != 0 {
			out = append(out, "unknown bit 0x"+strings.TrimLeft(hex16(bit), "0"))
			rest &^= bit
		}
	}
	return out
}

func hex16(v uint64) string {
	const digits = "0123456789abcdef"
	var b [16]byte
	for i := 15; i >= 0; i-- {
		b[i] = digits[v&0xf]
		v >>= 4
	}
	return string(b[:])
}
