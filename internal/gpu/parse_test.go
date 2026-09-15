package gpu

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return string(b)
}

const mibI = 1024 * 1024

func TestParseQueryGPUTwoRTX3090(t *testing.T) {
	rows, err := ParseQueryGPU(fixture(t, "querygpu-2x3090.csv"))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows: got %d want 2", len(rows))
	}
	got := rows[0]
	want := DeviceRow{
		Index:            0,
		Name:             "NVIDIA GeForce RTX 3090",
		MemoryTotalBytes: 24576 * mibI,
		MemoryUsedBytes:  23102 * mibI,
		UtilPct:          98,
		TempC:            71,
		PowerW:           342.15,
		PowerLimitW:      350,
		ClockSMMHz:       1740,
		ThrottleMask:     ThrottleNone,
		ThrottleKnown:    true,
		Driver:           "570.86.15",
		PCIeGen:          "4",
		PCIeWidth:        "16",
		UUID:             "GPU-0e4f2a1b-8c3d-4e5f-9a0b-1c2d3e4f5a6b",
	}
	if got != want {
		t.Errorf("row 0:\n got %+v\nwant %+v", got, want)
	}
	if got.PCIe() != "4.0 x16" {
		t.Errorf("PCIe: got %q want %q", got.PCIe(), "4.0 x16")
	}
	if rows[1].ThrottleMask != ThrottleSWPowerCap || !Throttled(rows[1].ThrottleMask) {
		t.Errorf("row 1 throttle: got %#x, throttled=%v", rows[1].ThrottleMask, Throttled(rows[1].ThrottleMask))
	}
	if rows[1].Index != 1 || rows[1].MemoryUsedBytes != 21044*mibI {
		t.Errorf("row 1: got %+v", rows[1])
	}
}

func TestParseQueryGPUUnknownCells(t *testing.T) {
	rows, err := ParseQueryGPU(fixture(t, "querygpu-5080.csv"))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows: got %d want 1", len(rows))
	}
	r := rows[0]
	if r.PowerW != 0 {
		t.Errorf("[N/A] power: got %v want 0", r.PowerW)
	}
	if r.PowerLimitW != 0 {
		t.Errorf("[N/A] power limit: got %v want 0", r.PowerLimitW)
	}
	if r.ThrottleKnown {
		t.Errorf("[Not Supported] throttle reasons should be unknown, got mask %#x", r.ThrottleMask)
	}
	if Throttled(r.ThrottleMask) {
		t.Errorf("unknown throttle mask must not read as throttled")
	}
	if r.Name != "NVIDIA GeForce RTX 5080" || r.PCIe() != "5.0 x16" || r.ClockSMMHz != 2610 {
		t.Errorf("row: got %+v", r)
	}
	if r.MemoryTotalBytes != 16303*mibI {
		t.Errorf("memory.total: got %d want %d (MiB, not MB)", r.MemoryTotalBytes, 16303*mibI)
	}
}

func TestParseQueryGPUFourGPUBox(t *testing.T) {
	rows, err := ParseQueryGPU(fixture(t, "querygpu-4xa100.csv"))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 4 {
		t.Fatalf("rows: got %d want 4", len(rows))
	}
	for i, r := range rows {
		if r.Index != i {
			t.Errorf("row %d: index %d", i, r.Index)
		}
		if r.UUID == "" {
			t.Errorf("row %d: empty uuid", i)
		}
	}
	// Idle-only is not throttling; a hw thermal slowdown is.
	if Throttled(rows[2].ThrottleMask) {
		t.Errorf("gpu idle must not read as throttled (mask %#x)", rows[2].ThrottleMask)
	}
	if !Throttled(rows[3].ThrottleMask) {
		t.Errorf("hw thermal slowdown must read as throttled (mask %#x)", rows[3].ThrottleMask)
	}
}

// TestParseQueryGUPowerLimit (lead, 2026-09-15): the limit sits directly
// after the draw it is only a figure against, and an unreadable cell is the
// zero value exactly as an unreadable draw is — the field count is
// position-based, so this is also the test that the column landed where
// QueryGPUFields says it did (the clock and throttle cells after it still
// parse).
func TestParseQueryGUPowerLimit(t *testing.T) {
	rows, err := ParseQueryGPU(
		// index,name,total,used,util,temp,draw,limit,clocks.sm,throttle,driver,gen,width,uuid
		"0, NVIDIA RTX A6000, 49140, 28889, 97, 66, 281.03, 300.00, 1950, 0x0000000000000004, 570.86.15, 4, 16, GPU-1\n" +
			"1, NVIDIA RTX A6000, 49140, 28889, 97, 66, [N/A], [Not Supported], 1710, 0x0, 570.86.15, 4, 16, GPU-2\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows: got %d want 2", len(rows))
	}
	if rows[0].PowerLimitW != 300 {
		t.Errorf("power.limit: got %v want 300", rows[0].PowerLimitW)
	}
	if rows[0].ThrottleMask != ThrottleSWPowerCap || rows[0].ClockSMMHz != 1950 {
		t.Errorf("the cells after the new column shifted: mask %#x, clocks %d", rows[0].ThrottleMask, rows[0].ClockSMMHz)
	}
	if rows[1].PowerLimitW != 0 {
		t.Errorf("[Not Supported] limit: got %v want 0", rows[1].PowerLimitW)
	}
	if rows[1].ClockSMMHz != 1710 || rows[1].UUID != "GPU-2" {
		t.Errorf("row 1 after an unreadable limit: %+v", rows[1])
	}
}

