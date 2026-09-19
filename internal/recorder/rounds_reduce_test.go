package recorder

import (
	"math"
	"testing"
	"time"

	"github.com/midagedev/toktape/internal/card"
	"github.com/midagedev/toktape/internal/procmon"
	"github.com/midagedev/toktape/internal/server"
	"github.com/midagedev/toktape/internal/tape"
)

// roundRec is one reduced stream of a multi-round fixture: when it was sent,
// when its first and last tokens arrived (both since the run start), and the
// server figures it reported.
func roundRec(round, index int, sent, first, last time.Duration, predictedN int, rate float64) tape.RequestRecord {
	return tape.RequestRecord{
		Index:     index,
		Round:     round,
		StartedAt: sent,
		Tokens: []tape.TokenEvent{
			{T: first - sent, Index: 0},
			{T: last - sent, Index: 1},
		},
		Timings: tape.TimingsSummary{
			PromptN:            100,
			PredictedN:         predictedN,
			PredictedPerSecond: rate,
			TTFTMs:             float64((first - sent) / time.Millisecond),
		},
	}
}

// raggedRec is one reduced stream with a full token timeline: n tokens from
// first (absolute, since the run start) spaced gap apart, sent at sent. Built
// for the concurrent-window gates, which need streams that end at
// deliberately different times inside a round.
func raggedRec(round, index int, sent, first, gap time.Duration, n int) tape.RequestRecord {
	rec := tape.RequestRecord{Index: index, Round: round, StartedAt: sent}
	for i := 0; i < n; i++ {
		rec.Tokens = append(rec.Tokens, tape.TokenEvent{T: first - sent + time.Duration(i)*gap, Index: i})
	}
	rec.Timings = tape.TimingsSummary{
		PromptN:    100,
		PredictedN: n,
		TTFTMs:     float64((first - sent) / time.Millisecond),
	}
	return rec
}

// TestReduceRoundsConcurrentWindowIsTheRoundsOwn (TTP-138): the run's
// concurrent window is the sum of the rounds' own, taken from the same
// server.Aggregate that computes them for a single-round run — the window
// arithmetic has one owner, and the rounds path reads its per-round output
// the same way it reads the decode window, rather than re-deriving it.
//
// Over every record at once the window is worse than naive: serial rounds
// make the latest first token come after the earliest last one, so the span
// in which "every stream was decoding" is negative and the naive figure is
// the schema's unknown — which is exactly why the override here must exist.
func TestReduceRoundsConcurrentWindowIsTheRoundsOwn(t *testing.T) {
	s, ms := time.Second, time.Millisecond
	recs := []tape.RequestRecord{
		// Round 0: A 100→1000 ms, B 300→1200 ms — window [300, 1000], 16
		// tokens inside (A 8, B 8).
		raggedRec(0, 0, 0, 100*ms, 100*ms, 10),
		raggedRec(0, 1, 0, 300*ms, 100*ms, 10),
		// Round 1: C 10.5→11.4 s, D 10.7→11.2 s — window [10700, 11200],
		// 12 tokens inside (C 6, D 6).
		raggedRec(1, 0, 10*s, 10500*ms, 100*ms, 10),
		raggedRec(1, 1, 10*s, 10700*ms, 100*ms, 6),
	}

	// The precondition the override exists for: across the serial rounds the
	// window inverts (latest first 10700 > earliest last 1000), so the naive
	// aggregate over every record reports the schema's unknown.
	if naive := server.Aggregate(recs); naive.ConcurrentWindowMs != 0 || naive.ConcurrentPredictedN != 0 {
		t.Fatalf("the naive concurrent window is %v ms / %d tokens, want 0 — the fixture no longer inverts across rounds",
			naive.ConcurrentWindowMs, naive.ConcurrentPredictedN)
	}

	agg, _, _ := reduceRounds(recs, []string{"ragged", "later"}, 2)

	// The same function, per round: whatever server.Aggregate says each
	// round's window was, the run's is the sum of exactly those figures. A
	// second implementation of the arithmetic would have to keep agreeing
	// with every one of these to pass.
	perRound := []tape.AggregateTimings{
		server.Aggregate(recs[:2]),
		server.Aggregate(recs[2:]),
	}
	var wantMs, wantN int = 0, 0
	for _, a := range perRound {
		wantMs += int(a.ConcurrentWindowMs)
		wantN += a.ConcurrentPredictedN
	}
	if wantMs != 700+500 || wantN != 16+12 {
		t.Fatalf("per-round windows summed to %d ms / %d tokens, want 1200 / 28 — the fixture drifted", wantMs, wantN)
	}
	if got := int(agg.ConcurrentWindowMs); got != wantMs {
		t.Errorf("run ConcurrentWindowMs = %d, want %d (the rounds' own windows summed)", got, wantMs)
	}
	if got := agg.ConcurrentPredictedN; got != wantN {
		t.Errorf("run ConcurrentPredictedN = %d, want %d", got, wantN)
	}
	if got, want := agg.ConcurrentPredictedPerSecond, 28.0/1.2; math.Abs(got-want) > 0.01 {
		t.Errorf("run ConcurrentPredictedPerSecond = %v, want %v (28 tokens over the summed 1.2 s)", got, want)
	}
	// The recombination is a rate over summed windows, not a mean of rates:
	// round 0 says 16/0.7 = 22.9 and round 1 says 12/0.5 = 24, and the run
	// must carry the pooled 23.3 rather than either round's own figure.
	if math.Abs(agg.ConcurrentPredictedPerSecond-16.0/0.7) < 0.1 {
		t.Errorf("run concurrent rate = %v, equals round 0's own figure; want the pooled one", agg.ConcurrentPredictedPerSecond)
	}
}

