package server

import (
	"bytes"
	"context"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/midagedev/toktape/internal/tape"
)

// synthStream builds one recorded stream: n tokens spaced gap apart, starting
// firstTok after the request was sent, with the server timings that generation
// implies.
func synthStream(index int, startedAt, firstTok, gap time.Duration, n int) tape.RequestRecord {
	rec := tape.RequestRecord{Index: index, Slot: index, StartedAt: startedAt}
	for i := 0; i < n; i++ {
		rec.Tokens = append(rec.Tokens, tape.TokenEvent{
			T:     firstTok + time.Duration(i)*gap,
			Index: i,
			Text:  "x",
		})
	}
	rec.Timings = tape.TimingsSummary{
		PromptN:            40,
		PromptMs:           100,
		PromptPerSecond:    400,
		PredictedN:         n,
		PredictedMs:        float64(time.Duration(n) * gap / time.Millisecond),
		PredictedPerSecond: float64(time.Second) / float64(gap),
	}
	rec.Timings = Reduce(&rec, time.Time{}, 0)
	return rec
}

// TestAggregateFourStreamsOneFailed is the concurrent run the product is built
// around: several agent sessions hitting one server at once. The failed stream
// counts in Streams and StreamsFailed and contributes to no rate.
func TestAggregateFourStreamsOneFailed(t *testing.T) {
	recs := []tape.RequestRecord{
		synthStream(0, 0, 200*time.Millisecond, 25*time.Millisecond, 100),
		synthStream(1, 2*time.Millisecond, 260*time.Millisecond, 25*time.Millisecond, 80),
		synthStream(2, 5*time.Millisecond, 310*time.Millisecond, 25*time.Millisecond, 120),
		{Index: 3, Slot: -1, StartedAt: 7 * time.Millisecond, Error: "server: stream error: context shift is disabled"},
	}
	agg := Aggregate(recs)

	if got, want := agg.Streams, 4; got != want {
		t.Errorf("Streams = %d, want %d", got, want)
	}
	if got, want := agg.StreamsFailed, 1; got != want {
		t.Errorf("StreamsFailed = %d, want %d", got, want)
	}
	if got, want := agg.TotalPredictedN, 300; got != want {
		t.Errorf("TotalPredictedN = %d, want %d (the failed stream adds nothing)", got, want)
	}
	if got, want := agg.TotalPromptN, 120; got != want {
		t.Errorf("TotalPromptN = %d, want %d", got, want)
	}

	// The decode window runs from the earliest first token (stream 0 at 200)
	// to the latest last token (stream 2 started 5 ms in, first token 310,
	// 119 gaps of 25 ms => 5 + 310 + 2975 = 3290 ms).
	wantWall := 3290.0
	if math.Abs(agg.WallMs-wantWall) > 0.001 {
		t.Errorf("WallMs = %v, want %v", agg.WallMs, wantWall)
	}
	wantRate := 300.0 / ((3290.0 - 200.0) / 1000.0)
	if math.Abs(agg.AggregatePredictedPerSecond-wantRate) > 0.01 {
		t.Errorf("AggregatePredictedPerSecond = %v, want %v", agg.AggregatePredictedPerSecond, wantRate)
	}
	// Three streams at 40 tok/s each, overlapping, must aggregate to more than
	// any one of them and to no more than their sum.
	if agg.AggregatePredictedPerSecond <= agg.PerStreamPredictedPerSecond {
		t.Errorf("aggregate %v is not above the per-stream mean %v", agg.AggregatePredictedPerSecond, agg.PerStreamPredictedPerSecond)
	}
	if agg.AggregatePredictedPerSecond > 3*agg.PerStreamPredictedPerSecond {
		t.Errorf("aggregate %v exceeds 3 streams x %v", agg.AggregatePredictedPerSecond, agg.PerStreamPredictedPerSecond)
	}
	if math.Abs(agg.PerStreamPredictedPerSecond-40) > 0.001 {
		t.Errorf("PerStreamPredictedPerSecond = %v, want 40", agg.PerStreamPredictedPerSecond)
	}

	// TTFT percentiles over the three streams that answered: 200, 260, 310.
	if got, want := agg.TTFTp50Ms, 260.0; got != want {
		t.Errorf("TTFTp50Ms = %v, want %v", got, want)
	}
	if got, want := agg.TTFTp95Ms, 310.0; got != want {
		t.Errorf("TTFTp95Ms = %v, want %v", got, want)
	}
	// Prefill of the run ends when the last stream produced its first token.
	wantPrompt := 120.0 / ((5.0 + 310.0) / 1000.0)
	if math.Abs(agg.AggregatePromptPerSecond-wantPrompt) > 0.01 {
		t.Errorf("AggregatePromptPerSecond = %v, want %v", agg.AggregatePromptPerSecond, wantPrompt)
	}
	// Neither is derivable from the records alone, so neither may be invented.
	if agg.SlotsBusyMax != 0 || agg.Scaling != 0 {
		t.Errorf("SlotsBusyMax=%d Scaling=%v, want both 0 (not observable here)", agg.SlotsBusyMax, agg.Scaling)
	}
}

