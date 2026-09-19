package card

import (
	"strings"
	"testing"

	"github.com/midagedev/toktape/internal/tape"
)

// TTP-108 (FAIL-first, 2026-09-19): the aggregate decode rate is diluted by a
// ragged run — streams stopping at different times — and the Decode row
// prints "144 tok/s aggregate · 42.1 tok/s each" with nothing at the site of
// the arithmetic saying why 4 × 42.1 is not 144.
func TestRaggedAggregateIsACaveat(t *testing.T) {
	s := Example()
	s.Concurrency = 4
	s.Aggregate.Streams = 4
	s.Aggregate.MinPredictedN = 280
	// Four streams at 17.4 each is 69.6, not this aggregate: one stream
	// finished while the others kept decoding.
	s.Aggregate.AggregatePredictedPerSecond = 12.0
	s.Aggregate.PerStreamPredictedPerSecond = 17.4
	s.Aggregate.TTFTp50Ms, s.Aggregate.TTFTp95Ms = 1200, 1500
	got := Caveats(s)
	if len(got) != 1 || got[0].Code != "ragged_aggregate" {
		t.Fatalf("Caveats = %+v, want exactly [ragged_aggregate]", got)
	}
	if got[0].Severity != SeverityFigure {
		t.Errorf("ragged_aggregate severity = %q, want figure: it qualifies the headline", got[0].Severity)
	}
	// The sentence must name the arithmetic it disputes.
	for _, want := range []string{"4", "17.4", "69.6", "12"} {
		if !strings.Contains(got[0].Text, want) {
			t.Errorf("ragged_aggregate sentence = %q, which does not name %q", got[0].Text, want)
		}
	}
	if !strings.Contains(string(mustJSON(t, s)), `"code": "ragged_aggregate"`) {
		t.Errorf("-o json does not carry ragged_aggregate")
	}
	if line := lineFor(t, ExplainCaveats(s), "ragged_aggregate"); !strings.Contains(line, "Figure") {
		t.Errorf("explain does not list ragged_aggregate as fired: %q", line)
	}
}

// A run whose streams shared a window is not ragged, however many of them
// there were; and a single stream has no sum to dispute.
func TestBalancedRunsAreNotRagged(t *testing.T) {
	for name, s := range map[string]*tape.RunSummary{
		"concurrent": ExampleConcurrent(),
		"single":     Example(),
	} {
		for _, c := range Caveats(s) {
			if c.Code == "ragged_aggregate" {
				t.Errorf("%s raises ragged_aggregate: %q", name, c.Text)
			}
		}
	}
}
