package card

import (
	"strings"
	"testing"

	"github.com/midagedev/toktape/internal/tape"
)

// TestExplainCaveatsListsEveryCheck: the listing is the debugging tool, so its
// value is in the checks that did NOT fire — "why is this card not warning
// about the short prompt" is the question it exists to answer in one command,
// and a listing that only shows what fired cannot answer it.
func TestExplainCaveatsListsEveryCheck(t *testing.T) {
	out := ExplainCaveats(clean(t))
	for _, c := range caveatCases() {
		if !strings.Contains(out, c.code) {
			t.Errorf("the listing skips %s:\n%s", c.code, out)
		}
	}
	if !strings.Contains(out, "every number on the card is quotable") {
		t.Errorf("a clean run's listing does not say so:\n%s", out)
	}
	// Every row aligns: a listing whose columns wander is one nobody reads.
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n")[1:] {
		if !strings.HasPrefix(line, "  ") {
			t.Errorf("line %q is not indented into the listing", line)
		}
	}
}

// TestExplainCaveatsAgreesWithCaveats: the listing's verdict for a code is the
// severity Caveats gave it, and never a second opinion — the readings printed
// are the predicates' inputs, not a re-derivation.
func TestExplainCaveatsAgreesWithCaveats(t *testing.T) {
	s := clean(t)
	s.Cache.Label = tape.CacheCold
	s.Contention.Contended = true

	out := ExplainCaveats(s)
	got := Caveats(s)
	if len(got) != 2 {
		t.Fatalf("Caveats = %+v, want cold_cache and machine_contended", got)
	}
	for _, c := range got {
		line := lineFor(t, out, c.Code)
		want := strings.ToUpper(c.Severity[:1]) + c.Severity[1:]
		if !strings.Contains(line, want) {
			t.Errorf("%s is listed %q, want the severity %q", c.Code, line, want)
		}
	}
	if line := lineFor(t, out, CodeShortGeneration); !strings.Contains(line, " no ") {
		t.Errorf("short_generation did not fire but is listed %q", line)
	}
	// The rows the card actually printed are in the listing too, so a figure
	// nobody expected can be traced to the branch that produced it.
	if !strings.Contains(out, "decode row") || !strings.Contains(out, "prefill row") {
		t.Errorf("the listing does not show the rows it explains:\n%s", out)
	}
}

func lineFor(t *testing.T, out, code string) string {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), code+" ") {
			return line
		}
	}
	t.Fatalf("no line for %s:\n%s", code, out)
	return ""
}

// TestExplainCaveatsSurvivesAnEmptySummary: it is a debugging tool and is
// reached on the tapes that are hardest to read, so it must not panic on one
// that recorded nothing.
func TestExplainCaveatsSurvivesAnEmptySummary(t *testing.T) {
	for _, s := range []*tape.RunSummary{nil, {}} {
		if out := ExplainCaveats(s); !strings.Contains(out, "caveats of") {
			t.Errorf("ExplainCaveats = %q", out)
		}
	}
}
