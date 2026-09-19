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

// The context-exhaustion spellings (lead, 2026-09-19). "limit" with
// `truncated` set is the sequence running out of context, not the token cap —
// a different problem with a different fix — and the row must say which it
// was. The denominator is EndingsObserved in every spelling, and the counts
// are disjoint, so the "both" case names each against the same denominator
// rather than nesting one inside the other.
func TestContextRowNamesContextExhaustion(t *testing.T) {
	s := Example()
	s.Limit.EndingsObserved = 4

	s.Limit.CappedStreams = 0
	s.Limit.ContextExhaustedStreams = 4
	if row := rowBlock(t, Text(s), "Context"); !strings.Contains(row, "4 of 4 ran out of context") {
		t.Errorf("Context row = %q, want the clause \"4 of 4 ran out of context\"", row)
	}

	s.Limit.CappedStreams = 3
	s.Limit.ContextExhaustedStreams = 1
	if row := rowBlock(t, Text(s), "Context"); !strings.Contains(row, "3 of 4 hit the cap · 1 ran out of context") {
		t.Errorf("Context row = %q, want \"3 of 4 hit the cap · 1 ran out of context\"", row)
	}

	// EndingsObserved == 0 is the tape that cannot say, and it stays silent
	// for the exhausted count exactly as it does for the capped one.
	s.Limit.CappedStreams = 0
	s.Limit.ContextExhaustedStreams = 4
	s.Limit.EndingsObserved = 0
	if got, want := rowBlock(t, Text(s), "Context"), rowBlock(t, Text(Example()), "Context"); got != want {
		t.Errorf("EndingsObserved == 0 moved the Context row:\n got %q\nwant %q", got, want)
	}
}

// A wrapped Context row says it is not finished (vision, 2026-09-19). The
// value is one parenthesised group, so a first line ending on an open
// bracket with nothing after it reads as a broken line rather than a row
// continued — measured against the same row one character shorter, which
// fits on one line and closes. The mark is the row's own separator, so the
// card does not invent a punctuation for this.
func TestWrappedContextRowSaysItContinues(t *testing.T) {
	s := Example()
	s.Limit.CappedStreams, s.Limit.ContextExhaustedStreams, s.Limit.EndingsObserved = 3, 1, 4
	row := rowBlock(t, Text(s), "Context")
	lines := strings.Split(strings.TrimRight(row, "\n"), "\n")
	if len(lines) < 2 {
		t.Fatalf("Context row did not wrap, so this gate is about nothing:\n%s", row)
	}
	for i, ln := range lines[:len(lines)-1] {
		if !strings.HasSuffix(strings.TrimRight(ln, " │"), "·") {
			t.Errorf("continued line %d = %q, want it to end on the row's own separator", i, ln)
		}
	}
	if !strings.HasSuffix(strings.TrimRight(lines[len(lines)-1], " │"), ")") {
		t.Errorf("last line = %q, want the group closed where the value ends", lines[len(lines)-1])
	}
	// A row that fits keeps no mark: the separator says "more is coming",
	// and nothing is coming.
	one := Example()
	one.Limit.ContextExhaustedStreams, one.Limit.EndingsObserved = 4, 4
	if got := rowBlock(t, Text(one), "Context"); strings.Count(strings.TrimRight(got, "\n"), "\n") != 0 {
		t.Errorf("the one-line spelling wrapped:\n%s", got)
	}
}
