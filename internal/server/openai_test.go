package server

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/midagedev/toktape/internal/tape"
)

// openaiServer is a generic OpenAI-compatible fake: /v1/models lists two
// models, /props and /slots 404, and /v1/chat/completions streams whatever
// the case hands it.
func openaiServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/props", func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	})
	mux.HandleFunc("/slots", func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	})
	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"meta-llama-3-8b","object":"model","owned_by":"vllm"},{"id":"other-model","object":"model","owned_by":"vllm"}]}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestModelsListsIDs(t *testing.T) {
	srv := openaiServer(t)
	m, err := New(srv.URL).Models(context.Background())
	if err != nil {
		t.Fatalf("Models: %v", err)
	}
	if len(m.Data) != 2 {
		t.Fatalf("models = %d, want 2", len(m.Data))
	}
	// FAIL-first: FirstID returned m.Data[1].ID ("other-model") while the
	// contract is the first id the server listed.
	if got, want := m.FirstID(), "meta-llama-3-8b"; got != want {
		t.Errorf("FirstID = %q, want %q", got, want)
	}
}

func TestModelsEmptyDataIsEmptyNotACrash(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"object":"list","data":[]}`))
	}))
	t.Cleanup(srv.Close)
	m, err := New(srv.URL).Models(context.Background())
	if err != nil {
		t.Fatalf("Models: %v", err)
	}
	if got := m.FirstID(); got != "" {
		t.Errorf("FirstID = %q, want empty (the server listed nothing)", got)
	}
}

func TestModelsRejects(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		code int
	}{
		{"non-2xx", `{"error":"nope"}`, http.StatusNotFound},
		{"non-JSON", `not json`, http.StatusOK},
		{"data not an array", `{"data":"meta-llama-3-8b"}`, http.StatusOK},
		{"data an object", `{"data":{"id":"x"}}`, http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.code)
				_, _ = w.Write([]byte(tc.body))
			}))
			t.Cleanup(srv.Close)
			// FAIL-first: the "data not an array" rows decoded to an empty
			// listing with nil error, so a foreign schema became a model
			// name nobody observed.
			if _, err := New(srv.URL).Models(context.Background()); !errors.Is(err, ErrUnreachable) {
				t.Errorf("Models = %v, want ErrUnreachable", err)
			}
		})
	}
}

func TestProps404IsNoPropsAndUnreachable(t *testing.T) {
	for _, code := range []int{http.StatusNotFound, http.StatusMethodNotAllowed} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(code)
			_, _ = w.Write([]byte("no such route"))
		}))
		t.Cleanup(srv.Close)
		_, err := New(srv.URL).Props(context.Background())
		// FAIL-first: on the pre-change code Props classified 404 as plain
		// ErrUnreachable, so errors.Is(err, ErrNoProps) was false and auto
		// mode never took the second pass.
		if !errors.Is(err, ErrNoProps) {
			t.Errorf("status %d: errors.Is(err, ErrNoProps) = false (%v)", code, err)
		}
		if !errors.Is(err, ErrUnreachable) {
			t.Errorf("status %d: errors.Is(err, ErrUnreachable) = false (%v)", code, err)
		}
	}
}

func TestPropsOtherFailuresAreNotNoProps(t *testing.T) {
	// A refusal is not ErrNoProps: auto mode must not take the second pass
	// on it.
	closed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := closed.URL
	closed.Close()
	if _, err := New(url).Props(context.Background()); !errors.Is(err, ErrUnreachable) || errors.Is(err, ErrNoProps) {
		t.Errorf("refused Props err = %v, want ErrUnreachable without ErrNoProps", err)
	}
	// A 503 while loading is not ErrNoProps either.
	loading := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":{"message":"Loading model","type":"invalid_request_error"}}`))
	}))
	t.Cleanup(loading.Close)
	if _, err := New(loading.URL).Props(context.Background()); !errors.Is(err, ErrLoading) || errors.Is(err, ErrNoProps) {
		t.Errorf("loading Props err = %v, want ErrLoading without ErrNoProps", err)
	}
}

func TestDiscoverFindsOpenAICandidate(t *testing.T) {
	openai := openaiServer(t)
	got, err := New("").Discover(context.Background(), []string{openai.URL})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	// FAIL-first: on the pre-change code Discover probed /props only, so a
	// server that 404s it was never found and this errored.
	if got != openai.URL {
		t.Errorf("Discover = %q, want %q (404s /props, 200s /v1/models)", got, openai.URL)
	}
}

