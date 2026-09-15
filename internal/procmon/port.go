package procmon

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// FindPIDByPort returns the pid of the process listening on port: the one
// holding a socket whose /proc/net/tcp or /proc/net/tcp6 row is in LISTEN
// with that local port.
//
// It is the generic fallback for FindPID: an engine that is not llama.cpp has
// no model path in its argv to match on, but whatever serves HTTP still holds
// the listening socket, and the socket's inode in /proc/<pid>/fd names whose
// it is (2026-09-15, ExLlamaV3: a Python shim on a port).
//
// Exactly one process must hold the socket. Zero or several distinct holders
// are errors that name the count, because printing either of them as THE pid
// would pin memory and fault readings onto a process toktape never identified.
func FindPIDByPort(fsRoot string, port int) (int, error) {
	if port <= 0 || port > 65535 {
		return 0, fmt.Errorf("procmon: find by port: %d is not a port", port)
	}
	want := fmt.Sprintf("%04X", port)
	var inodes []string
	for _, table := range []string{"net/tcp", "net/tcp6"} {
		data, err := os.ReadFile(filepath.Join(fsRoot, "proc", table))
		if err != nil {
			if os.IsNotExist(err) {
				continue // a kernel with, or without, IPv6
			}
			return 0, fmt.Errorf("procmon: read %s: %w", table, err)
		}
		inodes = append(inodes, listenInodes(string(data), want)...)
	}
	if len(inodes) == 0 {
		return 0, fmt.Errorf("procmon: port %d: %w", port, ErrNotFound)
	}
	pids := pidsHoldingInodes(fsRoot, inodes)
	if len(pids) == 0 {
		return 0, fmt.Errorf("procmon: port %d: %w", port, ErrNotFound)
	}
	if len(pids) > 1 {
		sort.Ints(pids)
		return 0, fmt.Errorf("procmon: port %d: %d processes hold the listening socket (%v)",
			port, len(pids), pids)
	}
	return pids[0], nil
}

// listenInodes returns the socket inodes of the LISTEN rows on wantPort in
// one /proc/net/tcp{,6} table. Rows are whitespace-split; local_address is
// field 1 as IP:port in hex, st is field 3 (0A is LISTEN), inode field 9.
func listenInodes(table, wantPort string) []string {
	var inodes []string
	for _, line := range strings.Split(table, "\n")[1:] { // [1:] skips the header
		f := strings.Fields(line)
		if len(f) < 10 {
			continue
		}
		if f[3] != "0A" {
			continue
		}
		// local_address is "IP:PORT" with both halves in hex; the port is
		// after the one colon, zero-padded to four digits.
		addr := f[1]
		i := strings.LastIndexByte(addr, ':')
		if i < 0 || addr[i+1:] != wantPort {
			continue
		}
		inode := f[9]
		if inode != "0" {
			inodes = append(inodes, inode)
		}
	}
	return inodes
}

// pidsHoldingInodes scans /proc/<pid>/fd/* for a "socket:[<inode>]" symlink
// naming one of inodes, and returns the distinct pids that hold any of them,
// sorted ascending.
func pidsHoldingInodes(fsRoot string, inodes []string) []int {
	want := make(map[string]bool, len(inodes))
	for _, in := range inodes {
		want["socket:["+in+"]"] = true
	}
	entries, err := os.ReadDir(filepath.Join(fsRoot, "proc"))
	if err != nil {
		return nil
	}
	var pids []int
	seen := map[int]bool{}
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || !e.IsDir() {
			continue // not a pid directory
		}
		fds, err := os.ReadDir(filepath.Join(fsRoot, "proc", e.Name(), "fd"))
		if err != nil {
			continue // not ours, or already gone
		}
		for _, fd := range fds {
			link, err := os.Readlink(filepath.Join(fsRoot, "proc", e.Name(), "fd", fd.Name()))
			if err != nil {
				continue
			}
			if want[link] && !seen[pid] {
				seen[pid] = true
				pids = append(pids, pid)
			}
		}
	}
	sort.Ints(pids)
	return pids
}
