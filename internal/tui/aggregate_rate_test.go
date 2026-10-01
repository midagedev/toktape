package tui

import (
	"math"
	"sort"
	"testing"
	"time"

	"github.com/midagedev/toktape/internal/server"
	"github.com/midagedev/toktape/internal/tape"
)

// 2026-10-02: the live aggregate headline of a multi-stream run is the
// record's window (server.Aggregate: the earliest first token to the newest
// token) over every stream's decode intervals (its tokens after its first),
// not the sum of each stream's own rate. The sum kept a finished stream adding its rate until the run ended, so
// the headline dropped at completion (74 live, 55.5 recorded in the ragged
// case below).

// liveHeadlineAt is the headline as the screen shows it mid-run at the last
// token: the run is made not-Done so the server's record cannot win, and t is
// past the ease so the figure has settled on the step.
func liveHeadlineAt(t *testing.T, tp *tape.Tape, at time.Duration) float64 {
	t.Helper()
	m := ModelAt(tp, at)
	m.Done = false
	rate, ok := headlineRate(m, at+time.Second)
	if !ok {
		t.Fatalf("no headline at %v", at)
	}
	return rate
}

func lastTokenAt(tp *tape.Tape) time.Duration {
	var last time.Duration
	for _, r := range tp.Requests {
		if n := len(r.Tokens); n > 0 && r.StartedAt+r.Tokens[n-1].T > last {
			last = r.StartedAt + r.Tokens[n-1].T
		}
	}
	return last
}

func steadyStream(index int, n int, itl time.Duration) tape.RequestRecord {
	r := tape.RequestRecord{Index: index, Slot: index}
	for k := 0; k < n; k++ {
		r.Tokens = append(r.Tokens, tape.TokenEvent{T: time.Duration(k+1) * itl, Index: k, Text: "x"})
	}
	return r
}

// TestLiveAggregateRaggedStreams blocks the sum-of-rates definition: two
// streams at ~37 tok/s, one ending at 10 s and one at 20 s. The sum says 74
// until the end; the record is (370+740)/20 = 55.5.
func TestLiveAggregateRaggedStreams(t *testing.T) {
	itl := time.Second / 37
	tp := &tape.Tape{Schema: tape.SchemaVersion, Summary: tape.RunSummary{Concurrency: 2},
		Requests: []tape.RequestRecord{steadyStream(0, 370, itl), steadyStream(1, 740, itl)}}
	want := server.Aggregate(tp.Requests).AggregatePredictedPerSecond
	got := liveHeadlineAt(t, tp, lastTokenAt(tp))
	if math.Abs(got-want)/want > 0.01 {
		t.Errorf("live aggregate %.2f, record formula %.2f: want within 1%%", got, want)
	}
}

