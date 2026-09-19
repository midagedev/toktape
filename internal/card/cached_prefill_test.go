package card

import (
	"strings"
	"testing"

	"github.com/midagedev/toktape/internal/tape"
)

// TTP-88 (FAIL-first, 2026-09-19): a prefill served from the prompt cache is
// not a prefill measurement, and the card prints the prefill rate with
// nothing saying so. The Prefix cache row carries the share on its own
// unlinked row; the figure-severity caveat is what connects them.
func TestCachedPrefillIsACaveat(t *testing.T) {
	s := Example()
	// A second run of the same prompt: 497 of 512 tokens from the cache.
	s.Cache.HitTokens, s.Cache.PromptTotal = 497, 512
	s.Cache.HitRatio = 497.0 / 512
	s.Cache.Label = tape.CacheCached
	s.Timings.PromptN, s.Timings.CacheN = 15, 497
	var hit *Caveat
	for i, c := range Caveats(s) {
		if c.Code == "cached_prefill" {
			hit = &Caveats(s)[i]
		}
	}
	if hit == nil {
		t.Fatalf("no cached_prefill caveat for a 97%% cache hit: %+v", Caveats(s))
	}
	if hit.Severity != SeverityFigure {
		t.Errorf("cached_prefill severity = %q, want figure: it qualifies the headline", hit.Severity)
	}
	// The sentence must name the numbers: how much of the prompt was a hit.
	for _, want := range []string{"97%", "512", "497"} {
		if !strings.Contains(hit.Text, want) {
			t.Errorf("cached_prefill sentence = %q, which does not name %q", hit.Text, want)
		}
	}
	if !strings.Contains(string(mustJSON(t, s)), `"code": "cached_prefill"`) {
		t.Errorf("-o json does not carry cached_prefill")
	}
	if line := lineFor(t, ExplainCaveats(s), "cached_prefill"); !strings.Contains(line, "Figure") {
		t.Errorf("explain does not list cached_prefill as fired: %q", line)
	}
}

// A warm quarter-hit run is an evaluation with a cached prefix, not a cache
// hit: the fixtures sit at 25 % and must stay quiet.
func TestWarmCacheIsNotACachedPrefill(t *testing.T) {
	for _, c := range Caveats(Example()) {
		if c.Code == "cached_prefill" {
			t.Errorf("a 25%% cache hit raises cached_prefill: %q", c.Text)
		}
	}
}

// No prompt total recorded is unknown, not cached.
func TestUnknownCacheIsNotACachedPrefill(t *testing.T) {
	s := Example()
	s.Cache = tape.CacheSummary{}
	for _, c := range Caveats(s) {
		if c.Code == "cached_prefill" {
			t.Errorf("an unrecorded cache raises cached_prefill: %q", c.Text)
		}
	}
}