func intp(v int) *int { return &v }

// twoRoundsWithAGap is two rounds of two streams with a 7.5 s pause between
// them — the time a user's prompts file spends in /apply-template, or a slow
// server spends between requests. Nothing is generated in the pause.
//
//	round 0  sent 0 s, tokens 0.5 → 2.5 s, 40 tokens a stream: wall 2.5 s,
//	         decode window 2 s, prefill window 0.5 s
//	round 1  sent 10 s, tokens 10.5 → 11.5 s, 20 tokens a stream: wall 1.5 s,
//	         decode window 1 s, prefill window 0.5 s
//
// The honest run is 4 s of wall, 120 tokens over 3 s of decoding = 40 tok/s,
// and 400 prompt tokens over 1 s of prefill = 400 tok/s.
func twoRoundsWithAGap() []tape.RequestRecord {
	s := time.Second
	ms := time.Millisecond
	return []tape.RequestRecord{
		roundRec(0, 0, 0, 500*ms, 2500*ms, 40, 21),
		roundRec(0, 1, 0, 500*ms, 2500*ms, 40, 19),
		roundRec(1, 0, 10*s, 10500*ms, 11500*ms, 20, 11),
		roundRec(1, 1, 10*s, 10500*ms, 11500*ms, 20, 9),
	}
}

