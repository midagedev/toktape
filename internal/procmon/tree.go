package procmon

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"

	"github.com/midagedev/toktape/internal/tape"
)

// FaultSampler is the fault-timeline contract the recorder's state holds: the
// per-token major-fault delta, the latch call that anchors it, and the close.
// Sampler satisfies it for one process; TreeSampler for a parent and his
// children. The recorder holds the interface, so choosing between the two is
// a decision it makes once per run and never revisits per token.
type FaultSampler interface {
	FaultDelta() (majDelta, minDelta uint64, err error)
	MajDelta() (uint64, error)
	Close() error
}

// scanStats reads every parseable /proc/<pid>/stat under fsRoot in one pass.
// Children are found only through a PPid match over this scan — procfs has no
// parent→children link to walk — so the tree helpers all start here.
func scanStats(fsRoot string) map[int]Stat {
	entries, err := os.ReadDir(filepath.Join(fsRoot, "proc"))
	if err != nil {
		return nil
	}
	out := make(map[int]Stat, len(entries))
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || !e.IsDir() {
			continue
		}
		data, err := os.ReadFile(pidPath(fsRoot, pid, "stat"))
		if err != nil {
			continue // exited between ReadDir and ReadFile
		}
		if st, err := ParseStat(data); err == nil {
			out[pid] = st
		}
	}
	return out
}

// treePIDs returns pid and every recursive descendant of it under fsRoot's
// /proc, sorted ascending.
//
// The scan repeats on every call, so a child that spawns or exits mid-run is
// picked up or dropped at the next sample rather than frozen at open time
// (2026-09-15, ExLlamaV3: the CPU expert work runs in a multiprocessing child
// of the port listener, and workers come and go).
//
// Reparenting cannot create a cycle in a live tree, but the scan is over
// user-visible state, so a seen-set bounds the walk anyway.
func treePIDs(fsRoot string, pid int) ([]int, error) {
	stats := scanStats(fsRoot)
	if stats == nil {
		return nil, fmt.Errorf("procmon: tree: %s: no /proc under this root", fsRoot)
	}
	if _, ok := stats[pid]; !ok {
		return nil, fmt.Errorf("procmon: pid %d: %w", pid, ErrNotFound)
	}
	children := map[int][]int{}
	for child, st := range stats {
		children[st.PPid] = append(children[st.PPid], child)
	}
	out := []int{pid}
	seen := map[int]bool{pid: true}
	for i := 0; i < len(out); i++ {
		for _, c := range children[out[i]] {
			if !seen[c] {
				seen[c] = true
				out = append(out, c)
			}
		}
	}
	sort.Ints(out[1:])
	return out, nil
}

// ReadMemTree reads the memory picture of pid's whole tree: parent and
// descendants, summed — RSS, both fault counters and CPU seconds alike. n is
// how many processes went into the sum, so the caller can say it in a
// warning instead of letting one number quietly stand for two processes.
//
// Summed RSS double-counts the file pages the processes share (the weights,
// typically mapped by both). That is the honest ceiling and the caller's
// warning must not claim otherwise.
//
// A descendant that exits between discovery and read is skipped, not fatal:
// its counters stopped at its exit either way.
func ReadMemTree(fsRoot string, pid int) (m tape.MemSample, n int, err error) {
	pids, err := treePIDs(fsRoot, pid)
	if err != nil {
		return tape.MemSample{}, 0, err
	}
	for _, p := range pids {
		one, err := ReadMem(fsRoot, p)
		if err != nil {
			continue // exited since discovery; its contribution is over
		}
		m.VirtBytes += one.VirtBytes
		m.RSSBytes += one.RSSBytes
		m.RSSFileBytes += one.RSSFileBytes
		m.RSSAnonBytes += one.RSSAnonBytes
		m.RSSShmemBytes += one.RSSShmemBytes
		m.SwapBytes += one.SwapBytes
		m.MajFaults += one.MajFaults
		m.MinFaults += one.MinFaults
		m.CPUSeconds += one.CPUSeconds
		n++
	}
	if n == 0 {
		return tape.MemSample{}, 0, fmt.Errorf("procmon: pid %d: %w", pid, ErrNotFound)
	}
	return m, n, nil
}

// TreeSampler is Sampler over a parent and its children: the per-token fault
// delta of the whole tree. Every call re-scans /proc — discovery cannot be
// cheaper than that, since children are only nameable through a PPid match —
// so unlike Sampler there is no held handle; the scan is the read.
//
// Latches are per pid, so a child that exits stops contributing without
// dragging the tree's counters backwards, and one that spawns mid-run starts
// from its counters at first sight instead of donating its whole history to
// one token.
//
// A TreeSampler is not safe for concurrent use.
type TreeSampler struct {
	fsRoot string
	pid    int

	have bool
	last map[int]treeCounters
}

// treeCounters is the per-pid latch: the two cumulative fault counters.
type treeCounters struct {
	maj, min uint64
}

// NewTreeSamplerAt opens a tree sampler for pid and his descendants under
// fsRoot. The parent must exist at open; after that, membership follows the
// scan. Tests use it against a fixture tree; the recorder builds it from
// whatever FSRoot it was given.
func NewTreeSamplerAt(fsRoot string, pid int) (*TreeSampler, error) {
	if _, err := ReadMem(fsRoot, pid); err != nil {
		return nil, err
	}
	return &TreeSampler{fsRoot: fsRoot, pid: pid, last: map[int]treeCounters{}}, nil
}

// Close releases nothing — the sampler holds no handles — and exists so the
// FaultSampler interface is uniform.
func (s *TreeSampler) Close() error { return nil }

// FaultDelta is Sampler.FaultDelta over the tree: the summed movement of the
// fault counters since the previous call, with the same first-call rule —
// nothing to subtract from yet, so 0, 0. FaultDelta and MajDelta share one
// timeline, exactly as the single-process sampler's do.
func (s *TreeSampler) FaultDelta() (majDelta, minDelta uint64, err error) {
	stats := scanStats(s.fsRoot)
	if _, ok := stats[s.pid]; !ok {
		return 0, 0, fmt.Errorf("procmon: pid %d: %w", s.pid, ErrNotFound)
	}
	children := map[int][]int{}
	for child, st := range stats {
		children[st.PPid] = append(children[st.PPid], child)
	}
	pids := []int{s.pid}
	seen := map[int]bool{s.pid: true}
	for i := 0; i < len(pids); i++ {
		for _, c := range children[pids[i]] {
			if !seen[c] {
				seen[c] = true
				pids = append(pids, c)
			}
		}
	}
	had := s.have
	for _, p := range pids {
		st := stats[p]
		cur := treeCounters{maj: st.MajFaults, min: st.MinFaults}
		prev, latched := s.last[p]
		s.last[p] = cur
		if !had || !latched {
			continue // the tree's or this child's first sight: baseline only
		}
		majDelta += sub(cur.maj, prev.maj)
		minDelta += sub(cur.min, prev.min)
	}
	// Latches for processes no longer in the tree are dropped so the map
	// cannot grow with the run.
	for p := range s.last {
		if !seen[p] {
			delete(s.last, p)
		}
	}
	s.have = true
	return majDelta, minDelta, nil
}

// MajDelta is only the major-fault delta of the tree: the sparkline's sample.
func (s *TreeSampler) MajDelta() (uint64, error) {
	maj, _, err := s.FaultDelta()
	return maj, err
}
