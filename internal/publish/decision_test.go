package publish

import (
	"context"
	"errors"
	"testing"

	"github.com/midagedev/toktape/internal/tape"
)

// TestUploadRefusesDecisionTape: a decision-model tape never reaches the hub
// (TTP-192), whose index and page read decode figures it does not have. The
// client has no URL, so a refusal that came after the request would fail
// with a network error instead of ErrDecision.
func TestUploadRefusesDecisionTape(t *testing.T) {
	tp := &tape.Tape{Summary: tape.RunSummary{Mode: tape.ModeDecision}}
	if _, err := (&Client{}).Upload(context.Background(), tp, Index{}, Options{}); !errors.Is(err, ErrDecision) {
		t.Fatalf("err = %v, want ErrDecision", err)
	}
}