// TestReduceRoundsLeavesTheGapsOut (TTP-31, 2026-09-13) is the FAIL-first
// gate for the aggregate of a multi-round run. server.Aggregate over every
// record stretches its windows from the first round's start to the last
// round's last token, so the pause between rounds lands in the wall figure and
// in both rate denominators: the naive wall is 11.5 s instead of 4 s and the
// naive decode rate 10.9 tok/s instead of 40.
func TestReduceRoundsLeavesTheGapsOut(t *testing.T) {
	recs := twoRoundsWithAGap()
	naive := server.Aggregate(recs)
	agg, per, spread := reduceRounds(recs, []string{"sql", ""}, 2)

	close := func(got, want float64) bool { return math.Abs(got-want) <= 1e-9*math.Max(1, math.Abs(want)) }
	if !close(agg.WallMs, 4000) {
		t.Errorf("WallMs = %v, want 4000 (2500 + 1500, the rounds' own windows); naive figure %v", agg.WallMs, naive.WallMs)
	}
	if !close(agg.AggregatePredictedPerSecond, 40) {
		t.Errorf("AggregatePredictedPerSecond = %v, want 40 (120 tokens / 3 s of decoding); naive figure %v",
			agg.AggregatePredictedPerSecond, naive.AggregatePredictedPerSecond)
	}
	if !close(agg.AggregatePromptPerSecond, 400) {
		t.Errorf("AggregatePromptPerSecond = %v, want 400 (400 prompt tokens / 1 s of prefill); naive figure %v",
			agg.AggregatePromptPerSecond, naive.AggregatePromptPerSecond)
	}
	if agg.Streams != 2 {
		t.Errorf("Streams = %d, want 2: the streams sent at once, not streams × rounds", agg.Streams)
	}
	if agg.TotalPredictedN != 120 || agg.TotalPromptN != 400 {
		t.Errorf("totals = %d predicted / %d prompt, want 120 / 400", agg.TotalPredictedN, agg.TotalPromptN)
	}
	if !close(agg.PerStreamPredictedPerSecond, 15) {
		t.Errorf("PerStreamPredictedPerSecond = %v, want 15, the mean of all four streams", agg.PerStreamPredictedPerSecond)
	}

	if len(per) != 2 {
		t.Fatalf("PerRound has %d entries, want 2", len(per))
	}
	want := []tape.RoundSummary{
		{Index: 0, Name: "sql", Streams: 2, PerStreamPredictedPerSecond: 20, AggregatePredictedPerSecond: 40, PredictedN: 80, TTFTp50Ms: 500},
		{Index: 1, Name: "", Streams: 2, PerStreamPredictedPerSecond: 10, AggregatePredictedPerSecond: 40, PredictedN: 40, TTFTp50Ms: 500},
	}
	for k := range want {
		g, w := per[k], want[k]
		if g.Index != w.Index || g.Name != w.Name || g.Streams != w.Streams || g.PredictedN != w.PredictedN ||
			!close(g.PerStreamPredictedPerSecond, w.PerStreamPredictedPerSecond) ||
			!close(g.AggregatePredictedPerSecond, w.AggregatePredictedPerSecond) ||
			!close(g.TTFTp50Ms, w.TTFTp50Ms) || g.DraftN != nil {
			t.Errorf("PerRound[%d] = %+v, want %+v", k, g, w)
		}
	}
	if spread == nil {
		t.Fatal("Spread is nil for two rounds")
	}
	if got := spread.PerStreamPredictedPerSecond; got != (tape.Spread{Median: 15, Min: 10, Max: 20}) {
		t.Errorf("rate spread = %+v, want median 15 (mean of the two middle values) in 10–20", got)
	}
	if got := spread.DraftAcceptRate; got != (tape.Spread{}) {
		t.Errorf("accept spread = %+v, want all-zero: no round drafted", got)
	}
}

// TestReduceRoundsSingleRoundAgreesWithAggregate: the corrections change
// nothing when there is no gap to leave out. This is the drift guard between
// the window arithmetic here and server.Aggregate's.
func TestReduceRoundsSingleRoundAgreesWithAggregate(t *testing.T) {
	recs := twoRoundsWithAGap()[:2]
	want := server.Aggregate(recs)
	got, per, spread := reduceRounds(recs, []string{"only"}, 2)
	rel := func(a, b float64) float64 { return math.Abs(a-b) / math.Max(1, math.Abs(b)) }
	if rel(got.WallMs, want.WallMs) > 1e-9 ||
		rel(got.AggregatePredictedPerSecond, want.AggregatePredictedPerSecond) > 1e-9 ||
		rel(got.AggregatePromptPerSecond, want.AggregatePromptPerSecond) > 1e-9 {
		t.Errorf("one round: got %+v, server.Aggregate says %+v", got, want)
	}
	if per != nil || spread != nil {
		t.Errorf("one round: PerRound %v / Spread %v, want both empty (schema: 0 or 1 round)", per, spread)
	}
}

