package gpu

import (
	"context"
	"testing"
)

// TestGB10ReportsNoMemory (TTP-178, 2026-10-11). On a DGX Spark nvidia-smi lists
// the GPU but answers [N/A] for memory.total and memory.used: one LPDDR5x pool
// is shared with the CPU. The collector must keep the device with a VRAMBytes
// of 0 — unknown, which the card prints as unified and never as "0 GB" — and
// must not invent a bandwidth for it (the table has no GB10 and gets none).
//
// The fixtures are a real DGX Spark's answers (driver 595.84, toktape 0.7.1,
// captured 2026-10-11 by a contributor on bloomery PR #16). memory.total,
// memory.used and power.limit are [N/A]; the PCIe link reads "1 x1", a
// figure about no bus a token crosses, which no card prints. compute-apps
// does report what the server holds: 8205 MiB here, 66883 MiB with a 68 GB
// model serving.
func TestGB10ReportsNoMemory(t *testing.T) {
	rows, err := ParseQueryGPU(fixture(t, "querygpu-gb10.csv"))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Name != "NVIDIA GB10" {
		t.Fatalf("rows: %+v", rows)
	}
	if rows[0].MemoryTotalBytes != 0 || rows[0].MemoryUsedBytes != 0 {
		t.Errorf("[N/A] memory parsed as %d / %d, want 0 (unknown)", rows[0].MemoryTotalBytes, rows[0].MemoryUsedBytes)
	}

	f := &fakeRunner{gpuOut: fixture(t, "querygpu-gb10.csv")}
	c, _ := OpenWith(context.Background(), f.run)
	defer c.Close()
	infos, err := c.Devices(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 1 || infos[0].Name != "NVIDIA GB10" || infos[0].VRAMBytes != 0 {
		t.Errorf("devices: %+v, want one NVIDIA GB10 with VRAMBytes 0", infos)
	}
	if infos[0].PeakBandwidthBytesPerSec != 0 {
		t.Errorf("GB10 peak bandwidth %d: the table must not carry a spec-sheet figure", infos[0].PeakBandwidthBytesPerSec)
	}

	if rows[0].PowerLimitW != 0 {
		t.Errorf("[N/A] power.limit parsed as %v W, want 0 (unknown)", rows[0].PowerLimitW)
	}

	apps, err := ParseComputeApps(fixture(t, "computeapps-gb10.csv"))
	if err != nil {
		t.Fatal(err)
	}
	if len(apps) != 1 || apps[0].PID != 1137262 || apps[0].UsedBytes != 8205*mib {
		t.Errorf("compute-apps: %+v, want pid 1137262 holding 8205 MiB", apps)
	}
}
