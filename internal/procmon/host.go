package procmon

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/midagedev/toktape/internal/tape"
)

// HostInfo fills the hardware line of the card from /proc, minus the GPUs,
// which another reader supplies.
//
// It is deliberately lenient: a field whose file is missing or unreadable
// stays "" or 0 and the card prints "?", per the tape contract. Only a
// <fsRoot>/proc that is not a readable directory is an error, because then
// nothing below it can be trusted.
//
// RAMSpeed and RAMChannels are always left empty. They live in the DMI tables,
// and on every distribution checked /sys/firmware/dmi/tables/DMI is mode 0400
// root-only, so reading them would mean shelling out to dmidecode under sudo.
// The card prints "?" rather than a guess; a future track can fill them from
// an explicit --ram-speed flag or from a root helper.
func HostInfo(fsRoot string) (tape.HostInfo, error) {
	root := procPath(fsRoot)
	fi, err := os.Stat(root)
	if err != nil {
		return tape.HostInfo{}, fmt.Errorf("procmon: host info: stat %s: %w", root, err)
	}
	if !fi.IsDir() {
		return tape.HostInfo{}, fmt.Errorf("procmon: host info: %s is not a directory", root)
	}

	h := tape.HostInfo{OS: "linux"}
	if data, err := os.ReadFile(procPath(fsRoot, "sys", "kernel", "hostname")); err == nil {
		h.Hostname = strings.TrimSpace(string(data))
	}
	if data, err := os.ReadFile(procPath(fsRoot, "version")); err == nil {
		h.Kernel = ParseKernelVersion(data)
	}
	if data, err := os.ReadFile(procPath(fsRoot, "cpuinfo")); err == nil {
		cpu := ParseCPUInfo(data)
		h.CPU, h.CPUCores, h.CPUThreads = cpu.Model, cpu.Cores, cpu.Threads
	}
	if data, err := os.ReadFile(procPath(fsRoot, "meminfo")); err == nil {
		n, err := ParseMemTotal(data)
		if err == nil {
			h.RAMBytes = n
		}
	}
	return h, nil
}

// ParseKernelVersion pulls the release string out of /proc/version: the first
// token after "Linux version". It returns "" when the line does not start that
// way rather than guessing at another layout.
func ParseKernelVersion(data []byte) string {
	s := strings.TrimSpace(string(data))
	const prefix = "Linux version "
	if !strings.HasPrefix(s, prefix) {
		return ""
	}
	f := strings.Fields(s[len(prefix):])
	if len(f) == 0 {
		return ""
	}
	return f[0]
}

// CPUInfo is the processor half of the hardware line.
type CPUInfo struct {
	Model   string // "model name" of the first processor
	Cores   int    // distinct (physical id, core id) pairs
	Threads int    // "processor" entries
}

// ParseCPUInfo reads /proc/cpuinfo.
//
// Threads is the number of logical processors. Cores counts distinct
// (physical id, core id) pairs, which is the only way to get the physical
// count right on a multi-socket box: "cpu cores" is per socket, and on a
// 2-socket 16-core part both sockets report 16. When the kernel does not
// publish those two keys — it does not on most ARM platforms — Cores stays 0
// and the card prints "?" rather than repeating the thread count.
func ParseCPUInfo(data []byte) CPUInfo {
	var info CPUInfo
	cores := make(map[string]struct{})

	var havePhys, haveCore bool
	var phys, core string
	flush := func() {
		if havePhys && haveCore {
			cores[phys+"/"+core] = struct{}{}
		}
		havePhys, haveCore = false, false
	}

	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		key, value, ok := splitKeyValue(sc.Text())
		if !ok {
			continue
		}
		switch key {
		case "processor":
			flush()
			info.Threads++
		case "model name":
			if info.Model == "" {
				info.Model = value
			}
		case "physical id":
			phys, havePhys = value, true
		case "core id":
			core, haveCore = value, true
		}
	}
	flush()
	info.Cores = len(cores)
	return info
}

// ParseMemTotal returns MemTotal from /proc/meminfo in bytes.
func ParseMemTotal(data []byte) (int64, error) {
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		key, value, ok := splitKeyValue(sc.Text())
		if !ok || key != "MemTotal" {
			continue
		}
		n, err := parseKB(value)
		if err != nil {
			return 0, fmt.Errorf("procmon: meminfo: MemTotal: %w", err)
		}
		return n, nil
	}
	if err := sc.Err(); err != nil {
		return 0, fmt.Errorf("procmon: meminfo: %w", err)
	}
	return 0, fmt.Errorf("procmon: meminfo: no MemTotal line: %w", ErrNotFound)
}

// ParseLoadAvg returns the 1-minute load average from a /proc/loadavg line.
func ParseLoadAvg(data []byte) (float64, error) {
	f := strings.Fields(string(data))
	if len(f) == 0 {
		return 0, fmt.Errorf("procmon: loadavg: empty: %w", ErrNotFound)
	}
	v, err := strconv.ParseFloat(f[0], 64)
	if err != nil {
		return 0, fmt.Errorf("procmon: loadavg: %w", err)
	}
	return v, nil
}

// LoadAvg reads the 1-minute load average. It is half of the contended label
// (handover lesson 6): a busy machine's numbers are void, so the run is
// labelled rather than silently reported.
func LoadAvg(fsRoot string) (float64, error) {
	p := procPath(fsRoot, "loadavg")
	data, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, fmt.Errorf("procmon: %s: %w", p, ErrNotFound)
		}
		return 0, fmt.Errorf("procmon: read %s: %w", p, err)
	}
	return ParseLoadAvg(data)
}