// TestReduceRoundsDraftAndFailures: acceptance is pooled per round, a round
// that drafted nothing or failed outright has no figure in the spread, and a
// lone stream's draft counts are copied, not aliased.
func TestReduceRoundsDraftAndFailures(t *testing.T) {
	ms := time.Millisecond
	a := roundRec(0, 0, 0, 500*ms, 2500*ms, 40, 20)
	a.Timings.DraftN, a.Timings.DraftNAccepted = intp(100), intp(87)
	b := roundRec(1, 0, 3000*ms, 3500*ms, 5500*ms, 40, 10)
	b.Timings.DraftN, b.Timings.DraftNAccepted = intp(200), intp(26)
	c := roundRec(2, 0, 6000*ms, 6500*ms, 8500*ms, 40, 14)
	c.Timings.DraftN, c.Timings.DraftNAccepted = intp(0), intp(0)
	failed := tape.RequestRecord{Index: 0, Round: 3, StartedAt: 9000 * ms, Slot: -1, Error: "boom"}
	recs := []tape.RequestRecord{a, b, c, failed}

	agg, per, spread := reduceRounds(recs, []string{"sql", "prose", "empty", "dead"}, 1)
	if agg.StreamsFailed != 1 || agg.Streams != 1 {
		t.Errorf("Streams / failed = %d / %d, want 1 / 1", agg.Streams, agg.StreamsFailed)
	}
	if per[0].DraftN == nil || *per[0].DraftN != 100 || *per[0].DraftNAccepted != 87 {
		t.Fatalf("PerRound[0] draft = %v/%v, want 87/100", per[0].DraftNAccepted, per[0].DraftN)
	}
	if per[0].DraftN == recs[0].Timings.DraftN || per[0].DraftNAccepted == recs[0].Timings.DraftNAccepted {
		t.Error("PerRound[0] aliases the record's draft counters")
	}
	if per[3].Streams != 1 || per[3].PerStreamPredictedPerSecond != 0 || per[3].DraftN != nil {
		t.Errorf("failed round = %+v, want one stream, no rate, no draft", per[3])
	}
	if got := spread.PerStreamPredictedPerSecond; got != (tape.Spread{Median: 14, Min: 10, Max: 20}) {
		t.Errorf("rate spread = %+v, want median 14 in 10–20 with the failed round left out", got)
	}
	acc := spread.DraftAcceptRate
	if math.Abs(acc.Median-0.5) > 1e-12 || math.Abs(acc.Min-0.13) > 1e-12 || math.Abs(acc.Max-0.87) > 1e-12 {
		t.Errorf("accept spread = %+v, want median 0.5 in 0.13–0.87 with the zero-drafted round left out", acc)
	}
}

func TestSpreadOf(t *testing.T) {
	cases := []struct {
		name string
		in   []float64
		want tape.Spread
	}{
		{"none is unobserved", nil, tape.Spread{}},
		{"one value", []float64{9.1}, tape.Spread{Median: 9.1, Min: 9.1, Max: 9.1}},
		{"odd count takes the middle", []float64{3, 1, 2}, tape.Spread{Median: 2, Min: 1, Max: 3}},
		{"even count means the two middle values", []float64{4, 1, 3, 2}, tape.Spread{Median: 2.5, Min: 1, Max: 4}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := append([]float64(nil), tc.in...)
			if got := spreadOf(tc.in); got != tc.want {
				t.Errorf("spreadOf(%v) = %+v, want %+v", tc.in, got, tc.want)
			}
			for i := range in {
				if in[i] != tc.in[i] {
					t.Fatalf("spreadOf sorted its argument in place: %v", tc.in)
				}
			}
		})
	}
}

// TestRoundFaultsSplitsEachRound: every round is a prefill followed by a
// decode, so faults are split inside each round at that round's last first
// token. The single split of a one-round run, taken at the last stream's
// first token of the whole timeline, would call round 0's decode faults
// prompt faults.
func TestRoundFaultsSplitsEachRound(t *testing.T) {
	// Two rounds of two streams. Round 0: tokens 0..5, streams start at 0 and
	// 1, decode faults 3+3 on tokens 2..5 region. Round 1: tokens 6..9, first
	// tokens at 6 and 7, one prefill fault at 6.
	timeline := []uint64{9, 1, 3, 0, 3, 0, 1, 0, 0, 0}
	st := newState(4, nil, nil)
	st.perRound = 2
	st.roundStart = []int{0, 6}
	st.firstIdx = []int{0, 1, 6, 7}

	var mem tape.MemorySummary
	st.roundFaults(&mem, timeline)
	// Round 0: prompt = timeline[0:2] = 10, decode = timeline[2:6] = 6 over 4
	// tokens. Round 1: prompt = timeline[6:8] = 1, decode 0 over 2 tokens.
	if mem.MajFaultsPrompt != 11 || mem.MajFaultsDecode != 6 || mem.MajFaultsTotal != 17 {
		t.Errorf("faults = prompt %d / decode %d / total %d, want 11 / 6 / 17",
			mem.MajFaultsPrompt, mem.MajFaultsDecode, mem.MajFaultsTotal)
	}
	if math.Abs(mem.MajFaultsPerToken-1.0) > 1e-12 {
		t.Errorf("MajFaultsPerToken = %v, want 1.0 (6 decode faults / 6 decoded tokens)", mem.MajFaultsPerToken)
	}
	single := procmon.Summarize(nil, timeline, st.firstTokenIndex())
	if single.MajFaultsDecode == mem.MajFaultsDecode {
		t.Errorf("the one-split figure (%d) equals the per-round one; the fixture no longer tells them apart", single.MajFaultsDecode)
	}
}