// TestAggregateConcurrentWindow is the TTP-138 gate: the window in which
// every answered stream was decoding, and what was produced inside it.
//
// Four streams, all at a 25 ms gap (40 tok/s each), ending at deliberately
// different times — 2675, 2237, 3290 and 2056 ms absolute:
//
//	stream 0  first  200, last 2675   (100 tokens)
//	stream 1  first  262, last 2237   ( 80 tokens)
//	stream 2  first  315, last 3290   (120 tokens)
//	stream 3  first  281, last 2056   ( 72 tokens)
//
// The window is [latest first token 315, earliest last token 2056] = 1741 ms,
// both ends inclusive — stream 2's first token and stream 3's last token sit
// exactly on the boundaries. Tokens inside per stream: 70, 69, 70, 70 = 279.
func TestAggregateConcurrentWindow(t *testing.T) {
	recs := []tape.RequestRecord{
		synthStream(0, 0, 200*time.Millisecond, 25*time.Millisecond, 100),
		synthStream(1, 2*time.Millisecond, 260*time.Millisecond, 25*time.Millisecond, 80),
		synthStream(2, 5*time.Millisecond, 310*time.Millisecond, 25*time.Millisecond, 120),
		synthStream(3, 1*time.Millisecond, 280*time.Millisecond, 25*time.Millisecond, 72),
	}
	agg := Aggregate(recs)

	// The whole-wall figure stays exactly what it was: 372 tokens over the
	// window from the earliest first token (200) to the latest last (3290).
	// This clause is the addition-not-redefinition guard.
	wantAggregate := 372.0 / ((3290.0 - 200.0) / 1000.0)
	if math.Abs(agg.AggregatePredictedPerSecond-wantAggregate) > 0.01 {
		t.Errorf("AggregatePredictedPerSecond = %v, want the unchanged whole-wall %v", agg.AggregatePredictedPerSecond, wantAggregate)
	}
	if got, want := agg.TotalPredictedN, 372; got != want {
		t.Errorf("TotalPredictedN = %d, want %d", got, want)
	}

	if got, want := agg.ConcurrentWindowMs, 1741.0; math.Abs(got-want) > 0.001 {
		t.Errorf("ConcurrentWindowMs = %v, want %v (latest first 315 .. earliest last 2056)", got, want)
	}
	if got, want := agg.ConcurrentPredictedN, 279; got != want {
		t.Errorf("ConcurrentPredictedN = %d, want %d (70+69+70+70 inside the window, both ends inclusive)", got, want)
	}
	if got, want := agg.ConcurrentPredictedPerSecond, 279.0/1.741; math.Abs(got-want) > 0.01 {
		t.Errorf("ConcurrentPredictedPerSecond = %v, want %v", got, want)
	}

	// The reconciliation the field exists for: over the same window, the
	// concurrent rate is the sum of the streams' own rates in that window —
	// four equal-gap streams, so ≈ 4 x any one of them — while the whole-wall
	// figure cannot reconcile with its own per-stream mean (the ragged tail
	// is the survivors, not four streams).
	var sumPerStream float64
	for i := range recs {
		n := 0
		for _, tk := range recs[i].Tokens {
			at := recs[i].StartedAt + tk.T
			if at >= 315*time.Millisecond && at <= 2056*time.Millisecond {
				n++
			}
		}
		sumPerStream += float64(n) / 1.741
	}
	if math.Abs(agg.ConcurrentPredictedPerSecond-sumPerStream) > 1e-6 {
		t.Errorf("ConcurrentPredictedPerSecond = %v, want the sum of per-stream window rates %v", agg.ConcurrentPredictedPerSecond, sumPerStream)
	}
	// The one-token boundary effect: counts inside the window differ by a
	// token between equal-gap streams (70, 69, 70, 70), so 4 x any single
	// stream's rate is only good to a couple of percent.
	if fourX := 4 * (70.0 / 1.741); math.Abs(sumPerStream-fourX) > 0.02*fourX {
		t.Errorf("sum of per-stream window rates = %v, want ≈ 4 x one 25 ms-gap stream's rate in the window (%v)", sumPerStream, fourX)
	}
	if agg.ConcurrentPredictedPerSecond <= agg.AggregatePredictedPerSecond {
		t.Errorf("concurrent %v is not above the whole-wall %v: the ragged tail must dilute the whole-wall figure, not the all-live one",
			agg.ConcurrentPredictedPerSecond, agg.AggregatePredictedPerSecond)
	}

	// The per-stream figure and its count (lead, 2026-09-19). A surface that
	// prints the window aggregate beside PerStreamPredictedPerSecond puts two
	// spans in one row: the whole-wall mean is a stream's rate including the
	// tail where it had more of the machine to itself. These two are the pair
	// that reconciles, and the count is the divisor made visible.
	if got, want := agg.ConcurrentStreams, 4; got != want {
		t.Errorf("ConcurrentStreams = %d, want %d: the answered streams the window was taken over", got, want)
	}
	if got, want := agg.ConcurrentPerStreamPredictedPerSecond, agg.ConcurrentPredictedPerSecond/4; math.Abs(got-want) > 1e-9 {
		t.Errorf("ConcurrentPerStreamPredictedPerSecond = %v, want %v", got, want)
	}
	if got := agg.ConcurrentPerStreamPredictedPerSecond * float64(agg.ConcurrentStreams); math.Abs(got-agg.ConcurrentPredictedPerSecond) > 1e-9 {
		t.Errorf("%d x %v = %v, want the aggregate %v: the pair must reconcile by construction",
			agg.ConcurrentStreams, agg.ConcurrentPerStreamPredictedPerSecond, got, agg.ConcurrentPredictedPerSecond)
	}
	// Not asserted here: that the window per-stream figure sits below the
	// whole-wall one. That is true of a real box — a stream that outlives the
	// others gets more of the machine, so its own mean rises — and it is why
	// the two must not be printed as one figure, but these streams are
	// synthetic and decode at a fixed 25 ms gap whatever else is running, so
	// the fixture cannot show it (measured, lead 2026-09-19: 40.06 window
	// against 40.00 whole-wall, the boundary token, not contention). An
	// assertion here would pin the fixture's physics, not the arithmetic's.
}

