package recorder_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/midagedev/toktape/internal/gpu"
	"github.com/midagedev/toktape/internal/recorder"
	"github.com/midagedev/toktape/internal/server"
	"github.com/midagedev/toktape/internal/tape"
)

// A run you can aim at a clip length (TTP-76, 2026-09-14).
//
// The thing these tests exist to stop is a cut run being handled as a failure.
// Cancelling every live stream is, one layer down, indistinguishable from every
// stream failing at once — which is the recorder's other fatal condition — so
// "the budget ran out" has to survive that layer as a run, with a record that
// says how it ended and figures that are still measurements.

// paced is a server that generates at a fixed pace and records the cap every
// request carried.
type paced struct {
	*httptest.Server
	mu   sync.Mutex
	caps []int
}

// sentCaps is the n_predict of every request the server received, in arrival
// order.
func (p *paced) sentCaps() []int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]int(nil), p.caps...)
}

// pacedServer generates one token every interval, up to the cap the request
// asked for or maxTokens, whichever is smaller.
//
// Every chunk carries the server's own timings, which is what a real request
// gets under timings_per_token (internal/server/sse.go) and the reason a stream
// cut mid-flight still has the server's figures for the tokens that did arrive.
// Those timings are measured off this handler's own clock rather than written
// down in advance: the client's window and the server's are then two
// measurements of the same real sleeps, so the tolerance check below is a
// property of the record and not of arithmetic the fixture did for both sides.
func pacedServer(t *testing.T, interval time.Duration, maxTokens int) *paced {
	t.Helper()
	p := &paced{}
	mux := http.NewServeMux()
	mux.HandleFunc("/props", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Server", "llama.cpp")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(propsJSON))
	})
	mux.HandleFunc("/slots", func(w http.ResponseWriter, r *http.Request) {
		// 32768, the hero rig's room: a tighter slot would end these runs at
		// the answer cap the slot forces (TTP-148), and the clock is the story
		// here, not the slot.
		_, _ = w.Write([]byte(`[{"id":0,"is_processing":true,"n_ctx":32768,"n_past":12}]`))
	})
	mux.HandleFunc("/apply-template", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"prompt": "<|user|>hi<|assistant|>"})
	})
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			MaxTokens int `json:"max_tokens"`
			NPredict  int `json:"n_predict"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		cap := in.MaxTokens
		if cap == 0 {
			cap = in.NPredict
		}
		p.mu.Lock()
		p.caps = append(p.caps, cap)
		p.mu.Unlock()

		n := maxTokens
		if cap > 0 && cap < n {
			n = cap
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		decodeStart := time.Now()
		for k := 0; k < n; k++ {
			select {
			case <-r.Context().Done():
				return
			case <-time.After(interval):
			}
			ms := float64(time.Since(decodeStart)) / float64(time.Millisecond)
			chunk := map[string]any{
				"choices": []any{map[string]any{
					"index": 0,
					"delta": map[string]any{"content": fmt.Sprintf(" t%d", k)},
				}},
				"timings": map[string]any{
					"prompt_n": 12, "prompt_ms": 40.0, "prompt_per_second": 300.0,
					"predicted_n": k + 1, "predicted_ms": ms,
					"predicted_per_second": float64(k+1) / (ms / 1000),
				},
			}
			b, _ := json.Marshal(chunk)
			if _, err := fmt.Fprintf(w, "data: %s\n\n", b); err != nil {
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
		// The cap was reached without the clock getting there first: the
		// server says so in its own word, which is the case Cut must not be
		// confused with.
		_, _ = fmt.Fprintf(w, "data: %s\n\n",
			`{"choices":[{"index":0,"delta":{},"finish_reason":"length"}]}`)
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	p.Server = srv
	return p
}

// clockOptions is a run with no /proc, no GPU and a fixed start stamp, so only
// the clock is under test.
func clockOptions(t *testing.T, srv *paced) recorder.Options {
	t.Helper()
	return recorder.Options{
		BaseURL:        srv.URL,
		FSRoot:         t.TempDir(),
		GPU:            gpu.Null{},
		SampleInterval: 50 * time.Millisecond,
		Clock:          fixedClock{time.Date(2026, 9, 14, 9, 0, 0, 0, time.UTC)},
		Version:        "0.1.0-test",
	}
}

// TestBudgetCutSavesAValidTape: a budget shorter than the generation ends the
// run, and what comes out is a tape and not an error.
//
// Every assertion here is one half of "a cut run must never read as a completed
// generation": Cut says the clock ended it, FinishReason stays exactly as the
// server left it — empty, because no final chunk ever arrived and a word we
// made up would be the invented default the schema forbids — and no stream is
// counted as failed.
func TestBudgetCutSavesAValidTape(t *testing.T) {
	const (
		interval = 6 * time.Millisecond
		budget   = 700 * time.Millisecond
	)
	// The floor is 64 tokens, which this server reaches in about 384ms, so the
	// budget is what decides the cut and not the floor.
	srv := pacedServer(t, interval, 100000)
	opts := clockOptions(t, srv)
	opts.Concurrency, opts.For = 2, budget

	tp, err := recorder.Record(context.Background(), opts)
	if err != nil {
		t.Fatalf("a run its own budget ended came back as a failure: %v", err)
	}
	s := tp.Summary

	if s.Limit.For != budget {
		t.Errorf("Limit.For = %v, want the budget that was asked for (%v)", s.Limit.For, budget)
	}
	if s.Limit.MaxTokens != recorder.DefaultMaxTokens {
		t.Errorf("Limit.MaxTokens = %d, want the runaway guard %d", s.Limit.MaxTokens, recorder.DefaultMaxTokens)
	}
	if s.Limit.MinTokens != tape.MinCutTokens {
		t.Errorf("Limit.MinTokens = %d, want the floor %d", s.Limit.MinTokens, tape.MinCutTokens)
	}
	if s.Limit.CutAt < budget {
		t.Errorf("Limit.CutAt = %v, want at or after the budget %v", s.Limit.CutAt, budget)
	}
	for _, c := range srv.sentCaps() {
		if c != recorder.DefaultMaxTokens {
			t.Errorf("a request carried n_predict %d, want the guard %d; a clock that failed would be a runaway",
				c, recorder.DefaultMaxTokens)
		}
	}

	if len(tp.Requests) != 2 {
		t.Fatalf("%d records, want 2", len(tp.Requests))
	}
	for i, r := range tp.Requests {
		if !r.Prompt.Cut {
			t.Errorf("stream %d: Cut is false; the clock ended it and the tape does not say so", i)
		}
		if r.Prompt.FinishReason != "" {
			t.Errorf("stream %d: FinishReason = %q, want empty; the server never sent a final chunk",
				i, r.Prompt.FinishReason)
		}
		if r.Error != "" {
			t.Errorf("stream %d: Error = %q; a cut run is not a failed one", i, r.Error)
		}
		if len(r.Tokens) < tape.MinCutTokens {
			t.Errorf("stream %d: %d tokens, want at least the floor of %d", i, len(r.Tokens), tape.MinCutTokens)
		}
		if r.Timings.DecodeLabel != "decode" {
			t.Errorf("stream %d: label %q, want decode; the floor exists so a budget cannot produce a sample",
				i, r.Timings.DecodeLabel)
		}
	}
	if s.Aggregate.StreamsFailed != 0 {
		t.Errorf("StreamsFailed = %d, want 0; the clock cut them, nothing failed", s.Aggregate.StreamsFailed)
	}
	if s.Aggregate.AggregatePredictedPerSecond <= 0 {
		t.Error("the aggregate rate is zero: a cut run lost the figures it was recorded for")
	}
}

// TestCutStreamStillAgreesWithTheServer is the test that says cancellation is
// not corrupting the record.
//
// The client rate is measured over the tokens that arrived and the server rate
// comes off the last chunk received; under timings_per_token both cover the
// same window, so they must agree within tape.RateTolerance exactly as they do
// on a stream that ran to EOS. If a future refactor drops the last chunk's
// timings, keeps a token the timings do not count, or truncates the two
// windows differently, this is what notices.
//
// Truncation costs the late-run behaviour — thermal drift, a cache that had not
// grown yet — and nothing else. It does not bias the rate, and nothing here
// claims more than that.
func TestCutStreamStillAgreesWithTheServer(t *testing.T) {
	const (
		interval = 8 * time.Millisecond
		budget   = 1200 * time.Millisecond
	)
	srv := pacedServer(t, interval, 100000)
	opts := clockOptions(t, srv)
	// One stream: the agreement is a per-stream property, and a single
	// generation keeps the scheduler out of the measurement.
	opts.Concurrency, opts.For = 1, budget

	tp, err := recorder.Record(context.Background(), opts)
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	r := tp.Requests[0]
	if !r.Prompt.Cut {
		t.Fatalf("the stream was not cut (%d tokens, finish %q); this test has nothing to measure",
			len(r.Tokens), r.Prompt.FinishReason)
	}
	tm := r.Timings
	if tm.PredictedN == 0 || tm.PredictedPerSecond == 0 {
		t.Fatalf("a cut stream lost the server's own figures: %+v", tm)
	}
	if tm.ClientPredictedPerSecond == 0 {
		t.Fatalf("a cut stream lost the client-side cross-check: %+v", tm)
	}
	off := math.Abs(tm.ClientPredictedPerSecond-tm.PredictedPerSecond) / tm.PredictedPerSecond
	if !tm.ClientAgreesWithServer {
		t.Errorf("client %.2f tok/s and server %.2f tok/s differ by %.2f%%, over tape.RateTolerance of %.2f%%; "+
			"cancelling the stream moved one window and not the other",
			tm.ClientPredictedPerSecond, tm.PredictedPerSecond, off*100, tape.RateTolerance*100)
	}
	if !tp.Summary.Timings.ClientAgreesWithServer {
		t.Error("the run-level figures disagree although the only stream's agree")
	}
}

// TestTheFloorHoldsTheCutBack: a budget under the floor does not produce a
// stream too short to have a decode rate. The run is longer than was asked for
// instead, and the tape says so by itself — CutAt past For is the whole record
// of that trade.
func TestTheFloorHoldsTheCutBack(t *testing.T) {
	const (
		interval = 8 * time.Millisecond
		budget   = time.Millisecond // the clock wants to cut at once and may not
	)
	srv := pacedServer(t, interval, 100000)
	opts := clockOptions(t, srv)
	opts.Concurrency, opts.For = 1, budget

	tp, err := recorder.Record(context.Background(), opts)
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	s := tp.Summary
	if s.Limit.CutAt <= s.Limit.For {
		t.Fatalf("CutAt %v is not past For %v; the floor did not hold the cut back and the trade is unrecorded",
			s.Limit.CutAt, s.Limit.For)
	}
	// The floor is in tokens, so the honest lower bound on the wait is the
	// floor's worth of them at this server's pace.
	if want := time.Duration(tape.MinCutTokens) * interval; s.Limit.CutAt < want {
		t.Errorf("CutAt = %v, want at least %v: %d tokens at %v each", s.Limit.CutAt, want, tape.MinCutTokens, interval)
	}
	r := tp.Requests[0]
	if !r.Prompt.Cut || len(r.Tokens) < tape.MinCutTokens {
		t.Errorf("Cut = %v with %d tokens; the floor is %d and exists so a budget cannot report a sample",
			r.Prompt.Cut, len(r.Tokens), tape.MinCutTokens)
	}
	if r.Timings.DecodeLabel != "decode" {
		t.Errorf("label %q, want decode", r.Timings.DecodeLabel)
	}
}

// TestNamedTokenCapRunsWithoutAClock: naming --n-predict is an answer on the
// "how long" axis, so the default budget does not also apply. The run ends on
// the server's own word and the tape records no cut.
//
// This is the row that keeps a command somebody already recorded a card with
// behaving as it was recorded.
func TestNamedTokenCapRunsWithoutAClock(t *testing.T) {
	const cap = 40
	srv := pacedServer(t, time.Millisecond, 100000)
	opts := clockOptions(t, srv)
	opts.MaxTokens = cap

	tp, err := recorder.Record(context.Background(), opts)
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	s := tp.Summary
	if s.Limit.For != 0 || s.Limit.MinTokens != 0 || s.Limit.CutAt != 0 {
		t.Errorf("Limit = %+v, want no clock at all: a named cap silences the default budget", s.Limit)
	}
	if s.Limit.MaxTokens != cap {
		t.Errorf("Limit.MaxTokens = %d, want the %d that was named", s.Limit.MaxTokens, cap)
	}
	r := tp.Requests[0]
	if r.Prompt.Cut {
		t.Error("the run was cut although no clock was in force")
	}
	if r.Prompt.FinishReason != "length" {
		t.Errorf("FinishReason = %q, want the server's own word", r.Prompt.FinishReason)
	}
	if got := srv.sentCaps(); len(got) != 1 || got[0] != cap {
		t.Errorf("requests carried %v, want [%d]", got, cap)
	}
}

// TestEveryStreamFailingIsStillAFailure: the clock's arrival must not turn a
// server that answers nothing into a "cut" run. Cut is a claim that tokens were
// recorded and a budget ended them; a run with no tokens has neither.
func TestEveryStreamFailingIsStillAFailure(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/props", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(propsJSON))
	})
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "no slot", http.StatusServiceUnavailable)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	_, err := recorder.Record(context.Background(), recorder.Options{
		BaseURL:     srv.URL,
		Concurrency: 2,
		For:         5 * time.Second,
		FSRoot:      t.TempDir(),
		GPU:         gpu.Null{},
	})
	if err == nil {
		t.Fatal("a run in which every stream failed came back as a tape")
	}
	if !errors.Is(err, recorder.ErrAllStreamsFailed) {
		t.Fatalf("error = %v, want ErrAllStreamsFailed", err)
	}
}

// TestBudgetSpansEveryRound: a prompts file is one run, so one budget covers
// all of its rounds rather than resetting at each.
//
// `--for 20s` over a four-round file is twenty seconds of run, not eighty. The
// clock ends the round that is live and the rounds after it are never sent,
// which is a cut of the run — so the tape carries CutAt and the run says which
// round it stopped after, and none of it is an error.
func TestBudgetSpansEveryRound(t *testing.T) {
	const (
		interval = 6 * time.Millisecond
		budget   = 600 * time.Millisecond
	)
	srv := pacedServer(t, interval, 100000)
	opts := clockOptions(t, srv)
	opts.For = budget
	opts.Concurrency = 1
	for _, name := range []string{"one", "two", "three"} {
		opts.Rounds = append(opts.Rounds, recorder.Round{
			Name:    name,
			Prompts: []server.StreamRequest{{Messages: []tape.Message{{Role: "user", Content: name}}}},
		})
	}

	tp, err := recorder.Record(context.Background(), opts)
	if err != nil {
		t.Fatalf("a multi-round run its budget ended came back as a failure: %v", err)
	}
	s := tp.Summary
	if s.Limit.CutAt < budget {
		t.Errorf("Limit.CutAt = %v, want at or after the budget %v", s.Limit.CutAt, budget)
	}
	if s.Rounds >= len(opts.Rounds) {
		t.Errorf("%d of %d rounds were sent; the budget should have stopped the run short",
			s.Rounds, len(opts.Rounds))
	}
	cut := 0
	for _, r := range tp.Requests {
		if r.Prompt.Cut {
			cut++
			if r.Prompt.FinishReason != "" || r.Error != "" {
				t.Errorf("round %d: cut record carries finish %q / error %q",
					r.Round, r.Prompt.FinishReason, r.Error)
			}
		}
	}
	if cut != 1 {
		t.Errorf("%d streams marked cut, want the one that was live when the budget ran out", cut)
	}
	if !warnsAbout(s.Warnings, "budget ran out") {
		t.Errorf("nothing in the warnings says where the run stopped: %q", s.Warnings)
	}
	if warnsAbout(s.Warnings, "cancelled") {
		t.Errorf("the budget was reported as a cancellation: %q", s.Warnings)
	}
}
