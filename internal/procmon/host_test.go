package procmon_test

import (
	"errors"
	"testing"

	"github.com/midagedev/toktape/internal/procmon"
)

func TestParseKernelVersion(t *testing.T) {
	tests := []struct{ in, want string }{
		{"Linux version 6.8.0-45-generic (buildd@lcy02) (gcc 12) #45~22.04.1-Ubuntu SMP\n", "6.8.0-45-generic"},
		{"Linux version 6.6.87.2-microsoft-standard-WSL2 (root@x) #1 SMP\n", "6.6.87.2-microsoft-standard-WSL2"},
		{"Darwin Kernel Version 25.6.0", ""},
		{"", ""},
		{"Linux version ", ""},
	}
	for _, tt := range tests {
		if got := procmon.ParseKernelVersion([]byte(tt.in)); got != tt.want {
			t.Errorf("ParseKernelVersion(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestParseCPUInfo(t *testing.T) {
	got := procmon.ParseCPUInfo(readFixture(t, "proc/cpuinfo"))
	want := procmon.CPUInfo{Model: "AMD EPYC 7313 16-Core Processor", Cores: 32, Threads: 64}
	if got != want {
		t.Errorf("ParseCPUInfo =\n got %+v\nwant %+v", got, want)
	}
	// "cpu cores: 16" is per socket. Reading it instead of counting
	// (physical id, core id) pairs would halve the answer on this 2-socket box.
	if got.Cores == 16 {
		t.Error("Cores read the per-socket 'cpu cores' value instead of counting pairs")
	}
}

func TestParseCPUInfoWithoutTopology(t *testing.T) {
	// Most ARM kernels publish neither physical id nor core id. Cores must
	// stay 0 — the card prints "?" rather than repeating the thread count.
	in := "processor\t: 0\nmodel name\t: Cortex-A76\n\nprocessor\t: 1\nmodel name\t: Cortex-A76\n"
	got := procmon.ParseCPUInfo([]byte(in))
	want := procmon.CPUInfo{Model: "Cortex-A76", Cores: 0, Threads: 2}
	if got != want {
		t.Errorf("ParseCPUInfo =\n got %+v\nwant %+v", got, want)
	}
}

func TestParseCPUInfoSingleSocketNoSMT(t *testing.T) {
	var in string
	for i := 0; i < 4; i++ {
		in += "processor\t: " + string(rune('0'+i)) + "\n" +
			"model name\t: Intel(R) Core(TM) i7-9700K CPU @ 3.60GHz\n" +
			"physical id\t: 0\n" +
			"core id\t\t: " + string(rune('0'+i)) + "\n\n"
	}
	got := procmon.ParseCPUInfo([]byte(in))
	want := procmon.CPUInfo{Model: "Intel(R) Core(TM) i7-9700K CPU @ 3.60GHz", Cores: 4, Threads: 4}
	if got != want {
		t.Errorf("ParseCPUInfo =\n got %+v\nwant %+v", got, want)
	}
}

func TestParseMemTotal(t *testing.T) {
	got, err := procmon.ParseMemTotal(readFixture(t, "proc/meminfo"))
	if err != nil {
		t.Fatal(err)
	}
	if want := int64(792723456) * kB; got != want {
		t.Errorf("ParseMemTotal = %d, want %d", got, want)
	}
	if _, err := procmon.ParseMemTotal([]byte("MemFree: 1 kB\n")); !errors.Is(err, procmon.ErrNotFound) {
		t.Errorf("ParseMemTotal without a MemTotal line = %v, want ErrNotFound", err)
	}
}

func TestParseLoadAvg(t *testing.T) {
	got, err := procmon.ParseLoadAvg([]byte("3.21 2.87 2.44 5/2317 48291\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got != 3.21 {
		t.Errorf("ParseLoadAvg = %v, want 3.21", got)
	}
	if _, err := procmon.ParseLoadAvg([]byte("")); !errors.Is(err, procmon.ErrNotFound) {
		t.Errorf("ParseLoadAvg of an empty file = %v, want ErrNotFound", err)
	}
	if _, err := procmon.ParseLoadAvg([]byte("x y z\n")); err == nil {
		t.Error("ParseLoadAvg of a malformed file: want error, got nil")
	}
}

func TestLoadAvg(t *testing.T) {
	got, err := procmon.LoadAvg(fsRoot)
	if err != nil {
		t.Fatal(err)
	}
	if got != 3.21 {
		t.Errorf("LoadAvg = %v, want 3.21", got)
	}
	// Contention is judged against the thread count: 3.21 on 64 threads is a
	// quiet machine, and the label must not fire on the load average alone.
	if _, err := procmon.LoadAvg(t.TempDir()); !errors.Is(err, procmon.ErrNotFound) {
		t.Errorf("LoadAvg with no proc tree = %v, want ErrNotFound", err)
	}
}

func TestHostInfo(t *testing.T) {
	h, err := procmon.HostInfo(fsRoot)
	if err != nil {
		t.Fatal(err)
	}
	if h.OS != "linux" {
		t.Errorf("OS = %q, want linux", h.OS)
	}
	if h.Hostname != "rig-epyc" {
		t.Errorf("Hostname = %q, want rig-epyc", h.Hostname)
	}
	if h.Kernel != "6.8.0-45-generic" {
		t.Errorf("Kernel = %q, want 6.8.0-45-generic", h.Kernel)
	}
	if h.CPU != "AMD EPYC 7313 16-Core Processor" {
		t.Errorf("CPU = %q", h.CPU)
	}
	if h.CPUCores != 32 || h.CPUThreads != 64 {
		t.Errorf("CPUCores/CPUThreads = %d/%d, want 32/64", h.CPUCores, h.CPUThreads)
	}
	if want := int64(792723456) * kB; h.RAMBytes != want {
		t.Errorf("RAMBytes = %d, want %d", h.RAMBytes, want)
	}
	// DMI is root-only, so these stay unknown and the card prints "?".
	if h.RAMSpeed != "" || h.RAMChannels != 0 {
		t.Errorf("RAMSpeed/RAMChannels = %q/%d, want \"\"/0", h.RAMSpeed, h.RAMChannels)
	}
	// GPUs come from another reader.
	if len(h.GPUs) != 0 {
		t.Errorf("GPUs = %v, want none", h.GPUs)
	}
}

func TestHostInfoMissingFiles(t *testing.T) {
	// An empty but present /proc: every field is unknown, and that is not an
	// error — the card prints "?" for each.
	root := t.TempDir()
	mkdirAll(t, root, "proc")
	h, err := procmon.HostInfo(root)
	if err != nil {
		t.Fatal(err)
	}
	if h.OS != "linux" {
		t.Errorf("OS = %q, want linux", h.OS)
	}
	if h.Hostname != "" || h.Kernel != "" || h.CPU != "" || h.CPUCores != 0 || h.RAMBytes != 0 {
		t.Errorf("want every field unknown, got %+v", h)
	}
	// No /proc at all is an error: nothing below it can be trusted.
	if _, err := procmon.HostInfo(t.TempDir()); err == nil {
		t.Error("HostInfo with no proc directory: want error, got nil")
	}
}
