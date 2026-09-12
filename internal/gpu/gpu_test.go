package gpu

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// fakeRunner answers the two queries this package issues from fixture text.
// It records the calls so a test can assert a query was not made at all.
type fakeRunner struct {
	gpuOut  string
	gpuErr  error
	appsOut string
	appsErr error
	calls   []string
}

func (f *fakeRunner) run(_ context.Context, args ...string) (string, error) {
	joined := strings.Join(args, " ")
	f.calls = append(f.calls, joined)
	switch {
	case strings.Contains(joined, "--query-compute-apps"):
		return f.appsOut, f.appsErr
	case strings.Contains(joined, "--query-gpu"):
		return f.gpuOut, f.gpuErr
	}
	return "", errors.New("unexpected nvidia-smi invocation: " + joined)
}

func (f *fakeRunner) countOf(substr string) int {
	n := 0
	for _, c := range f.calls {
		if strings.Contains(c, substr) {
			n++
		}
	}
	return n
}

func TestOpenPicksNvidiaSMI(t *testing.T) {
	f := &fakeRunner{gpuOut: fixture(t, "querygpu-2x3090.csv")}
	c, warnings := OpenWith(context.Background(), f.run)
	defer c.Close()
	if c.Name() != "nvidia-smi" {
		t.Fatalf("Name = %q, want nvidia-smi", c.Name())
	}
	if len(warnings) != 1 || warnings[0] != WarnNoNVML {
		t.Fatalf("warnings = %q, want [%q]", warnings, WarnNoNVML)
	}
}

func TestOpenFallsBackToNull(t *testing.T) {
	tests := []struct {
		name    string
		f       *fakeRunner
		wantMsg string
	}{
		{
			name:    "nvidia-smi is not installed",
			f:       &fakeRunner{gpuErr: errors.New(`exec: "nvidia-smi": executable file not found in $PATH`)},
			wantMsg: "nvidia-smi unavailable",
		},
		{
			name:    "the query returned something we do not understand",
			f:       &fakeRunner{gpuOut: "0, NVIDIA GeForce RTX 4090, 24564\n"},
			wantMsg: "nvidia-smi output not understood",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c, warnings := OpenWith(context.Background(), tc.f.run)
			defer c.Close()
			if c.Name() != "none" {
				t.Fatalf("Name = %q, want none", c.Name())
			}
			if len(warnings) != 1 || !strings.Contains(warnings[0], tc.wantMsg) {
				t.Fatalf("warnings = %q, want one containing %q", warnings, tc.wantMsg)
			}
			// Open never fails, so the caller needs no GPU-present branch.
			devs, err := c.Devices(context.Background())
			if err != nil || len(devs) != 0 {
				t.Fatalf("Null.Devices = %v, %v; want empty, nil", devs, err)
			}
			samples, err := c.Sample(context.Background(), 4242)
			if err != nil || len(samples) != 0 {
				t.Fatalf("Null.Sample = %v, %v; want empty, nil", samples, err)
			}
		})
	}
}

