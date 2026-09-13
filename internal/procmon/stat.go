package procmon

import (
	"fmt"
	"strconv"
	"strings"
)

// Stat is the subset of /proc/<pid>/stat toktape reads. MajFaults is the
// counter behind the per-token sparkline (handover lesson 3).
type Stat struct {
	PID       int    // field 1
	Comm      string // field 2, without the surrounding parentheses
	State     string // field 3
	MinFaults uint64 // field 10, minflt, cumulative
	MajFaults uint64 // field 12, majflt, cumulative
	// UTime and STime are the process's cumulative user and system CPU time
	// in clock ticks (USER_HZ, see ClockTicks). Their sum over the tick is
	// tape.MemSample.CPUSeconds (TTP-39).
	UTime uint64 // field 14, utime
	STime uint64 // field 15, stime
}

// ParseStat parses one /proc/<pid>/stat line.
//
// Field 2 is the executable name in parentheses and is the only field that may
// itself contain spaces and parentheses — "(llama-server (cuda))" is a real
// comm. Splitting the whole line on whitespace therefore shifts every later
// field, so the fixed fields are located relative to the LAST ')'. After it,
// the first token is field 3, so field N is at index N-3.
func ParseStat(data []byte) (Stat, error) {
	s := strings.TrimSpace(string(data))
	openIdx := strings.IndexByte(s, '(')
	closeIdx := strings.LastIndexByte(s, ')')
	if openIdx < 0 || closeIdx <= openIdx {
		return Stat{}, fmt.Errorf("procmon: stat: no comm field in %q", truncate(s, 64))
	}
	st := Stat{Comm: s[openIdx+1 : closeIdx]}
	pid, err := strconv.Atoi(strings.TrimSpace(s[:openIdx]))
	if err != nil {
		return Stat{}, fmt.Errorf("procmon: stat: pid field: %w", err)
	}
	st.PID = pid

	f := strings.Fields(s[closeIdx+1:])
	const stimeIdx = 15 - 3 // field 15 at index 12, the last one read
	if len(f) <= stimeIdx {
		return Stat{}, fmt.Errorf("procmon: stat: want at least 15 fields, got %d", len(f)+2)
	}
	st.State = f[0]
	if st.MinFaults, err = strconv.ParseUint(f[10-3], 10, 64); err != nil {
		return Stat{}, fmt.Errorf("procmon: stat: minflt field: %w", err)
	}
	if st.MajFaults, err = strconv.ParseUint(f[12-3], 10, 64); err != nil {
		return Stat{}, fmt.Errorf("procmon: stat: majflt field: %w", err)
	}
	if st.UTime, err = strconv.ParseUint(f[14-3], 10, 64); err != nil {
		return Stat{}, fmt.Errorf("procmon: stat: utime field: %w", err)
	}
	if st.STime, err = strconv.ParseUint(f[stimeIdx], 10, 64); err != nil {
		return Stat{}, fmt.Errorf("procmon: stat: stime field: %w", err)
	}
	return st, nil
}
