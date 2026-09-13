package server

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/midagedev/toktape/internal/tape"
)

// loadFixture replays a recorded stream plus its arrival sidecar.
func loadFixture(t *testing.T, name string, hooks StreamHooks) (*tape.RequestRecord, ServerTimings) {
	t.Helper()
	rec, tim, err := replayFixture(t, name, hooks)
	if err != nil {
		t.Fatalf("ReplayStream(%s): %v", name, err)
	}
	return rec, tim
}

func replayFixture(t *testing.T, name string, hooks StreamHooks) (*tape.RequestRecord, ServerTimings, error) {
	t.Helper()
	sse := readFixture(t, name+".sse")
	arrivals, err := ParseArrivals(readFixture(t, name+".arrivals"))
	if err != nil {
		t.Fatalf("ParseArrivals(%s): %v", name, err)
	}
	return ReplayStream(sse, arrivals, hooks)
}

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return b
}

// fixtureEvent is one decoded event of a fixture with its arrival time, used
// by the trap tests to build reducers that get the window wrong on purpose.
type fixtureEvent struct {
	at       time.Duration
	chunk    streamChunk
	hasDelta bool
	text     string
	finish   bool
	done     bool
}

func fixtureEvents(t *testing.T, name string) []fixtureEvent {
	t.Helper()
	sse := readFixture(t, name+".sse")
	arrivals, err := ParseArrivals(readFixture(t, name+".arrivals"))
	if err != nil {
		t.Fatalf("ParseArrivals: %v", err)
	}
	var sc sseScanner
	raw := sc.feed(sse)
	raw = append(raw, sc.close()...)
	if len(raw) != len(arrivals) {
		t.Fatalf("%s: %d events but %d arrival times", name, len(raw), len(arrivals))
	}
	out := make([]fixtureEvent, 0, len(raw))
	for i, r := range raw {
		ev := fixtureEvent{at: arrivals[i]}
		if strings.TrimSpace(string(r)) == doneMarker {
			ev.done = true
			out = append(out, ev)
			continue
		}
		if err := json.Unmarshal(r, &ev.chunk); err != nil {
			t.Fatalf("%s: event %d: %v", name, i, err)
		}
		for _, ch := range ev.chunk.Choices {
			ev.hasDelta = true
			if ch.Delta.Content != nil {
				ev.text = *ch.Delta.Content
			}
			if ch.FinishReason != nil && *ch.FinishReason != "" {
				ev.finish = true
			}
		}
		out = append(out, ev)
	}
	return out
}

func relDiff(client, server float64) float64 { return math.Abs(client-server) / server }

