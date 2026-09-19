package card

import (
	"strings"
	"testing"

	"github.com/midagedev/toktape/internal/tape"
)

// TTP-137 (FAIL-first, 2026-09-19): the Prefill row gains the machine's own
// rate from the probe pass as an additional line under it, in the row's own
// voice — the two figures the two-point fit separates, not a third spelling
// of the row's existing figures.
func TestPrefillRowCarriesTheProbesFigures(t *testing.T) {
	row := rowBlock(t, Text(ExampleProbed()), "Prefill")
	if !strings.Contains(row, "1182 tok/s on one stream · 28 ms fixed") {
		t.Errorf("Prefill row = %q, want the probe's line \"1182 tok/s on one stream · 28 ms fixed\" under it", row)
	}
	// The row's own figures do not move: this run's prompts, at this run's
	// concurrency, are still what the row says.
	if !strings.Contains(row, "610 tok/s") || !strings.Contains(row, "512 prompt tokens") {
		t.Errorf("Prefill row = %q, want the run's own figures unchanged beside the probe's", row)
	}
}

// The same clause under a concurrent Prefill row, where the row's own
// figures are aggregate-shaped: the machine's rate is the number that does
// not move with the stream count, which is exactly why it rides this row.
func TestPrefillRowCarriesTheProbesFiguresWhenConcurrent(t *testing.T) {
	s := ExampleConcurrent()
	s.Probe = ExampleProbed().Probe
	row := rowBlock(t, Text(s), "Prefill")
	if !strings.Contains(row, "1182 tok/s on one stream · 28 ms fixed") {
		t.Errorf("Prefill row = %q, want the probe's line under the concurrent row as well", row)
	}
	if !strings.Contains(row, "2927 tok/s aggregate") {
		t.Errorf("Prefill row = %q, want the run's own aggregate figure unchanged", row)
	}
}

// Absent entirely — never a "?" — when the fit was refused (both figures 0,
// the points kept) and when there was no probe at all.
func TestPrefillRowHasNoProbeLineWithoutAFit(t *testing.T) {
	refused := Example()
	refused.Probe = &tape.ProbeSummary{
		Prefill: []tape.PrefillPoint{
			{PromptN: 128, PromptMs: 200},
			{PromptN: 2048, PromptMs: 140}, // longer prompt answered faster: refused
		},
	}
	for name, s := range map[string]*tape.RunSummary{
		"fit refused": refused,
		"no probe":    Example(),
	} {
		t.Run(name, func(t *testing.T) {
			row := rowBlock(t, Text(s), "Prefill")
			for _, gone := range []string{"on one stream", "fixed", "?"} {
				if strings.Contains(row, gone) {
					t.Errorf("Prefill row = %q, want no probe clause and no invented figure (%q absent)", row, gone)
				}
			}
		})
	}
}

// The clause is led by the word that separates it from the figure above it
// (vision, 2026-09-19). On a single-stream run the Prefill row's own rate is
// also a one-stream rate, so "on one stream" divides nothing and the row
// becomes two prefill figures a factor apart with no clause between them —
// the defect the card exists to prevent. "probe" names the provenance, which
// is what actually differs: the row's figure carries the per-request fixed
// cost inside it, the probe's names that cost separately.
func TestProbeLineNamesItsProvenance(t *testing.T) {
	row := rowBlock(t, Text(ExampleProbed()), "Prefill")
	if !strings.Contains(row, "probe 1182 tok/s on one stream") {
		t.Errorf("Prefill row = %q, want the probe clause led by \"probe\"", row)
	}
	// And the row's own figure is still there, unled and unchanged: the two
	// are both true and the clause replaces nothing.
	if !strings.Contains(row, "610 tok/s") {
		t.Errorf("Prefill row = %q, want the run's own prefill figure untouched beside it", row)
	}
}
