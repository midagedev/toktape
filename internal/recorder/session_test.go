package recorder_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/midagedev/toktape/internal/recorder"
	"github.com/midagedev/toktape/internal/server"
	"github.com/midagedev/toktape/internal/tape"
)

// chatServer is a llama-server fake for chat sessions (2026-09-24): /props
// and /slots name one slot of ctx tokens, /apply-template renders
// "<|role|>content" per message, /tokenize counts whitespace-separated
// fields, and every chat request is answered with three reasoning tokens
// followed by "Answer number N." for the N-th request, each chunk carrying
// the server's own timings. Every chat body is kept, raw, in arrival order.
//
// A request whose last user message contains "slow" is answered with sixty
// content tokens twenty milliseconds apart, and stops writing when the client
// goes away — the cancel test's turn.
type chatServer struct {
	*httptest.Server
	mu     sync.Mutex
	bodies [][]byte
}

func newChatServer(t *testing.T, ctx int) *chatServer {
	t.Helper()
	cs := &chatServer{}
	mux := http.NewServeMux()
	mux.HandleFunc("/props", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Server", "llama.cpp")
		fmt.Fprintf(w, `{"model_path":%q,"build_info":"b4321-abcdef12","chat_template":"chatml","total_slots":1,"default_generation_settings":{"n_ctx":%d}}`, modelPath, ctx)
	})
	mux.HandleFunc("/slots", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `[{"id":0,"is_processing":false,"n_ctx":%d,"n_past":0}]`, ctx)
	})
	mux.HandleFunc("/apply-template", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Messages []tape.Message `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var b strings.Builder
		for _, m := range in.Messages {
			b.WriteString("<|" + m.Role + "|>" + m.Content)
		}
		b.WriteString("<|assistant|>")
		_ = json.NewEncoder(w).Encode(map[string]string{"prompt": b.String()})
	})
	mux.HandleFunc("/tokenize", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Content string `json:"content"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		toks := make([]int, len(strings.Fields(in.Content)))
		_ = json.NewEncoder(w).Encode(map[string][]int{"tokens": toks})
	})
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		cs.mu.Lock()
		cs.bodies = append(cs.bodies, body)
		n := len(cs.bodies)
		cs.mu.Unlock()
		var in struct {
			Messages []tape.Message `json:"messages"`
		}
		_ = json.Unmarshal(body, &in)
		slow := len(in.Messages) > 0 && strings.Contains(in.Messages[len(in.Messages)-1].Content, "slow")

		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		type tok struct {
			text      string
			reasoning bool
		}
		toks := []tok{{"The user", true}, {" asks", true}, {" again.", true}}
		delay := time.Millisecond
		if slow {
			delay = 20 * time.Millisecond
			for i := range 60 {
				toks = append(toks, tok{fmt.Sprintf(" w%d", i), false})
			}
		} else {
			toks = append(toks, tok{"Answer", false}, tok{" number", false}, tok{fmt.Sprintf(" %d.", n), false})
		}
		write := func(delta map[string]any, finish any, predicted int) bool {
			chunk := map[string]any{
				"object":  "chat.completion.chunk",
				"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}},
				"timings": map[string]any{
					"prompt_n": 40, "prompt_ms": 40.0, "prompt_per_second": 1000.0,
					"predicted_n": predicted, "predicted_ms": 25.0 * float64(predicted), "predicted_per_second": 40.0,
				},
			}
			b, _ := json.Marshal(chunk)
			if _, err := fmt.Fprintf(w, "data: %s\n\n", b); err != nil {
				return false
			}
			if flusher != nil {
				flusher.Flush()
			}
			return true
		}
		for i, tk := range toks {
			select {
			case <-r.Context().Done():
				return
			case <-time.After(delay):
			}
			key := "content"
			if tk.reasoning {
				key = "reasoning_content"
			}
			if !write(map[string]any{key: tk.text}, nil, i+1) {
				return
			}
		}
		write(map[string]any{}, "stop", len(toks))
		fmt.Fprint(w, "data: [DONE]\n\n")
	})
	cs.Server = httptest.NewServer(mux)
	t.Cleanup(cs.Close)
	return cs
}

