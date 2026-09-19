package card

import (
	"strings"
	"testing"
	"time"

	"github.com/midagedev/toktape/internal/tape"
)

// twoStreamsOneDisagreeing is the card's view of the tape this round is about:
// two streams, both answered, the means agreeing (11.7 against 11.5, 1.3 %)
// while one stream on its own read 11.79 against 11.49 (2.6 %).
func twoStreamsOneDisagreeing(t *testing.T) *tape.RunSummary {
	t.Helper()
	s := clean(t)
	s.Concurrency = 2
	s.Timings.PredictedPerSecond = 11.5355
	s.Timings.ClientPredictedPerSecond = 11.6770
	s.Timings.ClientAgreesWithServer = true
	s.Aggregate.Streams = 2
	s.Aggregate.DisagreeingStreams = 1
	// Two streams at the per-stream rate share a window here: the clean
	// fixture's aggregate is one stream's rate, and two streams at that
	// rate with this aggregate is a ragged run too (TTP-108, 2026-09-19) —
	// these tests are about the disagreement, not the arithmetic.
	s.Aggregate.AggregatePredictedPerSecond = 2 * s.Aggregate.PerStreamPredictedPerSecond
	return s
}

// TestTheDisagreementCaveatMatchesTheFiguresItPrints (lead, 2026-09-15).
// client_disagrees_with_server used to fire on the AND of the per-stream flags
// while printing the run-level means, so a two-stream take whose means agreed
// to 1.3 % got "the client measured 11.7 tok/s where the server reported
// 11.5 tok/s, over the 2% tolerance" — a caveat contradicting its own numbers.
// The run-level flag now describes the run-level figures, and the caveat says
// the per-stream fact as a count.
//
// FAIL-first: before this commit the count case below produced the
// means-sentence.
func TestTheDisagreementCaveatMatchesTheFiguresItPrints(t *testing.T) {
	t.Run("the means disagree keeps today's sentence", func(t *testing.T) {
		s := clean(t)
		s.Timings.ClientAgreesWithServer = false
		cs := Caveats(s)
		if len(cs) != 1 || cs[0].Code != CodeClientDisagrees {
			t.Fatalf("Caveats = %+v, want exactly [%s]", cs, CodeClientDisagrees)
		}
		if want := "the client measured 17.3 tok/s where the server reported 17.4 tok/s, over the 2% tolerance"; cs[0].Text != want {
			t.Errorf("text = %q, want today's sentence %q", cs[0].Text, want)
		}
	})

	t.Run("a disagreeing stream under agreeing means says the count", func(t *testing.T) {
		cs := Caveats(twoStreamsOneDisagreeing(t))
		if len(cs) != 1 || cs[0].Code != CodeClientDisagrees {
			t.Fatalf("Caveats = %+v, want exactly [%s]", cs, CodeClientDisagrees)
		}
		if want := "1 of 2 streams measured a rate the server did not confirm within 2%: the card's figures are the mean over the streams"; cs[0].Text != want {
			t.Errorf("text = %q, want %q", cs[0].Text, want)
		}
		if cs[0].Severity != SeverityRun {
			t.Errorf("severity = %q, want %q: the means still mean what they say", cs[0].Severity, SeverityRun)
		}
	})

	t.Run("agreeing means and no disagreeing stream fire nothing", func(t *testing.T) {
		s := twoStreamsOneDisagreeing(t)
		s.Aggregate.DisagreeingStreams = 0
		if cs := Caveats(s); len(cs) != 0 {
			t.Errorf("Caveats = %+v, want none: the figures agree and so did every stream", cs)
		}
	})

	t.Run("an old tape keeps today's sentence", func(t *testing.T) {
		// Recorded before the count existed: no DisagreeingStreams, and the
		// means themselves disagree — the case the sentence was written for.
		s := clean(t)
		s.Timings.ClientPredictedPerSecond = 25
		s.Timings.ClientAgreesWithServer = false
		cs := Caveats(s)
		if len(cs) != 1 || cs[0].Code != CodeClientDisagrees {
			t.Fatalf("Caveats = %+v, want exactly [%s]", cs, CodeClientDisagrees)
		}
		if want := "the client measured 25.0 tok/s where the server reported 17.4 tok/s, over the 2% tolerance"; cs[0].Text != want {
			t.Errorf("text = %q, want today's sentence %q", cs[0].Text, want)
		}
	})

	t.Run("the count is the answered streams, not the sent ones", func(t *testing.T) {
		s := twoStreamsOneDisagreeing(t)
		s.Aggregate.Streams, s.Aggregate.StreamsFailed = 4, 1
		// Four sent now, so the shared window's aggregate is four times the
		// per-stream rate (TTP-108, 2026-09-19 — see the helper).
		s.Aggregate.AggregatePredictedPerSecond = 4 * s.Aggregate.PerStreamPredictedPerSecond
		cs := Caveats(s)
		if len(cs) != 2 || cs[0].Code != CodeStreamsFailed || cs[1].Code != CodeClientDisagrees {
			t.Fatalf("Caveats = %+v, want streams_failed then client_disagrees", cs)
		}
		if want := "1 of 3 streams measured a rate the server did not confirm within 2%: the card's figures are the mean over the streams"; cs[1].Text != want {
			t.Errorf("text = %q, want the answered count %q", cs[1].Text, want)
		}
	})
}

