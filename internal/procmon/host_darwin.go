//go:build darwin

package procmon

import (
	"strings"

	"github.com/midagedev/toktape/internal/tape"
	"golang.org/x/sys/unix"
)

// readLiveHost fills the hardware line of the card from this Mac's sysctls —
// the platform's half of the seam HostInfo calls when, and only when, the
// caller asked about the live machine and there is no procfs to read
// instead (TTP-164). The reads are four sysctls, no subprocess, no cgo.
//
// Two decisions are the lead's, recorded here so they are not re-litigated:
//
// OS is "macos", not goos's "darwin". It is the platform facet the hub
// indexes and filters on (internal/publish), the bare token a user picks
// from a list next to linux and windows. Kernel is kern.osproductversion
// ("26.6.2"), the version a macOS user actually quotes — on Linux Kernel is
// the kernel release, and the macOS counterpart of that is the product
// version, not the Darwin kernel release. kern.osrelease ("25.6.0") is
// deliberately dropped: it is derivable from the product version and means
// nothing to the reader the card is for.
//
// And three non-reads. Hostname is left for --host-label: HostnameSource
// exists so a name is never asserted, and the recorder applies the label
// after this call either way. GPUs stay empty: Apple Silicon has no discrete
// GPU and no VRAM of its own, GPUInfo.VRAMBytes means memory the device owns
// exclusively — false on unified memory — and writing the chip in would make
// the card's VRAM bar a claim about a pool that does not exist (the unified
// pool is TTP-178, out of scope here). RAMBytesPerSec stays 0 unless the
// operator states it with --ram-gbs: a chip's published bandwidth is a
// spec-sheet figure, not a reading of this machine, and RAMSource already
// draws exactly that line.
//
// A sysctl that errors leaves its field alone ("" or 0, printed as "?"),
// per the tape contract. The function itself never errors: the OS is known
// at build time and each field degrades on its own, which is the same
// leniency the /proc half shows a tree whose files are missing.
func readLiveHost() (tape.HostInfo, error) {
	h := tape.HostInfo{OS: "macos"}
	if s, err := unix.Sysctl("machdep.cpu.brand_string"); err == nil {
		h.CPU = strings.TrimSpace(s)
	}
	// hw.physicalcpu and hw.logicalcpu are 32-bit sysctls; reading either
	// with SysctlUint64 yields 0 and an input/output error (measured
	// 2026-09-21). hw.memsize is the 64-bit one.
	if n, err := unix.SysctlUint32("hw.physicalcpu"); err == nil {
		h.CPUCores = int(n)
	}
	if n, err := unix.SysctlUint32("hw.logicalcpu"); err == nil {
		h.CPUThreads = int(n)
	}
	if n, err := unix.SysctlUint64("hw.memsize"); err == nil {
		h.RAMBytes = int64(n)
	}
	if s, err := unix.Sysctl("kern.osproductversion"); err == nil {
		h.Kernel = strings.TrimSpace(s)
	}
	return h, nil
}
