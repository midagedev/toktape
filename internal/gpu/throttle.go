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

// ThrottleHeldBelowSettingsMask is the narrower verdict the CARD prints: the
// bits that say the hardware stepped in and held the device below the
// operating point it was set to, as opposed to the device running at the
// operating point it was SET to.
//
// The difference is sw power cap (and applications clocks), and it is the
// whole point. A power sweep of one model on one card with nothing but the
// board power limit changed (lead, 2026-09-15) set sw power cap at every
// level, because any card boosting into its own cap sets it:
//
//	300 W cap: drew 281 W at 1950 MHz, 141 tok/s
//	200 W cap: drew 199 W at 1710 MHz, 133 tok/s
//	150 W cap: drew 150 W at 1335 MHz, 120 tok/s
//
//	Three different machines by the wide verdict's own account, all
//	"throttled: yes" — a sentence true of every run is not a reading. At each
//	level the card did exactly what it was set to do: it drew up to its limit
//	and no further. The held-below bits — hw slowdown, hw/sw thermal slowdown,
//	hw power brake, sync boost — are the ones that say the card could not hold
//	the operating point, and they are the card's verdict. Throttled above keeps
//	the wide mask for the tape and `-o json`, where a figure below peak clocks
//	for any reason is still worth recording.
const ThrottleHeldBelowSettingsMask = ThrottleHWSlowdown |
	ThrottleSyncBoost |
	ThrottleSWThermalSlowdown |
	ThrottleHWThermalSlowdown |
	ThrottleHWPowerBrakeSlowdown

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