func TestDiscoverPrefersPropsServerListedEarlier(t *testing.T) {
	openai := openaiServer(t)
	llama := propsServer(t, "")
	got, err := New("").Discover(context.Background(), []string{llama.URL, openai.URL})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if got != llama.URL {
		t.Errorf("Discover = %q, want the /props server %q listed earlier", got, llama.URL)
	}
}

// openaiSSE builds a timings-free stream: a role chunk, len(words) content
// deltas, and — unless usageTokens is negative — a final stop chunk carrying
// that completion count, then [DONE]. Every event 20 ms apart.
func openaiSSE(words []string, usageTokens int) ([]byte, []time.Duration) {
	var b strings.Builder
	var arrivals []time.Duration
	at := 100 * time.Millisecond
	emit := func(payload string) {
		b.WriteString("data: " + payload + "\n\n")
		arrivals = append(arrivals, at)
		at += 20 * time.Millisecond
	}
	emit(`{"choices":[{"index":0,"delta":{"role":"assistant"}}]}`)
	for _, w := range words {
		emit(`{"choices":[{"index":0,"delta":{"content":` + strconv.Quote(w) + `}}]}`)
	}
	final := `{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]`
	if usageTokens >= 0 {
		final += fmt.Sprintf(`,"usage":{"prompt_tokens":10,"completion_tokens":%d}`, usageTokens)
	}
	final += `}`
	emit(final)
	b.WriteString("data: [DONE]\n\n")
	arrivals = append(arrivals, at)
	return []byte(b.String()), arrivals
}

func TestClientTimedUsageRecalibration(t *testing.T) {
	words := []string{"a", "b", "c", "d", "e", "f"}
	sse, arrivals := openaiSSE(words, 12)
	rec, _, err := ReplayStream(sse, arrivals, StreamHooks{})
	if err != nil {
		t.Fatalf("ReplayStream: %v", err)
	}
	tm := rec.Timings
	if tm.Source != "client" || tm.PredictedNSource != "usage" || tm.PredictedN != 12 {
		t.Errorf("got %q/%q/%d, want client/usage/12", tm.Source, tm.PredictedNSource, tm.PredictedN)
	}
	// 12 tokens over the 5-gap... the 6-delta window: (12-1)/window.
	// A chunk-counted rate would be (6-1)/window, 120% off.
	window := rec.Tokens[len(rec.Tokens)-1].T - rec.Tokens[0].T
	want := 11.0 / window.Seconds()
	if tm.PredictedPerSecond < want*0.95 || tm.PredictedPerSecond > want*1.05 {
		t.Errorf("PredictedPerSecond = %v, want 11/window = %v", tm.PredictedPerSecond, want)
	}
	if tm.ClientPredictedPerSecond != tm.PredictedPerSecond {
		t.Errorf("ClientPredictedPerSecond = %v, want the recalibrated %v",
			tm.ClientPredictedPerSecond, tm.PredictedPerSecond)
	}
	if tm.ClientAgreesWithServer {
		t.Error("ClientAgreesWithServer = true, want false (nothing to agree with)")
	}
	if tm.PromptPerSecond != 0 {
		t.Errorf("PromptPerSecond = %v, want 0", tm.PromptPerSecond)
	}
	// Idempotent: a second Reduce over the reduced record changes nothing.
	if again := Reduce(rec, time.Time{}, 0); again != tm {
		t.Errorf("second Reduce moved the summary:\n%+v\n%+v", tm, again)
	}
}

func TestClientTimedNegativeUsageIsNoUsage(t *testing.T) {
	sse, arrivals := openaiSSE([]string{"a", "b", "c"}, -3)
	rec, _, err := ReplayStream(sse, arrivals, StreamHooks{})
	if err != nil {
		t.Fatalf("ReplayStream: %v", err)
	}
	// FAIL-first: completion_tokens -3 became PredictedN -3 with a rate over
	// it — a malformed figure recorded as a count.
	tm := rec.Timings
	if tm.PredictedNSource != "chunks" || tm.PredictedN != 3 {
		t.Errorf("got %q/%d, want chunks/3 (the malformed figure is refused)", tm.PredictedNSource, tm.PredictedN)
	}
	if tm.PredictedPerSecond != 0 || tm.ClientPredictedPerSecond != 0 {
		t.Errorf("rates = %v/%v, want 0/0", tm.PredictedPerSecond, tm.ClientPredictedPerSecond)
	}
}

