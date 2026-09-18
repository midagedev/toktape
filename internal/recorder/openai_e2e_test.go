package recorder_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/midagedev/toktape/internal/card"
	"github.com/midagedev/toktape/internal/recorder"
	"github.com/midagedev/toktape/internal/tape"
)

// The generic OpenAI-compatible end-to-end (TTP-99, 2026-09-19): a fake that
// is not llama.cpp — 404 on /props and /slots, a /v1/models listing, and a
// /v1/chat/completions stream of N deltas over ~100 ms with a final usage
// chunk whose completion_tokens (12) is not the delta count (6), so the
// recalibration from chunks to usage is observable in the rate.

// openaiBodies records the request bodies the fake's chat route received.
type openaiBodies struct {
	mu     sync.Mutex
	bodies []map[string]any
	// applyCalls counts /apply-template hits: the openai mode must not make
	// any — the template is the server's own business.
	applyCalls int
}

func (b *openaiBodies) add(m map[string]any) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.bodies = append(b.bodies, m)
}

func (b *openaiBodies) first() map[string]any {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.bodies) == 0 {
		return nil
	}
	return b.bodies[0]
}

// openaiFake streams deltas words with gap between them, then one final
// choice carrying finish_reason and — unless usageTokens is negative — a
// usage object with that completion count, then [DONE].
func openaiFake(t *testing.T, words []string, gap time.Duration, usageTokens int) (*httptest.Server, *openaiBodies) {
	t.Helper()
	var log openaiBodies
	mux := http.NewServeMux()
	mux.HandleFunc("/props", func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	})
	mux.HandleFunc("/slots", func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	})
	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"test-model","object":"model","owned_by":"vllm"}]}`))
	})
	mux.HandleFunc("/apply-template", func(w http.ResponseWriter, r *http.Request) {
		log.mu.Lock()
		log.applyCalls++
		log.mu.Unlock()
		http.Error(w, "no template here", http.StatusNotFound)
	})
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		log.add(body)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		emit := func(payload string) {
			_, _ = fmt.Fprintf(w, "data: %s\n\n", payload)
			if flusher != nil {
				flusher.Flush()
			}
		}
		emit(`{"id":"1","object":"chat.completion.chunk","created":1,"model":"test-model","choices":[{"index":0,"delta":{"role":"assistant"}}]}`)
		for _, wd := range words {
			time.Sleep(gap)
			emit(fmt.Sprintf(`{"id":"1","object":"chat.completion.chunk","created":1,"model":"test-model","choices":[{"index":0,"delta":{"content":%s}}]}`, mustJSON(t, wd)))
		}
		time.Sleep(gap)
		final := `{"id":"1","object":"chat.completion.chunk","created":1,"model":"test-model","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]`
		if usageTokens >= 0 {
			final += fmt.Sprintf(`,"usage":{"prompt_tokens":10,"completion_tokens":%d}`, usageTokens)
		}
		final += `}`
		emit(final)
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
		if flusher != nil {
			flusher.Flush()
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, &log
}

func mustJSON(t *testing.T, s string) string {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func openaiOpts(url, kind, claim string) recorder.Options {
	return recorder.Options{
		BaseURL:        url,
		EngineKind:     kind,
		EngineClaim:    claim,
		Concurrency:    1,
		MaxTokens:      64,
		SampleInterval: 10 * time.Millisecond,
		FSRoot:         procRoot,
		GPU:            nil, // set by the caller: fakeGPU needs *testing.T
		Clock:          fixedClock{time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)},
		Version:        "0.1.0-test",
	}
}

func hasCaveat(s *tape.RunSummary, code string) bool {
	for _, c := range card.Caveats(s) {
		if c.Code == code {
			return true
		}
	}
	return false
}

func TestRecordOpenAIEndToEnd(t *testing.T) {
	words := []string{"alpha", "beta", "gamma", "delta", "eps", "zeta"}
	srv, log := openaiFake(t, words, 20*time.Millisecond, 12)
	opts := openaiOpts(srv.URL, "openai", "vLLM 0.11")
	opts.GPU = fakeGPU(t)
	tp, err := recorder.Record(context.Background(), opts)
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	s := tp.Summary

	// FAIL-first (each row verified by breaking it: pointing the run at the
	// llama path, zeroing the usage count, or dropping the claim):
	if s.Server.Kind != tape.ServerOpenAI {
		t.Errorf("Kind = %q, want %q", s.Server.Kind, tape.ServerOpenAI)
	}
	if s.Server.NSlots != 0 {
		t.Errorf("NSlots = %d, want 0 (slots unknown, never a limit)", s.Server.NSlots)
	}
	if s.Timings.Source != "client" {
		t.Errorf("Source = %q, want \"client\" (the server reported no timings)", s.Timings.Source)
	}
	if s.Timings.PredictedN != 12 {
		t.Errorf("PredictedN = %d, want 12 (usage.completion_tokens, not 6 deltas)", s.Timings.PredictedN)
	}
	if s.Timings.PredictedNSource != "usage" {
		t.Errorf("PredictedNSource = %q, want \"usage\"", s.Timings.PredictedNSource)
	}
	// The recalibration: 12 tokens over the observed window, not 6 chunks.
	// A chunk-counted rate would read (6-1)/window — 120% off — so the 5%
	// band proves the usage count is what the rate is over.
	rec := tp.Requests[0]
	if len(rec.Tokens) != len(words) {
		t.Fatalf("tokens = %d, want %d deltas", len(rec.Tokens), len(words))
	}
	window := rec.Tokens[len(rec.Tokens)-1].T - rec.Tokens[0].T
	want := 11.0 / window.Seconds()
	if got := s.Timings.PredictedPerSecond; got < want*0.95 || got > want*1.05 {
		t.Errorf("PredictedPerSecond = %v, want 11/window = %v ±5%%", got, want)
	}
	if got := s.Timings.ClientPredictedPerSecond; got != s.Timings.PredictedPerSecond {
		t.Errorf("ClientPredictedPerSecond = %v, want the recalibrated %v", got, s.Timings.PredictedPerSecond)
	}
	if s.Timings.PromptPerSecond != 0 {
		t.Errorf("PromptPerSecond = %v, want 0 (TTFT is not a prefill measurement)", s.Timings.PromptPerSecond)
	}
	if s.Timings.ClientAgreesWithServer {
		t.Error("ClientAgreesWithServer = true, want false (nothing to agree with)")
	}
	if !reflect.DeepEqual(s.Server.Flags, tape.ServerFlags{}) {
		t.Errorf("Flags = %+v, want zero", s.Server.Flags)
	}
	if s.Server.PID != 0 {
		t.Errorf("PID = %d, want 0 (never guessed)", s.Server.PID)
	}
	if len(s.Server.Args) != 0 {
		t.Errorf("Args = %q, want none", s.Server.Args)
	}
	if s.Server.EngineClaim != "vLLM 0.11" {
		t.Errorf("EngineClaim = %q, want the user's claim verbatim", s.Server.EngineClaim)
	}
	if s.Model.FileName != "test-model" || s.Model.Path != "" || s.Model.Repo != "" {
		t.Errorf("Model = %+v, want FileName from /v1/models and no Path/Repo", s.Model)
	}
	if !hasCaveat(&s, card.CodeClientTimed) {
		t.Errorf("caveats %v lack %q", card.Caveats(&s), card.CodeClientTimed)
	}
	if hasCaveat(&s, card.CodeTokensUncounted) {
		t.Errorf("caveats %v wrongly contain %q (usage arrived)", card.Caveats(&s), card.CodeTokensUncounted)
	}
	if hasCaveat(&s, card.CodeClientDisagrees) {
		t.Errorf("caveats %v wrongly contain client_disagrees (one clock cannot disagree)", card.Caveats(&s))
	}
	text := card.Text(&s)
	if !strings.Contains(text, "client-timed") {
		t.Error("card lacks the client-timed label beside the decode headline")
	}
	if !strings.Contains(text, "openai · claim: vLLM 0.11") {
		t.Error("card engine line lacks `openai · claim: vLLM 0.11`")
	}
	if strings.Contains(text, "FLAGS") {
		t.Error("card prints a FLAGS block for a server with no argv")
	}

	// The wire: no llama-shaped fields, and the model defaulted from /v1/models.
	body := log.first()
	if body == nil {
		t.Fatal("the server received no request")
	}
	for _, k := range []string{"timings_per_token", "return_progress"} {
		if _, ok := body[k]; ok {
			t.Errorf("request body carries %q: %v", k, body)
		}
	}
	if body["model"] != "test-model" {
		t.Errorf("body model = %v, want the first /v1/models id", body["model"])
	}
	log.mu.Lock()
	applyCalls := log.applyCalls
	log.mu.Unlock()
	if applyCalls != 0 {
		t.Errorf("/apply-template called %d times, want 0", applyCalls)
	}
}

func TestRecordOpenAINoUsage(t *testing.T) {
	words := []string{"alpha", "beta", "gamma"}
	srv, _ := openaiFake(t, words, 10*time.Millisecond, -1)
	opts := openaiOpts(srv.URL, "openai", "")
	opts.GPU = fakeGPU(t)
	tp, err := recorder.Record(context.Background(), opts)
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	s := tp.Summary

	// FAIL-first: with the chunks branch dropped, PredictedPerSecond read the
	// silent client fallback over 3 chunks — a rate over chunks as tokens.
	if s.Timings.PredictedNSource != "chunks" {
		t.Errorf("PredictedNSource = %q, want \"chunks\"", s.Timings.PredictedNSource)
	}
	if s.Timings.PredictedN != len(words) {
		t.Errorf("PredictedN = %d, want the %d chunk count", s.Timings.PredictedN, len(words))
	}
	if s.Timings.PredictedPerSecond != 0 || s.Timings.ClientPredictedPerSecond != 0 {
		t.Errorf("rates = %v/%v, want 0/0 (no decode rate without a token count)",
			s.Timings.PredictedPerSecond, s.Timings.ClientPredictedPerSecond)
	}
	if !hasCaveat(&s, card.CodeTokensUncounted) {
		t.Errorf("caveats %v lack %q", card.Caveats(&s), card.CodeTokensUncounted)
	}
	if !hasCaveat(&s, card.CodeClientTimed) {
		t.Errorf("caveats %v lack %q", card.Caveats(&s), card.CodeClientTimed)
	}
	// The decode row prints "?" in place of the rate; TTFT is still measured.
	ex := card.ExplainCaveats(&s)
	line := ""
	for _, ln := range strings.Split(ex, "\n") {
		if strings.Contains(ln, "decode row") {
			line = ln
		}
	}
	if !strings.Contains(line, "?") {
		t.Errorf("decode row %q has no ?", line)
	}
	if s.Timings.TTFTMs <= 0 {
		t.Errorf("TTFTMs = %v, want it measured (per-chunk latencies survive)", s.Timings.TTFTMs)
	}
	text := card.Text(&s)
	if !strings.Contains(text, "openai · ?") {
		t.Error("card engine line lacks `openai · ?` (no claim given)")
	}
}

func TestRecordAutoDetectsOpenAI(t *testing.T) {
	words := []string{"alpha", "beta"}
	srv, _ := openaiFake(t, words, 10*time.Millisecond, 12)
	opts := openaiOpts(srv.URL, "auto", "")
	opts.GPU = fakeGPU(t)
	tp, err := recorder.Record(context.Background(), opts)
	if err != nil {
		t.Fatalf("Record auto against an OpenAI-only server: %v", err)
	}
	// FAIL-first: on the pre-change code auto mode returned the /props 404
	// as unreachable and this errored.
	if tp.Summary.Server.Kind != tape.ServerOpenAI {
		t.Errorf("Kind = %q, want %q", tp.Summary.Server.Kind, tape.ServerOpenAI)
	}
}

func TestRecordAutoKeepsLlamaServer(t *testing.T) {
	srv := fakeServer(t)
	opts := openaiOpts(srv.URL, "auto", "")
	opts.GPU = fakeGPU(t)
	opts.FSRoot = procRoot
	tp, err := recorder.Record(context.Background(), opts)
	if err != nil {
		t.Fatalf("Record auto against the llama fake: %v", err)
	}
	s := tp.Summary
	if s.Server.Kind != tape.ServerLlamaCPP {
		t.Errorf("Kind = %q, want %q (exactly as before)", s.Server.Kind, tape.ServerLlamaCPP)
	}
	if s.Timings.Source != "" || s.Timings.PredictedNSource != "" {
		t.Errorf("Source/PredictedNSource = %q/%q, want empty (server timings are the record)",
			s.Timings.Source, s.Timings.PredictedNSource)
	}
}

func TestRecordOpenAIRefusesCompletionEndpoint(t *testing.T) {
	words := []string{"alpha"}
	srv, _ := openaiFake(t, words, time.Millisecond, 12)
	opts := openaiOpts(srv.URL, "openai", "")
	opts.GPU = fakeGPU(t)
	opts.Endpoint = tape.EndpointCompletion
	_, err := recorder.Record(context.Background(), opts)
	if err == nil || !strings.Contains(err.Error(), "/v1/chat/completions") {
		t.Errorf("Record err = %v, want the completion-endpoint refusal", err)
	}
}

func TestRecordOpenAIInvalidKind(t *testing.T) {
	opts := openaiOpts("http://127.0.0.1:9", "vllm", "")
	opts.GPU = fakeGPU(t)
	// FAIL-first: an unknown kind silently behaved as auto.
	if _, err := recorder.Record(context.Background(), opts); err == nil {
		t.Error("Record with --engine-kind vllm succeeded, want a usage error")
	}
}
