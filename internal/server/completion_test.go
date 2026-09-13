package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/midagedev/toktape/internal/tape"
)

// replayFixturePair replays the same generation through both endpoints.
func replayFixturePair(t *testing.T) (chat, raw *tape.RequestRecord) {
	t.Helper()
	load := func(name string) ([]byte, []time.Duration) {
		sse, err := os.ReadFile(filepath.Join("testdata", name+".sse"))
		if err != nil {
			t.Fatalf("read %s.sse: %v", name, err)
		}
		raw, err := os.ReadFile(filepath.Join("testdata", name+".arrivals"))
		if err != nil {
			t.Fatalf("read %s.arrivals: %v", name, err)
		}
		arrivals, err := ParseArrivals(raw)
		if err != nil {
			t.Fatalf("ParseArrivals(%s): %v", name, err)
		}
		return sse, arrivals
	}

	sse, arrivals := load("stream_basic")
	chat, _, err := ReplayStream(sse, arrivals, StreamHooks{})
	if err != nil {
		t.Fatalf("ReplayStream(stream_basic): %v", err)
	}
	sse, arrivals = load("completion_basic")
	raw, _, err = ReplayCompletionStream(sse, arrivals, StreamHooks{})
	if err != nil {
		t.Fatalf("ReplayCompletionStream(completion_basic): %v", err)
	}
	return chat, raw
}

// TestCompletionStreamReducesLikeChat is the rule TTP-55 turns on: the raw
// path must not be a second reducer.
//
// The two fixtures are one generation told twice (testdata/completion_basic
// .gen.py builds the second from the first), so every figure a card ever
// prints has to come out the same. If this ever fails, a number on a
// /completion card is not comparable with the same number on a chat card, and
// the whole flag was worse than not having it.
func TestCompletionStreamReducesLikeChat(t *testing.T) {
	chat, raw := replayFixturePair(t)

	if !reflect.DeepEqual(chat.Tokens, raw.Tokens) {
		t.Fatalf("token events differ: chat %d tokens, completion %d", len(chat.Tokens), len(raw.Tokens))
	}
	if !reflect.DeepEqual(chat.Timings, raw.Timings) {
		t.Fatalf("timings differ:\nchat       %+v\ncompletion %+v", chat.Timings, raw.Timings)
	}
	if !reflect.DeepEqual(chat.Progress, raw.Progress) {
		t.Fatalf("prefill progress differs:\nchat       %+v\ncompletion %+v", chat.Progress, raw.Progress)
	}
	if !reflect.DeepEqual(chat.Cache, raw.Cache) {
		t.Fatalf("cache verdict differs:\nchat       %+v\ncompletion %+v", chat.Cache, raw.Cache)
	}
	if chat.Prompt.Completion != raw.Prompt.Completion {
		t.Fatalf("answer text differs:\nchat       %q\ncompletion %q", chat.Prompt.Completion, raw.Prompt.Completion)
	}
	if chat.Error != "" || raw.Error != "" {
		t.Fatalf("a clean stream recorded an error: chat %q, completion %q", chat.Error, raw.Error)
	}

	// The one field that is deliberately NOT equal. llama-server's two
	// endpoints have two vocabularies for why generation stopped, and the
	// server's own word is the record: "eos" is not renamed to "stop" here,
	// because nothing downstream branches on it and renaming it would put a
	// word in the server's mouth.
	if chat.Prompt.FinishReason != "stop" || raw.Prompt.FinishReason != "eos" {
		t.Fatalf("finish reasons: chat %q (want stop), completion %q (want eos)",
			chat.Prompt.FinishReason, raw.Prompt.FinishReason)
	}
}

// TestCompletionStopEndsTheStream pins the end marker. The chat path closes
// with [DONE]; this one closes with "stop": true and nothing else, so a
// recorder that waited for [DONE] would call a complete generation truncated.
func TestCompletionStopEndsTheStream(t *testing.T) {
	_, raw := replayFixturePair(t)
	if raw.Error != "" {
		t.Fatalf("a stream that ended on stop:true was recorded as %q", raw.Error)
	}
	if n := len(raw.Tokens); n != 44 {
		t.Fatalf("got %d tokens, want 44", n)
	}
}

