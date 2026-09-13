package procmon

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/midagedev/toktape/internal/tape"
)

// ClockTicks is the kernel's USER_HZ, the unit of the starttime field of
// /proc/<pid>/stat. The true value comes from sysconf(_SC_CLK_TCK), which Go
// reaches only through cgo, and toktape builds without cgo. USER_HZ is 100 on
// every architecture Linux ships for x86, arm64, ppc64 and riscv — the kernel
// fixed it at 100 for userspace precisely so this figure would not change
// with CONFIG_HZ. An age computed with it is off only on an exotic kernel
// that overrides USER_HZ, and then only in scale, never in which process is
// the older.
const ClockTicks = 100

// llamaCommPrefix is what every llama.cpp tool's comm starts with:
// llama-server, llama-bench, llama-cli and the truncated 15-character forms
// "llama-perplexi", "llama-server-cu".
const llamaCommPrefix = "llama-"

// ParseBootTime returns the "btime" line of /proc/stat: the moment the system
// booted, in seconds since the epoch.
func ParseBootTime(data []byte) (time.Time, error) {
	sc := bufio.NewScanner(bytes.NewReader(data))
	// /proc/stat's intr line has one column per IRQ and runs to kilobytes on
	// a large box.
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 2 || f[0] != "btime" {
			continue
		}
		sec, err := strconv.ParseInt(f[1], 10, 64)
		if err != nil {
			return time.Time{}, fmt.Errorf("procmon: stat: btime: %w", err)
		}
		return time.Unix(sec, 0), nil
	}
	if err := sc.Err(); err != nil {
		return time.Time{}, fmt.Errorf("procmon: stat: %w", err)
	}
	return time.Time{}, fmt.Errorf("procmon: stat: no btime line: %w", ErrNotFound)
}

// ReadBootTime reads the boot time from <fsRoot>/proc/stat.
func ReadBootTime(fsRoot string) (time.Time, error) {
	p := procPath(fsRoot, "stat")
	data, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return time.Time{}, fmt.Errorf("procmon: %s: %w", p, ErrNotFound)
		}
		return time.Time{}, fmt.Errorf("procmon: read %s: %w", p, err)
	}
	return ParseBootTime(data)
}

// ParseStartTime returns field 22 of a /proc/<pid>/stat line, starttime: when
// the process started, in clock ticks after boot. Fields are located from the
// last ')' for the reason ParseStat documents.
func ParseStartTime(data []byte) (uint64, error) {
	s := strings.TrimSpace(string(data))
	closeIdx := strings.LastIndexByte(s, ')')
	if closeIdx < 0 {
		return 0, fmt.Errorf("procmon: stat: no comm field in %q", truncate(s, 64))
	}
	f := strings.Fields(s[closeIdx+1:])
	const startIdx = 22 - 3 // field 3 is at index 0
	if len(f) <= startIdx {
		return 0, fmt.Errorf("procmon: stat: want at least 22 fields, got %d", len(f)+2)
	}
	n, err := strconv.ParseUint(f[startIdx], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("procmon: stat: starttime field: %w", err)
	}
	return n, nil
}

// IsLlamaComm reports whether a comm names a llama.cpp tool.
func IsLlamaComm(comm string) bool {
	return strings.HasPrefix(strings.TrimSpace(comm), llamaCommPrefix)
}

// LlamaProcs lists every live llama-* process under <fsRoot>/proc, ordered by
// PID, with its age at this moment. See LlamaProcsAt.
func LlamaProcs(fsRoot string, selfPID int, bootTime time.Time, clkTck int64) ([]tape.LlamaProc, error) {
	return LlamaProcsAt(fsRoot, selfPID, bootTime, clkTck, time.Now())
}

// LlamaProcsAt lists every live llama-* process under <fsRoot>/proc, ordered
// by PID, with its age at now. A second llama-server or a llama-bench on the
// box is contention the load average does not show (TTP-36).
//
// The name comes from /proc/<pid>/comm only. Every Linux kernel since 2.6.33
// has that file, and a process whose comm cannot be read has exited (or is
// not ours to read) and is skipped; stat's comm is deliberately not a
// fallback, so a tree without comm files lists nothing rather than guessing.
//
// Attached is set for selfPID, the server the run measured. AgeSec is
// now - (bootTime + starttime/clkTck), clamped at 0; it stays 0 (unknown)
// when bootTime is zero or clkTck is not positive.
//
// The only error is an unreadable <fsRoot>/proc directory: then nothing was
// scanned and an empty list would be a false "no other process".
func LlamaProcsAt(fsRoot string, selfPID int, bootTime time.Time, clkTck int64, now time.Time) ([]tape.LlamaProc, error) {
	root := procPath(fsRoot)
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("procmon: llama procs: read %s: %w", root, err)
	}
	pids := make([]int, 0, len(entries))
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid <= 0 {
			continue // not a process directory
		}
		pids = append(pids, pid)
	}
	sort.Ints(pids)

	var procs []tape.LlamaProc
	for _, pid := range pids {
		data, err := os.ReadFile(pidPath(fsRoot, pid, "comm"))
		if err != nil {
			continue // exited between readdir and read, or unreadable
		}
		comm := strings.TrimSpace(string(data))
		if !IsLlamaComm(comm) {
			continue
		}
		p := tape.LlamaProc{PID: pid, Comm: comm, Attached: pid == selfPID}
		if !bootTime.IsZero() && clkTck > 0 {
			if st, err := os.ReadFile(pidPath(fsRoot, pid, "stat")); err == nil {
				if ticks, err := ParseStartTime(st); err == nil {
					p.AgeSec = ageSeconds(bootTime, ticks, clkTck, now)
				}
			}
		}
		procs = append(procs, p)
	}
	return procs, nil
}

// ageSeconds is now - (boot + ticks/clkTck) in seconds, never negative. The
// start offset is computed in whole ticks so a long uptime loses no precision.
func ageSeconds(boot time.Time, ticks uint64, clkTck int64, now time.Time) float64 {
	sec := int64(ticks) / clkTck
	rem := int64(ticks) % clkTck
	started := boot.Add(time.Duration(sec)*time.Second + time.Duration(rem)*time.Second/time.Duration(clkTck))
	age := now.Sub(started).Seconds()
	if age < 0 {
		return 0
	}
	return age
}
