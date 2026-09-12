package procmon

import (
	"bufio"
	"bytes"
	"fmt"
)

// Status is the subset of /proc/<pid>/status toktape reads. Sizes are bytes;
// the file reports kB (1024 bytes).
//
// RSS is not "loaded" (handover lesson 3): with mmap the RSS is only what the
// process has touched, and it is short by whatever was copied to VRAM and
// dropped. The split into file / anon / shmem is what makes that visible, so
// all four are recorded rather than RSS alone.
type Status struct {
	Name          string // comm, truncated to 15 characters by the kernel
	VirtBytes     int64  // VmSize
	RSSBytes      int64  // VmRSS
	RSSFileBytes  int64  // RssFile
	RSSAnonBytes  int64  // RssAnon
	RSSShmemBytes int64  // RssShmem
	SwapBytes     int64  // VmSwap
	Threads       int
}

// ParseStatus parses /proc/<pid>/status. Fields the kernel did not report stay
// zero; only a malformed value for a field that is present is an error.
func ParseStatus(data []byte) (Status, error) {
	var st Status
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		key, value, ok := splitKeyValue(sc.Text())
		if !ok {
			continue
		}
		var dst *int64
		switch key {
		case "Name":
			st.Name = value
			continue
		case "Threads":
			n, err := parseKB(value)
			if err != nil {
				return Status{}, fmt.Errorf("procmon: status: Threads: %w", err)
			}
			st.Threads = int(n)
			continue
		case "VmSize":
			dst = &st.VirtBytes
		case "VmRSS":
			dst = &st.RSSBytes
		case "RssFile":
			dst = &st.RSSFileBytes
		case "RssAnon":
			dst = &st.RSSAnonBytes
		case "RssShmem":
			dst = &st.RSSShmemBytes
		case "VmSwap":
			dst = &st.SwapBytes
		default:
			continue
		}
		n, err := parseKB(value)
		if err != nil {
			return Status{}, fmt.Errorf("procmon: status: %s: %w", key, err)
		}
		*dst = n
	}
	if err := sc.Err(); err != nil {
		return Status{}, fmt.Errorf("procmon: status: %w", err)
	}
	return st, nil
}

// Rollup is the subset of /proc/<pid>/smaps_rollup toktape reads, in bytes.
// It refines the status view: Pss shares a mapping fairly between the
// processes that hold it, which matters when a second server is attached to
// the same model file at -ngl 0 and shares the mapping (handover lesson 8).
type Rollup struct {
	RSSBytes          int64 // Rss
	PSSBytes          int64 // Pss
	SharedCleanBytes  int64 // Shared_Clean — the page-cache-backed model pages
	PrivateDirtyBytes int64 // Private_Dirty
	SwapBytes         int64 // Swap
}

// ParseSmapsRollup parses /proc/<pid>/smaps_rollup. The first line is the
// address range header and carries no colon-separated value, so it is skipped
// like any other non key-value line.
func ParseSmapsRollup(data []byte) (Rollup, error) {
	var r Rollup
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		key, value, ok := splitKeyValue(sc.Text())
		if !ok {
			continue
		}
		var dst *int64
		switch key {
		case "Rss":
			dst = &r.RSSBytes
		case "Pss":
			dst = &r.PSSBytes
		case "Shared_Clean":
			dst = &r.SharedCleanBytes
		case "Private_Dirty":
			dst = &r.PrivateDirtyBytes
		case "Swap":
			dst = &r.SwapBytes
		default:
			continue
		}
		n, err := parseKB(value)
		if err != nil {
			return Rollup{}, fmt.Errorf("procmon: smaps_rollup: %s: %w", key, err)
		}
		*dst = n
	}
	if err := sc.Err(); err != nil {
		return Rollup{}, fmt.Errorf("procmon: smaps_rollup: %w", err)
	}
	return r, nil
}
