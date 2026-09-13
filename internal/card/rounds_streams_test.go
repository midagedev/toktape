package card

import (
	"strings"
	"testing"
)

// TestRoundsStreamsLineIsNotAnEquation pins the Streams row of a multi-round
// run (TTP-31, lead, 2026-09-13). The single-round form "N × per-stream =
// aggregate" is an equation, and on a rounds run it is false: the aggregate
// is token-weighted over rounds of different lengths (the example prints
// 4 × 14.4 = 50.6). A rounds run lists the three figures instead, and counts
// failures against every stream it sent.
func TestRoundsStreamsLineIsNotAnEquation(t *testing.T) {
	s := ExampleRounds()
	var block []string
	in := false
	for _, l := range strings.Split(Text(s), "\n") {
		if strings.Contains(l, "Streams ") {
			in = true
		}
		if in && (strings.Contains(l, "Prompts ") || strings.HasPrefix(l, "├")) {
			break
		}
		if in {
			block = append(block, l)
		}
	}
	joined := strings.Join(block, "\n")
	if len(block) == 0 {
		t.Fatal("no Streams row on the rounds example")
	}
	if strings.Contains(joined, "=") {
		t.Errorf("rounds Streams row is an equation:\n%s", joined)
	}
	for _, want := range []string{"4 streams per round", "14.4 tok/s each", "50.6 tok/s aggregate"} {
		if !strings.Contains(joined, want) {
			t.Errorf("rounds Streams row lacks %q:\n%s", want, joined)
		}
	}

	s.Aggregate.StreamsFailed = 3
	if got := Text(s); !strings.Contains(got, "3 of 24 failed") {
		t.Errorf("rounds Streams row does not count failures against all 24 streams sent")
	}
}