// TestReduceBasicFixture pins the whole reduction of a realistic recorded
// stream: token count, TTFT, the decode label, and the client rate agreeing
// with the server's predicted_per_second within tape.RateTolerance.
//
// The fixture's arrival times are the client clock and its timings objects are
// the server clock; they differ by a constant offset plus per-token jitter, so
// this agreement is a real measurement and not an identity.
func TestReduceBasicFixture(t *testing.T) {
	rec, tim := loadFixture(t, "stream_basic", StreamHooks{})

	if got, want := len(rec.Tokens), 44; got != want {
		t.Errorf("tokens = %d, want %d (the role chunk, the two empty deltas and the finish chunk are not tokens)", got, want)
	}
	if got, want := rec.Timings.TTFTMs, 194.0; got != want {
		t.Errorf("TTFTMs = %v, want %v (first token that carried text, not the role chunk at 185)", got, want)
	}
	if got, want := rec.Prompt.FinishReason, "stop"; got != want {
		t.Errorf("FinishReason = %q, want %q", got, want)
	}
	if got, want := len(rec.Progress), 4; got != want {
		t.Errorf("Progress events = %d, want %d", got, want)
	}
	if got, want := rec.Progress[3], (tape.PromptProgress{T: 166 * time.Millisecond, Total: 38, Cache: 0, Processed: 38, TimeMs: 168}); got != want {
		t.Errorf("last progress = %+v, want %+v", got, want)
	}
	if got, want := rec.Slot, -1; got != want {
		t.Errorf("Slot = %d, want %d (the OAI chunks carry no slot id)", got, want)
	}
	if !strings.HasPrefix(rec.Prompt.Completion, " Memory-mapped files let") ||
		!strings.HasSuffix(rec.Prompt.Completion, " already touched") {
		t.Errorf("Completion = %q, want the concatenated token text", rec.Prompt.Completion)
	}

	// Server figures are the record.
	if got, want := tim.PredictedN, 44; got != want {
		t.Errorf("server predicted_n = %d, want %d", got, want)
	}
	if got, want := rec.Timings.PredictedPerSecond, 45.45454545454545; got != want {
		t.Errorf("server predicted_per_second = %v, want %v", got, want)
	}
	if got, want := rec.Timings.PromptN, 38; got != want {
		t.Errorf("server prompt_n = %d, want %d", got, want)
	}

	// Client figures are the check.
	d := relDiff(rec.Timings.ClientPredictedPerSecond, rec.Timings.PredictedPerSecond)
	if d > tape.RateTolerance {
		t.Errorf("client %v vs server %v: %.4f%% apart, want within %.0f%%",
			rec.Timings.ClientPredictedPerSecond, rec.Timings.PredictedPerSecond, d*100, tape.RateTolerance*100)
	}
	if !rec.Timings.ClientAgreesWithServer {
		t.Errorf("ClientAgreesWithServer = false, want true (%.4f%% apart)", d*100)
	}
	if got, want := rec.Timings.DecodeLabel, "decode"; got != want {
		t.Errorf("DecodeLabel = %q, want %q (44 >= tape.MinDecodeTokens)", got, want)
	}
	if rec.Timings.ITLp50Ms <= 0 || rec.Timings.ITLp95Ms < rec.Timings.ITLp50Ms || rec.Timings.ITLp99Ms < rec.Timings.ITLp95Ms {
		t.Errorf("ITL percentiles not ordered: p50=%v p95=%v p99=%v",
			rec.Timings.ITLp50Ms, rec.Timings.ITLp95Ms, rec.Timings.ITLp99Ms)
	}
	if got, want := rec.Timings.ITLp99Ms, 48.0; got != want {
		t.Errorf("ITLp99Ms = %v, want %v (the one stall in the fixture)", got, want)
	}
	if got, want := rec.Cache.Label, tape.CacheWarm; got != want {
		t.Errorf("Cache.Label = %q, want %q (no cache hit, no fault counters)", got, want)
	}
}

// TestReduceTraps is the FAIL-first half of the reducer. Each naive rule below
// is one of the mistakes measured in docs/research/00-handover-brief.md lesson
// 1. The test asserts twice: that the naive rule disagrees with the server by
// more than tape.RateTolerance, so the trap is real in this fixture and is not
// hidden by a lucky number, and that Reduce agrees.
//
// Without the first assertion a fixture could be rewritten into one where
// every rule happens to pass, and the trap tests would keep reporting green.
func TestReduceTraps(t *testing.T) {
	rec, _ := loadFixture(t, "stream_basic", StreamHooks{})
	events := fixtureEvents(t, "stream_basic")
	server := rec.Timings.PredictedPerSecond
	if server <= 0 {
		t.Fatalf("fixture has no server rate")
	}

	// Everything with a delta, finish chunk excluded: the role-only chunk and
	// the two empty content deltas become tokens and drag the window start
	// back to 185 ms.
	var counted []fixtureEvent
	for _, e := range events {
		if e.hasDelta && !e.finish {
			counted = append(counted, e)
		}
	}
	traps := []struct {
		name string
		rate float64
		why  string
	}{
		{
			name: "role chunk and empty deltas counted as tokens",
			rate: float64(len(counted)-1) / (counted[len(counted)-1].at - counted[0].at).Seconds(),
			why:  "a delta without text is not a token",
		},
		{
			name: "finish chunk counted as a token",
			rate: traceRate(events, true, 0),
			why:  "the finish chunk arrives after generation stopped",
		},
		{
			name: "window runs to now instead of to the last token",
			rate: traceRate(events, false, 1993*time.Millisecond),
			why:  "wall time keeps running after the last token",
		},
		{
			name: "n tokens over the n-1 gaps between them",
			rate: traceRateN(events),
			why:  "the server divides n tokens by a window that starts before the first one",
		},
	}
	for _, tr := range traps {
		d := relDiff(tr.rate, server)
		if d <= tape.RateTolerance {
			t.Errorf("trap %q gave %.4f tok/s, only %.4f%% from the server's %.4f: the fixture no longer exercises this trap (%s)",
				tr.name, tr.rate, d*100, server, tr.why)
		}
	}

	if d := relDiff(rec.Timings.ClientPredictedPerSecond, server); d > tape.RateTolerance {
		t.Errorf("Reduce gave %.4f tok/s, %.4f%% from the server's %.4f", rec.Timings.ClientPredictedPerSecond, d*100, server)
	}
}