func TestDevices(t *testing.T) {
	f := &fakeRunner{gpuOut: fixture(t, "querygpu-2x3090.csv")}
	c, _ := OpenWith(context.Background(), f.run)
	defer c.Close()
	devs, err := c.Devices(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(devs) != 2 {
		t.Fatalf("devices: got %d want 2", len(devs))
	}
	d := devs[0]
	if d.Index != 0 || d.Name != "NVIDIA GeForce RTX 3090" || d.VRAMBytes != 24576*mibI {
		t.Errorf("device 0: %+v", d)
	}
	if d.Driver != "570.86.15" || d.PCIe != "4.0 x16" {
		t.Errorf("device 0 driver/pcie: %+v", d)
	}
	if d.PeakBandwidthBytesPerSec != 936_200_000_000 {
		t.Errorf("peak bandwidth: got %d want %d", d.PeakBandwidthBytesPerSec, 936_200_000_000)
	}
	if devs[1].Index != 1 {
		t.Errorf("device 1 index: %d", devs[1].Index)
	}
}

func TestDevicesUnknownCardHasNoBandwidth(t *testing.T) {
	f := &fakeRunner{gpuOut: "0, NVIDIA GeForce RTX 9090, 24564, 1024, 10, 40, 70.1, 1500, 0x0, 590.1, 5, 16, GPU-1\n"}
	c, _ := OpenWith(context.Background(), f.run)
	defer c.Close()
	devs, err := c.Devices(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if devs[0].PeakBandwidthBytesPerSec != 0 {
		t.Errorf("an unknown card must report 0, got %d", devs[0].PeakBandwidthBytesPerSec)
	}
}

func TestSampleAttributesProcessesByUUID(t *testing.T) {
	f := &fakeRunner{
		gpuOut:  fixture(t, "querygpu-4xa100.csv"),
		appsOut: fixture(t, "computeapps-4xa100.csv"),
	}
	c, _ := OpenWith(context.Background(), f.run)
	defer c.Close()
	samples, err := c.Sample(context.Background(), 4242)
	if err != nil {
		t.Fatal(err)
	}
	if len(samples) != 4 {
		t.Fatalf("samples: got %d want 4", len(samples))
	}
	want := []struct {
		procBytes  int64
		otherProcs int
		throttled  bool
	}{
		{38112 * mibI, 0, false},
		{37904 * mibI, 0, false},
		// GPU2 carries a compute app whose pid cell was unreadable. It is
		// neither ours nor counted against us: an unreadable cell must not
		// flip the card's contention verdict. Its throttle mask is
		// idle-only, which is not throttling.
		{0, 0, false},
		{0, 1, true}, // someone else's job, and hw thermal slowdown
	}
	for i, w := range want {
		got := samples[i]
		if got.Index != i {
			t.Errorf("sample %d: index %d", i, got.Index)
		}
		if got.ProcBytes != w.procBytes {
			t.Errorf("sample %d ProcBytes: got %d want %d", i, got.ProcBytes, w.procBytes)
		}
		if got.OtherProcs != w.otherProcs {
			t.Errorf("sample %d OtherProcs: got %d want %d", i, got.OtherProcs, w.otherProcs)
		}
		if got.Throttled != w.throttled {
			t.Errorf("sample %d Throttled: got %v want %v", i, got.Throttled, w.throttled)
		}
	}
	if samples[0].UsedBytes != 78214*mibI || samples[0].UtilPct != 100 || samples[0].TempC != 62 || samples[0].PowerW != 391.44 || samples[0].ClockMHz != 1410 {
		t.Errorf("sample 0 readings: %+v", samples[0])
	}
}

func TestSampleWithoutServerPIDAttributesNothing(t *testing.T) {
	f := &fakeRunner{
		gpuOut:  fixture(t, "querygpu-4xa100.csv"),
		appsOut: fixture(t, "computeapps-4xa100.csv"),
	}
	c, _ := OpenWith(context.Background(), f.run)
	defer c.Close()
	before := f.countOf("--query-compute-apps")
	samples, err := c.Sample(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	// An unknown server pid must not make the server itself count as a
	// foreign GPU process, which would flip the card's contention verdict.
	for i, s := range samples {
		if s.ProcBytes != 0 || s.OtherProcs != 0 {
			t.Errorf("sample %d: got ProcBytes %d OtherProcs %d, want 0/0", i, s.ProcBytes, s.OtherProcs)
		}
	}
	if got := f.countOf("--query-compute-apps") - before; got != 0 {
		t.Errorf("compute-apps was queried %d times with an unknown pid, want 0", got)
	}
	// The device readings are still taken.
	if len(samples) != 4 || samples[0].UsedBytes != 78214*mibI {
		t.Errorf("device readings missing: %+v", samples)
	}
}

func TestSampleSingleGPUAcceptsTwoColumnComputeApps(t *testing.T) {
	f := &fakeRunner{
		gpuOut:  fixture(t, "querygpu-5080.csv"),
		appsOut: fixture(t, "computeapps-2col.csv"),
	}
	c, _ := OpenWith(context.Background(), f.run)
	defer c.Close()
	samples, err := c.Sample(context.Background(), 4242)
	if err != nil {
		t.Fatal(err)
	}
	if len(samples) != 1 {
		t.Fatalf("samples: got %d want 1", len(samples))
	}
	if samples[0].ProcBytes != 38112*mibI {
		t.Errorf("ProcBytes: got %d want %d", samples[0].ProcBytes, 38112*mibI)
	}
	if samples[0].OtherProcs != 1 {
		t.Errorf("OtherProcs: got %d want 1", samples[0].OtherProcs)
	}
	if samples[0].PowerW != 0 || samples[0].Throttled {
		t.Errorf("unknown cells must stay zero: %+v", samples[0])
	}
}

func TestSampleDropsUnattributableProcessesOnMultiGPU(t *testing.T) {
	f := &fakeRunner{
		gpuOut:  fixture(t, "querygpu-2x3090.csv"),
		appsOut: fixture(t, "computeapps-2col.csv"),
	}
	c, _ := OpenWith(context.Background(), f.run)
	defer c.Close()
	samples, err := c.Sample(context.Background(), 4242)
	if err != nil {
		t.Fatal(err)
	}
	// With no uuid column and more than one device there is no honest
	// attribution, so nothing is assigned rather than guessed.
	for i, s := range samples {
		if s.ProcBytes != 0 || s.OtherProcs != 0 {
			t.Errorf("sample %d: got ProcBytes %d OtherProcs %d, want 0/0", i, s.ProcBytes, s.OtherProcs)
		}
	}
}

func TestSampleReportsQueryErrors(t *testing.T) {
	f := &fakeRunner{
		gpuOut:  fixture(t, "querygpu-2x3090.csv"),
		appsErr: errors.New("Function Not Found"),
	}
	c, _ := OpenWith(context.Background(), f.run)
	defer c.Close()
	if _, err := c.Sample(context.Background(), 4242); err == nil {
		t.Fatal("want an error when compute-apps fails")
	} else if !strings.Contains(err.Error(), "query-compute-apps") {
		t.Fatalf("error does not name the query: %v", err)
	}
}

func TestQueryArgs(t *testing.T) {
	gpu := strings.Join(queryGPUArgs(), " ")
	if !strings.Contains(gpu, "--query-gpu=index,name,memory.total,memory.used,utilization.gpu,temperature.gpu,power.draw,clocks.sm,clocks_throttle_reasons.active,driver_version,pcie.link.gen.current,pcie.link.width.current,uuid") {
		t.Errorf("query-gpu args: %s", gpu)
	}
	if !strings.Contains(gpu, "--format=csv,noheader,nounits") {
		t.Errorf("query-gpu format: %s", gpu)
	}
	apps := strings.Join(queryComputeAppsArgs(), " ")
	if !strings.Contains(apps, "--query-compute-apps=gpu_uuid,pid,used_memory") {
		t.Errorf("query-compute-apps args: %s", apps)
	}
}
