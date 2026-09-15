package bandwidth

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/midagedev/toktape/internal/placement"
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

	// What this test asserted until the v0.2.0 hero was recorded (lead,
	// 2026-09-14): that every device fell back to the class-proportion
	// estimate, that the fallback left a 10.71 % gap, and that the card
	// therefore printed "of peak ?". All three were true of a tape recorded
	// before TTP-68 and TTP-45 landed, and the point of those tickets was that
	// they should stop being true of anything this tool records. The shipped
	// asset was re-recorded on a build that has both, so the assertions are
	// turned over rather than relaxed: the same file now has to show the fixes.
	//
	// The fallback did not lose its coverage. wsLegacySummary() in
	// split_test.go is that shape, its doc comment says so, and eight tests in
	// this package read it — which is the arrangement the comment on it always
	// described: the fixture keeps the fallback exercised after the recorder
	// stops producing it. This is the release where the recorder stopped.
	for _, d := range e.Devices {
		if !d.Recorded {
			t.Errorf("%s has no recorded active-bytes figure; a tape recorded on this build carries one per device (TTP-68)", d.Device)
		}
	}
	if e.Gap != 0 {
		t.Errorf("gap = %d, want 0: the per-device figures are the recorder's own, so they sum to the model's", e.Gap)
	}
	if !e.WithinTolerance {
		t.Error("a zero gap is inside SplitTolerance by construction")
	}
	if !e.HostKnown {
		t.Error("the hero is recorded with --ram-gbs-measured, so the host bus is known")
	}
	if !e.CeilingKnown || !e.OfPeakKnown {
		t.Error("a known host bus and a gap inside tolerance are exactly the two conditions for a ceiling and a ratio (TTP-45)")
	}
	if strings.Contains(e.String(), "of peak ?") {
		t.Errorf("String() still reports the ratio as unknown:\n%s", e)
	}

	// TTP-67: the verify-step view must work on this run, because this run is
	// the card anyone sees. Before the fix it refused every N > 1.
	v, ok := Speculative(s)
	if !ok {
		t.Fatal("Speculative not ok on the real two-stream hero tape")
	}
	if v.Streams != 2 {
		t.Errorf("Streams = %d, want 2", v.Streams)
	}
	if v.Steps != 170 {
		t.Errorf("Steps = %d, want 170 (total_predicted_n 430 - accepted 260)", v.Steps)
	}
	// The step is a batch of more than one token or the draft did nothing, and
	// a batch over n_max + 1 would mean the two sums were mixed again.
	if v.Batch <= 1 || v.Batch > 4 {
		t.Errorf("batch = %v, want more than one token and no more than n_max + 1 = 4", v.Batch)
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

// TestExplainEnginePlacementPrintsAbsencesAsAbsences (lead, 2026-09-15): the
// explain block of the exl3 trial printed "active 0" per device, a
// "sum 0/token gap +100 %" line and "effective 238.7 GB/s", and all three read
// as figures. None is one: the engine reported no per-device active bytes —
// which is an absence, and absences print as "?" (CLAUDE.md) — and the
// combined figure is refused for exactly this placement (Combined). The block
// says so per line instead, with the reason attached to the "?".
func TestExplainEnginePlacementPrintsAbsencesAsAbsences(t *testing.T) {
	s := &tape.RunSummary{
		Model:     engineModelFixture(),
		Placement: tape.PlacementSummary{Source: placement.SourceEngine, Devices: []tape.DevicePlacement{engineDeviceFixture()}},
		// The recorder did write this figure (it is ActiveBytesPerToken × the
		// decode rate); the point of the row is that the card refuses it.
		Timings: tape.TimingsSummary{EffectiveBandwidthBytesPerSec: 138_600_000_000},
	}
	e := Explain(s)
	out := e.String()
	for _, want := range []string{
		"active ?",
		"sum     ? — the engine reported no per-device active bytes, so there is nothing to sum",
		"effective ? — refused for an engine placement: one figure over RAM and VRAM buses is not a bandwidth",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the engine block lacks %q:\n%s", want, out)
		}
	}
	for _, stale := range []string{"active 0", "sum     0/token", "+100", "effective 138.6"} {
		if strings.Contains(out, stale) {
			t.Errorf("the engine block still prints %q, a figure nobody measured:\n%s", stale, out)
		}
	}
	// The rendering changed; the arithmetic did not: the gap against the
	// model's own figure stays computed, for a consumer that wants the number.
	if e.Gap != s.Model.ActiveBytesPerToken {
		t.Errorf("Gap = %d, want the record figure it always was", e.Gap)
	}
	if e.WithinTolerance {
		t.Error("a 100 % gap is within tolerance; the check it feeds is unchanged")
	}
	if e.EffectiveKnown {
		t.Error("EffectiveKnown = true; Combined refuses the figure for an engine placement")
	}
}

// TestExplainGGUFWritesWhatItAlwaysWrote: the engine changes are rendering
// changes for an engine placement only. A GGUF tape keeps every line this file
// printed before them — recorded active bytes, the class estimate where there
// is no recording, the gap sum line, and the effective figure Combined keeps.
func TestExplainGGUFWritesWhatItAlwaysWrote(t *testing.T) {
	s := heroSummary()
	e := Explain(s)
	if !e.EffectiveKnown {
		t.Fatal("a GGUF tape with a recorded effective figure has EffectiveKnown = false")
	}
	if out := e.String(); !strings.Contains(out, "effective 103.0 GB/s") {
		t.Errorf("the GGUF block lost its effective figure:\n%s", out)
	} else if strings.Contains(out, "refused for an engine placement") {
		t.Errorf("a GGUF tape is refused an engine placement's refusal:\n%s", out)
	}

	// The estimate path: a device with no recorded figure under gguf+args is
	// estimated, and an estimate prints as the figure it is, never as "?".
	legacy := Explain(wsLegacySummary())
	out := legacy.String()
	if strings.Contains(out, "active ?") {
		t.Errorf("a GGUF class estimate prints as an absence:\n%s", out)
	}
	if !strings.Contains(out, "sum     ") || strings.Contains(out, "sum     ?") {
		t.Errorf("the GGUF sum line is not the gap line it always was:\n%s", out)
	}
}