// TestCompletionChunkCarriesDraftAndSlot covers the two fields the shared
// fixture cannot: speculative decoding figures and the slot id. Both are the
// server's own and must survive the translation untouched, because the Draft
// row of the card is read straight off them.
func TestCompletionChunkCarriesDraftAndSlot(t *testing.T) {
	sse := strings.Join([]string{
		`data: {"index":0,"content":"","stop":false,"id_slot":3,"prompt_progress":{"total":9,"cache":4,"processed":9,"time_ms":12.0}}`,
		``,
		`data: {"index":0,"content":"hi","stop":false,"id_slot":3,"timings":{"prompt_n":9,"prompt_ms":12.0,"prompt_per_second":750.0,"predicted_n":1,"predicted_ms":20.0,"predicted_per_second":50.0,"cache_n":4,"draft_n":8,"draft_n_accepted":6}}`,
		``,
		`data: {"index":0,"content":" there","stop":false,"id_slot":3,"timings":{"prompt_n":9,"prompt_ms":12.0,"prompt_per_second":750.0,"predicted_n":2,"predicted_ms":40.0,"predicted_per_second":50.0,"cache_n":4,"draft_n":16,"draft_n_accepted":13}}`,
		``,
		`data: {"index":0,"content":"","stop":true,"stop_type":"limit","id_slot":3,"timings":{"prompt_n":9,"prompt_ms":12.0,"prompt_per_second":750.0,"predicted_n":2,"predicted_ms":40.0,"predicted_per_second":50.0,"cache_n":4,"draft_n":16,"draft_n_accepted":13}}`,
		``,
	}, "\n")

	rec, srv, err := ReplayCompletionStream([]byte(sse), nil, StreamHooks{})
	if err != nil {
		t.Fatalf("ReplayCompletionStream: %v", err)
	}
	if rec.Slot != 3 {
		t.Fatalf("slot %d, want 3 from id_slot", rec.Slot)
	}
	if srv.DraftN == nil || *srv.DraftN != 16 || srv.DraftNAccepted == nil || *srv.DraftNAccepted != 13 {
		t.Fatalf("draft figures lost in translation: %+v", srv)
	}
	if rec.Timings.DraftN == nil || *rec.Timings.DraftN != 16 {
		t.Fatalf("record draft_n %v, want 16", rec.Timings.DraftN)
	}
	if rec.Timings.CacheN != 4 {
		t.Fatalf("cache_n %d, want 4", rec.Timings.CacheN)
	}
	if len(rec.Progress) != 1 || rec.Progress[0].Cache != 4 || rec.Progress[0].Total != 9 {
		t.Fatalf("prefill progress %+v, want one event with total 9 cache 4", rec.Progress)
	}
	if rec.Prompt.FinishReason != "limit" {
		t.Fatalf("finish reason %q, want the server's own word %q", rec.Prompt.FinishReason, "limit")
	}
	if rec.Prompt.Completion != "hi there" {
		t.Fatalf("answer %q, want %q", rec.Prompt.Completion, "hi there")
	}
}

// TestCompletionChunkDropsStopTypeNone: "none" is the chunk saying it has not
// stopped. Recording it would leave every mid-stream chunk claiming a finish
// reason, and a record whose FinishReason is "none" reads as a run that ended
// for a reason nobody can name.
func TestCompletionChunkDropsStopTypeNone(t *testing.T) {
	c, err := parseCompletionChunk(`{"content":"x","stop":false,"stop_type":"none"}`)
	if err != nil {
		t.Fatalf("parseCompletionChunk: %v", err)
	}
	if len(c.Choices) != 1 || c.Choices[0].FinishReason != nil {
		t.Fatalf("stop_type none became a finish reason: %+v", c.Choices)
	}
}

