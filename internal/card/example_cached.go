package card

import (
	"time"

	"github.com/midagedev/toktape/internal/tape"
)

// ExampleCachedPrefill returns the same rig's second run of the same prompt.
//
// It is the run TTP-88 (2026-09-19) is about: the first run evaluated 384
// tokens and left them in the prefix cache, so this one serves 497 of its
// 512 prompt tokens from the cache and evaluates 15. The server's prompt
// rate is then a cache-hit serving rate — 2500 tok/s beside the honest 610
// of the first run — with the Prefix cache row naming the 97 % share on its
// own unlinked row. Only the cached_prefill caveat connects the two.
//
// The derivation: 15 evaluated tokens at the server's prompt_ms of 6 ms is
// 2500 tok/s, and the first token lands 12 ms after the request. The decode
// side is Example's own: 320 tokens at 17.4 tok/s, client agreeing.
func ExampleCachedPrefill() *tape.RunSummary {
	s := Example()
	started := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	s.ID = "20260919-120000-r1-distill-llama-70b"
	s.StartedAt = started
	s.FinishedAt = started.Add(18400 * time.Millisecond)
	s.Timings.PromptN = 15
	s.Timings.CacheN = 497
	s.Timings.PromptMs = 6
	s.Timings.PromptPerSecond = 2500.0
	s.Timings.TTFTMs = 12
	s.Timings.ClientPromptPerSecond = 1250.0
	s.Cache = tape.CacheSummary{
		HitTokens:   497,
		PromptTotal: 512,
		HitRatio:    497.0 / 512,
		Label:       tape.CacheCached,
	}
	return s
}
