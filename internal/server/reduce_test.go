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

// TestReduceReasoningFixture: a thinking model's reasoning deltas are
// generated tokens on the server's count but are not part of the answer. They
// must stay out of Completion and out of Tokens, and the disagreement that
// causes with the server's predicted_n must be reported, not smoothed over.
func TestReduceReasoningFixture(t *testing.T) {
	var reasoning []tape.TokenEvent
	rec, _ := loadFixture(t, "stream_reasoning", StreamHooks{
		OnReasoning: func(ev tape.TokenEvent) { reasoning = append(reasoning, ev) },
	})

	if got, want := len(rec.Tokens), 4; got != want {
		t.Errorf("tokens = %d, want %d (only the content deltas)", got, want)
	}
	if got, want := len(reasoning), 3; got != want {
		t.Errorf("OnReasoning fired %d times, want %d", got, want)
	}
	if got, want := rec.Prompt.Completion, "Four bytes per word."; got != want {
		t.Errorf("Completion = %q, want %q (no reasoning text)", got, want)
	}
	if strings.Contains(rec.Prompt.Completion, "The user") {
		t.Errorf("Completion leaked reasoning text: %q", rec.Prompt.Completion)
	}
	if got, want := rec.Timings.TTFTMs, 165.0; got != want {
		t.Errorf("TTFTMs = %v, want %v (first content token, not the first reasoning delta at 90)", got, want)
	}
	if got, want := rec.Timings.DecodeLabel, "sample"; got != want {
		t.Errorf("DecodeLabel = %q, want %q (7 predicted tokens is below tape.MinDecodeTokens)", got, want)
	}
	// The server counted 7 generated tokens; the client recorded 4 content
	// ones. The COUNTS differ and both are visible, which is the point: a
	// renderer that prints len(Tokens) as "tokens generated" would understate
	// a thinking run by the whole thinking budget.
	if got, want := rec.Timings.PredictedN, 7; got != want {
		t.Errorf("PredictedN = %d, want %d (the server counts reasoning tokens)", got, want)
	}
	// The RATES still agree: reasoning tokens are produced at the same speed
	// as content tokens, so excluding them from the client window changes the
	// count but not the tok/s. The cross-check therefore survives a thinking
	// model and must not be weakened for one.
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
