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
	if !strings.Contains(joined, "4 streams per round") {
		t.Errorf("rounds Streams row lacks the per-round count:\n%s", joined)
	}
	// The rates left this row on 2026-09-17 (TTP-110): they are the Decode
	// row's and were printed twice on every concurrent card. "per round"
	// already says the streams were not all in one window, which is the thing
	// the listed figures were standing in for.
	for _, gone := range []string{"14.4", "50.6", "not all decoding at once"} {
		if strings.Contains(joined, gone) {
			t.Errorf("rounds Streams row still carries %q:\n%s", gone, joined)
		}
	}

	s.Aggregate.StreamsFailed = 3
	if got := Text(s); !strings.Contains(got, "3 of 24 failed") {
		t.Errorf("rounds Streams row does not count failures against all 24 streams sent")
	}
}
