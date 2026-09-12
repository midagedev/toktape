package gpu

import (
	"fmt"

	"github.com/midagedev/toktape/internal/tape"
)

// Thresholds for the contention verdict (handover lesson 6: a busy
// machine's numbers are void, so the run is labelled rather than silently
// reported). Change only with an attributed comment and a FAIL-first test.
const (
	// LoadPerCPUThreadFactor: with no server thread count known, the host
	// is contended when the 1-minute load average exceeds this fraction of
	// the CPU thread count. 0.9 leaves room for the run's own load.
	LoadPerCPUThreadFactor = 0.9
	// LoadPerServerThreadFactor: when the server's own thread count is
	// known it is the better base, because a server told to use all 64
	// threads drives load to ~64 during decode all by itself and would
	// self-flag under LoadPerCPUThreadFactor. Load above 1.5x the server's
	// threads is work that is not ours.
	LoadPerServerThreadFactor = 1.5
)

// Contention decides whether the host was busy enough to void the run's
// numbers. It is pure: the caller supplies the readings.
//
// Rule: exactly one load rule fires. When serverThreads > 0 the threshold
// is serverThreads * LoadPerServerThreadFactor; otherwise it is
// cpuThreads * LoadPerCPUThreadFactor. Either way the comparison is
// strict (load exactly at the threshold is not contended). Independently,
// any GPU compute process other than the server marks the run contended.
//
// LoadAvg1 and OtherGPUProcs are always recorded, contended or not.
func Contention(load1 float64, cpuThreads int, otherGPUProcs int, serverThreads int) tape.ContentionInfo {
	info := tape.ContentionInfo{
		LoadAvg1:      load1,
		OtherGPUProcs: otherGPUProcs,
	}
	switch {
	case serverThreads > 0:
		threshold := float64(serverThreads) * LoadPerServerThreadFactor
		if load1 > threshold {
			info.Contended = true
			info.Reasons = append(info.Reasons, fmt.Sprintf(
				"loadavg %.1f > %.1f (%d%% of %d server threads)",
				load1, threshold, int(LoadPerServerThreadFactor*100), serverThreads))
		}
	case cpuThreads > 0:
		threshold := float64(cpuThreads) * LoadPerCPUThreadFactor
		if load1 > threshold {
			info.Contended = true
			info.Reasons = append(info.Reasons, fmt.Sprintf(
				"loadavg %.1f > %.1f (%d%% of %d threads)",
				load1, threshold, int(LoadPerCPUThreadFactor*100), cpuThreads))
		}
	}
	// With neither thread count known there is no load threshold to
	// compare against, so no load reason is produced. Unknown is not a
	// verdict.
	if otherGPUProcs > 0 {
		info.Contended = true
		noun := "processes"
		if otherGPUProcs == 1 {
			noun = "process"
		}
		info.Reasons = append(info.Reasons, fmt.Sprintf("%d other GPU compute %s", otherGPUProcs, noun))
	}
	return info
}
