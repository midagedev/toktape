package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/midagedev/toktape/internal/tape"
)

// replayServer serves a recorded SSE fixture, flushing after every event so
// the client sees a stream rather than one buffered response. delay is slept
// between events.
func replayServer(t *testing.T, fixture string, delay time.Duration, seen *[]byte) *httptest.Server {
	t.Helper()
	sse, err := os.ReadFile(filepath.Join("testdata", fixture))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != ChatPath {
			http.NotFound(w, r)
			return
		}
		body, _ := readAllLimited(r)
		if seen != nil {
			mu.Lock()
			*seen = body
			mu.Unlock()
		}
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
			if delay > 0 {
				time.Sleep(delay)
			}
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func readAllLimited(r *http.Request) ([]byte, error) {
	defer r.Body.Close()
	buf := new(bytes.Buffer)
	_, err := buf.ReadFrom(r.Body)
	return buf.Bytes(), err
}

func TestStreamAgainstServer(t *testing.T) {
	var sent []byte
	srv := replayServer(t, "stream_basic.sse", 0, &sent)

	var (
		tokens   []tape.TokenEvent
		progress []tape.PromptProgress
	)
	c := New(srv.URL)
	rec, tim, err := c.Stream(context.Background(), StreamRequest{
		Messages:            []tape.Message{{Role: "user", Content: "explain mmap"}},
		MaxTokens:           320,
		Params:              map[string]any{"temperature": 0.2, "reasoning_effort": "none"},
		ActiveBytesPerToken: 2 << 30,
		RenderedPrompt:      "<|user|>explain mmap<|assistant|>",
	}, StreamHooks{
		OnToken:    func(ev tape.TokenEvent) { tokens = append(tokens, ev) },
		OnProgress: func(ev tape.PromptProgress) { progress = append(progress, ev) },
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}

	if got, want := len(rec.Tokens), 44; got != want {
		t.Errorf("tokens = %d, want %d", got, want)
	}
	if len(tokens) != len(rec.Tokens) {
		t.Errorf("OnToken fired %d times, want %d", len(tokens), len(rec.Tokens))
	}
	if got, want := len(progress), 4; got != want {
		t.Errorf("OnProgress fired %d times, want %d (return_progress chunks)", got, want)
	}
	if got, want := rec.Prompt.FinishReason, "stop"; got != want {
		t.Errorf("FinishReason = %q, want %q", got, want)
	}
	if got, want := tim.PredictedN, 44; got != want {
		t.Errorf("server predicted_n = %d, want %d", got, want)
	}
	if got, want := rec.Timings.PredictedPerSecond, 45.45454545454545; got != want {
		t.Errorf("PredictedPerSecond = %v, want %v", got, want)
	}
	if rec.Timings.DecodeLabel != "decode" {
		t.Errorf("DecodeLabel = %q, want decode", rec.Timings.DecodeLabel)
	}
	if rec.Timings.EffectiveBandwidthBytesPerSec == 0 {
		t.Error("EffectiveBandwidthBytesPerSec = 0 with ActiveBytesPerToken set")
	}
	if got, want := rec.Prompt.RenderedPrompt, "<|user|>explain mmap<|assistant|>"; got != want {
		t.Errorf("RenderedPrompt = %q, want %q", got, want)
	}
	if len(rec.Prompt.Messages) != 1 || rec.Prompt.Messages[0].Content != "explain mmap" {
		t.Errorf("Messages = %+v, want the request recorded verbatim", rec.Prompt.Messages)
	}
	// Params record what went over the wire, not what the caller typed, so a
	// cap the caller set through a field is still on the card (lesson 4).
	if got, want := rec.Prompt.Params["max_tokens"], 320; got != want {
		t.Errorf("Params[max_tokens] = %v, want %v", got, want)
	}
	if got, want := rec.Prompt.Params["reasoning_effort"], "none"; got != want {
		t.Errorf("Params[reasoning_effort] = %v, want %v", got, want)
	}
	if _, ok := rec.Prompt.Params["messages"]; ok {
		t.Error("Params duplicated the messages, which have their own field")
	}
	// TTFT comes from the wall clock here, so only its ordering is asserted.
	if rec.Timings.TTFTMs <= 0 {
		t.Errorf("TTFTMs = %v, want a positive measurement", rec.Timings.TTFTMs)
	}

	// The request must ask for everything the tape needs to record.
	var body map[string]any
	if err := json.Unmarshal(sent, &body); err != nil {
		t.Fatalf("decode sent body: %v", err)
	}
	for _, k := range []string{"timings_per_token", "return_progress", "stream"} {
		if body[k] != true {
			t.Errorf("request %q = %v, want true", k, body[k])
		}
	}
	so, ok := body["stream_options"].(map[string]any)
	if !ok || so["include_usage"] != true {
		t.Errorf("stream_options = %v, want include_usage true", body["stream_options"])
	}
	if body["max_tokens"] != float64(320) {
		t.Errorf("max_tokens = %v, want 320", body["max_tokens"])
	}
	if body["reasoning_effort"] != "none" {
		t.Errorf("reasoning_effort = %v, want none (Params merged into the body)", body["reasoning_effort"])
	}
}

// TestStreamIgnoresControlTimeout is the regression test for the classic
// streaming bug: http.Client.Timeout and the control-call deadline both bound
// a whole request, so applying either to Stream truncates any generation
// longer than it. WithTimeout must reach Props and Slots and nothing else.
func TestStreamIgnoresControlTimeout(t *testing.T) {
	srv := replayServer(t, "stream_reasoning.sse", 20*time.Millisecond, nil)
	c := New(srv.URL, WithTimeout(30*time.Millisecond))
	rec, _, err := c.Stream(context.Background(), StreamRequest{
		Messages: []tape.Message{{Role: "user", Content: "hi"}},
	}, StreamHooks{})
	if err != nil {
		t.Fatalf("Stream died on the control timeout: %v", err)
	}
	if got, want := len(rec.Tokens), 4; got != want {
		t.Errorf("tokens = %d, want %d", got, want)
	}
}

func TestStreamHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"message":"model not loaded"}}`, http.StatusServiceUnavailable)
	}))
	t.Cleanup(srv.Close)
	rec, _, err := New(srv.URL).Stream(context.Background(), StreamRequest{}, StreamHooks{})
	if err == nil {
		t.Fatal("Stream returned no error for 503")
	}
	if rec != nil {
		t.Errorf("rec = %+v, want nil when the request never became a stream", rec)
	}
	if !strings.Contains(err.Error(), "model not loaded") {
		t.Errorf("err = %v, want the server's message", err)
	}
}

func TestStreamErrorFrameKeepsPartialRecord(t *testing.T) {
	srv := replayServer(t, "stream_error.sse", 0, nil)
	rec, _, err := New(srv.URL).Stream(context.Background(), StreamRequest{}, StreamHooks{})
	if err == nil {
		t.Fatal("Stream returned no error for an error frame")
	}
	if rec == nil {
		t.Fatal("Stream discarded the partial record")
	}
	if got, want := len(rec.Tokens), 2; got != want {
		t.Errorf("tokens = %d, want %d", got, want)
	}
	if !strings.Contains(rec.Error, "context shift is disabled") {
		t.Errorf("rec.Error = %q", rec.Error)
	}
}

func TestStreamContextCancel(t *testing.T) {
	srv := replayServer(t, "stream_basic.sse", 20*time.Millisecond, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()
	rec, _, err := New(srv.URL).Stream(ctx, StreamRequest{}, StreamHooks{})
	if err == nil {
		t.Fatal("Stream ignored the cancelled context")
	}
	if rec == nil {
		t.Fatal("a cancelled stream discarded its partial record")
	}
	// The cause must survive: reporting only "no finish chunk" would hide
	// that the run was cancelled rather than mis-served.
	if !strings.Contains(rec.Error, "context") {
		t.Errorf("rec.Error = %q, want the read failure that caused it", rec.Error)
	}
}