// TestConcurrentPerStreamDividesByAnsweredStreams: the divisor is the streams
// the window was taken over, not every record (lead, 2026-09-19). Streams is
// len(recs) and includes the ones that failed and the ones that answered with
// nothing, so dividing by it prints a per-stream figure that is silently low
// and a product that does not hold — which is the whole reason the count is
// stored beside the rate rather than left to each surface to guess.
func TestConcurrentPerStreamDividesByAnsweredStreams(t *testing.T) {
	recs := []tape.RequestRecord{
		synthStream(0, 0, 200*time.Millisecond, 25*time.Millisecond, 100),
		synthStream(1, 2*time.Millisecond, 260*time.Millisecond, 25*time.Millisecond, 80),
		synthStream(2, 5*time.Millisecond, 310*time.Millisecond, 25*time.Millisecond, 120),
	}
	failed := synthStream(3, 1*time.Millisecond, 280*time.Millisecond, 25*time.Millisecond, 72)
	failed.Error = "connection reset"
	silent := synthStream(4, 3*time.Millisecond, 0, 0, 0)
	silent.Tokens = nil
	recs = append(recs, failed, silent)

	agg := Aggregate(recs)
	if got, want := agg.Streams, 5; got != want {
		t.Fatalf("Streams = %d, want %d: the fixture must carry both a failed and a silent stream", got, want)
	}
	if got, want := agg.ConcurrentStreams, 3; got != want {
		t.Errorf("ConcurrentStreams = %d, want %d: neither the failed nor the silent stream is in the window", got, want)
	}
	if got := agg.ConcurrentPerStreamPredictedPerSecond * float64(agg.ConcurrentStreams); math.Abs(got-agg.ConcurrentPredictedPerSecond) > 1e-9 {
		t.Errorf("%d x %v = %v, want the aggregate %v", agg.ConcurrentStreams, agg.ConcurrentPerStreamPredictedPerSecond, got, agg.ConcurrentPredictedPerSecond)
	}
	if byStreams := agg.ConcurrentPredictedPerSecond / float64(agg.Streams); math.Abs(agg.ConcurrentPerStreamPredictedPerSecond-byStreams) < 1e-9 {
		t.Errorf("ConcurrentPerStreamPredictedPerSecond = %v, which is the aggregate over all %d records: the divisor must be the answered streams", agg.ConcurrentPerStreamPredictedPerSecond, agg.Streams)
	}
}

