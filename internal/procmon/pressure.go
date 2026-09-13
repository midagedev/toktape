package procmon

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// The readings in this file are the IO half of a contention witness (TTP-36).
// The load average stays low while a large model is read from NVMe into the
// page cache; /proc/pressure/io "some avg10" goes high from the first second
// of that load, and the page cache is where the bytes land.

// ParseIOPressure returns the "some avg10" figure of /proc/pressure/io: the
// share of the last ten seconds, in percent, in which at least one task was
// stalled on IO.
//
// The file has a "some" line and, on every kernel since 5.2, a "full" line:
//
//	some avg10=1.23 avg60=0.87 avg300=0.21 total=918273645
//	full avg10=1.01 avg60=0.70 avg300=0.18 total=887766554
//
// Only "some" is read. A file without a "some" line, or a "some" line without
// a parseable avg10, is an error: an absent figure must stay unknown, not
// become a quiet 0.
func ParseIOPressure(data []byte) (float64, error) {
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 0 || f[0] != "some" {
			continue
		}
		for _, kv := range f[1:] {
			v, ok := strings.CutPrefix(kv, "avg10=")
			if !ok {
				continue
			}
			n, err := strconv.ParseFloat(v, 64)
			if err != nil {
				return 0, fmt.Errorf("procmon: pressure: some avg10: %w", err)
			}
			return n, nil
		}
		return 0, fmt.Errorf("procmon: pressure: some line has no avg10: %w", ErrNotFound)
	}
	if err := sc.Err(); err != nil {
		return 0, fmt.Errorf("procmon: pressure: %w", err)
	}
	return 0, fmt.Errorf("procmon: pressure: no some line: %w", ErrNotFound)
}

// ReadIOPressure reads <fsRoot>/proc/pressure/io. A kernel built without
// CONFIG_PSI, or booted with psi=0, has no such file (or refuses the read);
// either way the error is returned and the caller records the figure as
// unknown.
func ReadIOPressure(fsRoot string) (float64, error) {
	p := procPath(fsRoot, "pressure", "io")
	data, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, fmt.Errorf("procmon: %s: %w", p, ErrNotFound)
		}
		return 0, fmt.Errorf("procmon: read %s: %w", p, err)
	}
	return ParseIOPressure(data)
}

// ParseMeminfoCached returns the "Cached:" figure of /proc/meminfo in bytes:
// the page cache, which grows by the model's size while it is read from disk.
// "SwapCached:" is a different line and is not matched.
func ParseMeminfoCached(data []byte) (int64, error) {
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		key, value, ok := splitKeyValue(sc.Text())
		if !ok || key != "Cached" {
			continue
		}
		n, err := parseKB(value)
		if err != nil {
			return 0, fmt.Errorf("procmon: meminfo: Cached: %w", err)
		}
		return n, nil
	}
	if err := sc.Err(); err != nil {
		return 0, fmt.Errorf("procmon: meminfo: %w", err)
	}
	return 0, fmt.Errorf("procmon: meminfo: no Cached line: %w", ErrNotFound)
}

// ReadPageCache reads the page-cache size from <fsRoot>/proc/meminfo.
func ReadPageCache(fsRoot string) (int64, error) {
	p := procPath(fsRoot, "meminfo")
	data, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, fmt.Errorf("procmon: %s: %w", p, ErrNotFound)
		}
		return 0, fmt.Errorf("procmon: read %s: %w", p, err)
	}
	return ParseMeminfoCached(data)
}