// traceRate measures the content tokens over a window that optionally swallows
// the finish chunk or runs on to a later instant.
func traceRate(events []fixtureEvent, includeFinish bool, until time.Duration) float64 {
	var ats []time.Duration
	for _, e := range events {
		if e.text != "" {
			ats = append(ats, e.at)
		} else if includeFinish && e.finish {
			ats = append(ats, e.at)
		}
	}
	end := ats[len(ats)-1]
	if until > end {
		end = until
	}
	return float64(len(ats)-1) / (end - ats[0]).Seconds()
}

// traceRateN divides the right token count by the right window with the wrong
// numerator: n tokens span n-1 gaps.
func traceRateN(events []fixtureEvent) float64 {
	var ats []time.Duration
	for _, e := range events {
		if e.text != "" {
			ats = append(ats, e.at)
		}
	}
	return float64(len(ats)) / (ats[len(ats)-1] - ats[0]).Seconds()
}

// TestReduceReasoningFixture: a reasoning delta IS a decode token.
//
// Rewritten 2026-09-13 (TTP-20, lead decision). Until then this test pinned
// the opposite contract — reasoning deltas stayed out of rec.Tokens, so TTFT
// was the first *content* token at 165 ms and the client window spanned four
// tokens. The first real end-to-end run killed that reading: a thinking model
// whose whole budget went to reasoning_content produced tokens=null,
// ttft_ms=0 and client_agrees_with_server=false against the server's
// predicted_n=96. The server counts a reasoning token in predicted_n exactly
// like an answer token, so every rate and TTFT must count it too; only the
// transcript keeps the two apart (Prompt.Reasoning vs Prompt.Completion).
// This is not a loosened assertion: the counts and the window got LARGER and
// stricter, and TestReduceReasoningOnlyFixture pins the case that had no
// assertion at all before.
func TestReduceReasoningFixture(t *testing.T) {
	var reasoning, tokens []tape.TokenEvent
	rec, _ := loadFixture(t, "stream_reasoning", StreamHooks{
		OnReasoning: func(ev tape.TokenEvent) { reasoning = append(reasoning, ev) },
		OnToken:     func(ev tape.TokenEvent) { tokens = append(tokens, ev) },
	})

	if got, want := len(rec.Tokens), 7; got != want {
		t.Errorf("tokens = %d, want %d (3 reasoning + 4 content)", got, want)
	}
	// OnToken fires for every token the record keeps, reasoning included: the
	// process recorder stitches its per-token major-fault deltas back onto
	// rec.Tokens by arrival order, so a token that skipped the hook would
	// shift every later delta onto the wrong token.
	if got, want := len(tokens), 7; got != want {
		t.Errorf("OnToken fired %d times, want %d (every recorded token)", got, want)
	}
	// OnReasoning still fires as well: it is the transcript hook, and a caller
	// that wants only the thinking text should not have to filter OnToken.
	if got, want := len(reasoning), 3; got != want {
		t.Errorf("OnReasoning fired %d times, want %d", got, want)
	}
	wantFlags := []bool{true, true, true, false, false, false, false}
	for i, want := range wantFlags {
		if i >= len(rec.Tokens) {
			break
		}
		if got := rec.Tokens[i].Reasoning; got != want {
			t.Errorf("Tokens[%d].Reasoning = %v, want %v (%q)", i, got, want, rec.Tokens[i].Text)
		}
		if got := rec.Tokens[i].Index; got != i {
			t.Errorf("Tokens[%d].Index = %d, want %d (one shared sequence)", i, got, i)
		}
	}
	if got, want := rec.Prompt.Completion, "Four bytes per word."; got != want {
		t.Errorf("Completion = %q, want %q (answer only)", got, want)
	}
	if strings.Contains(rec.Prompt.Completion, "The user") {
		t.Errorf("Completion leaked reasoning text: %q", rec.Prompt.Completion)
	}
	if got, want := rec.Prompt.Reasoning, "The user wants a short answer."; got != want {
		t.Errorf("Reasoning = %q, want %q", got, want)
	}
	if got, want := rec.Prompt.ReasoningN, 3; got != want {
		t.Errorf("ReasoningN = %d, want %d", got, want)
	}
	if got, want := rec.Timings.TTFTMs, 90.0; got != want {
		t.Errorf("TTFTMs = %v, want %v (the first reasoning delta; the server was already decoding)", got, want)
	}
	if got, want := rec.Timings.DecodeLabel, "sample"; got != want {
		t.Errorf("DecodeLabel = %q, want %q (7 predicted tokens is below tape.MinDecodeTokens)", got, want)
	}
	// The counts now match: the server's predicted_n and the number of tokens
	// the client recorded are the same 7. Before TTP-20 they were 7 and 4, and
	// a renderer printing len(Tokens) as "tokens generated" understated a
	// thinking run by the whole thinking budget.
	if got, want := rec.Timings.PredictedN, 7; got != want {
		t.Errorf("PredictedN = %d, want %d (the server counts reasoning tokens)", got, want)
	}
	if got, want := len(rec.Tokens), rec.Timings.PredictedN; got != want {
		t.Errorf("len(Tokens) = %d, PredictedN = %d; the two counts must agree for a thinking model", got, want)
	}
	// The rates agree over the wider window too: 6 gaps of 25 ms is the same
	// 40 tok/s the server reports. The cross-check survives a thinking model
	// and must not be weakened for one.
	if !rec.Timings.ClientAgreesWithServer {
		t.Errorf("ClientAgreesWithServer = false; client %v vs server %v, %.4f%% apart",
			rec.Timings.ClientPredictedPerSecond, rec.Timings.PredictedPerSecond,
			relDiff(rec.Timings.ClientPredictedPerSecond, rec.Timings.PredictedPerSecond)*100)
	}
	if got, want := rec.Cache.Label, tape.CacheCached; got != want {
		t.Errorf("Cache.Label = %q, want %q (30 of 38 prompt tokens from the prefix cache)", got, want)
	}
	if got, want := rec.Cache.HitTokens, 30; got != want {
		t.Errorf("Cache.HitTokens = %d, want %d", got, want)
	}
}