func TestParseQueryGPUErrors(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		wantErr string
	}{
		{
			name: "empty input",
			in:   "",
		},
		{
			name: "blank lines only",
			in:   "\n\n",
		},
		{
			name:    "too few columns",
			in:      "0, NVIDIA GeForce RTX 4090, 24564, 1024\n",
			wantErr: "row 1: got 4 columns, want 14",
		},
		{
			name:    "non-numeric cell",
			in:      "0, NVIDIA GeForce RTX 4090, twelve, 1024, 10, 40, 70.1, 300, 1500, 0x0, 570.86.15, 4, 16, GPU-1\n",
			wantErr: "row 1 field memory.total",
		},
		{
			name:    "bad bitmask",
			in:      "0, NVIDIA GeForce RTX 4090, 24564, 1024, 10, 40, 70.1, 300, 1500, zzz, 570.86.15, 4, 16, GPU-1\n",
			wantErr: "row 1 field clocks_throttle_reasons.active",
		},
		{
			name:    "error reports the offending row number",
			in:      "0, A, 1, 1, 1, 1, 1, 1, 1, 0x0, d, 4, 16, GPU-1\n1, B, 1, 1, 1, 1, 1, 1, 0x0, d, 4, 16\n",
			wantErr: "row 2: got 12 columns",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rows, err := ParseQueryGPU(tc.in)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if len(rows) != 0 {
					t.Fatalf("rows: got %d want 0", len(rows))
				}
				return
			}
			if err == nil {
				t.Fatalf("want error containing %q, got rows %+v", tc.wantErr, rows)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error %q does not contain %q", err, tc.wantErr)
			}
		})
	}
}

func TestParseComputeApps(t *testing.T) {
	apps, err := ParseComputeApps(fixture(t, "computeapps-4xa100.csv"))
	if err != nil {
		t.Fatal(err)
	}
	want := []ComputeApp{
		{GPUUUID: "GPU-aaaa1111-2222-3333-4444-555566667777", PID: 4242, UsedBytes: 38112 * mibI},
		{GPUUUID: "GPU-bbbb1111-2222-3333-4444-555566667777", PID: 4242, UsedBytes: 37904 * mibI},
		// An unreadable pid cell parses as 0 (unknown), not as an error.
		{GPUUUID: "GPU-cccc1111-2222-3333-4444-555566667777", PID: 0, UsedBytes: 1024 * mibI},
		{GPUUUID: "GPU-dddd1111-2222-3333-4444-555566667777", PID: 9001, UsedBytes: 19844 * mibI},
	}
	if len(apps) != len(want) {
		t.Fatalf("apps: got %d want %d", len(apps), len(want))
	}
	for i := range want {
		if apps[i] != want[i] {
			t.Errorf("app %d:\n got %+v\nwant %+v", i, apps[i], want[i])
		}
	}
}

func TestParseComputeAppsTwoColumnForm(t *testing.T) {
	apps, err := ParseComputeApps(fixture(t, "computeapps-2col.csv"))
	if err != nil {
		t.Fatal(err)
	}
	want := []ComputeApp{
		{PID: 4242, UsedBytes: 38112 * mibI},
		{PID: 9001, UsedBytes: 1024 * mibI},
	}
	if len(apps) != len(want) {
		t.Fatalf("apps: got %d want %d", len(apps), len(want))
	}
	for i := range want {
		if apps[i] != want[i] {
			t.Errorf("app %d:\n got %+v\nwant %+v", i, apps[i], want[i])
		}
	}
}

func TestParseComputeAppsEdgeCases(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    int
		wantErr string
	}{
		{name: "no processes running", in: fixtureEmpty, want: 0},
		{name: "unsupported memory cell", in: "GPU-1, 4242, [Not Supported]\n", want: 1},
		{name: "one column", in: "4242\n", wantErr: "row 1: got 1 columns"},
		{name: "non-numeric pid", in: "GPU-1, root, 1024\n", wantErr: "row 1 field pid"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			apps, err := ParseComputeApps(tc.in)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("want error containing %q, got %v", tc.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(apps) != tc.want {
				t.Fatalf("apps: got %d want %d", len(apps), tc.want)
			}
		})
	}
}

const fixtureEmpty = ""

func TestPCIeUnknownHalf(t *testing.T) {
	if got := (DeviceRow{PCIeGen: "4"}).PCIe(); got != "" {
		t.Errorf("half-known PCIe must be unknown, got %q", got)
	}
	if got := (DeviceRow{PCIeWidth: "16"}).PCIe(); got != "" {
		t.Errorf("half-known PCIe must be unknown, got %q", got)
	}
}
