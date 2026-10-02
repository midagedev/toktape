package publish

import (
	"testing"

	"github.com/midagedev/toktape/internal/tape"
)

// TestIndexOfDecisionTape: a decision run publishes (2026-10-02; until then
// Upload refused it with ErrDecision, because the hub read only decode
// figures). Its row says the mode and carries the decision figures, and
// none of the token ones: a decision row with a decode rate or a stream
// count would sort and filter beside the benchmarks.
func TestIndexOfDecisionTape(t *testing.T) {
	tp, err := tape.Read("../tape/testdata/decision-example.tape")
	if err != nil {
		t.Fatal(err)
	}
	idx := IndexOf(tp)
	d := tp.Summary.Decision
	if idx.Mode != tape.ModeDecision {
		t.Errorf("Mode = %q, want %q", idx.Mode, tape.ModeDecision)
	}
	if idx.DecisionP50Ms != d.WarmP50Ms || idx.DecisionEngineP50Ms != d.EngineWarmP50Ms ||
		idx.DecisionColdMs != d.ColdMs || idx.DecisionReqPerSec != d.RequestsPerSecond {
		t.Errorf("decision figures %v %v %v %v, want %v %v %v %v",
			idx.DecisionP50Ms, idx.DecisionEngineP50Ms, idx.DecisionColdMs, idx.DecisionReqPerSec,
			d.WarmP50Ms, d.EngineWarmP50Ms, d.ColdMs, d.RequestsPerSecond)
	}
	if idx.DecisionP50Ms == 0 || idx.DecisionColdMs == 0 || idx.DecisionReqPerSec == 0 {
		t.Errorf("a figure the example has came through as 0: %+v", idx)
	}
	if idx.DecodePerSec != 0 || idx.PrefillPerSec != 0 || idx.TTFTp50Ms != 0 || idx.Sessions != 0 {
		t.Errorf("token figures on a decision row: decode %v prefill %v ttft %v sessions %d",
			idx.DecodePerSec, idx.PrefillPerSec, idx.TTFTp50Ms, idx.Sessions)
	}
	t.Logf("caveats %v", idx.Caveats)
}

// A benchmark row carries no mode and no decision figure.
func TestIndexOfBenchmarkHasNoMode(t *testing.T) {
	idx := IndexOf(&tape.Tape{Summary: tape.RunSummary{Concurrency: 1}})
	if idx.Mode != "" || idx.DecisionP50Ms != 0 {
		t.Errorf("benchmark row: mode %q decision p50 %v", idx.Mode, idx.DecisionP50Ms)
	}
}
