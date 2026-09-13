package bandwidth

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/midagedev/toktape/internal/tape"
)

// heroTapePath is the README's own recording, checked into the repo.
const heroTapePath = "../../assets/hero.tape"

// readHeroTape loads it, or skips: the bandwidth package must stay testable in
// a tree where the asset has been moved by another track.
func readHeroTape(t *testing.T) *tape.Tape {
	t.Helper()
	p, err := filepath.Abs(heroTapePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); err != nil {
		t.Skipf("hero tape not present at %s: %v", p, err)
	}
	tp, err := tape.Read(p)
	if err != nil {
		t.Fatalf("read %s: %v", p, err)
	}
	return tp
}

// TestExplainHeroTape is the debuggability layer of TTP-56/68 made permanent,
// and it is the command this round's numbers came out of:
//
//	go test ./internal/bandwidth/ -run TestExplainHeroTape -v
//
// Finding the 10.71 % gap took a scratch script against a tape dump, twice.
// Explain walks the same path the card's clauses walk and prints every figure
// they consulted, so the next person answers "why is there no ratio?" from one
// command instead of re-deriving it by hand.
//
// It also pins the real recording against heroSummary(): the fixture below is
// a hand transcription of this file, and a transcription that drifts is worse
// than no fixture.
func TestExplainHeroTape(t *testing.T) {
	s := &readHeroTape(t).Summary
	e := Explain(s)
	t.Logf("assets/hero.tape as recorded:\n%s", e)

	if s.Concurrency != 2 {
		t.Fatalf("hero tape Concurrency = %d, want the two-stream run", s.Concurrency)
	}

	// The recording predates TTP-68, so every device falls back — which is
	// exactly why it prints no ratio, and Explain says so in one line.
	for _, d := range e.Devices {
		if d.Recorded {
			t.Errorf("%s claims a recorded active-bytes figure; this tape predates the field", d.Device)
		}
	}
	if want := int64(818_173_440); e.Gap != want {
		t.Errorf("gap = %d, want %d — the router and shared expert scaled as if sparse", e.Gap, want)
	}
	if e.WithinTolerance {
		t.Error("the gap should be over SplitTolerance, which is why this tape has no ceiling")
	}
	if e.CeilingKnown || e.OfPeakKnown {
		t.Error("a tape with no host bandwidth and a 10.7 % gap must report no ceiling and no ratio")
	}
	if e.HostKnown {
		t.Error("procmon cannot read the DMI tables, so this recording has no host bandwidth")
	}
	if !strings.Contains(e.String(), "of peak ?") {
		t.Error("String() should say the ratio is unknown rather than print a figure")
	}

	// TTP-67: the verify-step view must work on this run, because this run is
	// the card anyone sees. Before the fix it refused every N > 1.
	v, ok := Speculative(s)
	if !ok {
		t.Fatal("Speculative not ok on the real two-stream hero tape")
	}
	if v.Steps != 156 || v.Streams != 2 {
		t.Errorf("Steps/Streams = %d/%d, want 156/2", v.Steps, v.Streams)
	}

	// And the transcription check: the fixture must reproduce the recording's
	// own figures, on the fallback path the recording is in.
	fx := wsLegacySummary()
	fx.Concurrency = s.Concurrency
	fx.Timings = s.Timings
	fx.Aggregate = s.Aggregate
	fxv, ok := Speculative(fx)
	if !ok {
		t.Fatal("Speculative not ok on the fixture")
	}
	if fxv != v {
		t.Errorf("fixture placement disagrees with the recording:\n got %+v\nwant %+v", fxv, v)
	}
	for i, d := range s.Placement.Devices {
		if got, want := d.Classes, fx.Placement.Devices[i].Classes; !sameClasses(got, want) {
			t.Errorf("device %s classes drifted from the fixture:\n got %v\nwant %v", d.Device, got, want)
		}
	}
}

func sameClasses(a, b map[tape.TensorClass]int64) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// TestExplainSaysWhyTheRatioAppears is the other half: the same recording with
// the two fixes applied explains itself the other way round, and every Known
// flag the card reads is true.
func TestExplainSaysWhyTheRatioAppears(t *testing.T) {
	s := heroSummary()
	s.Host.RAMBytesPerSec = wsSTREAMBytesPerSec
	s.Host.RAMSource = tape.RAMSourceMeasured

	e := Explain(s)
	t.Logf("the same run with TTP-68 and TTP-45 applied:\n%s", e)

	for _, d := range e.Devices {
		if !d.Recorded {
			t.Errorf("%s should carry a recorded active-bytes figure", d.Device)
		}
		if d.PeakBytesPerSec <= 0 {
			t.Errorf("%s has no peak bandwidth; the ceiling cannot be derived", d.Device)
		}
	}
	if e.Gap != 0 || !e.WithinTolerance {
		t.Errorf("gap = %d (%.4f), want 0", e.Gap, e.GapFraction)
	}
	if !e.HostKnown || e.HostSource != tape.RAMSourceMeasured {
		t.Errorf("host = %d/%q/%v, want the stated STREAM figure", e.HostBytesPerSec, e.HostSource, e.HostKnown)
	}
	if !e.CeilingKnown || !e.OfPeakKnown || !e.RAMKnown || !e.VerifyKnown {
		t.Errorf("ceiling %v, of peak %v, ram %v, verify %v; want every clause derivable",
			e.CeilingKnown, e.OfPeakKnown, e.RAMKnown, e.VerifyKnown)
	}
	if !e.RAM.Exact {
		t.Error("RAM should be exact: every device states its own active bytes")
	}
	if strings.Contains(e.String(), "of peak ?") {
		t.Error("String() should print the ratio now that it is derivable")
	}
}

func TestExplainNil(t *testing.T) {
	e := Explain(nil)
	if e.CeilingKnown || e.OfPeakKnown || e.RAMKnown || e.VerifyKnown || e.HostKnown {
		t.Errorf("Explain(nil) claimed something: %+v", e)
	}
	if e.String() == "" {
		t.Error("String() should still render an all-unknown explanation")
	}
}
