package recorder_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/midagedev/toktape/internal/gpu"
	"github.com/midagedev/toktape/internal/recorder"
	"github.com/midagedev/toktape/internal/server"
	"github.com/midagedev/toktape/internal/tape"
)

// roundsServer is fakeServer with one difference: a chat request whose body
// contains "boom" is answered with the recorded error stream, so a test can
// fail one round and not the others.
func roundsServer(t *testing.T) *httptest.Server {
	t.Helper()
	good, err := os.ReadFile(sseFile)
	if err != nil {
		t.Fatalf("read SSE fixture: %v", err)
	}
	bad, err := os.ReadFile("../server/testdata/stream_error.sse")
	if err != nil {
		t.Fatalf("read error fixture: %v", err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/props", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Server", "llama.cpp")
		_, _ = w.Write([]byte(propsJSON))
	})
	mux.HandleFunc("/apply-template", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"prompt": "<|user|>hi<|assistant|>"})
	})
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		if bytes.Contains(body, []byte("boom")) {
			_, _ = w.Write(bad)
			return
		}
		flusher, _ := w.(http.Flusher)
		for _, ev := range bytes.SplitAfter(good, []byte("\n\n")) {
			if len(bytes.TrimSpace(ev)) == 0 {
				continue
			}
			if _, err := w.Write(ev); err != nil {
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
			time.Sleep(time.Millisecond)
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func userPrompt(content string, maxTokens int) server.StreamRequest {
	return server.StreamRequest{
		Messages:  []tape.Message{{Role: "user", Content: content}},
		MaxTokens: maxTokens,
	}
}

// TestRecordRounds (TTP-31) drives a two-round run of two streams each through
// the whole recorder and checks what the reduction and the renderers rely on:
// run order, per-round indices, run-relative start times, one sample series,
// the rounds' own names and caps, and round-scoped progress events.
func TestRecordRounds(t *testing.T) {
	srv := roundsServer(t)
	const streams = 2

	var (
		mu     sync.Mutex
		starts []recorder.Event
		tokens = map[[2]int]int{}
	)
	tp, err := recorder.Record(context.Background(), recorder.Options{
		BaseURL:     srv.URL,
		Concurrency: streams,
		MaxTokens:   256,
		Rounds: []recorder.Round{
			{Name: "sql-1", Prompts: []server.StreamRequest{userPrompt("alpha", 64)}},
			{Name: "", Prompts: []server.StreamRequest{userPrompt("beta", 0)}},
		},
		SampleInterval: 5 * time.Millisecond,
		FSRoot:         absRoot(t),
		GPU:            fakeGPU(t),
		Clock:          fixedClock{time.Date(2026, 9, 13, 7, 15, 0, 0, time.UTC)},
		Progress: func(ev recorder.Event) {
			mu.Lock()
			defer mu.Unlock()
			switch ev.Kind {
			case recorder.EventStreamStarted:
				starts = append(starts, ev)
			case recorder.EventToken:
				tokens[[2]int{ev.Round, ev.Stream}]++
			}
		},
	})
	if err != nil {
		t.Fatalf("Record: %v", err)
	}

	reqs := tp.Requests
	if len(reqs) != 4 {
		t.Fatalf("requests = %d, want 2 rounds × 2 streams", len(reqs))
	}
	want := []struct {
		round, index, maxTokens int
		name, content           string
	}{
		{0, 0, 64, "sql-1", "alpha"},
		{0, 1, 64, "sql-1", "alpha"},
		{1, 0, 256, "", "beta"},
		{1, 1, 256, "", "beta"},
	}
	for i, w := range want {
		r := reqs[i]
		if r.Round != w.round || r.Index != w.index {
			t.Errorf("request %d is round %d index %d, want round %d index %d", i, r.Round, r.Index, w.round, w.index)
		}
		if r.Prompt.Name != w.name {
			t.Errorf("request %d name = %q, want %q", i, r.Prompt.Name, w.name)
		}
		// The line's own cap beats --n-predict; a line without one gets it.
		if r.Prompt.MaxTokens != w.maxTokens {
			t.Errorf("request %d MaxTokens = %d, want %d", i, r.Prompt.MaxTokens, w.maxTokens)
		}
		if got := r.Prompt.Messages[0].Content; got != w.content {
			t.Errorf("request %d prompt = %q, want %q", i, got, w.content)
		}
		if r.Error != "" || len(r.Tokens) == 0 || r.Prompt.RenderedPrompt == "" {
			t.Errorf("request %d: error %q, %d tokens, rendered %q", i, r.Error, len(r.Tokens), r.Prompt.RenderedPrompt)
		}
	}

	// StartedAt is since the run start: round 1 is sent after round 0's last
	// token arrived, not at 0 as its own RunConcurrent call would stamp it.
	var round0End time.Duration
	for _, r := range reqs[:2] {
		if end := r.StartedAt + r.Tokens[len(r.Tokens)-1].T; end > round0End {
			round0End = end
		}
	}
	for _, r := range reqs[2:] {
		if r.StartedAt < round0End {
			t.Errorf("round 1 stream %d StartedAt %v is before round 0 ended at %v", r.Index, r.StartedAt, round0End)
		}
	}

	s := tp.Summary
	if s.Concurrency != streams || s.Aggregate.Streams != streams {
		t.Errorf("Concurrency / Aggregate.Streams = %d / %d, want %d: streams at once, not streams × rounds",
			s.Concurrency, s.Aggregate.Streams, streams)
	}
	if s.Rounds != 2 || len(s.PerRound) != 2 || s.Spread == nil {
		t.Fatalf("Rounds %d, PerRound %d, Spread %v; want 2, 2, non-nil", s.Rounds, len(s.PerRound), s.Spread)
	}
	if s.PerRound[0].Name != "sql-1" || s.PerRound[1].Name != "" || s.PerRound[1].Index != 1 {
		t.Errorf("PerRound = %+v", s.PerRound)
	}
	var wall float64
	for k := 0; k < 2; k++ {
		wall += server.Aggregate(reqs[2*k : 2*k+2]).WallMs
	}
	if math.Abs(s.Aggregate.WallMs-wall) > 1e-6 {
		t.Errorf("Aggregate.WallMs = %v, want %v, the sum of the two rounds' windows", s.Aggregate.WallMs, wall)
	}
	if s.Aggregate.TotalPredictedN == 0 || s.Timings.PredictedN == 0 {
		t.Errorf("empty reduction: aggregate %+v, timings %+v", s.Aggregate, s.Timings)
	}

	// One sample series across the whole run.
	if len(tp.Samples) == 0 {
		t.Fatal("no host samples")
	}
	for i := 1; i < len(tp.Samples); i++ {
		if tp.Samples[i].T < tp.Samples[i-1].T {
			t.Fatalf("sample %d T %v runs backwards from %v: not one series", i, tp.Samples[i].T, tp.Samples[i-1].T)
		}
	}

	mu.Lock()
	defer mu.Unlock()
	if len(starts) != 4 {
		t.Fatalf("%d stream-start events, want 4", len(starts))
	}
	for i, ev := range starts {
		if ev.Round != i/2 || ev.Stream != i%2 || ev.Streams != streams {
			t.Errorf("start event %d = round %d stream %d of %d, want round %d stream %d of %d",
				i, ev.Round, ev.Stream, ev.Streams, i/2, i%2, streams)
		}
	}
	for _, key := range [][2]int{{0, 0}, {0, 1}, {1, 0}, {1, 1}} {
		if tokens[key] == 0 {
			t.Errorf("no token event for round %d stream %d", key[0], key[1])
		}
	}
	if len(tokens) != 4 {
		t.Errorf("token events carry round/stream pairs %v, want exactly the four sent", tokens)
	}
}

// TestRecordRoundThatFailedIsKept: a round whose streams all failed is
// recorded with its errors and the run goes on to the next round.
func TestRecordRoundThatFailedIsKept(t *testing.T) {
	srv := roundsServer(t)
	tp, err := recorder.Record(context.Background(), recorder.Options{
		BaseURL: srv.URL,
		Rounds: []recorder.Round{
			{Name: "broken", Prompts: []server.StreamRequest{userPrompt("boom", 0)}},
			{Name: "fine", Prompts: []server.StreamRequest{userPrompt("alpha", 0)}},
		},
		SampleInterval: 20 * time.Millisecond,
		FSRoot:         t.TempDir(),
		GPU:            gpu.Null{},
	})
	if err != nil {
		t.Fatalf("Record: %v (one failed round must not fail the run)", err)
	}
	if len(tp.Requests) != 2 {
		t.Fatalf("requests = %d, want one per round (Concurrency defaults to the round's one prompt)", len(tp.Requests))
	}
	if tp.Requests[0].Error == "" || tp.Requests[0].Prompt.Name != "broken" {
		t.Errorf("round 0 = %+v, want the failure recorded under its name", tp.Requests[0])
	}
	if tp.Requests[1].Error != "" || len(tp.Requests[1].Tokens) == 0 {
		t.Errorf("round 1 error %q with %d tokens, want a normal stream", tp.Requests[1].Error, len(tp.Requests[1].Tokens))
	}
	s := tp.Summary
	if s.Rounds != 2 || s.Aggregate.StreamsFailed != 1 || s.Concurrency != 1 {
		t.Errorf("Rounds %d, StreamsFailed %d, Concurrency %d; want 2, 1, 1", s.Rounds, s.Aggregate.StreamsFailed, s.Concurrency)
	}
	if s.Spread == nil || s.Spread.PerStreamPredictedPerSecond.Min <= 0 {
		t.Errorf("Spread = %+v, want the failed round left out of the rate range", s.Spread)
	}
}

// TestRecordEveryRoundFailed is the only multi-round failure the CLI maps to
// its all-streams-failed exit code.
func TestRecordEveryRoundFailed(t *testing.T) {
	srv := roundsServer(t)
	_, err := recorder.Record(context.Background(), recorder.Options{
		BaseURL: srv.URL,
		Rounds: []recorder.Round{
			{Prompts: []server.StreamRequest{userPrompt("boom one", 0)}},
			{Prompts: []server.StreamRequest{userPrompt("boom two", 0)}},
		},
		FSRoot: t.TempDir(),
		GPU:    gpu.Null{},
	})
	if !errors.Is(err, recorder.ErrAllStreamsFailed) {
		t.Fatalf("error = %v, want ErrAllStreamsFailed", err)
	}
	if !strings.Contains(err.Error(), "every round failed") {
		t.Errorf("error %q does not say every round failed", err)
	}
}

// TestRecordWithoutRoundsHasNoRoundFields: a plain run is not a one-round
// multi-prompt run; the new fields stay empty so its tape is unchanged.
func TestRecordWithoutRoundsHasNoRoundFields(t *testing.T) {
	srv := fakeServer(t)
	tp, err := recorder.Record(context.Background(), recorder.Options{
		BaseURL:        srv.URL,
		Concurrency:    2,
		SampleInterval: 20 * time.Millisecond,
		FSRoot:         t.TempDir(),
		GPU:            gpu.Null{},
	})
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	s := tp.Summary
	if s.Rounds != 0 || s.PerRound != nil || s.Spread != nil {
		t.Errorf("a plain run carries rounds: %d / %v / %v", s.Rounds, s.PerRound, s.Spread)
	}
	for _, r := range tp.Requests {
		if r.Round != 0 || r.Prompt.Name != "" {
			t.Errorf("stream %d has round %d name %q", r.Index, r.Round, r.Prompt.Name)
		}
	}
}