// TestReduceReasoningOnlyFixture is the run that found the defect (2026-09-13,
// llama-server b40, DeepSeek-V4.1-Flash, --jinja): all 96 predicted tokens
// arrived as reasoning_content and not one as content. Every figure the card
// prints has to survive that, because for a thinking model asked a short
// question it is the ordinary case, not an edge one.
func TestReduceReasoningOnlyFixture(t *testing.T) {
	var reasoning, tokens []tape.TokenEvent
	rec, srv := loadFixture(t, "stream_reasoning_only", StreamHooks{
		OnReasoning: func(ev tape.TokenEvent) { reasoning = append(reasoning, ev) },
		OnToken:     func(ev tape.TokenEvent) { tokens = append(tokens, ev) },
	})

	if got, want := len(rec.Tokens), 96; got != want {
		t.Fatalf("tokens = %d, want %d (the defect recorded 0)", got, want)
	}
	if got, want := len(tokens), 96; got != want {
		t.Errorf("OnToken fired %d times, want %d", got, want)
	}
	if got, want := len(reasoning), 96; got != want {
		t.Errorf("OnReasoning fired %d times, want %d", got, want)
	}
	for i, tk := range rec.Tokens {
		if !tk.Reasoning {
			t.Fatalf("Tokens[%d].Reasoning = false; every token of this stream is a reasoning token", i)
		}
	}
	if got, want := rec.Prompt.ReasoningN, 96; got != want {
		t.Errorf("ReasoningN = %d, want %d", got, want)
	}
	// The answer is genuinely empty: the model was cut off by n_predict while
	// still thinking. Empty is the measurement here, not an unknown.
	if got := rec.Prompt.Completion; got != "" {
		t.Errorf("Completion = %q, want empty (no content delta in this stream)", got)
	}
	if !strings.HasPrefix(rec.Prompt.Reasoning, "Okay, the user is asking") {
		t.Errorf("Reasoning = %q..., want the concatenated thinking text", clip(rec.Prompt.Reasoning, 40))
	}
	if got, want := len(rec.Prompt.Reasoning), 496; got != want {
		t.Errorf("len(Reasoning) = %d, want %d bytes (the whole monologue, joined without gaps)", got, want)
	}
	if got, want := rec.Prompt.FinishReason, "length"; got != want {
		t.Errorf("FinishReason = %q, want %q", got, want)
	}
	// TTFT was 0 before TTP-20: no token had been recorded, so there was
	// nothing to take the first arrival from and the card printed "TTFT ?".
	if got, want := rec.Timings.TTFTMs, 222.0; got != want {
		t.Errorf("TTFTMs = %v, want %v (arrival of the first reasoning token)", got, want)
	}
	// 96 tokens is above tape.MinDecodeTokens, so this is a decode rate and
	// not a sample — the label the defect got wrong in the other direction by
	// falling back to len(Tokens) == 0.
	if got, want := rec.Timings.DecodeLabel, "decode"; got != want {
		t.Errorf("DecodeLabel = %q, want %q", got, want)
	}
	if got, want := rec.Timings.PredictedN, 96; got != want {
		t.Errorf("PredictedN = %d, want %d", got, want)
	}
	if got, want := len(rec.Tokens), srv.PredictedN; got != want {
		t.Errorf("len(Tokens) = %d, server predicted_n = %d; the two counts must agree", got, want)
	}
	// The whole point of the cross-check: with the reasoning tokens counted,
	// the client's independent clock reproduces the server's rate. It read 0
	// before, which silently turned the check off.
	if rec.Timings.ClientPredictedPerSecond <= 0 {
		t.Fatalf("ClientPredictedPerSecond = %v, want > 0", rec.Timings.ClientPredictedPerSecond)
	}
	if !rec.Timings.ClientAgreesWithServer {
		t.Errorf("ClientAgreesWithServer = false; client %v vs server %v, %.4f%% apart (tolerance %v)",
			rec.Timings.ClientPredictedPerSecond, rec.Timings.PredictedPerSecond,
			relDiff(rec.Timings.ClientPredictedPerSecond, rec.Timings.PredictedPerSecond)*100,
			tape.RateTolerance)
	}
	// The 61 ms stall at index 40 is the only gap above 28 ms, so p99 sees it
	// and p50 does not: the latency strip stays informative for a stream that
	// never produced an answer token.
	if rec.Timings.ITLp50Ms > 30 || rec.Timings.ITLp99Ms < 55 {
		t.Errorf("ITL p50 %v / p99 %v, want p50 <= 30 and p99 >= 55 (the stall)",
			rec.Timings.ITLp50Ms, rec.Timings.ITLp99Ms)
	}
}

