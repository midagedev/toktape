package recorder_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/midagedev/toktape/internal/card"
	"github.com/midagedev/toktape/internal/gpu"
	"github.com/midagedev/toktape/internal/recorder"
	"github.com/midagedev/toktape/internal/server"
	"github.com/midagedev/toktape/internal/tape"
)

// The raw /completion path, end to end (TTP-55, 2026-09-14).

// rawServer answers /props and /completion, and logs the bodies. It serves no
// /apply-template and no /v1/chat/completions at all: a run on the raw path
// that touched either would fail here, which is the assertion.
func rawServer(t *testing.T) (*httptest.Server, *bodyLog) {
	t.Helper()
	sse, err := os.ReadFile("../server/testdata/completion_basic.sse")
	if err != nil {
		t.Fatalf("read completion fixture: %v", err)
	}
	log := &bodyLog{}
	mux := http.NewServeMux()
	mux.HandleFunc("/props", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Server", "llama.cpp")
		_, _ = w.Write([]byte(propsJSON))
	})
	mux.HandleFunc("/completion", func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		if err := json.Unmarshal(raw, &body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		log.mu.Lock()
		log.bodies = append(log.bodies, body)
		log.mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		for _, ev := range bytes.SplitAfter(sse, []byte("\n\n")) {
			if len(bytes.TrimSpace(ev)) == 0 {
				continue
			}
			if _, err := w.Write(ev); err != nil {
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, log
}

// TestRecordOnTheRawPath is the whole flag end to end: the run posts to
// /completion, never asks for a template it has no use for, and writes a tape
// whose figures are the ones the fixture carries.
func TestRecordOnTheRawPath(t *testing.T) {
	srv, log := rawServer(t)
	tp, err := recorder.Record(context.Background(), recorder.Options{
		BaseURL:     srv.URL,
		Prompts:     []server.StreamRequest{{Messages: []tape.Message{{Role: "user", Content: "Explain mmap."}}}},
		Concurrency: 1,
		MaxTokens:   320,
		Endpoint:    tape.EndpointCompletion,
		Params:      map[string]any{"temperature": 0.0},
		FSRoot:      t.TempDir(),
		GPU:         gpu.Null{},
	})
	if err != nil {
		t.Fatalf("Record: %v", err)
	}

	bodies := log.all()
	if len(bodies) != 1 {
		t.Fatalf("got %d requests, want 1", len(bodies))
	}
	if bodies[0]["prompt"] != "Explain mmap." {
		t.Fatalf("server saw prompt %v", bodies[0]["prompt"])
	}
	if bodies[0]["n_predict"] != float64(320) {
		t.Fatalf("n_predict %v, want 320", bodies[0]["n_predict"])
	}
	if bodies[0]["temperature"] != float64(0) {
		t.Fatalf("temperature %v, want 0", bodies[0]["temperature"])
	}

	if len(tp.Requests) != 1 {
		t.Fatalf("got %d records, want 1", len(tp.Requests))
	}
	rec := tp.Requests[0]
	if rec.Prompt.Endpoint != tape.EndpointCompletion {
		t.Fatalf("recorded endpoint %q", rec.Prompt.Endpoint)
	}
	if len(rec.Prompt.Messages) != 0 {
		t.Fatalf("recorded messages on the raw path: %+v", rec.Prompt.Messages)
	}
	if rec.Prompt.RenderedPrompt != "Explain mmap." {
		t.Fatalf("rendered prompt %q, want the prompt as sent", rec.Prompt.RenderedPrompt)
	}
	if n := len(rec.Tokens); n != 44 {
		t.Fatalf("recorded %d tokens, want the fixture's 44", n)
	}
	if got := tp.Summary.Timings.PredictedPerSecond; got < 45.4 || got > 45.5 {
		t.Fatalf("decode rate %.4f, want the fixture's 45.4545", got)
	}
	// /apply-template was never called: the mux has no handler for it, so a
	// call would have come back 404 and left this warning behind.
	for _, w := range tp.Summary.Warnings {
		if strings.HasPrefix(w, "/apply-template") {
			t.Fatalf("the raw path asked for a template: %q", w)
		}
	}

	// A tape from this path must render. The record carries no Messages at
	// all, which no tape had before TTP-55, so this is the cheap standing
	// check that a renderer never depended on them: the card is built from
	// the summary, every line comes out the full width, and the rate the
	// fixture carries is on it.
	text := card.Text(&tp.Summary)
	if text == "" {
		t.Fatal("a /completion tape rendered an empty card")
	}
	for i, line := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
		if w := card.Width(line); w != card.CardWidth {
			t.Fatalf("card line %d is %d columns, want %d: %q", i, w, card.CardWidth, line)
		}
	}
	if !strings.Contains(text, "45.5") {
		t.Fatalf("the card does not show the fixture's decode rate:\n%s", text)
	}
}
