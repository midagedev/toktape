package procmon_test

import (
	"strings"
	"testing"

	"github.com/midagedev/toktape/internal/procmon"
)

// The GB10 fixture is SYNTHETIC: built from the field set of the kernel's
// arch/arm64/kernel/cpuinfo.c c_show, with the GB10's two core kinds. Replace
// it with a real Spark /proc/cpuinfo when one is at hand (2026-10-11, TTP-202).
func TestParseCPUInfoARMNamesCoresFromImplementerAndPart(t *testing.T) {
	got := procmon.ParseCPUInfo(readFixture(t, "cpuinfo-gb10-synthetic.txt"))
	if want := "10× Cortex-X925 + 10× Cortex-A725"; got.Model != want {
		t.Errorf("Model = %q, want %q", got.Model, want)
	}
	if got.Threads != 20 || got.Cores != 0 {
		t.Errorf("Threads, Cores = %d, %d; want 20, 0 (no topology keys on ARM)", got.Threads, got.Cores)
	}
}

func armBlocks(parts ...string) []byte {
	var b strings.Builder
	for i, p := range parts {
		b.WriteString("processor\t: " + string(rune('0'+i)) + "\nCPU implementer\t: 0x41\nCPU part\t: " + p + "\n\n")
	}
	return []byte(b.String())
}

func TestParseCPUInfoARMInterleavedGroupsInFirstSeenOrder(t *testing.T) {
	got := procmon.ParseCPUInfo(armBlocks("0xd85", "0xd87", "0xd85", "0xd87", "0xd87"))
	if want := "2× Cortex-X925 + 3× Cortex-A725"; got.Model != want {
		t.Errorf("Model = %q, want %q", got.Model, want)
	}
}

func TestParseCPUInfoARMOneKind(t *testing.T) {
	got := procmon.ParseCPUInfo(armBlocks("0xd4f", "0xd4f"))
	if want := "2× Neoverse-V2"; got.Model != want {
		t.Errorf("Model = %q, want %q", got.Model, want)
	}
}

func TestParseCPUInfoARMUnknownPartPrintsObservedHex(t *testing.T) {
	got := procmon.ParseCPUInfo(armBlocks("0xd99"))
	if want := "1× Arm part 0xd99"; got.Model != want {
		t.Errorf("Model = %q, want %q", got.Model, want)
	}
}

func TestParseCPUInfoARMNVIDIAAndUnknownImplementer(t *testing.T) {
	in := "processor\t: 0\nCPU implementer\t: 0x4e\nCPU part\t: 0x004\n\nprocessor\t: 1\nCPU implementer\t: 0x99\nCPU part\t: 0x004\n\n"
	got := procmon.ParseCPUInfo([]byte(in))
	if want := "1× Carmel + 1× 0x99/0x004"; got.Model != want {
		t.Errorf("Model = %q, want %q", got.Model, want)
	}
}

func TestParseCPUInfoModelNameWinsOverImplementer(t *testing.T) {
	in := "processor\t: 0\nmodel name\t: Foo\nCPU implementer\t: 0x41\nCPU part\t: 0xd85\n\n"
	if got := procmon.ParseCPUInfo([]byte(in)); got.Model != "Foo" {
		t.Errorf("Model = %q, want Foo", got.Model)
	}
}