// sent is every chat body the server received, decoded.
func (cs *chatServer) sent(t *testing.T) []map[string]any {
	t.Helper()
	cs.mu.Lock()
	defer cs.mu.Unlock()
	out := make([]map[string]any, len(cs.bodies))
	for i, b := range cs.bodies {
		if err := json.Unmarshal(b, &out[i]); err != nil {
			t.Fatalf("chat body %d is not JSON: %v", i, err)
		}
	}
	return out
}

// sentMessages is the messages array of every chat body, decoded as sent.
func (cs *chatServer) sentMessages(t *testing.T) [][]tape.Message {
	t.Helper()
	cs.mu.Lock()
	defer cs.mu.Unlock()
	out := make([][]tape.Message, len(cs.bodies))
	for i, b := range cs.bodies {
		var in struct {
			Messages []tape.Message `json:"messages"`
		}
		if err := json.Unmarshal(b, &in); err != nil {
			t.Fatalf("chat body %d is not JSON: %v", i, err)
		}
		out[i] = in.Messages
	}
	return out
}

func chatOpts(t *testing.T, url string) recorder.Options {
	t.Helper()
	return recorder.Options{
		BaseURL:        url,
		SampleInterval: 5 * time.Millisecond,
		FSRoot:         absRoot(t),
		GPU:            fakeGPU(t),
		Clock:          fixedClock{time.Date(2026, 9, 24, 9, 0, 0, 0, time.UTC)},
		Version:        "0.1.0-test",
	}
}

// words is n distinct words, so no test prompt could be mistaken for a
// shortened one.
func words(tag string, n int) string {
	w := make([]string, n)
	for i := range w {
		w[i] = fmt.Sprintf("%s%d", tag, i)
	}
	return strings.Join(w, " ")
}