func TestReduceZeroGuards(t *testing.T) {
	tok := func(ms ...int) []tape.TokenEvent {
		var out []tape.TokenEvent
		for i, m := range ms {
			out = append(out, tape.TokenEvent{T: time.Duration(m) * time.Millisecond, Index: i, Text: "x"})
		}
		return out
	}
	cases := []struct {
		name   string
		rec    tape.RequestRecord
		active int64
		check  func(t *testing.T, s tape.TimingsSummary)
	}{
		{
			name: "no tokens",
			rec:  tape.RequestRecord{Timings: tape.TimingsSummary{PredictedPerSecond: 40, PredictedN: 0}},
			check: func(t *testing.T, s tape.TimingsSummary) {
				if s.TTFTMs != 0 || s.ClientPredictedPerSecond != 0 || s.ClientAgreesWithServer {
					t.Errorf("got %+v, want zero client figures and no agreement", s)
				}
				if s.DecodeLabel != "sample" {
					t.Errorf("DecodeLabel = %q, want sample", s.DecodeLabel)
				}
			},
		},
		{
			name: "one token has a TTFT but no rate",
			rec:  tape.RequestRecord{Tokens: tok(120), Timings: tape.TimingsSummary{PredictedPerSecond: 40, PredictedN: 1}},
			check: func(t *testing.T, s tape.TimingsSummary) {
				if s.TTFTMs != 120 {
					t.Errorf("TTFTMs = %v, want 120", s.TTFTMs)
				}
				if s.ClientPredictedPerSecond != 0 || s.ITLp50Ms != 0 {
					t.Errorf("got rate %v itl %v, want both 0", s.ClientPredictedPerSecond, s.ITLp50Ms)
				}
				if s.ClientAgreesWithServer {
					t.Error("ClientAgreesWithServer = true with no client rate")
				}
			},
		},
		{
			name: "server reported no rate",
			rec:  tape.RequestRecord{Tokens: tok(100, 120, 140), Timings: tape.TimingsSummary{PredictedN: 3}},
			check: func(t *testing.T, s tape.TimingsSummary) {
				if s.ClientPredictedPerSecond == 0 {
					t.Error("client rate = 0, want it measured")
				}
				if s.ClientAgreesWithServer {
					t.Error("ClientAgreesWithServer = true with no server figure to agree with")
				}
			},
		},
		{
			name: "long stream from a build without timings is still decode",
			rec: tape.RequestRecord{Tokens: func() []tape.TokenEvent {
				ms := make([]int, 200)
				for i := range ms {
					ms[i] = 100 + 20*i
				}
				return tok(ms...)
			}()},
			check: func(t *testing.T, s tape.TimingsSummary) {
				if s.DecodeLabel != "decode" {
					t.Errorf("DecodeLabel = %q, want decode (200 tokens seen, server silent)", s.DecodeLabel)
				}
			},
		},
		{
			name:   "effective bandwidth uses the server rate",
			rec:    tape.RequestRecord{Tokens: tok(100, 120), Timings: tape.TimingsSummary{PredictedN: 2, PredictedPerSecond: 50}},
			active: 1 << 30,
			check: func(t *testing.T, s tape.TimingsSummary) {
				if got, want := s.EffectiveBandwidthBytesPerSec, int64(50*(1<<30)); got != want {
					t.Errorf("EffectiveBandwidthBytesPerSec = %d, want %d", got, want)
				}
			},
		},
		{
			name: "no active bytes leaves the bandwidth unknown",
			rec:  tape.RequestRecord{Tokens: tok(100, 120), Timings: tape.TimingsSummary{PredictedN: 2, PredictedPerSecond: 50}},
			check: func(t *testing.T, s tape.TimingsSummary) {
				if s.EffectiveBandwidthBytesPerSec != 0 {
					t.Errorf("EffectiveBandwidthBytesPerSec = %d, want 0", s.EffectiveBandwidthBytesPerSec)
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := tc.rec
			tc.check(t, Reduce(&rec, time.Time{}, tc.active))
		})
	}
}

func TestReduceIsIdempotent(t *testing.T) {
	rec, _ := loadFixture(t, "stream_basic", StreamHooks{})
	again := Reduce(rec, time.Time{}, 0)
	if again != rec.Timings {
		t.Errorf("second Reduce changed the summary:\n%+v\n%+v", again, rec.Timings)
	}
}

func TestCacheVerdict(t *testing.T) {
	cases := []struct {
		name        string
		cacheN      int
		promptN     int
		majFaults   uint64
		predictedN  int
		wantLabel   tape.CacheLabel
		wantRatio   float64
		wantHits    int
		wantTotal   int
		ratioApprox bool
	}{
		{name: "cold run pages weights in during decode", cacheN: 0, promptN: 512, majFaults: 300, predictedN: 200, wantLabel: tape.CacheCold, wantTotal: 512},
		{name: "cold beats a full prefix cache", cacheN: 500, promptN: 12, majFaults: 200, predictedN: 100, wantLabel: tape.CacheCold, wantHits: 500, wantTotal: 512, wantRatio: 500.0 / 512.0, ratioApprox: true},
		{name: "prefix cache hit", cacheN: 400, promptN: 112, predictedN: 200, wantLabel: tape.CacheCached, wantHits: 400, wantTotal: 512, wantRatio: 400.0 / 512.0, ratioApprox: true},
		{name: "evaluated prompt, no faults", cacheN: 0, promptN: 38, predictedN: 44, wantLabel: tape.CacheWarm, wantTotal: 38},
		{name: "a small hit is not a cached run", cacheN: 10, promptN: 490, predictedN: 44, wantLabel: tape.CacheWarm, wantHits: 10, wantTotal: 500, wantRatio: 0.02, ratioApprox: true},
		{name: "nothing measured", wantLabel: tape.CacheWarm},
		{name: "faults but no token count cannot be called cold", majFaults: 900, predictedN: 0, promptN: 38, wantLabel: tape.CacheWarm, wantTotal: 38},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := CacheVerdict(tape.TimingsSummary{CacheN: tc.cacheN, PromptN: tc.promptN}, tc.majFaults, tc.predictedN)
			if got.Label != tc.wantLabel {
				t.Errorf("Label = %q, want %q", got.Label, tc.wantLabel)
			}
			if got.HitTokens != tc.wantHits || got.PromptTotal != tc.wantTotal {
				t.Errorf("hits/total = %d/%d, want %d/%d", got.HitTokens, got.PromptTotal, tc.wantHits, tc.wantTotal)
			}
			if tc.ratioApprox {
				if math.Abs(got.HitRatio-tc.wantRatio) > 1e-9 {
					t.Errorf("HitRatio = %v, want %v", got.HitRatio, tc.wantRatio)
				}
			} else if got.HitRatio != tc.wantRatio {
				t.Errorf("HitRatio = %v, want %v", got.HitRatio, tc.wantRatio)
			}
		})
	}
}

