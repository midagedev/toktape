package compare

import (
	"testing"

	"github.com/midagedev/toktape/internal/tape"
)

// TestTapesRefusesDecisionTape: a decision-model run has no token figure for
// the table (TTP-192), in either position and against another decision run.
func TestTapesRefusesDecisionTape(t *testing.T) {
	d := &tape.Tape{Summary: tape.RunSummary{Mode: tape.ModeDecision}}
	b := &tape.Tape{}
	for _, pair := range [][2]*tape.Tape{{d, b}, {b, d}, {d, d}} {
		if _, err := Tapes(pair[0], pair[1]); err == nil {
			t.Errorf("Tapes(%q, %q) compared", pair[0].Summary.Mode, pair[1].Summary.Mode)
		}
	}
}