// TestChatHistoryGrowsTurnByTurn is the session's contract end to end: three
// turns send the conversation so far — exactly, every byte of every message
// as typed and as answered, no salt, no trim, no system line — the assistant
// turns carry the answer without its reasoning, and the tape is a chat tape
// of three rounds with no clock and no default cap.
func TestChatHistoryGrowsTurnByTurn(t *testing.T) {
	srv := newChatServer(t, 32768)
	var (
		mu     sync.Mutex
		starts []recorder.Event
		kinds  = map[recorder.EventKind]int{}
		mode   string
		tokenR = map[int]int{}
	)
	opts := chatOpts(t, srv.URL)
	opts.Progress = func(ev recorder.Event) {
		mu.Lock()
		defer mu.Unlock()
		kinds[ev.Kind]++
		switch ev.Kind {
		case recorder.EventAttached:
			mode = ev.Summary.Mode
		case recorder.EventStreamStarted:
			starts = append(starts, ev)
		case recorder.EventToken:
			tokenR[ev.Round]++
		}
	}
	s, err := recorder.Open(context.Background(), opts)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	// A long first message: a benchmark run's plan would have salted and
	// trimmed text this size, and a chat must send it whole.
	users := []string{words("alpha", 3000), "second question", "third question"}
	for i, u := range users {
		rec, err := s.Send(context.Background(), u)
		if err != nil {
			t.Fatalf("Send %d: %v", i+1, err)
		}
		if rec.Round != i {
			t.Errorf("turn %d record Round = %d", i+1, rec.Round)
		}
		if want := fmt.Sprintf("Answer number %d.", i+1); rec.Prompt.Completion != want {
			t.Errorf("turn %d completion = %q, want %q", i+1, rec.Prompt.Completion, want)
		}
		if rec.Timings.PredictedN == 0 {
			t.Errorf("turn %d record returned without reduced timings", i+1)
		}
	}

	want := [][]tape.Message{
		{{Role: "user", Content: users[0]}},
		{{Role: "user", Content: users[0]}, {Role: "assistant", Content: "Answer number 1."}, {Role: "user", Content: users[1]}},
		{{Role: "user", Content: users[0]}, {Role: "assistant", Content: "Answer number 1."}, {Role: "user", Content: users[1]},
			{Role: "assistant", Content: "Answer number 2."}, {Role: "user", Content: users[2]}},
	}
	got := srv.sentMessages(t)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("messages sent:\n%q\nwant\n%q", got, want)
	}
	hist := s.History()
	if len(hist) != 6 || hist[5].Content != "Answer number 3." {
		t.Errorf("History = %q, want three exchanges ending in the third answer", hist)
	}
	for _, m := range hist {
		if strings.Contains(m.Content, "asks again") {
			t.Errorf("reasoning text reached the history: %q", m.Content)
		}
	}
	for i, body := range srv.sent(t) {
		// Unnamed cap: the room the slot has left, never the benchmark
		// guard. 32768 minus a few thousand tokens of conversation is
		// well past DefaultMaxTokens on every turn.
		mt, _ := body["max_tokens"].(float64)
		if int(mt) <= recorder.DefaultMaxTokens || int(mt) >= 32768 {
			t.Errorf("turn %d max_tokens = %v, want the slot's room (> %d, < 32768)", i+1, body["max_tokens"], recorder.DefaultMaxTokens)
		}
	}

	tp, err := s.Close()
	if err != nil {
		t.Fatalf("Close: %v", err)
	}
	sum := tp.Summary
	if sum.Mode != tape.ModeChat || mode != tape.ModeChat {
		t.Errorf("Mode = %q (attached %q), want %q", sum.Mode, mode, tape.ModeChat)
	}
	if sum.Rounds != 3 || len(tp.Requests) != 3 || len(sum.PerRound) != 3 {
		t.Errorf("Rounds = %d, requests = %d, per-round = %d, want 3 each", sum.Rounds, len(tp.Requests), len(sum.PerRound))
	}
	if sum.Concurrency != 1 {
		t.Errorf("Concurrency = %d, want 1", sum.Concurrency)
	}
	if sum.Limit.For != 0 || sum.Limit.MinTokens != 0 || sum.Limit.MaxTokensNamed || sum.Limit.MaxTokens != 0 {
		t.Errorf("Limit = %+v, want no clock, no floor and no single cap", sum.Limit)
	}
	if sum.PromptSalt != "" || sum.PromptTrimTokens != 0 || sum.Plan != nil {
		t.Errorf("a chat was planned: salt %q, trim %d, plan %+v", sum.PromptSalt, sum.PromptTrimTokens, sum.Plan)
	}
	for i, rec := range tp.Requests {
		if rec.Round != i || rec.Error != "" {
			t.Errorf("request %d: round %d error %q", i, rec.Round, rec.Error)
		}
		if i > 0 && rec.StartedAt <= tp.Requests[i-1].StartedAt {
			t.Errorf("request %d StartedAt %v not after %v: StartedAt is since the session's first request", i, rec.StartedAt, tp.Requests[i-1].StartedAt)
		}
		if rec.Prompt.MaxTokens <= recorder.DefaultMaxTokens {
			t.Errorf("request %d recorded cap %d", i, rec.Prompt.MaxTokens)
		}
		if !strings.HasPrefix(rec.Prompt.RenderedPrompt, "<|user|>"+users[0]) {
			t.Errorf("request %d rendered prompt does not start with the first message as typed", i)
		}
	}
	if tp.Requests[2].Prompt.MaxTokens >= tp.Requests[0].Prompt.MaxTokens {
		t.Errorf("caps %d then %d: the room left must shrink as the history grows",
			tp.Requests[0].Prompt.MaxTokens, tp.Requests[2].Prompt.MaxTokens)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(starts) != 3 {
		t.Fatalf("EventStreamStarted = %d, want one per turn", len(starts))
	}
	for k, ev := range starts {
		if ev.Round != k || ev.Rounds != 0 || ev.Stream != 0 || ev.Streams != 1 || ev.MaxTokens != tp.Requests[k].Prompt.MaxTokens {
			t.Errorf("start %d = %+v, want Round %d, Rounds 0, Stream 0, Streams 1, the turn's cap", k, ev, k)
		}
	}
	for k := range 3 {
		if tokenR[k] != 6 {
			t.Errorf("turn %d: %d EventToken, want 6", k, tokenR[k])
		}
	}
	if kinds[recorder.EventAttached] != 1 || kinds[recorder.EventDone] != 1 || kinds[recorder.EventSample] == 0 {
		t.Errorf("events = %v, want one attached, one done and samples", kinds)
	}
	if _, err := s.Send(context.Background(), "after close"); !errors.Is(err, recorder.ErrSessionClosed) {
		t.Errorf("Send after Close: %v, want ErrSessionClosed", err)
	}
}