func TestClientTimedStreamWithoutDone(t *testing.T) {
	sse, arrivals := openaiSSE([]string{"a", "b"}, 7)
	// Drop the final chunk (with its finish_reason and usage) and [DONE]:
	// the connection died mid-generation.
	fin := bytes.LastIndex(sse, []byte(`"finish_reason"`))
	cut := bytes.LastIndex(sse[:fin], []byte("data: "))
	rec, _, err := ReplayStream(sse[:cut], arrivals[:3], StreamHooks{})
	if err == nil {
		t.Fatal("a truncated client-timed stream was reported as complete")
	}
	if rec == nil {
		t.Fatal("partial record lost")
	}
	if rec.Timings.Source != "client" {
		t.Errorf("partial Source = %q, want client", rec.Timings.Source)
	}
	if !strings.Contains(rec.Error, "without a finish chunk") {
		t.Errorf("rec.Error = %q", rec.Error)
	}
}

// TestLlamaTapeReducesUnchanged pins the contract that a timings-bearing
// stream reduces byte-identically after the client-timed branch landed: the
// branch is gated on Source "client", and a llama-server tape never carries
// it, so the server path below the gate must not move by a field.
func TestLlamaTapeReducesUnchanged(t *testing.T) {
	rec, _ := loadFixture(t, "stream_basic", StreamHooks{})
	if got := rec.Timings.Source; got != "" {
		t.Errorf("Source = %q, want empty (a timings tape is the server's record)", got)
	}
	if got := rec.Timings.PredictedNSource; got != "" {
		t.Errorf("PredictedNSource = %q, want empty", got)
	}
	before := rec.Timings
	again := Reduce(rec, time.Time{}, 0)
	if again != before {
		t.Errorf("Reduce moved a server-timed summary:\nbefore %+v\nafter  %+v", before, again)
	}
	// The pin the spec asks for: the whole reduction of the realistic
	// fixture still reads the pre-change expectations in reduce_test.go
	// (TestReduceBasicFixture passes unmodified beside this).
	if again.PredictedPerSecond <= 0 || !again.ClientAgreesWithServer {
		t.Errorf("fixture summary = %+v, want a server rate the client confirms", again)
	}
}

func TestOpenAIBodyKeys(t *testing.T) {
	msgs := []tape.Message{{Role: "user", Content: "hi"}}
	keys := func(b map[string]any) []string {
		out := make([]string, 0, len(b))
		for k := range b {
			out = append(out, k)
		}
		sort.Strings(out)
		return out
	}
	join := func(ks []string) string {
		s := ""
		for i, k := range ks {
			if i > 0 {
				s += ","
			}
			s += k
		}
		return s
	}

	full := StreamRequest{Protocol: tape.ServerOpenAI, Model: "m", Messages: msgs, MaxTokens: 64}.Body()
	// FAIL-first: on the pre-change code Body always sent timings_per_token
	// and return_progress, which strict vLLM builds reject with 400.
	if got, want := join(keys(full)), "max_tokens,messages,model,stream,stream_options"; got != want {
		t.Errorf("openai body keys = %q, want %q", got, want)
	}
	uncapped := StreamRequest{Protocol: tape.ServerOpenAI, Model: "m", Messages: msgs}.Body()
	if got, want := join(keys(uncapped)), "messages,model,stream,stream_options"; got != want {
		t.Errorf("uncapped openai body keys = %q, want %q", got, want)
	}
	for _, k := range []string{"timings_per_token", "return_progress"} {
		if _, ok := full[k]; ok {
			t.Errorf("openai body carries %q", k)
		}
	}
	if _, ok := full["model"]; !ok {
		t.Error("openai body has no model: these servers require one")
	}

	// The llama body is unchanged: the protocol switch must not move the
	// default path by a key.
	llama := StreamRequest{Messages: msgs, MaxTokens: 64}.Body()
	for _, k := range []string{"timings_per_token", "return_progress", "stream_options"} {
		if _, ok := llama[k]; !ok {
			t.Errorf("llama body lost %q", k)
		}
	}
}
