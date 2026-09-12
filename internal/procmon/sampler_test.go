package procmon_test

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/midagedev/toktape/internal/procmon"
	"github.com/midagedev/toktape/internal/tape"
)

func TestSamplerSample(t *testing.T) {
	s, err := procmon.NewSamplerAt(fsRoot, 1234)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.PID() != 1234 {
		t.Errorf("PID = %d, want 1234", s.PID())
	}
	got, err := s.Sample()
	if err != nil {
		t.Fatal(err)
	}
	want, err := procmon.ReadMem(fsRoot, 1234)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("Sample =\n got %+v\nwant %+v", got, want)
	}
}

func TestSamplerFaultDelta(t *testing.T) {
	// A writable copy of the fixture process so the counter can be advanced
	// the way a faulting server would advance it.
	root := t.TempDir()
	copyTree(t, filepath.Join(fsRoot, "proc", "1234"), filepath.Join(root, "proc", "1234"))
	statPath := filepath.Join(root, "proc", "1234", "stat")

	s, err := procmon.NewSamplerAt(root, 1234)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	// First reading has nothing to subtract from: 0, and the counter latched.
	maj, minor, err := s.FaultDelta()
	if err != nil {
		t.Fatal(err)
	}
	if maj != 0 || minor != 0 {
		t.Errorf("first FaultDelta = %d/%d, want 0/0", maj, minor)
	}

	writeStat(t, statPath, 12345+7, 987654+100)
	maj, minor, err = s.FaultDelta()
	if err != nil {
		t.Fatal(err)
	}
	if maj != 7 || minor != 100 {
		t.Errorf("FaultDelta = %d/%d, want 7/100", maj, minor)
	}

	// A second read with no change is 0, not a repeat of the last delta.
	if maj, err := s.MajDelta(); err != nil || maj != 0 {
		t.Errorf("MajDelta with no change = %d, %v, want 0, nil", maj, err)
	}

	writeStat(t, statPath, 12345+9, 987654+100)
	if maj, err := s.MajDelta(); err != nil || maj != 2 {
		t.Errorf("MajDelta = %d, %v, want 2, nil", maj, err)
	}

	// Sample latches too, so a periodic sample between two tokens does not
	// make the next token's delta count the same faults twice.
	writeStat(t, statPath, 12345+20, 987654+100)
	if _, err := s.Sample(); err != nil {
		t.Fatal(err)
	}
	if maj, err := s.MajDelta(); err != nil || maj != 0 {
		t.Errorf("MajDelta right after Sample = %d, %v, want 0, nil", maj, err)
	}

	// A counter that went backwards means the PID was reused; clamp at 0
	// rather than emitting a huge unsigned delta.
	writeStat(t, statPath, 3, 5)
	if maj, err := s.MajDelta(); err != nil || maj != 0 {
		t.Errorf("MajDelta after the counter reset = %d, %v, want 0, nil", maj, err)
	}
}

