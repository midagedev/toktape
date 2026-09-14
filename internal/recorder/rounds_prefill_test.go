package recorder

import (
	"math"
	"testing"
	"time"

	"github.com/midagedev/toktape/internal/tape"
)

// TestReduceRoundsPrefillIsTheServers (TTP-64, lead, 2026-09-14). A round of
// long prompts is how prefill is measured at a length, so a round's prefill
// must be the server's own prompt_n over its own prompt_ms. The fixture's
// client windows are deliberately different — 0.5 s from send to first token,
// which would read 16000 tok/s for round 0 — so only the server path gives
// these numbers. A stream the server reported no prompt timings for is
// unknown and stays out of both sums.
func TestReduceRoundsPrefillIsTheServers(t *testing.T) {
	ms := time.Millisecond
	s := time.Second
	r00 := roundRec(0, 0, 0, 500*ms, 2500*ms, 40, 20)
	r01 := roundRec(0, 1, 0, 500*ms, 2500*ms, 40, 20)
	r10 := roundRec(1, 0, 10*s, 10500*ms, 11500*ms, 20, 10)
	r11 := roundRec(1, 1, 10*s, 10500*ms, 11500*ms, 20, 10)
	r00.Timings.PromptN, r00.Timings.PromptMs = 4000, 2000
	r01.Timings.PromptN, r01.Timings.PromptMs = 4000, 2000
	r10.Timings.PromptN, r10.Timings.PromptMs = 16000, 16000
	r11.Timings.PromptN, r11.Timings.PromptMs = 0, 0 // no prompt timings reported

	_, per, _ := reduceRounds([]tape.RequestRecord{r00, r01, r10, r11}, []string{"4k", "16k"}, 2)
	if len(per) != 2 {
		t.Fatalf("PerRound has %d entries, want 2", len(per))
	}
	for k, w := range []struct {
		n    int
		rate float64
	}{{8000, 2000}, {16000, 1000}} {
		if per[k].PromptN != w.n || math.Abs(per[k].PromptPerSecond-w.rate) > 1e-9 {
			t.Errorf("round %d prefill = %d tokens at %v tok/s, want %d at %v (the server's prompt_ms, not the client's window)",
				k, per[k].PromptN, per[k].PromptPerSecond, w.n, w.rate)
		}
	}
}