// TestAggregateConcurrentWindowZeroes: the concurrent fields are 0 — never an
// invented figure — on a one-stream run (the window is the run and the
// existing field already says it), and when the answered streams never shared
// an instant (the window is not positive).
func TestAggregateConcurrentWindowZeroes(t *testing.T) {
	t.Run("one stream gets no window figure", func(t *testing.T) {
		agg := Aggregate([]tape.RequestRecord{synthStream(0, 0, 200*time.Millisecond, 25*time.Millisecond, 100)})
		if agg.ConcurrentWindowMs != 0 || agg.ConcurrentPredictedN != 0 || agg.ConcurrentPredictedPerSecond != 0 {
			t.Errorf("concurrent figures on one stream = %v ms / %d / %v, want all 0: the window is the run",
				agg.ConcurrentWindowMs, agg.ConcurrentPredictedN, agg.ConcurrentPredictedPerSecond)
		}
		if math.Abs(agg.PerStreamPredictedPerSecond-40) > 0.001 {
			t.Errorf("PerStreamPredictedPerSecond = %v, want 40: the run's own rate must stay", agg.PerStreamPredictedPerSecond)
		}
	})
	t.Run("streams that never overlapped have no window", func(t *testing.T) {
		// Stream 0 ends at 445 ms; stream 1's first token is at 600 ms.
		agg := Aggregate([]tape.RequestRecord{
			synthStream(0, 0, 200*time.Millisecond, 25*time.Millisecond, 10),
			synthStream(1, 0, 600*time.Millisecond, 25*time.Millisecond, 10),
		})
		if agg.ConcurrentWindowMs != 0 || agg.ConcurrentPredictedN != 0 || agg.ConcurrentPredictedPerSecond != 0 {
			t.Errorf("concurrent figures without overlap = %v ms / %d / %v, want all 0",
				agg.ConcurrentWindowMs, agg.ConcurrentPredictedN, agg.ConcurrentPredictedPerSecond)
		}
	})
}

func TestAggregateEdgeCases(t *testing.T) {
	t.Run("no records", func(t *testing.T) {
		if got := Aggregate(nil); got.Streams != 0 || got.AggregatePredictedPerSecond != 0 {
			t.Errorf("got %+v, want zero", got)
		}
	})
	t.Run("every stream failed", func(t *testing.T) {
		agg := Aggregate([]tape.RequestRecord{{Error: "boom"}, {Error: "boom"}})
		if agg.Streams != 2 || agg.StreamsFailed != 2 {
			t.Errorf("Streams/Failed = %d/%d, want 2/2", agg.Streams, agg.StreamsFailed)
		}
		if agg.AggregatePredictedPerSecond != 0 || agg.WallMs != 0 {
			t.Errorf("got rate %v wall %v, want both 0", agg.AggregatePredictedPerSecond, agg.WallMs)
		}
	})
	t.Run("single stream", func(t *testing.T) {
		agg := Aggregate([]tape.RequestRecord{synthStream(0, 0, 200*time.Millisecond, 25*time.Millisecond, 100)})
		if agg.Streams != 1 || agg.StreamsFailed != 0 {
			t.Errorf("Streams/Failed = %d/%d, want 1/0", agg.Streams, agg.StreamsFailed)
		}
		if math.Abs(agg.PerStreamPredictedPerSecond-40) > 0.001 {
			t.Errorf("PerStreamPredictedPerSecond = %v, want 40", agg.PerStreamPredictedPerSecond)
		}
	})
	t.Run("build without server timings falls back to tokens seen", func(t *testing.T) {
		rec := synthStream(0, 0, 100*time.Millisecond, 20*time.Millisecond, 50)
		rec.Timings = tape.TimingsSummary{}
		rec.Timings = Reduce(&rec, time.Time{}, 0)
		agg := Aggregate([]tape.RequestRecord{rec})
		if got, want := agg.TotalPredictedN, 50; got != want {
			t.Errorf("TotalPredictedN = %d, want %d", got, want)
		}
	})
}

