package gpu

import (
	"fmt"
	"strconv"

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

// Witnessed adds the verdict of the contention witnesses (TTP-36) to info and
// records them in info.Witnesses. It is pure, and it only ever adds: a run
// the load and GPU rules call contended stays contended, with its reasons
// first and in their order.
//
// Two further rules, each producing at most one reason that names the worst
// reading and where it was taken:
//
//   - IO pressure: any witness whose IOSomeAvg10 is strictly above
//     tape.ContendedIOSomeAvg10. The highest reading is named; on a tie, the
//     earliest. An unreadable figure (nil) is not a reading.
//   - Another llama-* process: any LlamaProc that is not Attached. The first
//     one seen, in witness order, is named; "(+N more)" counts the other
//     distinct PIDs seen across all witnesses.
//
// "where" is "round N start" for a multi-round run (Round is 1-based there)
// and plain "start"/"end" for a single-round run (Round 0).
//
// With no witnesses info is returned unchanged.
func Witnessed(info tape.ContentionInfo, ws []tape.ContentionWitness) tape.ContentionInfo {
	if len(ws) == 0 {
		return info
	}
	info.Witnesses = ws

	worst := -1
	for i, w := range ws {
		if w.IOSomeAvg10 == nil || *w.IOSomeAvg10 <= tape.ContendedIOSomeAvg10 {
			continue
		}
		if worst < 0 || *w.IOSomeAvg10 > *ws[worst].IOSomeAvg10 {
			worst = i
		}
	}
	if worst >= 0 {
		info.Contended = true
		info.Reasons = append(info.Reasons, fmt.Sprintf("io pressure %.1f > %s at %s",
			*ws[worst].IOSomeAvg10,
			strconv.FormatFloat(tape.ContendedIOSomeAvg10, 'f', -1, 64),
			witnessWhere(ws[worst])))
	}

	var (
		first   tape.LlamaProc
		firstAt tape.ContentionWitness
		foreign = map[int]bool{}
	)
	for _, w := range ws {
		for _, p := range w.LlamaProcs {
			if p.Attached {
				continue
			}
			if len(foreign) == 0 {
				first, firstAt = p, w
			}
			foreign[p.PID] = true
		}
	}
	if len(foreign) > 0 {
		info.Contended = true
		reason := fmt.Sprintf("%s pid %d running at %s", first.Comm, first.PID, witnessWhere(firstAt))
		if more := len(foreign) - 1; more > 0 {
			reason += fmt.Sprintf(" (+%d more)", more)
		}
		info.Reasons = append(info.Reasons, reason)
	}
	return info
}

// witnessWhere names when a witness was taken, for a reason line.
func witnessWhere(w tape.ContentionWitness) string {
	if w.Round > 0 {
		return fmt.Sprintf("round %d %s", w.Round, w.Edge)
	}
	return w.Edge
}
