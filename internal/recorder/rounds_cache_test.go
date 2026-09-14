package recorder

import (
	"testing"
	"time"

	"github.com/midagedev/toktape/internal/tape"
)

// TestReduceRoundsCountsThePrefixCache (TTP-66, lead, 2026-09-14). The
// measurement a coding agent's workload is mostly made of: the same prefix
// sent twice. The fixture is a prompts file whose second line repeats the
// first line's 11000-token prefix and adds 260 tokens; the server evaluated
// the whole prompt the first time and took the prefix from its cache the
// second. The round must say both numbers, summed over its streams.
func TestReduceRoundsCountsThePrefixCache(t *testing.T) {
	ms := time.Millisecond
	s := time.Second
	cold := roundRec(0, 0, 0, 500*ms, 2500*ms, 40, 20)
	cold.Timings.PromptN, cold.Timings.PromptMs, cold.Timings.CacheN = 11000, 110000, 0
	warm := roundRec(1, 0, 10*s, 10500*ms, 11500*ms, 20, 10)
	warm.Timings.PromptN, warm.Timings.PromptMs, warm.Timings.CacheN = 260, 2600, 11000

	_, per, _ := reduceRounds([]tape.RequestRecord{cold, warm}, []string{"prefix", "prefix+tail"}, 1)
	if len(per) != 2 {
		t.Fatalf("PerRound has %d entries, want 2", len(per))
	}
	if per[0].CacheN != 0 || per[0].PromptN != 11000 {
		t.Errorf("cold round = %d evaluated / %d cached, want 11000 / 0", per[0].PromptN, per[0].CacheN)
	}
	if per[1].CacheN != 11000 || per[1].PromptN != 260 {
		t.Errorf("warm round = %d evaluated / %d cached, want 260 / 11000: the repeated prefix came from the cache", per[1].PromptN, per[1].CacheN)
	}
}
