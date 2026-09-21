//go:build darwin

package procmon_test

import (
	"reflect"
	"testing"

	"github.com/midagedev/toktape/internal/procmon"
	"golang.org/x/sys/unix"
)

// TestHostInfoLiveDarwin is the darwin leg of TTP-164's recurrence gate: a
// live Mac must state the hardware line it can state — CPU, cores, threads,
// RAM, OS and OS version — instead of erroring and printing "?" for all of
// it. It reads the real machine, so it lives behind a darwin tag; the
// fixture-tree half of the gate (TestHostInfoProcTreeOwnsTheAnswer) runs on
// every platform.
//
// Both live spellings are exercised because recorder.Options.normalize
// writes "/" into FSRoot while its zero value means the same root: whatever
// the recorder hands HostInfo, the live Mac must be read, never warned about.
func TestHostInfoLiveDarwin(t *testing.T) {
	h, err := procmon.HostInfo("/")
	if err != nil {
		t.Fatalf("HostInfo(\"/\") on a live Mac: %v", err)
	}
	// The round's evidence line: the lead compares these against sysctl -n.
	brand, _ := unix.Sysctl("machdep.cpu.brand_string")
	product, _ := unix.Sysctl("kern.osproductversion")
	memsize, _ := unix.SysctlUint64("hw.memsize")
	physical, _ := unix.SysctlUint32("hw.physicalcpu")
	logical, _ := unix.SysctlUint32("hw.logicalcpu")
	t.Logf("live darwin HostInfo: %+v", h)
	t.Logf("sysctl oracle: brand=%q product=%q memsize=%d physicalcpu=%d logicalcpu=%d",
		brand, product, memsize, physical, logical)

	if h.OS != "macos" {
		t.Errorf("OS = %q, want macos — the platform facet the hub filters on, not goos's darwin", h.OS)
	}
	if h.CPU != brand {
		t.Errorf("CPU = %q, want the machdep.cpu.brand_string %q", h.CPU, brand)
	}
	if h.CPUCores != int(physical) {
		t.Errorf("CPUCores = %d, want hw.physicalcpu %d", h.CPUCores, physical)
	}
	if h.CPUThreads != int(logical) {
		t.Errorf("CPUThreads = %d, want hw.logicalcpu %d", h.CPUThreads, logical)
	}
	if h.RAMBytes != int64(memsize) {
		t.Errorf("RAMBytes = %d, want hw.memsize %d", h.RAMBytes, memsize)
	}
	if h.Kernel != product {
		t.Errorf("Kernel = %q, want the kern.osproductversion %q", h.Kernel, product)
	}

	// What the reader must NOT claim on this machine (TTP-164): the name is
	// --host-label's to give, never asserted, and unified memory is neither a
	// GPU's exclusive VRAM nor a bandwidth anyone measured on this Mac.
	if h.Hostname != "" || h.HostnameSource != "" {
		t.Errorf("Hostname/HostnameSource = %q/%q, want \"\"/\"\"", h.Hostname, h.HostnameSource)
	}
	if len(h.GPUs) != 0 {
		t.Errorf("GPUs = %v, want none on unified memory", h.GPUs)
	}
	if h.RAMBytesPerSec != 0 || h.RAMSource != "" || h.RAMSpeed != "" || h.RAMChannels != 0 {
		t.Errorf("bandwidth fields = %d/%q/%q/%d, want all unset",
			h.RAMBytesPerSec, h.RAMSource, h.RAMSpeed, h.RAMChannels)
	}

	// "" is the same live root: normalize spells it "/" but the zero value
	// must read the same machine.
	h2, err := procmon.HostInfo("")
	if err != nil {
		t.Fatalf("HostInfo(\"\") on a live Mac: %v", err)
	}
	if !reflect.DeepEqual(h2, h) {
		t.Errorf("HostInfo(\"\") = %+v, want the same answer as \"/\": %+v", h2, h)
	}
}