// TestReduceRoundsPeakIsTheLowestRoundPeak (lead, 2026-09-15): a peak over
// every record of a multi-round run counts two rounds' decoding as one
// instant, and rounds are sequential. The run's PeakDecodingStreams is the
// lowest per-round peak above zero — one serial round already makes the
// aggregate a queue — and 0 when no round had a window. RoundSummary keeps no
// peak of its own (the schema holds it run-level), so the per-round reduction
// is exactly the call it always was.
func TestReduceRoundsPeakIsTheLowestRoundPeak(t *testing.T) {
	s, ms := time.Second, time.Millisecond
	// tenRec: ten tokens, first at first (absolute), 100 ms apart — enough
	// span for a window, few enough to read at a glance.
	tenRec := func(round, index int, sent, first time.Duration) tape.RequestRecord {
		rec := tape.RequestRecord{Index: index, Round: round, StartedAt: sent}
		for i := 0; i < 10; i++ {
			rec.Tokens = append(rec.Tokens, tape.TokenEvent{T: first - sent + time.Duration(i)*100*ms, Index: i})
		}
		rec.Timings = tape.TimingsSummary{PredictedN: 10}
		return rec
	}
	// Round 0: two streams decoding together 1–2 s. Round 1: two serial
	// streams, 11–12 s and 13–14 s.
	recs := []tape.RequestRecord{
		tenRec(0, 0, 0, s),
		tenRec(0, 1, 0, s),
		tenRec(1, 0, 10*s, 11*s),
		tenRec(1, 1, 10*s, 13*s),
	}
	// The precondition the override exists for: the naive aggregate over every
	// record sees round 0's overlap and answers 2.
	if got := server.Aggregate(recs).PeakDecodingStreams; got != 2 {
		t.Fatalf("the naive peak is %d, want 2 — the fixture no longer tells the rounds apart", got)
	}
	agg, per, _ := reduceRounds(recs, []string{"overlap", "serial"}, 2)
	if agg.PeakDecodingStreams != 1 {
		t.Errorf("run peak = %d, want 1: round 1 decoded one stream at a time, and rounds never share an instant", agg.PeakDecodingStreams)
	}
	// The per-round summaries are untouched: no peak field to carry, and the
	// figures they do carry are the same reductions as before.
	if len(per) != 2 || per[0].Streams != 2 || per[1].Streams != 2 ||
		per[0].PredictedN != 20 || per[1].PredictedN != 20 {
		t.Errorf("PerRound = %+v, want two two-stream rounds of 20 tokens each", per)
	}
	// And no round with a window means the run reports the schema's unknown.
	serial := []tape.RequestRecord{
		roundRec(0, 0, 0, 500*ms, 2500*ms, 40, 21),
		roundRec(1, 0, 10*s, 10500*ms, 11500*ms, 20, 11),
	}
	if agg, _, _ := reduceRounds(serial, []string{"a", "b"}, 1); agg.PeakDecodingStreams != 0 {
		t.Errorf("no round had a window, run peak = %d, want 0", agg.PeakDecodingStreams)
	}
}

// TestExampleRoundsFollowsTheReduction: the card fixture's spread and run-level
// draft totals are what this package's reduction would compute from its
// rounds, so a golden drawn from the fixture pins the recorder's arithmetic
// and not a number typed in by hand.
func TestExampleRoundsFollowsTheReduction(t *testing.T) {
	s := card.ExampleRounds()
	if got := roundSpread(s.PerRound); *got != *s.Spread {
		t.Errorf("roundSpread(PerRound) = %+v, fixture says %+v", *got, *s.Spread)
	}
	drafted, accepted := 0, 0
	for _, p := range s.PerRound {
		drafted += *p.DraftN
		accepted += *p.DraftNAccepted
	}
	if drafted != *s.Timings.DraftN || accepted != *s.Timings.DraftNAccepted {
		t.Errorf("rounds sum to %d/%d drafted/accepted, run-level Timings say %d/%d",
			drafted, accepted, *s.Timings.DraftN, *s.Timings.DraftNAccepted)
	}
	if s.Rounds != len(s.PerRound) || s.Concurrency != s.Aggregate.Streams {
		t.Errorf("Rounds %d with %d PerRound; Concurrency %d vs Aggregate.Streams %d",
			s.Rounds, len(s.PerRound), s.Concurrency, s.Aggregate.Streams)
	}
}