// TestExplainShowsTheDisagreeingStreamsCount: the listing's reading carries
// the count beside the flag it now decides independently of, so "why did this
// card warn" and "why did it not" are both answered by the row.
func TestExplainShowsTheDisagreeingStreamsCount(t *testing.T) {
	s := twoStreamsOneDisagreeing(t)
	line := lineFor(t, ExplainCaveats(s), CodeClientDisagrees)
	for _, want := range []string{"Run", "server 11.5, client 11.7, agrees true, 1 of 2 streams disagree, tolerance 2%"} {
		if !strings.Contains(line, want) {
			t.Errorf("listing line %q lacks %q", line, want)
		}
	}
	// A tape older than the count: 0 streams disagree, printed as the plain
	// number it is, not "?" — the row is a count nobody derived, and the zero
	// is the schema's "every answered stream agreed".
	old := clean(t)
	if line := lineFor(t, ExplainCaveats(old), CodeClientDisagrees); !strings.Contains(line, "0 of 1 streams disagree") {
		t.Errorf("a tape older than the count reads %q", line)
	}
}

// TestARepeatedCodeIsListedOnce (lead, 2026-09-15). Two recorder warnings are
// two caveats — `-o json` keeps both — but the caveat line listed both codes
// verbatim, so the real tape printed "recorded · recorded". The line now names
// each code once with its count, "recorded ×2" (the × the Streams row already
// uses), and "%d caveats" stays the true number of caveats.
//
// FAIL-first: before this commit the line below read
// "3 caveats — … · recorded · recorded".
func TestARepeatedCodeIsListedOnce(t *testing.T) {
	s := clean(t)
	s.Warnings = []string{"the -ot rule was dropped", "pid not found, no /proc view"}
	s.Cache.Label = tape.CacheCold
	s.Memory.MajFaultsPerToken = tape.ColdMajFaultsPerToken + 0.4

	cs := Caveats(s)
	if len(cs) != 3 {
		t.Fatalf("Caveats = %+v, want cold_cache and two recorded", cs)
	}
	joined := strings.Join(warningSection(s), " ")
	if !strings.Contains(joined, "3 caveats") {
		t.Errorf("the count is not the true number of caveats:\n%s", joined)
	}
	if !strings.Contains(joined, "recorded ×2") {
		t.Errorf("the line does not collapse the repeated code:\n%s", joined)
	}
	if got := strings.Count(joined, "recorded"); got != 1 {
		t.Errorf("the code appears %d times, want once:\n%s", got, joined)
	}
	// Rank position: the collapsed code sits where one recorded caveat sat,
	// after cold_cache's spelled-out sentence and before machine_contended
	// when one fires.
	cold, recorded := strings.Index(joined, "cold run"), strings.Index(joined, "recorded ×2")
	if cold < 0 || recorded < cold {
		t.Errorf("recorded ×2 does not sit in rank order after the cold sentence:\n%s", joined)
	}

	// Three recorder warnings read ×3.
	s.Warnings = append(s.Warnings, "a third warning")
	if joined := strings.Join(warningSection(s), " "); !strings.Contains(joined, "recorded ×3") || !strings.Contains(joined, "4 caveats") {
		t.Errorf("three warnings do not read recorded ×3 over 4 caveats:\n%s", joined)
	}
}

// TestTheCaveatCodeCapCountsDeduplicatedCodes: maxCaveatCodesListed caps how
// many CODES the line names, and a repeated code costs one slot, not three —
// otherwise the fix for "recorded · recorded" would push a distinct warning
// out of the index.
func TestTheCaveatCodeCapCountsDeduplicatedCodes(t *testing.T) {
	s := clean(t)
	s.Aggregate.Streams, s.Aggregate.StreamsFailed = 8, 1
	s.Cache.Label = tape.CacheCold
	s.Memory.MajFaultsPerToken = tape.ColdMajFaultsPerToken + 0.4
	s.Cache.PromptTotal, s.Cache.HitTokens = 63, 0
	s.Timings.PromptN, s.Timings.CacheN = 63, 0
	s.Timings.ClientAgreesWithServer = false
	s.Warnings = []string{"w1", "w2", "w3"}
	s.Contention.Contended = true
	s.Contention.Witnesses = []tape.ContentionWitness{
		{Edge: "start", CPUMaxKHz: 3_600_000},
		{Edge: "end", CPUMaxKHz: 2_700_000},
	}
	s.Limit = tape.LimitSummary{For: 20 * time.Second, CutAt: 20 * time.Second, MinTokens: tape.MinCutTokens}

	cs := Caveats(s)
	// streams_failed is spelled out; the rest are seven distinct codes with
	// recorded three times among them: ten caveats, eight codes.
	if len(cs) != 10 {
		t.Fatalf("Caveats = %d entries, want 10 (eight codes, recorded ×3)", len(cs))
	}
	joined := strings.Join(warningSection(s), " ")
	if !strings.Contains(joined, "10 caveats") {
		t.Errorf("the count is not the true number of caveats:\n%s", joined)
	}
	if !strings.Contains(joined, "recorded ×3") {
		t.Errorf("the repeated code is not collapsed:\n%s", joined)
	}
	// Six codes listed, the seventh folded into "+1 more": run_cut_by_clock is
	// the one past the cap, and it must not be named.
	if !strings.Contains(joined, "+1 more") {
		t.Errorf("the line does not say how many codes it folded:\n%s", joined)
	}
	if strings.Contains(joined, CodeRunCutByClock) {
		t.Errorf("the line names a code past the cap:\n%s", joined)
	}
	// And the collapsed code sits in rank order: after client_disagrees, the
	// code before it, and before machine_contended, the code after it.
	dis, recorded, contended := strings.Index(joined, CodeClientDisagrees), strings.Index(joined, "recorded ×3"), strings.Index(joined, CodeMachineContended)
	if !(dis < recorded && recorded < contended) {
		t.Errorf("recorded ×3 is out of rank order (%d, %d, %d):\n%s", dis, recorded, contended, joined)
	}
}
