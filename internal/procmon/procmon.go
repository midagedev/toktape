// Package procmon reads the process and host picture toktape needs from the
// Linux /proc filesystem: the server's PID and argv, its memory split, the
// major-fault counter behind the per-token sparkline, and the hardware line of
// the card.
//
// Every reader takes an fsRoot — the directory that contains "proc". In
// production that is "/"; in tests it is a fixture tree. That is why each
// parser here is a pure function of bytes and the I/O wrapper around it is
// thin: the parsers are exercised on any platform, against files copied from a
// real machine, with no root and no network.
//
// Only Available reports whether this platform has a live /proc at all. On
// darwin and elsewhere it returns ErrUnsupported and the recorder is expected
// to record a warning and carry on without the /proc view, per the tape
// contract (unknown is "" / 0, never a guessed default).
package procmon

import (
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
)

// ErrNotFound is returned when a lookup completed but matched nothing — most
// often FindPID when the server runs on another host. Callers test it with
// errors.Is; every return path wraps it.
var ErrNotFound = errors.New("procmon: not found")

// ErrUnsupported is returned by Available, and by the entry points that read
// the live filesystem, on a platform with no /proc.
var ErrUnsupported = errors.New("procmon: /proc is not available on this platform")

// Available reports whether this platform exposes a live /proc tree. It
// returns nil on Linux and an error wrapping ErrUnsupported everywhere else.
func Available() error {
	_, err := defaultRoot()
	return err
}

// procPath joins fsRoot, "proc" and elem. fsRoot "" means the live root.
func procPath(fsRoot string, elem ...string) string {
	if fsRoot == "" {
		fsRoot = "/"
	}
	return filepath.Join(append([]string{fsRoot, "proc"}, elem...)...)
}

// sysPath joins fsRoot, "sys" and elem: sysfs, the sibling of procfs, where
// the machine's operating point lives (cpufreq, hwmon — TTP-57). fsRoot ""
// means the live root, as in procPath.
func sysPath(fsRoot string, elem ...string) string {
	if fsRoot == "" {
		fsRoot = "/"
	}
	return filepath.Join(append([]string{fsRoot, "sys"}, elem...)...)
}

// pidPath is procPath for a per-process file.
func pidPath(fsRoot string, pid int, elem ...string) string {
	return procPath(fsRoot, append([]string{strconv.Itoa(pid)}, elem...)...)
}

// parseKB parses a kernel size value such as "  123456 kB" as bytes. The
// kernel's "kB" is 1024 bytes. A bare number is accepted as bytes.
func parseKB(v string) (int64, error) {
	f := strings.Fields(v)
	if len(f) == 0 {
		return 0, errors.New("empty value")
	}
	n, err := strconv.ParseInt(f[0], 10, 64)
	if err != nil {
		return 0, err
	}
	if len(f) < 2 {
		return n, nil
	}
	switch strings.ToLower(f[1]) {
	case "b":
		return n, nil
	case "kb":
		return n * 1024, nil
	case "mb":
		return n * 1024 * 1024, nil
	default:
		return 0, fmt.Errorf("unknown unit %q", f[1])
	}
}

// splitKeyValue splits a "Key:  value" line. ok is false for a line with no
// colon, which callers skip.
func splitKeyValue(line string) (key, value string, ok bool) {
	i := strings.IndexByte(line, ':')
	if i < 0 {
		return "", "", false
	}
	return strings.TrimSpace(line[:i]), strings.TrimSpace(line[i+1:]), true
}

// truncate shortens s for an error message.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
