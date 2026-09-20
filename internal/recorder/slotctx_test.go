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
