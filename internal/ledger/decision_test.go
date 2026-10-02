package ledger

import (
	"testing"

	"github.com/midagedev/toktape/internal/tape"
)

// TestHoldsRefusesDecisionTape: every ledger column is a token figure, so a
// decision-model run (TTP-192) is kept out by the one gate both doors use.
func TestHoldsRefusesDecisionTape(t *testing.T) {
	if Holds(&tape.Tape{Summary: tape.RunSummary{Mode: tape.ModeDecision}}) {
		t.Fatal("Holds accepted a decision tape")
	}
	if !Holds(&tape.Tape{}) {
		t.Fatal("Holds refused a benchmark tape")
	}
}