// TestChatContextFull: a slot too small for the third turn refuses it before
// anything is sent, the session stays usable, and the tape holds the two
// turns that went out.
func TestChatContextFull(t *testing.T) {
	// 600 tokens a slot. Each user message is 200 tokens by the fake's count
	// and each answer 3, so turn 1 holds ~216 of 600, turn 2 ~419, and turn 3
	// ~622 — more than the slot before any answer at all.
	srv := newChatServer(t, 600)
	s, err := recorder.Open(context.Background(), chatOpts(t, srv.URL))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	for i := range 2 {
		if _, err := s.Send(context.Background(), words(fmt.Sprintf("u%d-", i), 200)); err != nil {
			t.Fatalf("Send %d: %v", i+1, err)
		}
	}
	rec, err := s.Send(context.Background(), words("u2-", 200))
	if !errors.Is(err, recorder.ErrContextFull) {
		t.Fatalf("turn 3 err = %v, want ErrContextFull", err)
	}
	if rec.Prompt.Messages != nil || rec.Tokens != nil {
		t.Errorf("a refused turn returned a record: %+v", rec)
	}
	if n := len(srv.sent(t)); n != 2 {
		t.Errorf("server received %d chat requests, want 2: the refused turn must not be sent", n)
	}
	if h := s.History(); len(h) != 4 {
		t.Errorf("history has %d messages after a refused turn, want 4", len(h))
	}
	tp, err := s.Close()
	if err != nil {
		t.Fatalf("Close after ErrContextFull: %v", err)
	}
	if tp.Summary.Rounds != 2 || len(tp.Requests) != 2 {
		t.Errorf("tape has %d rounds, %d requests, want 2 and 2", tp.Summary.Rounds, len(tp.Requests))
	}
	// Each cap is the room its turn left: the slot less the rendered
	// conversation's count and the counted margin.
	for i, r := range tp.Requests {
		if r.Prompt.MaxTokens <= 0 || r.Prompt.MaxTokens >= 600 {
			t.Errorf("turn %d cap %d, want the room left in a 600-token slot", i+1, r.Prompt.MaxTokens)
		}
	}
}

// TestChatNamedCapMustFit: a cap the user named is every turn's cap, and a
// conversation that no longer leaves that much room is full rather than sent
// with a smaller one.
func TestChatNamedCapMustFit(t *testing.T) {
	srv := newChatServer(t, 600)
	opts := chatOpts(t, srv.URL)
	opts.MaxTokens = 250
	s, err := recorder.Open(context.Background(), opts)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	rec, err := s.Send(context.Background(), words("a", 200))
	if err != nil {
		t.Fatalf("Send 1: %v", err)
	}
	if rec.Prompt.MaxTokens != 250 {
		t.Errorf("turn 1 cap %d, want the named 250", rec.Prompt.MaxTokens)
	}
	if _, err := s.Send(context.Background(), words("b", 200)); !errors.Is(err, recorder.ErrContextFull) {
		t.Fatalf("turn 2 err = %v, want ErrContextFull (~419 + 250 > 600)", err)
	}
	tp, err := s.Close()
	if err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !tp.Summary.Limit.MaxTokensNamed || tp.Summary.Limit.MaxTokens != 250 {
		t.Errorf("Limit = %+v, want the named 250", tp.Summary.Limit)
	}
}

