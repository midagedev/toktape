package procmon

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// treeFixture is a /proc tree shaped like the ExLlamaV3 engine's two
// processes (2026-09-15): a port-listening parent with one child that does
// the CPU expert work, plus a grandchild, and an unrelated process beside
// them. The counters are distinct so a sum that picks the wrong set is
// visible in the assertion.
//
//	3000 parent   (PPid 1)     RSS 60000 kB  maj 10  min 100   utime 1000 stime 100
//	3100 child    (PPid 3000)  RSS 40000 kB  maj 20  min 200   utime 2000 stime 200
//	3200 grandchild (PPid 3100) RSS 20000 kB maj 30  min 300   utime 3000 stime 300
//	3300 stranger (PPid 1)     RSS  8000 kB  maj  5  min  50   utime  500 stime  50
func treeFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeTreePID(t, root, 3000, 1, 60000, 10, 100, 1000, 100)
	writeTreePID(t, root, 3100, 3000, 40000, 20, 200, 2000, 200)
	writeTreePID(t, root, 3200, 3100, 20000, 30, 300, 3000, 300)
	writeTreePID(t, root, 3300, 1, 8000, 5, 50, 500, 50)
	return root
}

// writeTreePID writes one fixture process: status with the given RSS and stat
// with the given ppid, faults and CPU ticks.
func writeTreePID(t *testing.T, root string, pid, ppid, rss, maj, min, utime, stime int) {
	t.Helper()
	dir := filepath.Join(root, "proc", strconv.Itoa(pid))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	status := "Name:\tproc\nUmask:\t0022\nState:\tS (sleeping)\nTgid:\t" + strconv.Itoa(pid) +
		"\nPid:\t" + strconv.Itoa(pid) + "\nPPid:\t" + strconv.Itoa(ppid) +
		"\nVmPeak:\t 1048576 kB\nVmSize:\t 1048576 kB\nVmHWM:\t" + " " + strconv.Itoa(rss) + " kB\nVmRSS:\t " + strconv.Itoa(rss) + " kB" +
		"\nRssAnon:\t " + strconv.Itoa(rss/2) + " kB\nRssFile:\t " + strconv.Itoa(rss/2) + " kB\nRssShmem:\t 0 kB\nVmSwap:\t 0 kB\nThreads:\t8\n"
	if err := os.WriteFile(filepath.Join(dir, "status"), []byte(status), 0o644); err != nil {
		t.Fatal(err)
	}
	// stat: state ppid pgrp session tty tpgid flags minflt cminflt majflt
	// cmajflt utime stime, then padding ParseStat does not read.
	stat := strconv.Itoa(pid) + " (proc) S " + strconv.Itoa(ppid) + " " + strconv.Itoa(pid) + " " + strconv.Itoa(pid) +
		" 0 -1 0 " + strconv.Itoa(min) + " 0 " + strconv.Itoa(maj) + " 0 " + strconv.Itoa(utime) + " " + strconv.Itoa(stime) +
		" 0 0 20 0 8 0 12345 0 0 0 1 0 0 0 0 0 0 0 0 0\n"
	if err := os.WriteFile(filepath.Join(dir, "stat"), []byte(stat), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestTreePIDs: the parent plus every recursive descendant, and nothing
// else. A pid that does not exist is ErrNotFound.
func TestTreePIDs(t *testing.T) {
	root := treeFixture(t)
	got, err := treePIDs(root, 3000)
	if err != nil {
		t.Fatalf("treePIDs(3000): %v", err)
	}
	want := []int{3000, 3100, 3200}
	if len(got) != len(want) {
		t.Fatalf("treePIDs(3000) = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("treePIDs(3000) = %v, want %v", got, want)
		}
	}
	if got, err := treePIDs(root, 3300); err != nil || len(got) != 1 || got[0] != 3300 {
		t.Errorf("treePIDs(3300) = %v, %v; want [3300], nil", got, err)
	}
	if _, err := treePIDs(root, 4242); !errors.Is(err, ErrNotFound) {
		t.Errorf("treePIDs(4242) error = %v, want ErrNotFound", err)
	}
}

// TestReadMemTree: the tree's sample is the sum over parent and descendants —
// memory, faults and CPU ticks alike — with n naming how many processes went
// in. A child that exited between discovery and read is skipped, not fatal.
func TestReadMemTree(t *testing.T) {
	root := treeFixture(t)
	m, n, err := ReadMemTree(root, 3000)
	if err != nil {
		t.Fatalf("ReadMemTree(3000): %v", err)
	}
	if n != 3 {
		t.Errorf("ReadMemTree(3000) n = %d, want 3", n)
	}
	if want := int64(120000 * 1024); m.RSSBytes != want {
		t.Errorf("RSSBytes = %d, want %d", m.RSSBytes, want)
	}
	if m.MajFaults != 60 || m.MinFaults != 600 {
		t.Errorf("faults = %d/%d, want 60/600", m.MajFaults, m.MinFaults)
	}
	// utime+stime ticks: 1000+100 + 2000+200 + 3000+300 = 6600.
	if want := float64(6600) / ClockTicks; m.CPUSeconds != want {
		t.Errorf("CPUSeconds = %g, want %g", m.CPUSeconds, want)
	}
	// The grandchild exits; the parent and the child are still read.
	if err := os.RemoveAll(filepath.Join(root, "proc", "3200")); err != nil {
		t.Fatal(err)
	}
	m, n, err = ReadMemTree(root, 3000)
	if err != nil {
		t.Fatalf("ReadMemTree after grandchild exit: %v", err)
	}
	if n != 2 || m.RSSBytes != int64(100000*1024) || m.MajFaults != 30 {
		t.Errorf("ReadMemTree after exit = n %d, RSS %d, maj %d; want 2, %d, 30",
			n, m.RSSBytes, m.MajFaults, int64(100000*1024))
	}
}

// TestTreeSampler: the same latch timeline Sampler has — the first delta
// after opening returns 0, and a later delta reports the counter movement of
// the whole tree.
func TestTreeSampler(t *testing.T) {
	root := treeFixture(t)
	s, err := NewTreeSamplerAt(root, 3000)
	if err != nil {
		t.Fatalf("NewTreeSamplerAt: %v", err)
	}
	defer s.Close()
	if maj, min, err := s.FaultDelta(); err != nil || maj != 0 || min != 0 {
		t.Fatalf("first FaultDelta = %d, %d, %v; want 0, 0, nil", maj, min, err)
	}
	// The child takes 5 major and 50 minor faults.
	stat := "3100 (proc) S 3000 3100 3100 0 -1 0 250 0 25 0 2040 240 0 0 20 0 8 0 12345 0 0 0 1 0 0 0 0 0 0 0 0 0\n"
	if err := os.WriteFile(filepath.Join(root, "proc", "3100", "stat"), []byte(stat), 0o644); err != nil {
		t.Fatal(err)
	}
	if maj, min, err := s.FaultDelta(); err != nil || maj != 5 || min != 50 {
		t.Errorf("FaultDelta after child faults = %d, %d, %v; want 5, 50, nil", maj, min, err)
	}
	// FaultDelta and MajDelta share one timeline, as the single-process
	// sampler's do: the delta above was consumed, the next is zero.
	if maj, err := s.MajDelta(); err != nil || maj != 0 {
		t.Errorf("MajDelta after FaultDelta = %d, %v; want 0, nil", maj, err)
	}
	// A child that spawns mid-run arrives with its whole history already on
	// its counters; the per-pid latch baselines it instead of booking its
	// past faults as this token's.
	writeTreePID(t, root, 3400, 3000, 1000, 77, 700, 700, 70)
	if maj, min, err := s.FaultDelta(); err != nil || maj != 0 || min != 0 {
		t.Errorf("FaultDelta after a new child appeared = %d, %d, %v; want 0, 0, nil", maj, min, err)
	}
}
