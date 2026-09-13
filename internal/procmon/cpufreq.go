package procmon

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// The reading in this file is the clock half of a contention witness
// (TTP-57, 2026-09-14). A run whose rate sits below the same box's earlier
// rate at the same settings is unexplainable from the tape alone when nothing
// recorded that a thermal watchdog moved the clock cap mid-run; with a cap at
// each edge of every round, the card can say so.

// ReadCPUMaxKHz returns the largest scaling_max_freq over the online CPUs of
// <fsRoot>/sys/devices/system/cpu, in kHz.
//
// scaling_max_freq is deliberately the file read, not cpuinfo_max_freq:
// cpuinfo_max_freq is what the hardware can do and never moves, so a watchdog
// that drops the cap from 3.6 to 2.7 GHz would leave it unchanged and the
// witness would hide exactly the event it exists to catch. scaling_max_freq is
// the cap in force, which is what the run actually ran under.
//
// The largest cap over the CPUs, not the smallest or a mean: the run is bound
// by the fastest core it can get, and one core parked at a low cap by a
// per-core governor is not the machine's operating point.
//
// A CPU that went offline between the glob and the read refuses the read
// (EINVAL/ENODEV); that CPU is skipped rather than failing the whole reading.
// A file that is present and readable but does not parse is an error — a
// figure we cannot read must stay unknown, not become a quiet maximum of the
// others. When no CPU yielded a cap the error wraps ErrNotFound (a kernel
// without cpufreq, a VM, or not Linux) and the caller records 0 = unread.
func ReadCPUMaxKHz(fsRoot string) (int64, error) {
	pattern := sysPath(fsRoot, "devices", "system", "cpu", "cpu*", "cpufreq", "scaling_max_freq")
	paths, err := filepath.Glob(pattern)
	if err != nil {
		return 0, fmt.Errorf("procmon: cpufreq: glob %s: %w", pattern, err)
	}
	var maxKHz int64
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		n, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
		if err != nil {
			return 0, fmt.Errorf("procmon: cpufreq: %s: %w", p, err)
		}
		// A non-positive cap is not an operating point. 0 already means
		// "unread" in the tape, so it must not be returned as a reading.
		if n > maxKHz {
			maxKHz = n
		}
	}
	if maxKHz <= 0 {
		return 0, fmt.Errorf("procmon: cpufreq: no cap under %s: %w", pattern, ErrNotFound)
	}
	return maxKHz, nil
}
