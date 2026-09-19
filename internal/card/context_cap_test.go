package card

import (
	"strings"
	"testing"
)

// TTP-135 (FAIL-first, 2026-09-19): when the token cap ended streams, the
// Context row says so as a count against the endings observed — "4 of 4 hit
// the cap" — not a bare flag, because a reader asking "was the whole run
// guillotined or one long-winded stream" needs the denominator.
func TestContextRowNamesTheCappedStreams(t *testing.T) {
	s := Example()
	s.Limit.CappedStreams = 4
	s.Limit.EndingsObserved = 4
	row := rowBlock(t, Text(s), "Context")
	if !strings.Contains(row, "4 of 4 hit the cap") {
		t.Errorf("Context row = %q, want the clause \"4 of 4 hit the cap\"", row)
	}
	// The count, not a flag: one capped stream of four observed is a
	// different sentence.
	s.Limit.CappedStreams = 1
	if row := rowBlock(t, Text(s), "Context"); !strings.Contains(row, "1 of 4 hit the cap") {
		t.Errorf("Context row = %q, want \"1 of 4 hit the cap\": the clause counts", row)
	}
}

// EndingsObserved == 0 means the tape cannot say why streams stopped — an
// engine that reports no finish reason. Nothing is added and nothing is
// guessed: the row is byte-identical to a run with no cap involvement at
// all, and no "?" is invented for a question this row never asked.
func TestContextRowIsSilentWhenTheTapeCannotSay(t *testing.T) {
	s := Example()
	s.Limit.CappedStreams = 4
	s.Limit.EndingsObserved = 0
	if got, want := rowBlock(t, Text(s), "Context"), rowBlock(t, Text(Example()), "Context"); got != want {
		t.Errorf("EndingsObserved == 0 moved the Context row:\n got %q\nwant %q", got, want)
	}
	// Zero capped of four observed is an observed none, and stays silent the
	// same way — an honest none, not an unknown.
	s.Limit.CappedStreams = 0
	s.Limit.EndingsObserved = 4
	if got, want := rowBlock(t, Text(s), "Context"), rowBlock(t, Text(Example()), "Context"); got != want {
		t.Errorf("an observed zero moved the Context row:\n got %q\nwant %q", got, want)
	}
}