func TestPercentileNearestRank(t *testing.T) {
	xs := []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	for _, tc := range []struct {
		p    float64
		want float64
	}{{0.5, 5}, {0.95, 10}, {0.99, 10}, {0.1, 1}} {
		if got := percentile(xs, tc.p); got != tc.want {
			t.Errorf("percentile(%v) = %v, want %v", tc.p, got, tc.want)
		}
	}
	if got := percentile(nil, 0.5); got != 0 {
		t.Errorf("percentile(nil) = %v, want 0", got)
	}
	if got := percentile([]float64{42}, 0.99); got != 42 {
		t.Errorf("percentile single = %v, want 42", got)
	}
}

// TestReduceRealThinkingCapture replays bytes captured from a real run
// (llama-server b40, DeepSeek-V4.1-Flash, --jinja, 2026-09-13) — the run that
// found the defect. It is the only fixture here nobody wrote: the chunk shape,
// the null content in the role delta, the timings keys and the cut-off
// monologue are the server's own.
//
// There is no arrival sidecar, because the capture kept the bytes and not the
// client clock. ReplayStream then synthesises each arrival from that chunk's
// own prompt_ms + predicted_ms, which makes the client rate an identity of the
// server figures — so this test asserts the counts, the text and the labels,
// and leaves the independent rate cross-check to the fixtures that carry a
// separate client clock.
func TestReduceRealThinkingCapture(t *testing.T) {
	rec, srv, err := ReplayStream(readFixture(t, "stream_reasoning_real.sse"), nil, StreamHooks{})
	if err != nil {
		t.Fatalf("ReplayStream(stream_reasoning_real): %v", err)
	}

	// The defect recorded 0 of these 48 and wrote tokens=null to the tape.
	if got, want := len(rec.Tokens), 48; got != want {
		t.Fatalf("tokens = %d, want %d", got, want)
	}
	if got, want := srv.PredictedN, 48; got != want {
		t.Errorf("server predicted_n = %d, want %d", got, want)
	}
	if got, want := rec.Timings.ReasoningN, 48; got != want {
		t.Errorf("Timings.ReasoningN = %d, want %d (the figure the card's Context row prints)", got, want)
	}
	if got, want := rec.Prompt.ReasoningN, 48; got != want {
		t.Errorf("Prompt.ReasoningN = %d, want %d", got, want)
	}
	for i, tk := range rec.Tokens {
		if !tk.Reasoning {
			t.Fatalf("Tokens[%d].Reasoning = false; this capture has no content delta at all", i)
		}
	}
	if got := rec.Prompt.Completion; got != "" {
		t.Errorf("Completion = %q, want empty: the model hit n_predict while still thinking", got)
	}
	if !strings.HasPrefix(rec.Prompt.Reasoning, "We need answer one sentence.") {
		t.Errorf("Reasoning = %q..., want the captured monologue", clip(rec.Prompt.Reasoning, 40))
	}
	if got, want := rec.Prompt.FinishReason, "length"; got != want {
		t.Errorf("FinishReason = %q, want %q", got, want)
	}
	// TTFT was 0 and the card printed "TTFT ?". 505.849 ms is the server's own
	// prompt_ms plus the first decode step, which is what the synthesised
	// clock can know; a live run measures it against the send instant.
	if got, want := rec.Timings.TTFTMs, 505.849; got != want {
		t.Errorf("TTFTMs = %v, want %v", got, want)
	}
	// 48 tokens is above tape.MinDecodeTokens, so this is a decode rate. The
	// defect fell back to len(Tokens) == 0 and could not say even that.
	if got, want := rec.Timings.DecodeLabel, "decode"; got != want {
		t.Errorf("DecodeLabel = %q, want %q", got, want)
	}
	if rec.Timings.ITLp99Ms <= rec.Timings.ITLp50Ms {
		t.Errorf("ITL p99 %v <= p50 %v; the per-token timeline is degenerate",
			rec.Timings.ITLp99Ms, rec.Timings.ITLp50Ms)
	}
}