// TestLiveAggregateMatchesRecordOnRealTapes blocks drift between the live
// figure and the recorded one at the end of a real run: the per-stream sum was
// 0.75% and 0.6% off on these two.
func TestLiveAggregateMatchesRecordOnRealTapes(t *testing.T) {
	for _, path := range []string{
		"testdata/qwen36-35b-a3b-q6k-4stream-ik-sweep-0.2.4.tape",
		"../../assets/hero.tape",
	} {
		tp, err := tape.Read(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		want := tp.Summary.Aggregate.AggregatePredictedPerSecond
		if want <= 0 {
			t.Fatalf("%s: no recorded aggregate", path)
		}
		got := liveHeadlineAt(t, tp, lastTokenAt(tp))
		if d := math.Abs(got-want) / want; d > 0.005 {
			t.Errorf("%s: live %.2f vs record %.2f (%.2f%% off), want within 0.5%%", path, got, want, d*100)
		}
	}
}

// TestSingleStreamRateIsUnchanged blocks the aggregate definition leaking into
// one-stream runs: a stream's rate stays (n-1) intervals over first-to-newest
// token, the cumulative mean the llama.cpp web UI shows.
func TestSingleStreamRateIsUnchanged(t *testing.T) {
	tp := &tape.Tape{Schema: tape.SchemaVersion, Summary: tape.RunSummary{Concurrency: 1},
		Requests: []tape.RequestRecord{{Index: 0, Tokens: []tape.TokenEvent{
			{T: 5 * time.Second, Text: "a"},
			{T: 5100 * time.Millisecond, Text: "b"},
			{T: 5250 * time.Millisecond, Text: "c"},
		}}}}
	m := ModelAt(tp, 6*time.Second)
	_, cur, _ := m.decodeRateAt(6 * time.Second)
	if want := 2 / 0.25; math.Abs(cur-want) > 1e-9 {
		t.Errorf("single-stream rate = %.6f, want %.6f ((n-1)/window)", cur, want)
	}
}

// TestPerStreamRateIsNotTheAggregate blocks the per-stream figure being
// derived from the aggregate (agg/n): it stays the mean of each stream's own
// cumulative rate, which for the ragged case is ~37 and not 55.5/2.
func TestPerStreamRateIsNotTheAggregate(t *testing.T) {
	itl := time.Second / 37
	tp := &tape.Tape{Schema: tape.SchemaVersion, Summary: tape.RunSummary{Concurrency: 2},
		Requests: []tape.RequestRecord{steadyStream(0, 370, itl), steadyStream(1, 740, itl)}}
	m := ModelAt(tp, lastTokenAt(tp))
	m.Done = false
	_, per := decodeRates(m, lastTokenAt(tp)+time.Second)
	if math.Abs(per-37) > 0.5 {
		t.Errorf("per-stream rate = %.2f, want ~37", per)
	}
}

// TestLiveAggregateExcludesRoundGap blocks the idle gap between sequential
// rounds entering the denominator: a rounds run shows one round at a time, and
// inside round 1 the figure is that round's tokens over that round's window,
// as recorder.reduceRounds sums per-round windows.
func TestLiveAggregateExcludesRoundGap(t *testing.T) {
	tp := roundsTape()
	at := inRound1(tp)
	m := ModelAt(tp, at)
	m.Done = false
	var (
		n           int
		first, last = time.Duration(-1), time.Duration(0)
	)
	for _, r := range tp.Requests {
		if r.Round != 1 || len(r.Tokens) == 0 {
			continue
		}
		for i, tk := range r.Tokens {
			abs := r.StartedAt + tk.T
			if abs > at {
				break
			}
			if i == 0 {
				if first < 0 || abs < first {
					first = abs
				}
			} else {
				n++ // decode intervals: a stream's first token is not one
			}
			if abs > last {
				last = abs
			}
		}
	}
	if n < 1 {
		t.Fatal("fixture has no round-1 tokens at the instant")
	}
	want := float64(n) / (last - first).Seconds()
	got, ok := headlineRate(m, at+time.Second)
	if !ok || math.Abs(got-want)/want > 0.001 {
		t.Errorf("round-1 live aggregate %.2f, want %.2f (round-1 decode intervals over round-1 window)", got, want)
	}
}

// TestLiveAggregateHasNoStartSpike blocks a start spike: when two streams'
// first tokens land milliseconds apart, counting first tokens as output over
// an almost empty window put the headline in the thousands for the first few
// hundred ms of a multi-stream run (3465 tok/s on a 145.8 tok/s run). Every
// token arrival in the first 5% of tokens must stay within 1.5x the record.
func TestLiveAggregateHasNoStartSpike(t *testing.T) {
	for _, path := range []string{
		"testdata/qwen36-35b-a3b-q6k-4stream-ik-sweep-0.2.4.tape",
		"../../assets/hero.tape",
	} {
		tp, err := tape.Read(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		want := tp.Summary.Aggregate.AggregatePredictedPerSecond
		var times []time.Duration
		for _, r := range tp.Requests {
			for _, tk := range r.Tokens {
				times = append(times, r.StartedAt+tk.T)
			}
		}
		sort.Slice(times, func(i, j int) bool { return times[i] < times[j] })
		peak := 0.0
		for _, at := range times[:len(times)/20+1] {
			m := ModelAt(tp, at)
			m.Done = false
			if _, cur, _ := m.decodeRateAt(at); cur > peak {
				peak = cur
			}
		}
		if peak > 1.5*want {
			t.Errorf("%s: live peak %.1f tok/s in the first 5%% of tokens, record %.1f (limit 1.5x)", path, peak, want)
		}
	}
}