// TestRunConcurrent drives four real streams through one httptest server and
// checks the bookkeeping Aggregate depends on: index, a start time on the
// run's clock, and a failure confined to its own record.
func TestRunConcurrent(t *testing.T) {
	srv := replayServer(t, "stream_basic.sse", time.Millisecond, nil)
	c := New(srv.URL)

	var mu sync.Mutex
	counts := make([]int, 4)
	recs, err := RunConcurrent(context.Background(), c, DefaultPrompts(4), func(i int) StreamHooks {
		return StreamHooks{OnToken: func(tape.TokenEvent) {
			mu.Lock()
			counts[i]++
			mu.Unlock()
		}}
	})
	if err != nil {
		t.Fatalf("RunConcurrent: %v", err)
	}
	if got, want := len(recs), 4; got != want {
		t.Fatalf("records = %d, want %d", got, want)
	}
	for i, rec := range recs {
		if rec.Index != i {
			t.Errorf("record %d has Index %d", i, rec.Index)
		}
		if rec.Error != "" {
			t.Errorf("record %d failed: %s", i, rec.Error)
		}
		if len(rec.Tokens) != 44 {
			t.Errorf("record %d has %d tokens, want 44", i, len(rec.Tokens))
		}
		if counts[i] != 44 {
			t.Errorf("stream %d fired OnToken %d times, want 44", i, counts[i])
		}
		if rec.StartedAt < 0 {
			t.Errorf("record %d StartedAt = %v", i, rec.StartedAt)
		}
		if len(rec.Prompt.Messages) != 1 {
			t.Errorf("record %d lost its prompt", i)
		}
	}
	agg := Aggregate(recs)
	if agg.Streams != 4 || agg.StreamsFailed != 0 {
		t.Errorf("Aggregate: Streams/Failed = %d/%d, want 4/0", agg.Streams, agg.StreamsFailed)
	}
	if agg.TotalPredictedN != 4*44 {
		t.Errorf("TotalPredictedN = %d, want %d", agg.TotalPredictedN, 4*44)
	}
}

// TestRunConcurrentPartialFailure: one stream failing must not take the run
// with it. The run still returns three streams of evidence, the failure is
// confined to its own record, and RunConcurrent itself reports success.
func TestRunConcurrentPartialFailure(t *testing.T) {
	good, err := os.ReadFile(filepath.Join("testdata", "stream_basic.sse"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	bad, err := os.ReadFile(filepath.Join("testdata", "stream_error.sse"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	// The request whose prompt names stream 2 gets the broken stream, so which
	// one fails does not depend on goroutine scheduling.
	reqs := DefaultPrompts(4)
	marker := "STREAM-2-FAILS"
	reqs[2].Messages[0].Content = marker + " " + reqs[2].Messages[0].Content

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := readAllLimited(r)
		payload := good
		if bytes.Contains(body, []byte(marker)) {
			payload = bad
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write(payload)
	}))
	t.Cleanup(srv.Close)

	recs, err := RunConcurrent(context.Background(), New(srv.URL), reqs, nil)
	if err != nil {
		t.Fatalf("RunConcurrent failed the whole run for one broken stream: %v", err)
	}
	for i, rec := range recs {
		wantErr := i == 2
		if gotErr := rec.Error != ""; gotErr != wantErr {
			t.Errorf("record %d: error=%q, want error=%v", i, rec.Error, wantErr)
		}
		wantTokens := 44
		if wantErr {
			wantTokens = 2
		}
		if len(rec.Tokens) != wantTokens {
			t.Errorf("record %d has %d tokens, want %d", i, len(rec.Tokens), wantTokens)
		}
	}
	agg := Aggregate(recs)
	if agg.Streams != 4 || agg.StreamsFailed != 1 {
		t.Errorf("Streams/Failed = %d/%d, want 4/1", agg.Streams, agg.StreamsFailed)
	}
	if got, want := agg.TotalPredictedN, 3*44; got != want {
		t.Errorf("TotalPredictedN = %d, want %d (the failed stream contributes nothing)", got, want)
	}
}

func TestRunConcurrentAllFail(t *testing.T) {
	srv := replayServer(t, "stream_error.sse", 0, nil)
	recs, err := RunConcurrent(context.Background(), New(srv.URL), DefaultPrompts(3), nil)
	if err == nil {
		t.Fatal("RunConcurrent returned no error when every stream failed")
	}
	if got, want := len(recs), 3; got != want {
		t.Fatalf("records = %d, want %d", got, want)
	}
	for i, rec := range recs {
		if rec.Error == "" {
			t.Errorf("record %d has no Error", i)
		}
		if len(rec.Tokens) != 2 {
			t.Errorf("record %d kept %d tokens, want the 2 that arrived", i, len(rec.Tokens))
		}
	}
	if got := Aggregate(recs); got.StreamsFailed != 3 {
		t.Errorf("StreamsFailed = %d, want 3", got.StreamsFailed)
	}
}

func TestRunConcurrentRejectsEmpty(t *testing.T) {
	if _, err := RunConcurrent(context.Background(), New("http://127.0.0.1:1"), nil, nil); err == nil {
		t.Error("RunConcurrent accepted an empty request list")
	}
	if _, err := RunConcurrent(context.Background(), nil, DefaultPrompts(1), nil); err == nil {
		t.Error("RunConcurrent accepted a nil client")
	}
}
