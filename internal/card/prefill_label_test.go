package card

import (
	"strconv"
	"strings"
	"testing"

	"github.com/midagedev/toktape/internal/tape"
)

// fmtMsLikeTheTUI is internal/tui's fmtMs, restated here because internal/tui
// imports this package and cannot be imported back for one formatter. It has
// to be the real shape, not a placeholder: percentiles microseconds apart
// render identically through it, and a renderer without fmtMs's rounding would
// print them as two different figures and call the run a spread it does not
// have. TestTTFTPercentiles' "percentiles that render alike" case is that
// shape, held as a fixture since the hero stopped being one (2026-09-16).
func fmtMsLikeTheTUI(v float64) string {
	if v <= 0 {
		return unknown
	}
	if v >= 1000 {
		if v < 10000 {
			return strconv.FormatFloat(v/1000, 'f', 2, 64) + " s"
		}
		return strconv.FormatFloat(v/1000, 'f', 1, 64) + " s"
	}
	return strconv.FormatFloat(v, 'f', 0, 64) + " ms"
}

// TestPrefillLabel: the label carries the prompt length in llama-bench's pp
// vocabulary, the stream count above one stream, the rate the stream count
// makes authoritative, and the short-prompt qualification when the prompt
// earned it. The hero case reads the real tape rather than a fixture so the
// label is checked against the run it was written for.
func TestPrefillLabel(t *testing.T) {
	hero, err := tape.Read("../../assets/hero.tape")
	if err != nil {
		t.Fatalf("read ../../assets/hero.tape: %v", err)
	}
	for _, c := range []struct {
		name string
		s    *tape.RunSummary
		want string
	}{
		// 4 streams, PromptN 802 (the per-stream mean), aggregate 380 — the
		// run whose unlabelled "380 tok/s" is what this label exists to
		// qualify. Re-pinned 2026-09-16 for the Qwen four-stream recording,
		// again 2026-09-17 when it was re-cut on a released build, again the
		// same day when it was re-recorded with --jinja so thinking was
		// really off (TTP-106), and again 2026-09-21: hero re-recorded on
		// prompts@v2 under the run plan (salted, prompts trimmed to ~800
		// tokens by the clock ceiling, four streams). The figures travel with
		// the asset, which is why this case reads the tape instead of
		// restating it.
		{"hero run", &hero.Summary, "pp802 × 4 · 380 tok/s"},
		{"single stream", Example(), "pp384 · 610 tok/s"},
		{"eight streams", ExampleConcurrent(), "pp384 × 8 · 2927 tok/s"},
	} {
		if got := PrefillLabel(c.s); got != c.want {
			t.Errorf("%s: PrefillLabel = %q, want %q", c.name, got, c.want)
		}
	}

	// A prompt too short for the rate over it to be a prefill measurement says
	// so in the label itself, so the label cannot travel without its
	// qualification (TTP-65).
	s := *ExampleConcurrent()
	s.Cache.PromptTotal, s.Cache.HitTokens = 63, 0
	s.Timings.PromptN, s.Timings.CacheN = 63, 0
	if got := PrefillLabel(&s); !strings.HasSuffix(got, " · short prompt") {
		t.Errorf("short prompt: PrefillLabel = %q, want it to end with the qualification", got)
	}

	// A summary with nothing observed prints "?" for the count and the rate —
	// never a zero or a defaulted figure (repo rule, CLAUDE.md).
	if got, want := PrefillLabel(&tape.RunSummary{}), "pp? · ? tok/s"; got != want {
		t.Errorf("empty summary: PrefillLabel = %q, want %q", got, want)
	}
}

// TestTTFTPercentiles: one stream prints its own TTFT and no p95; several
// print the aggregate's pair — and only as a pair when the two renderings are
// two facts, by the rule TestPrefillLabel's neighbour formatGHzPair states
// (two identical renderings are not a spread).
//
// The hero used to be the case that rule exists for — its percentiles were
// 38 µs apart — and the Qwen four-stream recording is not: 8.25 s against
// 8.45 s is a spread a reader can act on (4.47 s/4.59 s on the previous
// take; re-quoted 2026-09-21, hero re-recorded on prompts@v2 under the run
// plan). So the rule keeps its own fixture
// below rather than borrowing whichever run the asset happens to be
// (2026-09-16, lead). A rule whose only witness is a replaceable asset is a
// rule that leaves the suite the next time the asset is re-recorded.
func TestTTFTPercentiles(t *testing.T) {
	hero, err := tape.Read("../../assets/hero.tape")
	if err != nil {
		t.Fatalf("read ../../assets/hero.tape: %v", err)
	}
	// Two streams whose percentiles are 38 µs apart: two readings, one
	// rendering, so the card says "first token" and drops the p95.
	alike := &tape.RunSummary{}
	alike.Aggregate.Streams = 2
	alike.Aggregate.TTFTp50Ms, alike.Aggregate.TTFTp95Ms = 10800.000, 10800.038
	for _, c := range []struct {
		name     string
		s        *tape.RunSummary
		p50, p95 string
		pair     bool
	}{
		{"hero run", &hero.Summary, "8.25 s", "8.45 s", true},
		{"percentiles that render alike", alike, "10.8 s", "10.8 s", false},
		{"eight streams", ExampleConcurrent(), "810 ms", "1.05 s", true},
		{"single stream", Example(), "630 ms", "", false},
	} {
		p50, p95, pair := TTFTPercentiles(c.s, fmtMsLikeTheTUI)
		if p50 != c.p50 || p95 != c.p95 || pair != c.pair {
			t.Errorf("%s: TTFTPercentiles = (%q, %q, %v), want (%q, %q, %v)",
				c.name, p50, p95, pair, c.p50, c.p95, c.pair)
		}
	}
}
