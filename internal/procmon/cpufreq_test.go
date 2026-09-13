package procmon_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/midagedev/toktape/internal/procmon"
)

// writeTree writes files (path relative to the returned root → contents) into
// a temporary directory, creating the directories on the way. The sysfs cases
// below are one or two small files each, which is cheaper to read here than as
// another fixture tree on disk.
func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for rel, body := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// TestReadCPUMaxKHz (TTP-57): the cap is the largest over the CPUs, and the
// busy fixture puts it on cpu1 with cpu0 already dropped to 2.7 GHz, so a
// reader that takes the first file it globs fails here.
func TestReadCPUMaxKHz(t *testing.T) {
	got, err := procmon.ReadCPUMaxKHz(busyRoot)
	if err != nil {
		t.Fatalf("ReadCPUMaxKHz: %v", err)
	}
	if want := int64(3600000); got != want {
		t.Errorf("ReadCPUMaxKHz(busy) = %d, want %d (the max over cpu0 2.7 GHz and cpu1 3.6 GHz)", got, want)
	}

	// The base tree is a box with one CPU and no sensor chip.
	if got, err := procmon.ReadCPUMaxKHz("testdata"); err != nil || got != 4200000 {
		t.Errorf("ReadCPUMaxKHz(testdata) = %d, %v; want 4200000, nil", got, err)
	}

	// A kernel without cpufreq (a VM, or not Linux) leaves the cap unknown.
	if _, err := procmon.ReadCPUMaxKHz(t.TempDir()); !errors.Is(err, procmon.ErrNotFound) {
		t.Errorf("no cpufreq: err = %v, want ErrNotFound", err)
	}

	// An offline CPU has no cpufreq directory and is skipped; the online one
	// still gives a reading.
	root := writeTree(t, map[string]string{
		"sys/devices/system/cpu/cpu0/cpufreq/scaling_max_freq": "2200000\n",
		"sys/devices/system/cpu/cpu1/online":                   "0\n",
	})
	if got, err := procmon.ReadCPUMaxKHz(root); err != nil || got != 2200000 {
		t.Errorf("one CPU offline: = %d, %v; want 2200000, nil", got, err)
	}

	// A present, readable file that does not parse is an error: the figure
	// must stay unknown rather than become a quiet maximum of the others.
	bad := writeTree(t, map[string]string{
		"sys/devices/system/cpu/cpu0/cpufreq/scaling_max_freq": "3600000\n",
		"sys/devices/system/cpu/cpu1/cpufreq/scaling_max_freq": "<unknown>\n",
	})
	if _, err := procmon.ReadCPUMaxKHz(bad); err == nil {
		t.Error("unparseable scaling_max_freq: want an error")
	}

	// A cap of 0 is not an operating point — 0 already means "unread".
	zero := writeTree(t, map[string]string{
		"sys/devices/system/cpu/cpu0/cpufreq/scaling_max_freq": "0\n",
	})
	if _, err := procmon.ReadCPUMaxKHz(zero); !errors.Is(err, procmon.ErrNotFound) {
		t.Errorf("zero cap: err = %v, want ErrNotFound", err)
	}
}
