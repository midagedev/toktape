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
