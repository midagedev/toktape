package recorder_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/midagedev/toktape/internal/gpu"
	"github.com/midagedev/toktape/internal/recorder"
	"github.com/midagedev/toktape/internal/server"
	"github.com/midagedev/toktape/internal/tape"
)

// TTP-148 (2026-09-20): the run reads the slot's context before it sends, the
// way it already reads the slot count, and the answer cap it asks for is one
// the slot can actually hold. The day's evidence: four runs refused by the
// server with context_length_exceeded — an offload box where trim's own
// economics kept the whole 7246-token prompt, and a -np 4 server whose slots
// each held -c/4 = 8192 — all four with a default n-predict of 24000 on the
// request. The server's refusal names none of that usefully; the recorder
// held every number it needed before the first byte went out.
//
// FAIL-first: before the cap, the request below carried the default 24000
// into an 8192 slot and the second one was refused by the server's generic
// 500 — when the fake server here had enforced ctx, both cases died exactly
// as the real ones did.

// slotMux layers an n_ctx of its own over the shared fake: /slots is answered
// here, everything else falls through to the base mux. The chat handler is
// wrapped to capture each request body's max_tokens.
func slotMux(t *testing.T, nctx int, caps *[]int) *http.ServeMux {
	t.Helper()
	inner := fakeMux(t)
	mux := http.NewServeMux()
	mux.HandleFunc("/slots", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"id": 0, "is_processing": false, "n_ctx": nctx, "n_past": 0},
		})
	})
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			MaxTokens *int `json:"max_tokens"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		if in.MaxTokens != nil {
			*caps = append(*caps, *in.MaxTokens)
		}
		inner.ServeHTTP(w, r)
	})
	mux.Handle("/", inner)
	return mux
}

func slotOpts(t *testing.T, srv *httptest.Server, prompt string) recorder.Options {
	return recorder.Options{
		BaseURL: srv.URL,
		FSRoot:  t.TempDir(),
		GPU:     gpu.Null{},
		Clock:   fixedClock{time.Date(2026, 9, 20, 19, 0, 0, 0, time.UTC)},
		Prompts: []server.StreamRequest{{Messages: []tape.Message{{Role: "user", Content: prompt}}}},
	}
}

// A 18000-byte prompt prices to 7500 tokens at the 2.4 bytes/token fallback
// (2026-09-20, run plan: the 3.5 the cap used to price at was measured off
// the set's floor that day at 2.43, and one price now has one owner,
// plan.go); an 8192 slot less the 192-token template then holds 500 answer
// tokens. The run that asked for the default 8000 must send a cap in that
// room, say so in its warnings, and record the capped budget as the run's
// own.
func TestTheAnswerCapFitsTheSlotTheRequestWillLandIn(t *testing.T) {
	caps := []int{}
	srv := httptest.NewServer(slotMux(t, 8192, &caps))
	t.Cleanup(srv.Close)

	tp, err := recorder.Record(context.Background(), slotOpts(t, srv, strings.Repeat("a ", 9000)))
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	if len(caps) == 0 {
		t.Fatal("no chat request was captured")
	}
	for _, got := range caps {
		if got > 500 {
			t.Errorf("max_tokens sent = %d, want a cap inside the 8192 slot (prompt 7500 priced tokens plus the 192 template)", got)
		}
		if got < 64 {
			t.Errorf("max_tokens sent = %d, want at least the run floor 64", got)
		}
	}
	if tp.Summary.Limit.MaxTokens > 500 {
		t.Errorf("Summary.Limit.MaxTokens = %d, want the capped budget", tp.Summary.Limit.MaxTokens)
	}
	warned := false
	for _, w := range tp.Summary.Warnings {
		if strings.Contains(w, "capped") && strings.Contains(w, "8192") {
			warned = true
		}
	}
	if !warned {
		t.Errorf("no warning names the cap and the slot: %v", tp.Summary.Warnings)
	}
}

// A prompt the slot cannot hold at all is refused here, with both numbers and
// the lever that moves them, before the first request goes out. The figure
// is priced (this server never answers /tokenize) and the message says so —
// 2026-09-20, run plan: counted and priced refusals must be legible as
// which they are, because a priced refusal is a guess the reader can
// recompute and a counted one is the server's own arithmetic.
func TestAPromptTooBigForItsSlotIsRefusedWithBothNumbers(t *testing.T) {
	caps := []int{}
	srv := httptest.NewServer(slotMux(t, 8192, &caps))
	t.Cleanup(srv.Close)

	_, err := recorder.Record(context.Background(), slotOpts(t, srv, strings.Repeat("b ", 20000)))
	if err == nil {
		t.Fatal("Record succeeded; the prompt cannot fit an 8192-token slot")
	}
	// 40000 bytes price to 16667 tokens at 2.4 bytes/token: both that
	// figure and the slot's own 8192 belong in the message, with the bytes
	// behind the estimate and the word "priced" beside it.
	if !strings.Contains(err.Error(), "8192") || !strings.Contains(err.Error(), "16667") || !strings.Contains(err.Error(), "40000") || !strings.Contains(err.Error(), "priced") {
		t.Errorf("the refusal must name the slot's context and the priced prompt: %v", err)
	}
	if len(caps) != 0 {
		t.Errorf("%d requests went out before the refusal; it belongs before the first", len(caps))
	}
}

// TestNoSlotsServerStillPlansAgainstItsPropsContext is the --no-slots gate
// (TTP-165's llama half, matrix gap 3 / row 5, 2026-09-21): a llama-server
// started with --no-slots answers 501 on /slots, smallestSlotCtx used to
// read nothing there, and the run sent the whole ~7.4k-token prompt with the
// 8000-token guard behind it into a context of 8192 — every stream lost,
// exit 3. /props carries the per-slot n_ctx on mainline (verified against a
// live -np 4 server that day: /props 4096 and every /slots entry 4096), and
// the fallback reads exactly that figure, undivided.
//
// FAIL-first (2026-09-21, pre-change source): Record failed with
// recorder: all streams failed: server: stream error:
// context_length_exceeded: the request exceeds the available context size.
func TestNoSlotsServerStillPlansAgainstItsPropsContext(t *testing.T) {
	mux := http.NewServeMux()
	// The mainline /props spelling: n_ctx is the per-slot context (the live
	// :8080 measurement above), so a --no-slots server of 4 slots x 8192
	// reports 8192 here, not 32768.
	mux.HandleFunc("/props", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Server", "llama.cpp")
		_, _ = w.Write([]byte(`{
		  "model_path": "` + modelPath + `",
		  "build_info": "b4321-abcdef12",
		  "chat_template": "chatml",
		  "total_slots": 4,
		  "default_generation_settings": {"n_ctx": 8192}
		}`))
	})
	// --no-slots: the endpoint exists and refuses.
	mux.HandleFunc("/slots", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "slots endpoint is disabled", http.StatusNotImplemented)
	})
	mux.HandleFunc("/tokenize", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Content string `json:"content"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"tokens": make([]int, firstTryTokens(in.Content)),
		})
	})
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Messages  []tape.Message `json:"messages"`
			MaxTokens *int           `json:"max_tokens"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var b strings.Builder
		for _, m := range in.Messages {
			b.WriteString(m.Content)
		}
		cap := 0
		if in.MaxTokens != nil {
			cap = *in.MaxTokens
		}
		if firstTryTokens(b.String())+firstTryTemplate+cap > firstTryNCtx {
			writeCtxRefused(w)
			return
		}
		writeFirstTryChat(w, firstTryTokens(b.String()), 25.0)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	tp, err := recorder.Record(context.Background(), recorder.Options{
		BaseURL:        srv.URL,
		Concurrency:    1,
		For:            3500 * time.Millisecond,
		FSRoot:         t.TempDir(),
		GPU:            gpu.Null{},
		SampleInterval: 50 * time.Millisecond,
		Clock:          fixedClock{time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC)},
	})
	if err != nil {
		t.Fatalf("Record against a --no-slots server: %v", err)
	}
	s := tp.Summary
	if s.Aggregate.StreamsFailed != 0 {
		t.Errorf("%d of %d streams failed; the /props context was there to plan against",
			s.Aggregate.StreamsFailed, s.Aggregate.Streams)
	}
	if len(tp.Requests) != 1 || tp.Requests[0].Error != "" {
		t.Fatalf("the stream did not finish clean: %+v", tp.Requests)
	}
	// The context the run planned against is the one /props carried, and
	// the prompt was trimmed into it rather than refused by the server.
	if s.Server.CtxSize != firstTryNCtx {
		t.Errorf("Server.CtxSize = %d, want the /props n_ctx %d", s.Server.CtxSize, firstTryNCtx)
	}
	if s.Plan == nil || s.Plan.SlotCtx != firstTryNCtx {
		t.Errorf("Plan.SlotCtx = %+v, want %d from /props", s.Plan, firstTryNCtx)
	}
	for i, rec := range tp.Requests {
		if got := rec.Timings.PromptN; got > 6976 || float64(got) < 0.97*6976 {
			t.Errorf("stream %d: prompt_n %d, want within [0.97 x 6976, 6976] (8192 less template and answer room)", i, got)
		}
	}
}