// TestCompletionBody pins the request shape against llama.cpp's own README:
// the cap is n_predict (not the chat path's max_tokens), the prompt is a bare
// string, and the two recording switches are asked for exactly as on chat.
func TestCompletionBody(t *testing.T) {
	req := StreamRequest{
		Endpoint:  tape.EndpointCompletion,
		Prompt:    "Explain mmap.",
		MaxTokens: 320,
		Model:     "ignored-on-this-path",
		Params:    map[string]any{"temperature": 0.0, "seed": 7},
	}
	body := req.Body()

	if got := body["prompt"]; got != "Explain mmap." {
		t.Fatalf("prompt %v, want the text verbatim", got)
	}
	if got := body["n_predict"]; got != 320 {
		t.Fatalf("n_predict %v, want 320", got)
	}
	if body["stream"] != true || body["timings_per_token"] != true || body["return_progress"] != true {
		t.Fatalf("recording switches missing: %+v", body)
	}
	if body["temperature"] != 0.0 || body["seed"] != 7 {
		t.Fatalf("params were not merged verbatim: %+v", body)
	}
	// max_tokens, messages, model and stream_options are the chat path's.
	// Sending them here would either be ignored or rejected, and either way
	// the tape would claim a request that was not made.
	for _, k := range []string{"max_tokens", "messages", "model", "stream_options"} {
		if _, ok := body[k]; ok {
			t.Fatalf("completion body carries the chat field %q: %+v", k, body)
		}
	}
	if req.SentMaxTokens() != 320 {
		t.Fatalf("SentMaxTokens %d, want 320 read off n_predict", req.SentMaxTokens())
	}
	if req.Path() != CompletionPath {
		t.Fatalf("path %q, want %q", req.Path(), CompletionPath)
	}
}

// TestRecordedParamsLeavesThePromptOut: Params is the card's record of what
// was sent, and the prompt already has its own field. Keeping it in both
// places would put the whole prompt into the parameter map of every tape.
func TestRecordedParamsLeavesThePromptOut(t *testing.T) {
	raw := StreamRequest{Endpoint: tape.EndpointCompletion, Prompt: "p", MaxTokens: 8}.RecordedParams()
	if _, ok := raw["prompt"]; ok {
		t.Fatalf("completion params kept the prompt: %+v", raw)
	}
	if _, ok := raw["stream"]; ok {
		t.Fatalf("completion params kept the stream switch: %+v", raw)
	}
	if raw["n_predict"] != 8 {
		t.Fatalf("completion params lost the cap: %+v", raw)
	}

	chat := StreamRequest{Messages: []tape.Message{{Role: "user", Content: "hi"}}, MaxTokens: 8}.RecordedParams()
	if _, ok := chat["messages"]; ok {
		t.Fatalf("chat params kept the messages: %+v", chat)
	}
	if chat["max_tokens"] != 8 {
		t.Fatalf("chat params lost the cap: %+v", chat)
	}
}