func TestSamplerCurrentFaults(t *testing.T) {
	s, err := procmon.NewSamplerAt(fsRoot, 1234)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	maj, minor, err := s.Faults()
	if err != nil {
		t.Fatal(err)
	}
	if maj != 12345 || minor != 987654 {
		t.Errorf("Faults = %d/%d, want 12345/987654", maj, minor)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Faults(); err == nil {
		t.Error("Faults after Close: want error, got nil")
	}
	if err := s.Close(); err != nil {
		t.Errorf("second Close = %v, want nil", err)
	}
}

func TestSamplerMissingPID(t *testing.T) {
	if _, err := procmon.NewSamplerAt(fsRoot, 31337); !errors.Is(err, procmon.ErrNotFound) {
		t.Errorf("NewSamplerAt for a dead pid = %v, want ErrNotFound", err)
	}
}

func TestSamplerLongStatLine(t *testing.T) {
	// The initial buffer is 2 KiB; a comm long enough to push the line past it
	// must grow the buffer rather than truncate the line and lose majflt.
	root := t.TempDir()
	dir := filepath.Join(root, "proc", "1234")
	mkdirAll(t, root, filepath.Join("proc", "1234"))
	comm := make([]byte, 3000)
	for i := range comm {
		comm[i] = 'x'
	}
	line := "1234 (" + string(comm) + ") S 1 1234 1234 0 -1 0 111 0 222 0 1 2 0 0 20 0 1 0 3\n"
	if err := os.WriteFile(filepath.Join(dir, "stat"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := procmon.NewSamplerAt(root, 1234)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	maj, minor, err := s.Faults()
	if err != nil {
		t.Fatal(err)
	}
	if maj != 222 || minor != 111 {
		t.Errorf("Faults = %d/%d, want 222/111", maj, minor)
	}
}

func TestNewSamplerLivePlatform(t *testing.T) {
	// Off Linux the live entry point must say so rather than silently
	// reporting zeros; on Linux it opens /proc/self.
	err := procmon.Available()
	s, sErr := procmon.NewSampler(os.Getpid())
	if err != nil {
		if !errors.Is(sErr, procmon.ErrUnsupported) {
			t.Errorf("NewSampler off Linux = %v, want ErrUnsupported", sErr)
		}
		return
	}
	if sErr != nil {
		t.Fatalf("NewSampler on Linux: %v", sErr)
	}
	defer s.Close()
	if _, _, err := s.Faults(); err != nil {
		t.Errorf("Faults on the live /proc: %v", err)
	}
}

func TestSummarize(t *testing.T) {
	// A warm run whose weights were paged in during prefill: one burst of
	// 4,000 major faults before the first text token, then a clean decode of
	// 40 tokens. Index 0 is the empty role chunk llama-server sends first,
	// so the first token that carried text is index 1.
	const firstTokenIdx = 1
	deltas := make([]uint64, 42)
	deltas[0] = 1500
	deltas[firstTokenIdx] = 2500
	deltas[20] = 3 // a stray fault mid-decode

	samples := []tape.RunSample{
		{T: 0, Mem: tape.MemSample{RSSBytes: 10 * gB, MajFaults: 1000}},
		{T: 250 * time.Millisecond, Mem: tape.MemSample{RSSBytes: 56 * gB, MajFaults: 5000}},
		{T: 500 * time.Millisecond, Mem: tape.MemSample{RSSBytes: 50 * gB, MajFaults: 5003}},
	}

	got := procmon.Summarize(samples, deltas, firstTokenIdx)

	if got.MajFaultsPrompt != 4000 {
		t.Errorf("MajFaultsPrompt = %d, want 4000", got.MajFaultsPrompt)
	}
	if got.MajFaultsDecode != 3 {
		t.Errorf("MajFaultsDecode = %d, want 3", got.MajFaultsDecode)
	}
	if got.MajFaultsTotal != 4003 {
		t.Errorf("MajFaultsTotal = %d, want 4003", got.MajFaultsTotal)
	}
	if want := 3.0 / 40.0; got.MajFaultsPerToken != want {
		t.Errorf("MajFaultsPerToken = %v, want %v", got.MajFaultsPerToken, want)
	}
	// This is the assertion the split exists for. Charging the prefill burst
	// to decode would give 4003/40 = 100 faults per token and label a run
	// that never faulted while decoding "cold".
	if procmon.IsCold(got) {
		t.Errorf("run labelled cold at %v faults/token (threshold %v): the prefill burst was counted as decode",
			got.MajFaultsPerToken, tape.ColdMajFaultsPerToken)
	}
	if got.PeakRSSBytes != 56*gB {
		t.Errorf("PeakRSSBytes = %d, want %d", got.PeakRSSBytes, int64(56*gB))
	}
	if got.AtEnd != samples[2].Mem {
		t.Errorf("AtEnd = %+v, want %+v", got.AtEnd, samples[2].Mem)
	}
	// Never inferred from these inputs (handover lesson 3).
	if got.MappedFileBytes != 0 {
		t.Errorf("MappedFileBytes = %d, want 0", got.MappedFileBytes)
	}
}

func TestSummarizeColdRun(t *testing.T) {
	// The other side of the same threshold: faults spread through decode.
	deltas := make([]uint64, 33)
	for i := 1; i < len(deltas); i++ {
		deltas[i] = 4
	}
	got := procmon.Summarize(nil, deltas, 0)
	if got.MajFaultsPrompt != 0 {
		t.Errorf("MajFaultsPrompt = %d, want 0", got.MajFaultsPrompt)
	}
	if got.MajFaultsDecode != 128 {
		t.Errorf("MajFaultsDecode = %d, want 128", got.MajFaultsDecode)
	}
	if got.MajFaultsPerToken != 4 {
		t.Errorf("MajFaultsPerToken = %v, want 4", got.MajFaultsPerToken)
	}
	if !procmon.IsCold(got) {
		t.Error("a run faulting 4 times per decoded token must be labelled cold")
	}
}

func TestSummarizeEdgeCases(t *testing.T) {
	if got := procmon.Summarize(nil, nil, 0); got != (tape.MemorySummary{}) {
		t.Errorf("Summarize of nothing = %+v, want the zero summary", got)
	}

	// Only one event and it carried the text: nothing was decoded after it, so
	// the per-token figure is unknown (0), not a division by zero.
	got := procmon.Summarize(nil, []uint64{9}, 0)
	if got.MajFaultsPrompt != 9 || got.MajFaultsDecode != 0 || got.MajFaultsPerToken != 0 {
		t.Errorf("single-event summary = %+v", got)
	}

	// firstTokenIdx past the end (a stream that failed before any text).
	got = procmon.Summarize(nil, []uint64{1, 2, 3}, 99)
	if got.MajFaultsPrompt != 6 || got.MajFaultsDecode != 0 {
		t.Errorf("out-of-range firstTokenIdx = %+v", got)
	}
	got = procmon.Summarize(nil, []uint64{1, 2, 3}, -5)
	if got.MajFaultsPrompt != 0 || got.MajFaultsDecode != 6 {
		t.Errorf("negative firstTokenIdx = %+v", got)
	}

	// No per-token deltas at all — a remote server with no /proc view, or a
	// tape recorded before the sparkline existed. The run total comes from the
	// periodic samples and is attributed to the prompt phase, so the coarse
	// figure can never be what sets the cold label.
	samples := []tape.RunSample{
		{Mem: tape.MemSample{RSSBytes: 1 * gB, MajFaults: 100}},
		{Mem: tape.MemSample{RSSBytes: 2 * gB, MajFaults: 350}},
	}
	got = procmon.Summarize(samples, nil, 0)
	if got.MajFaultsTotal != 250 || got.MajFaultsPrompt != 250 || got.MajFaultsDecode != 0 {
		t.Errorf("sample-only summary = %+v", got)
	}
	if procmon.IsCold(got) {
		t.Error("a sample-only summary must not label the run cold")
	}
	if got.PeakRSSBytes != 2*gB {
		t.Errorf("PeakRSSBytes = %d, want %d", got.PeakRSSBytes, int64(2*gB))
	}
}

// writeStat rewrites the fixture stat line with new fault counters.
func writeStat(t *testing.T, path string, maj, minor uint64) {
	t.Helper()
	line := "1234 (llama-server (cuda)) S 1 1234 1234 0 -1 4194560 " +
		strconv.FormatUint(minor, 10) + " 0 " + strconv.FormatUint(maj, 10) +
		" 0 456789 12345 0 0 20 0 97 0 8675309\n"
	if err := os.WriteFile(path, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
}
