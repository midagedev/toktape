package card

import (
	"strings"
	"testing"

	"github.com/midagedev/toktape/internal/tape"
)

// TTP-97 (FAIL-first, 2026-09-19): on a two-stream run the p50 and p95 are
// computed from two samples and frequently render as the same string, which
// reads as a bug. When the pair means nothing — too few samples, or two
// renderings of one reading — the card prints the single figure instead.
func TestStreamsBlockPrintsSingleTTFTWhenPercentilesRenderAlike(t *testing.T) {
	s := ExampleConcurrent()
	s.Aggregate.TTFTp50Ms, s.Aggregate.TTFTp95Ms = 10800, 10800
	out := Text(s)
	if strings.Contains(out, "p95") {
		t.Errorf("two percentiles rendering alike still print as a pair:\n%s", out)
	}
	if !strings.Contains(out, "TTFT 10800 ms") {
		t.Errorf("the alike pair does not print as a single figure:\n%s", out)
	}
}

// TestTTFTPairNeedsTwoSamplesAndTwoRenderings: the rule behind the row above.
// "Too few" is decided from the stream count the tape records — one stream
// has no distribution — and otherwise by whether the two renderings are two
// facts. Two streams with genuinely different values keep the pair: the tape
// records the aggregate's percentiles and two different renderings are two
// readings, the same rule formatGHzPair states, and the TUI already prints
// them that way — a single here would set the renderers disagreeing.
func TestTTFTPairNeedsTwoSamplesAndTwoRenderings(t *testing.T) {
	eight := ExampleConcurrent()
	twoAlike := ExampleConcurrent()
	twoAlike.Aggregate.Streams, twoAlike.Concurrency = 2, 2
	twoAlike.Aggregate.TTFTp50Ms, twoAlike.Aggregate.TTFTp95Ms = 10800, 10800
	for _, c := range []struct {
		name     string
		s        *tape.RunSummary
		p50, p95 string
		pair     bool
	}{
		{"many samples and different values", eight, "810 ms", "1050 ms", true},
		{"two streams with equal rendered strings", twoAlike, "10800 ms", "10800 ms", false},
		{"one stream", Example(), "630 ms", "", false},
	} {
		p50, p95, pair := TTFTPercentiles(c.s, formatMs)
		if p50 != c.p50 || p95 != c.p95 || pair != c.pair {
			t.Errorf("%s: TTFTPercentiles = (%q, %q, %v), want (%q, %q, %v)",
				c.name, p50, p95, pair, c.p50, c.p95, c.pair)
		}
	}
	// Two streams with genuinely different values keep the pair.
	p50, p95, pair := TTFTPercentiles(twoSpreadLike(), formatMs)
	if !pair || p50 == p95 {
		t.Errorf("two differing streams print as a single figure: (%q, %q, %v)", p50, p95, pair)
	}
}

func twoSpreadLike() *tape.RunSummary {
	s := ExampleConcurrent()
	s.Aggregate.Streams, s.Concurrency = 2, 2
	s.Aggregate.TTFTp50Ms, s.Aggregate.TTFTp95Ms = 1200, 1500
	return s
}