// TestChatIdleGapEntersNoRate: minutes a person spends reading between turns
// are in no rate and in no WallMs. The reduction must be the rounds one —
// each turn's own windows, summed — so the aggregate equals the per-turn
// windows computed from the tape's own records, and WallMs stays far under
// the pause.
func TestChatIdleGapEntersNoRate(t *testing.T) {
	const gap = 400 * time.Millisecond
	srv := newChatServer(t, 32768)
	s, err := recorder.Open(context.Background(), chatOpts(t, srv.URL))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := s.Send(context.Background(), "first"); err != nil {
		t.Fatalf("Send 1: %v", err)
	}
	time.Sleep(gap) // the person reads
	if _, err := s.Send(context.Background(), "second"); err != nil {
		t.Fatalf("Send 2: %v", err)
	}
	tp, err := s.Close()
	if err != nil {
		t.Fatalf("Close: %v", err)
	}
	agg := tp.Summary.Aggregate
	if tp.Requests[1].StartedAt < gap {
		t.Fatalf("turn 2 StartedAt %v, want past the %v pause (since the first request)", tp.Requests[1].StartedAt, gap)
	}

	// A run without the gap: each turn reduced over its own records, the
	// windows summed — what the same two turns sent back to back reduce to,
	// since no turn's window depends on when the next one was sent.
	var wallMs, decodeSec float64
	var predicted int
	for _, rec := range tp.Requests {
		a := server.Aggregate([]tape.RequestRecord{rec})
		wallMs += a.WallMs
		if a.AggregatePredictedPerSecond > 0 {
			decodeSec += float64(a.TotalPredictedN) / a.AggregatePredictedPerSecond
			predicted += a.TotalPredictedN
		}
	}
	wantRate := float64(predicted) / decodeSec
	if math.Abs(agg.WallMs-wallMs) > 1e-6 {
		t.Errorf("WallMs = %.3f, want %.3f (the turns' own windows)", agg.WallMs, wallMs)
	}
	if math.Abs(agg.AggregatePredictedPerSecond-wantRate) > 1e-6*wantRate {
		t.Errorf("AggregatePredictedPerSecond = %.3f, want %.3f (the turns' own windows)", agg.AggregatePredictedPerSecond, wantRate)
	}
	if agg.WallMs >= float64(gap.Milliseconds()) {
		t.Errorf("WallMs = %.1f ms, the %v pause is inside it", agg.WallMs, gap)
	}
}

// TestChatCancelMidTurn: ^C mid-answer ends that turn only. Its record keeps
// every token that arrived and says it was cancelled; the history keeps the
// question and the partial answer the person saw; the next turn works.
func TestChatCancelMidTurn(t *testing.T) {
	srv := newChatServer(t, 32768)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var (
		mu      sync.Mutex
		turn0   int
		cancel1 sync.Once
	)
	opts := chatOpts(t, srv.URL)
	opts.Progress = func(ev recorder.Event) {
		if ev.Kind != recorder.EventToken || ev.Round != 0 {
			return
		}
		mu.Lock()
		turn0++
		n := turn0
		mu.Unlock()
		// Three reasoning tokens, then five of the answer.
		if n == 8 {
			cancel1.Do(cancel)
		}
	}
	s, err := recorder.Open(context.Background(), opts)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	rec, err := s.Send(ctx, "a slow one please")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled turn err = %v, want context.Canceled", err)
	}
	if len(rec.Tokens) < 8 || len(rec.Tokens) >= 63 {
		t.Errorf("cancelled record kept %d tokens, want the ones that arrived (8..62)", len(rec.Tokens))
	}
	// Marked as a stream the client ended, the way the clock's cut is: no
	// Error (its tokens are a measurement) and no finish word (the server
	// never said one).
	if !rec.Prompt.Cut || rec.Error != "" || rec.Prompt.FinishReason != "" {
		t.Errorf("cancelled record: Cut %v, Error %q, FinishReason %q; want Cut, no error, no word",
			rec.Prompt.Cut, rec.Error, rec.Prompt.FinishReason)
	}
	partial := rec.Prompt.Completion
	if !strings.HasPrefix(partial, " w0 w1") {
		t.Errorf("partial answer %q", partial)
	}

	if _, err := s.Send(context.Background(), "and now a quick one"); err != nil {
		t.Fatalf("turn after a cancel: %v", err)
	}
	h := s.History()
	want := []tape.Message{
		{Role: "user", Content: "a slow one please"},
		{Role: "assistant", Content: partial},
		{Role: "user", Content: "and now a quick one"},
		{Role: "assistant", Content: "Answer number 2."},
	}
	if !reflect.DeepEqual(h, want) {
		t.Errorf("History = %q, want %q", h, want)
	}
	if msgs := srv.sentMessages(t); len(msgs) != 2 || !reflect.DeepEqual(msgs[1], want[:3]) {
		t.Errorf("turn 2 sent %q, want the partial answer in its history", msgs)
	}
	tp, err := s.Close()
	if err != nil {
		t.Fatalf("Close: %v", err)
	}
	if tp.Summary.Rounds != 2 || len(tp.Requests) != 2 {
		t.Fatalf("tape has %d rounds, %d requests, want 2 and 2 (the cancelled turn counts)", tp.Summary.Rounds, len(tp.Requests))
	}
	if got := tp.Requests[0]; !got.Prompt.Cut || got.Error != "" || len(got.Tokens) != len(rec.Tokens) {
		t.Errorf("the tape's cancelled record: cut %v, error %q, %d tokens, want cut with %d", got.Prompt.Cut, got.Error, len(got.Tokens), len(rec.Tokens))
	}
	if tp.Summary.Aggregate.StreamsFailed != 0 {
		t.Errorf("StreamsFailed = %d: a stopped turn is not a failed one", tp.Summary.Aggregate.StreamsFailed)
	}
	if tp.Requests[1].Error != "" {
		t.Errorf("turn 2 error %q", tp.Requests[1].Error)
	}
}

