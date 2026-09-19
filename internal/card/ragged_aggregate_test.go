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
//
// 2026-09-19 (TTP-138), re-pinned the same day the sentence was re-authored:
// this fixture carries no window, so its sentence is the window-0 shape —
// the arithmetic pieces below are the rule that still holds and are kept
// as-is; what changed is the ending, which now names the mechanism (a tail
// on fewer streams) and admits the length unknown, where the old one said
// only that the streams "did not all decode across the same window". The
// FAIL-first evidence for the re-authoring is the example-ragged golden,
// which failed against the new code carrying the old sentence.
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
	// ...and, since the re-authoring, the mechanism and its honest unknown —
	// never the old sentence's bare shrug.
	for _, want := range []string{"tail", "cannot say how long"} {
		if !strings.Contains(got[0].Text, want) {
			t.Errorf("ragged_aggregate sentence = %q, which does not say %q", got[0].Text, want)
		}
	}
	if strings.Contains(got[0].Text, "did not all decode across the same window") {
		t.Errorf("ragged_aggregate sentence = %q, the pre-TTP-138 ending is gone by design", got[0].Text)
	}
	if !strings.Contains(string(mustJSON(t, s)), `"code": "ragged_aggregate"`) {
		t.Errorf("-o json does not carry ragged_aggregate")
	}
	if line := lineFor(t, ExplainCaveats(s), "ragged_aggregate"); !strings.Contains(line, "Figure") {
		t.Errorf("explain does not list ragged_aggregate as fired: %q", line)
	}
}

// TTP-138 (2026-09-19): on a tape that carries the window, the sentence
// names it against the wall — how much of the run the tail was — with both
// rates, so the window figure the Decode row now leads with and the
// whole-wall figure it displaced are explained at the site of the arithmetic.
func TestRaggedAggregateNamesTheTailAgainstTheWall(t *testing.T) {
	got := Caveats(ExampleWindow())
	var ragged *Caveat
	for i := range got {
		if got[i].Code == "ragged_aggregate" {
			ragged = &got[i]
		}
	}
	if ragged == nil {
		t.Fatalf("Caveats = %+v, want ragged_aggregate: the window figure does not un-rag the run", got)
	}
	// Re-pinned stronger, 2026-09-19 (FAIL-first above: the sentence as first
	// authored named "3600 ms" and a bare "58.0"). Both rates must carry
	// "tok/s" — a caveat that exists so a reader can check arithmetic may not
	// state two of its four figures without a unit — and the window and wall
	// must be spelled the way the Streams row three lines above spells the
	// same window, in seconds, because one card saying a figure two ways is
	// the defect this caveat is about.
	for _, want := range []string{"4", "3.6 s", "16.6 s", "58.0 tok/s", "66.7 tok/s"} {
		if !strings.Contains(ragged.Text, want) {
			t.Errorf("ragged_aggregate sentence = %q, which does not name %q", ragged.Text, want)
		}
	}
	if strings.Contains(ragged.Text, "3600 ms") {
		t.Errorf("ragged_aggregate sentence = %q, spelling the window in ms disagrees with the Streams row", ragged.Text)
	}
	if strings.Contains(ragged.Text, "cannot say how long") {
		t.Errorf("ragged_aggregate sentence = %q, a tape that carries the window can say", ragged.Text)
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
