package procmon

import (
	"fmt"
	"strings"
)

// Linux does not print "model name" in aarch64 /proc/cpuinfo; each processor
// block carries the MIDR fields instead. The names below are the kernel's own,
// from arch/arm64/include/asm/cputype.h (ARM_CPU_IMP_*, ARM_CPU_PART_*,
// NVIDIA_CPU_PART_*). The file is not named cpu_arm.go: the _arm suffix is a
// GOARCH constraint and would build it only for 32-bit ARM.
const (
	implArm    = "0x41"
	implNVIDIA = "0x4e"
)

var armCPUNames = map[[2]string]string{
	{implArm, "0xd07"}:    "Cortex-A57",
	{implArm, "0xd08"}:    "Cortex-A72",
	{implArm, "0xd0c"}:    "Neoverse-N1",
	{implArm, "0xd40"}:    "Neoverse-V1",
	{implArm, "0xd42"}:    "Cortex-A78AE",
	{implArm, "0xd49"}:    "Neoverse-N2",
	{implArm, "0xd4f"}:    "Neoverse-V2",
	{implArm, "0xd81"}:    "Cortex-A720",
	{implArm, "0xd85"}:    "Cortex-X925",
	{implArm, "0xd87"}:    "Cortex-A725",
	{implArm, "0xd89"}:    "Cortex-A720AE",
	{implNVIDIA, "0x003"}: "Denver",
	{implNVIDIA, "0x004"}: "Carmel",
	{implNVIDIA, "0x010"}: "Olympus",
}

// armCPUCounts accumulates (implementer, part) pairs in first-seen order.
type armCPUCounts struct {
	kinds  [][2]string
	counts map[[2]string]int
}

func (a *armCPUCounts) add(impl, part string) {
	k := [2]string{strings.ToLower(impl), strings.ToLower(part)}
	if a.counts == nil {
		a.counts = make(map[[2]string]int)
	}
	if a.counts[k] == 0 {
		a.kinds = append(a.kinds, k)
	}
	a.counts[k]++
}

// name renders "10× Cortex-X925 + 10× Cortex-A725", or "" when no block
// carried both fields. An unnamed pair prints what the kernel reported.
func (a *armCPUCounts) name() string {
	parts := make([]string, 0, len(a.kinds))
	for _, k := range a.kinds {
		n, ok := armCPUNames[k]
		switch {
		case !ok && k[0] == implArm:
			n = "Arm part " + k[1]
		case !ok:
			n = k[0] + "/" + k[1]
		}
		parts = append(parts, fmt.Sprintf("%d× %s", a.counts[k], n))
	}
	return strings.Join(parts, " + ")
}