// TestChatRefusals: what a chat session will not take is refused before it
// attaches, each with its reason.
func TestChatRefusals(t *testing.T) {
	cases := map[string]recorder.Options{
		"concurrency": {Concurrency: 2},
		"rounds":      {Rounds: []recorder.Round{{Name: "r"}}},
		"prompts":     {Prompts: []server.StreamRequest{userPrompt("x", 0)}},
		"spec-n-max":  {SpecNMax: []int{2, 4}},
		"for":         {For: 20 * time.Second},
		"completion":  {Endpoint: tape.EndpointCompletion},
	}
	for name, o := range cases {
		t.Run(name, func(t *testing.T) {
			// A URL nothing listens on: a refusal must come before attach.
			o.BaseURL = "http://127.0.0.1:1"
			o.WaitForModel = recorder.NoWait
			s, err := recorder.Open(context.Background(), o)
			if err == nil || s != nil {
				t.Fatalf("Open = %v, %v; want a refusal", s, err)
			}
			if errors.Is(err, recorder.ErrUnreachable) {
				t.Fatalf("Open attached before refusing: %v", err)
			}
		})
	}
	// Concurrency 1 and NoClock are what a chat is, not refusals.
	srv := newChatServer(t, 32768)
	o := chatOpts(t, srv.URL)
	o.Concurrency, o.For = 1, recorder.NoClock
	s, err := recorder.Open(context.Background(), o)
	if err != nil {
		t.Fatalf("Open with Concurrency 1 and NoClock: %v", err)
	}
	if _, err := s.Close(); !errors.Is(err, recorder.ErrAllStreamsFailed) {
		t.Errorf("Close with no turn: %v, want ErrAllStreamsFailed", err)
	}
	if _, err := s.Close(); !errors.Is(err, recorder.ErrSessionClosed) {
		t.Errorf("second Close: %v, want ErrSessionClosed", err)
	}
}

// TestChatOnlyTurnCancelledStillWritesATape: one question, ^C mid-answer,
// /exit. The record is kept, so the tape is written — a stopped turn is not a
// failed one.
func TestChatOnlyTurnCancelledStillWritesATape(t *testing.T) {
	srv := newChatServer(t, 32768)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var (
		mu sync.Mutex
		n  int
	)
	opts := chatOpts(t, srv.URL)
	opts.Progress = func(ev recorder.Event) {
		if ev.Kind != recorder.EventToken {
			return
		}
		mu.Lock()
		n++
		if n == 8 {
			cancel()
		}
		mu.Unlock()
	}
	s, err := recorder.Open(context.Background(), opts)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := s.Send(ctx, "slow, then stopped"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Send: %v, want context.Canceled", err)
	}
	tp, err := s.Close()
	if err != nil {
		t.Fatalf("Close after a stopped only turn: %v", err)
	}
	if tp.Summary.Rounds != 1 || tp.Summary.Aggregate.TotalPredictedN == 0 {
		t.Errorf("Rounds %d, predicted %d: the stopped turn's tokens must be in the tape's figures",
			tp.Summary.Rounds, tp.Summary.Aggregate.TotalPredictedN)
	}
}
