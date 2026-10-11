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
// The fixture is SYNTHETIC: written from the forum reports (NVIDIA t/367765,
// unsloth #6757) and the column order of QueryGPUFields. Replace it with a
// real Spark capture when one exists.
func TestGB10ReportsNoMemory(t *testing.T) {
	rows, err := ParseQueryGPU(fixture(t, "querygpu-gb10-synthetic.csv"))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Name != "NVIDIA GB10" {
		t.Fatalf("rows: %+v", rows)
	}
	if rows[0].MemoryTotalBytes != 0 || rows[0].MemoryUsedBytes != 0 {
		t.Errorf("[N/A] memory parsed as %d / %d, want 0 (unknown)", rows[0].MemoryTotalBytes, rows[0].MemoryUsedBytes)
	}

	f := &fakeRunner{gpuOut: fixture(t, "querygpu-gb10-synthetic.csv")}
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
}
