package recorder

import (
	"bufio"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/midagedev/toktape/internal/procmon"
)

// The KV cache figure, from the server's own words (TTP-137, 2026-09-19).
//
// placement.vram_kv_bytes has been 0 on every tape so far, and the obvious
// way to fill it — arithmetic over layers, heads and head size — is wrong by
// 4.2x on the reference model: qwen35moe caches only the rotary 64 of a
// 256-wide key, so the formula says 2.68 GiB where the engine says 640 MiB.
// So the figure is taken only from a line the engine itself printed, the
// "KV self size" line of its load log:
//
//	llama_init_from_model: KV self size = 640.00 MiB, K (f16): 320.00 MiB, V (f16): 320.00 MiB
//
// A server someone redirected to a file leaves that log where the recorder
// can find it: the /proc/<pid>/fd link of stdout or stderr. Every miss along
// the way — a pipe, a tty, /dev/null, an unreadable file, a log without the
// line, a non-Linux host — is an ordinary "not observed": 0 stays 0, the
// card prints ?, and nothing is said to a user whose setup simply does not
// redirect. An engine that reported its own figure through /props is the
// record and is never overwritten by this scan.

// kvSelfMarker is the fixed text before the figure on the engine's line.
const kvSelfMarker = "KV self size = "

// kvLogScanLimit caps how much of a log is read looking for the line: the
// engine prints it within its first screens, and a redirected log can grow
// without bound while the run is going.
const kvLogScanLimit = 64 << 20

// collectVRAMKV fills r.place.VRAMKVBytes from the server's own load log,
// when the figure was not already observed and stdout or stderr was
// redirected to a readable regular file. It runs after collectPlacement, so
// an engine-reported figure has already won, and it never touches
// placement.Source: "unknown" with a figure is more honest than a source
// name the estimate never earned.
func (r *run) collectVRAMKV() {
	if r.place.VRAMKVBytes != 0 || r.pid <= 0 {
		return
	}
	for _, fd := range []int{1, 2} {
		target, err := procmon.FDTarget(r.opts.FSRoot, r.pid, fd)
		if err != nil || !filepath.IsAbs(target) {
			continue // a kernel spelling (pipe:[...]) or a relative link: not a file we can read
		}
		path := filepath.Join(r.opts.FSRoot, target)
		fi, err := os.Stat(path)
		if err != nil || !fi.Mode().IsRegular() {
			continue // a directory, a device (/dev/null), a socket: no log in it
		}
		f, err := os.Open(path)
		if err != nil {
			continue // unreadable is not the user's fault and not ours to say
		}
		n := kvBytesFromLog(io.LimitReader(f, kvLogScanLimit))
		f.Close()
		if n > 0 {
			r.place.VRAMKVBytes = n
			return
		}
	}
}

// kvBytesFromLog returns the KV self size of the first engine line in r, or
// 0 when there is none. The first is the one the load printed; later lines,
// if a rerun appends to the same log, belong to whoever wrote them.
func kvBytesFromLog(r io.Reader) int64 {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		if b, ok := parseKVSelfSize(sc.Text()); ok {
			return b
		}
	}
	return 0
}

// parseKVSelfSize reads one log line, taking the figure and its unit from
// after the marker up to the comma that ends the field:
//
//	"llama_init_from_model: KV self size = 640.00 MiB, K (f16): ..." -> 671088640
//
// Anything that does not carry a value with a unit it knows is a line not
// understood, not a zero the engine claimed.
func parseKVSelfSize(line string) (int64, bool) {
	i := strings.Index(line, kvSelfMarker)
	if i < 0 {
		return 0, false
	}
	rest := line[i+len(kvSelfMarker):]
	if j := strings.IndexByte(rest, ','); j >= 0 {
		rest = rest[:j]
	}
	f := strings.Fields(strings.TrimSpace(rest))
	if len(f) != 2 {
		return 0, false // a value without a unit, or two values: not our line
	}
	v, err := strconv.ParseFloat(f[0], 64)
	if err != nil || v <= 0 {
		return 0, false
	}
	switch f[1] {
	case "B":
		return int64(v), true
	case "KiB":
		return int64(v * 1024), true
	case "MiB":
		return int64(v * 1024 * 1024), true
	case "GiB":
		return int64(v * 1024 * 1024 * 1024), true
	case "TiB":
		return int64(v * 1024 * 1024 * 1024 * 1024), true
	}
	return 0, false
}
