package card

import (
	"strings"
	"testing"

	"github.com/midagedev/toktape/internal/tape"
)

// prefillRow is the Prefill row of a card, its continuation lines joined.
func prefillRow(s *tape.RunSummary) string {
	var out []string
	keep := false
	for _, line := range strings.Split(Text(s), "\n") {
		body := strings.TrimSuffix(strings.TrimPrefix(line, "│ "), " │")
		switch {
		case strings.HasPrefix(body, "Prefill "):
			keep = true
		case keep && !strings.HasPrefix(body, strings.Repeat(" ", speedLabelW)):
			keep = false
		}
		if keep {
			out = append(out, strings.TrimSpace(strings.TrimPrefix(body, "Prefill")))
		}
	}
	return strings.Join(out, " ")
}

// TestPrefillLeadsWithTheAggregateUnderConcurrency is TTP-59 (2026-09-14).
//
// tape.TimingsSummary.PromptPerSecond is the per-request mean once
// Concurrency > 1, and it was the only figure on the row: the code tape's card
// said "Prefill 13.0 tok/s" while the box was putting 24.2 through. A reader
// comparing engines takes the first figure on the row for the box's rate, so
// the aggregate leads and the mean stays beside it, which is what the PNG card
// and the right pane already do.
func TestPrefillLeadsWithTheAggregateUnderConcurrency(t *testing.T) {
	s := ExampleConcurrent()
	row := prefillRow(s)
	for _, want := range []string{
		formatRateUnit(s.Aggregate.AggregatePromptPerSecond) + " aggregate",
		formatRateUnit(s.Timings.PromptPerSecond) + " each",
	} {
		if !strings.Contains(row, want) {
			t.Errorf("the Prefill row has no %q:\n%s", want, row)
		}
	}
	// TTFT is not here on a concurrent run any more (TTP-110, 2026-09-17).
	// What this row printed was Aggregate.TTFTp50Ms — a statistic over the
	// streams wearing a prefill label — and the Streams block printed the same
	// figure beside its own p95. It belongs there, with its spread. The row
	// still decomposes the wait: engine prefill and queue are both on it.
	if strings.Contains(row, "TTFT") {
		t.Errorf("TTFT is the Streams block's on a concurrent run:\n%s", row)
	}
	for _, want := range []string{"engine prefill", "queue "} {
		if !strings.Contains(row, want) {
			t.Errorf("the Prefill row lost %q, which is where the wait went:\n%s", want, row)
		}
	}
	// The aggregate is the first figure, not the mean.
	agg := strings.Index(row, formatRateUnit(s.Aggregate.AggregatePromptPerSecond)+" aggregate")
	each := strings.Index(row, formatRateUnit(s.Timings.PromptPerSecond)+" each")
	if agg < 0 || each < 0 || agg > each {
		t.Errorf("the row must lead with the aggregate:\n%s", row)
	}
}

// TestSinglePrefillIsTheStreamsOwnRate: a run of one stream has no aggregate
// to lead with, and labelling its one rate "aggregate" would claim a
// measurement the run did not make. The row is unchanged there.
func TestSinglePrefillIsTheStreamsOwnRate(t *testing.T) {
	s := Example()
	if s.Concurrency != 1 {
		t.Fatalf("the fixture is not single-stream (%d)", s.Concurrency)
	}
	row := prefillRow(s)
	for _, forbidden := range []string{"aggregate", "each", "p50"} {
		if strings.Contains(row, forbidden) {
			t.Errorf("a single-stream Prefill row must not say %q:\n%s", forbidden, row)
		}
	}
	if !strings.Contains(row, "TTFT "+formatMs(s.Timings.TTFTMs)) {
		t.Errorf("the row lost its TTFT:\n%s", row)
	}
}

// TestPrefillPromptTokensStayPerRequest: the count says how long the prompt
// was, not how many tokens the run sent in total, and that meaning does not
// change with the stream count.
func TestPrefillPromptTokensStayPerRequest(t *testing.T) {
	s := ExampleConcurrent()
	want := formatInt(promptTokens(s)) + " prompt tokens"
	if row := prefillRow(s); !strings.Contains(row, want) {
		t.Errorf("the Prefill row has no %q:\n%s", want, row)
	}
}