// TestThinkingSetting: the record says "off" only when the request actually
// carried the engine's switch turned off. Everything else leaves the decision
// with the server, and the tape must not claim otherwise.
func TestThinkingSetting(t *testing.T) {
	kwargs := func(v any) map[string]any {
		return map[string]any{"chat_template_kwargs": map[string]any{"enable_thinking": v}}
	}
	for _, tc := range []struct {
		name   string
		params map[string]any
		want   string
	}{
		{"nothing sent", nil, ""},
		{"switch off", kwargs(false), "off"},
		{"switch on", kwargs(true), ""},
		{"other kwargs only", map[string]any{"chat_template_kwargs": map[string]any{"tools": "none"}}, ""},
		{"unrelated param", map[string]any{"temperature": 0.0}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := StreamRequest{Messages: []tape.Message{{Role: "user", Content: "hi"}}, Params: tc.params}
			if got := req.ThinkingSetting(); got != tc.want {
				t.Fatalf("ThinkingSetting() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestEndpointName: "" is a tape written before the field existed, and chat
// was the only path there was.
func TestEndpointName(t *testing.T) {
	for in, want := range map[string]string{
		"":                        tape.EndpointChat,
		tape.EndpointChat:         tape.EndpointChat,
		tape.EndpointCompletion:   tape.EndpointCompletion,
		"something-else-entirely": tape.EndpointChat,
	} {
		if got := (StreamRequest{Endpoint: in}).EndpointName(); got != want {
			t.Fatalf("EndpointName(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestStreamPostsTheRawPath is the end-to-end shape check: the request lands
// on /completion with the prompt verbatim, and the record says which path and
// what was asked of the thinking.
func TestStreamPostsTheRawPath(t *testing.T) {
	sse, err := os.ReadFile(filepath.Join("testdata", "completion_basic.sse"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var gotPath string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
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
	}))
	defer srv.Close()

	req := StreamRequest{
		Endpoint:  tape.EndpointCompletion,
		Prompt:    "Explain what a memory-mapped file is.",
		MaxTokens: 320,
		// A conversation the caller happened to carry: on this path it was
		// never sent, so it must not reach the record.
		Messages: []tape.Message{{Role: "user", Content: "never sent"}},
	}
	rec, _, err := New(srv.URL).Stream(context.Background(), req, StreamHooks{})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}

	if gotPath != CompletionPath {
		t.Fatalf("posted to %q, want %q", gotPath, CompletionPath)
	}
	if gotBody["prompt"] != req.Prompt {
		t.Fatalf("server saw prompt %v, want %q", gotBody["prompt"], req.Prompt)
	}
	if rec.Prompt.Endpoint != tape.EndpointCompletion {
		t.Fatalf("recorded endpoint %q, want %q", rec.Prompt.Endpoint, tape.EndpointCompletion)
	}
	if len(rec.Prompt.Messages) != 0 {
		t.Fatalf("recorded %d messages on a path that sends none: %+v", len(rec.Prompt.Messages), rec.Prompt.Messages)
	}
	if rec.Prompt.RenderedPrompt != req.Prompt {
		t.Fatalf("rendered prompt %q, want the prompt as sent %q", rec.Prompt.RenderedPrompt, req.Prompt)
	}
	if rec.Prompt.Thinking != "" {
		t.Fatalf("thinking %q, want empty: nothing was asked of it", rec.Prompt.Thinking)
	}
	if rec.Prompt.MaxTokens != 320 {
		t.Fatalf("max tokens %d, want 320", rec.Prompt.MaxTokens)
	}
	if n := len(rec.Tokens); n != 44 {
		t.Fatalf("recorded %d tokens, want 44", n)
	}
}

// TestStreamRecordsTheChatPathExplicitly: from TTP-55 on, a chat record says
// "chat" rather than leaving the field empty, so an empty Endpoint means one
// thing only — a tape older than the field.
func TestStreamRecordsTheChatPathExplicitly(t *testing.T) {
	sse, err := os.ReadFile(filepath.Join("testdata", "stream_basic.sse"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write(sse)
	}))
	defer srv.Close()

	req := StreamRequest{
		Messages: []tape.Message{{Role: "user", Content: "hi"}},
		Params:   map[string]any{"chat_template_kwargs": map[string]any{"enable_thinking": false}},
	}
	rec, _, err := New(srv.URL).Stream(context.Background(), req, StreamHooks{})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if gotPath != ChatPath {
		t.Fatalf("posted to %q, want %q", gotPath, ChatPath)
	}
	if rec.Prompt.Endpoint != tape.EndpointChat {
		t.Fatalf("recorded endpoint %q, want %q", rec.Prompt.Endpoint, tape.EndpointChat)
	}
	if rec.Prompt.Thinking != "off" {
		t.Fatalf("thinking %q, want off: the request carried the switch", rec.Prompt.Thinking)
	}
}
