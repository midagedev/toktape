package card

import (
	"strconv"
	"strings"
	"testing"

	"github.com/midagedev/toktape/internal/tape"
)

// fmtMsLikeTheTUI is internal/tui's fmtMs, restated here because internal/tui
// imports this package and cannot be imported back for one formatter. It has
// to be the real shape, not a placeholder: the hero's p50 and p95 are 38 µs
// apart, and a renderer without fmtMs's rounding would print them as two
// different figures and call the run a spread it does not have.
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
		// 2 streams, PromptN 279 (the per-stream mean), aggregate 51.795 —
		// the run whose unlabelled "51.8 tok/s" is what this label exists to
		// qualify.
		{"hero run", &hero.Summary, "pp279 × 2 · 51.8 tok/s"},
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
// (two identical renderings are not a spread). The hero is the case that rule
// exists for: 38 µs between percentiles is not a spread any reader can act on.
func TestTTFTPercentiles(t *testing.T) {
	hero, err := tape.Read("../../assets/hero.tape")
	if err != nil {
		t.Fatalf("read ../../assets/hero.tape: %v", err)
	}
	for _, c := range []struct {
		name     string
		s        *tape.RunSummary
		p50, p95 string
		pair     bool
	}{
		{"hero run", &hero.Summary, "10.8 s", "10.8 s", false},
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
