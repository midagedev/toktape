package main

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/midagedev/toktape/internal/card"
	"github.com/midagedev/toktape/internal/tape"
)

// The first-run scenario matrix (audit track, 2026-09-21).
//
// The north star says a reader who sees a card must get their own with one
// command, first try. One real server combination is verified
// (internal/recorder/firsttry_test.go); everything a first-time user can
// actually meet is not. This test runs the CLI in-process, exactly as that
// user would, against twenty-one fake servers — the shapes llama.cpp, Ollama,
// LM Studio and vLLM present — and records two verdicts per row, by rule:
//
//   - USABLE: yes iff exit 0, StreamsFailed 0, every stream predicted_n >= 64
//     and (the fake reports its prompt figures, so it always knows) every
//     stream prompt_n >= 256 — a cache hit reports 5 on these fakes.
//     "refused-cleanly" iff exit != 0 before any stream was sent; else no.
//   - EXPLAINED, assessed only when USABLE != yes or the tape carries a
//     figure-severity caveat: yes iff the user-facing output names the cause
//     in words a user would recognise AND offers at least one concrete next
//     action (a flag, a command, a server setting). A bare caveat code, or a
//     lever that does not fit the server, is "partial".
//
// Rows that are already yes/yes or refused-cleanly/yes are pinned so they
// cannot regress. Every other row is logged into knownGaps with a one-line
// reason and deliberately NOT asserted — the lead turns each into a failing
// gate when it is fixed (docs/research/firstrun-matrix.md holds the analysis,
// the ranked gaps and the inventory of every refusal sentence).
//
// The fakes answer instantly with planted cost figures, so the whole matrix
// runs in seconds: the real waits are the reasoning row's 2 s clock, the
// loading row's one 2 s attach poll, and the Ollama row's default 20 s clock
// cutting a stream the honest fake paces at real decode speed.

// ---------------------------------------------------------------------------
// The fake's tokenizer. Byte-compatible with internal/recorder/firsttry_test
// (Go test packages cannot import each other, and there is no non-test
// tokenizer to reuse — the recorder's countSet asks the server). Both model
// the same measured tokenizer (2026-09-20): prose at 4.0 bytes/token for the
// first 6000 bytes, code and logs at 2.2 after.
const (
	matHeadBytes = 6000
	matHeadBPT   = 4.0
	matTailBPT   = 2.2
	// matTemplateTokens is what the fake counts the chat template as adding
	// (firstTryTemplate). It is below the recorder's own 192-token margin
	// (slotctx.go), so a request the plan fitted stays fitted.
	matTemplateTokens = 100
)

func matTokens(s string) int {
	head := min(len(s), matHeadBytes)
	return int(math.Ceil(float64(head)/matHeadBPT)) +
		int(math.Ceil(float64(len(s)-head)/matTailBPT))
}

// The planted boxes. matFastPerTokMs is 1/64 ms per prompt token — 64 000
// tok/s at the margin, binary-exact so the probe's two-point fit recovers it
// without rounding (the same plant firsttry_test.go uses). matSlowPerSec is
// the slow box, where the probe's long point does not fit its 5 s budget and
// is refused (probe.go probeLongLength returns 0 under 3x the short point).
const (
	matFastPerTokMs = 0.015625
	matFastFixedMs  = 25.0
	matSlowPerSec   = 30.0
)

// ---------------------------------------------------------------------------
// A llama-server-shaped fake.

type matLlama struct {
	cfg   matLlamaCfg
	mu    sync.Mutex
	posts int // every POST the fake saw, probe requests included
	chatN int // chat requests so far, for per-request gates
	seen  map[string]bool
}

type matLlamaCfg struct {
	totalSlots int
	propsNCtx  int
	// slotNCtx is the per-slot n_ctx /slots reports. nil with noSlots501
	// false: no /slots route at all.
	slotNCtx   []int
	noSlots501 bool
	tokenize   bool
	// perTokMs/fixedMs plant the /completion (probe) cost curve.
	perTokMs float64
	fixedMs  float64
	// promptPerSec plants the chat route's prompt_ms derivation.
	promptPerSec float64
	// enforceCtx, when > 0, refuses (the server's own error frame) any
	// request whose tokens + template + answer cap exceed it.
	enforceCtx int
	// chatTokens is the content tokens a chat answer emits.
	chatTokens int
	// reasoningForever answers reasoning_content deltas until the client's
	// clock cuts the connection (llama-thinking-default).
	reasoningForever bool
	// tokenSleep spaces chunks so concurrent streams overlap.
	tokenSleep time.Duration
	// cache serves an exact-text repeat the way the day's warm ik did:
	// prompt_n 5, 2 ms (llama-rerun-same-server).
	cache bool
	// chatGate overrides a chat request's behavior by call number.
	chatGate func(call int) matGate
}

type matGate int

const (
	matGateNormal matGate = iota
	matGate429            // rate-limited: HTTP 429
	matGateCut            // connection cut mid-answer after 50 tokens
)

func matLlamaServer(t *testing.T, cfg matLlamaCfg) (*httptest.Server, *matLlama) {
	t.Helper()
	f := &matLlama{cfg: cfg, seen: map[string]bool{}}
	mux := http.NewServeMux()

	mux.HandleFunc("/props", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Server", "llama.cpp")
		_, _ = w.Write([]byte(fmt.Sprintf(`{
		  "model_path": "/models/gguf/Qwen3.5-35B-A3B-UD-Q4_K_M.gguf",
		  "build_info": "b4321-abcdef12",
		  "chat_template": "chatml",
		  "total_slots": %d,
		  "default_generation_settings": {"n_ctx": %d}
		}`, cfg.totalSlots, cfg.propsNCtx)))
	})
	switch {
	case cfg.noSlots501:
		mux.HandleFunc("/slots", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotImplemented)
			_, _ = w.Write([]byte(`{"error":{"message":"slots endpoint is disabled (server started with --no-slots)"}}`))
		})
	case cfg.slotNCtx != nil:
		mux.HandleFunc("/slots", func(w http.ResponseWriter, r *http.Request) {
			slots := make([]string, len(cfg.slotNCtx))
			for i, ctx := range cfg.slotNCtx {
				slots[i] = fmt.Sprintf(`{"id":%d,"is_processing":false,"n_ctx":%d,"n_past":0}`, i, ctx)
			}
			_, _ = w.Write([]byte("[" + strings.Join(slots, ",") + "]"))
		})
	}
	if cfg.tokenize {
		mux.HandleFunc("/tokenize", func(w http.ResponseWriter, r *http.Request) {
			var in struct {
				Content string `json:"content"`
			}
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"tokens": make([]int, matTokens(in.Content)),
			})
		})
	}
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

	// /completion is the probe pass's route. The cost is computed, never
	// slept: every figure the fit reads is the server's own ms.
	mux.HandleFunc("/completion", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.posts++
		f.mu.Unlock()
		var in struct {
			Prompt   string `json:"prompt"`
			NPredict *int   `json:"n_predict"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		cap := 1
		if in.NPredict != nil {
			cap = *in.NPredict
		}
		n := matTokens(in.Prompt)
		if cfg.enforceCtx > 0 && n+matTemplateTokens+cap > cfg.enforceCtx {
			matWriteCtxRefused(w)
			return
		}
		ms := cfg.fixedMs + cfg.perTokMs*float64(n)
		if cfg.cache {
			f.mu.Lock()
			hit := f.seen[in.Prompt]
			f.seen[in.Prompt] = true
			f.mu.Unlock()
			if hit {
				n, ms = 5, 2.0
			}
		}
		matWriteCompletion(w, n, ms)
	})

	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		call := f.chatN
		f.chatN++
		f.posts++
		f.mu.Unlock()
		if cfg.chatGate != nil {
			switch cfg.chatGate(call) {
			case matGate429:
				matWriteStatus(w, http.StatusTooManyRequests,
					`{"error":{"message":"This server is rate limited; try again later","type":"rate_limit_error"}}`)
				return
			case matGateCut:
				matCutMidAnswer(w, cfg.tokenSleep)
				return
			}
		}
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
		prompt := b.String()
		cap := 0
		if in.MaxTokens != nil {
			cap = *in.MaxTokens
		}
		n := matTokens(prompt)
		if cfg.enforceCtx > 0 && n+matTemplateTokens+cap > cfg.enforceCtx {
			matWriteCtxRefused(w)
			return
		}
		promptMs := float64(n) / cfg.promptPerSec * 1000
		if cfg.cache {
			f.mu.Lock()
			hit := f.seen[prompt]
			f.seen[prompt] = true
			f.mu.Unlock()
			if hit {
				n, promptMs = 5, 2.0
			}
		}
		if cfg.reasoningForever {
			matWriteReasoningForever(w, n, promptMs, cfg.tokenSleep)
			return
		}
		matWriteChat(w, n, promptMs, cfg.chatTokens, cfg.tokenSleep)
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, f
}

// postsSeen is how many POSTs the fake answered, for the "was any stream
// sent" half of the USABLE rule.
func (f *matLlama) postsSeen() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.posts
}

// ---------------------------------------------------------------------------
// An OpenAI-compatible fake (vLLM / LM Studio / Ollama shapes).

type matOpenAI struct {
	mu    sync.Mutex
	posts int
}

type matOpenAICfg struct {
	// ctxLimit, when > 0, refuses prompt+max_tokens over it with a
	// vLLM-shaped 400 whose numbers are the real ones.
	ctxLimit int
	// truncateTo, when > 0, is what usage.prompt_tokens reports for a
	// prompt longer than it (Ollama truncates long prompts silently; a
	// shorter prompt is reported as it was counted).
	truncateTo int
	chatTokens int
	apiTags    bool
	// advertiseMaxLen puts ctxLimit in /v1/models as max_model_len, which
	// is what a real vLLM does (measured 2026-09-21, vLLM 0.29.0).
	advertiseMaxLen bool
	// emptyModels, when set, is an Ollama with nothing pulled: the listings
	// carry nothing and the chat endpoint answers the 400 a real Ollama
	// 0.33.3 answered, verbatim (measured 2026-09-21 on Windows 11) — the
	// state a reader is in for the minutes between installing Ollama and
	// pulling something.
	emptyModels bool
	// decodeTokPerSec, when non-nil, paces the chat stream's deltas as a
	// function of the prompt tokens it received: real Ollama decodes slower
	// at a long prompt than at a short one — the KV cache the prompt leaves
	// behind is paid on every token. Real Ollama's counting shape — usage
	// only on a stream that ended on its own, never on one the client cut —
	// is not a knob: it is structural in matWriteOpenAIChat.
	decodeTokPerSec func(promptTokens int) float64
}

// matOllamaTokPerSec is the measured decode curve of the real Ollama this
// fake models (2026-09-21): ~110 tok/s at a ~59-token prompt, ~85 tok/s at a
// ~6,000-token one. Linear between the two measured points, clamped outside
// them.
func matOllamaTokPerSec(promptN int) float64 {
	const lo, loRate = 59, 110.0
	const hi, hiRate = 6000, 85.0
	if promptN <= lo {
		return loRate
	}
	if promptN >= hi {
		return hiRate
	}
	return loRate + (hiRate-loRate)*float64(promptN-lo)/float64(hi-lo)
}

func matOpenAIServer(t *testing.T, cfg matOpenAICfg) (*httptest.Server, *matOpenAI) {
	t.Helper()
	f := &matOpenAI{}
	mux := http.NewServeMux()
	for _, path := range []string{"/props", "/slots", "/tokenize", "/completion"} {
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			http.NotFound(w, r)
		})
	}
	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, r *http.Request) {
		if cfg.emptyModels {
			_, _ = w.Write([]byte(`{"object":"list","data":[]}`))
			return
		}
		if cfg.advertiseMaxLen {
			_, _ = fmt.Fprintf(w, `{"object":"list","data":[{"id":"mat-model","object":"model","owned_by":"vllm","max_model_len":%d}]}`, cfg.ctxLimit)
			return
		}
		_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"mat-model","object":"model"}]}`))
	})
	if cfg.apiTags {
		mux.HandleFunc("/api/tags", func(w http.ResponseWriter, r *http.Request) {
			if cfg.emptyModels {
				_, _ = w.Write([]byte(`{"models":[]}`))
				return
			}
			_, _ = w.Write([]byte(`{"models":[{"name":"mat-model:latest"}]}`))
		})
	}
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.posts++
		f.mu.Unlock()
		if cfg.emptyModels {
			// The real server's own sentence, byte for byte as measured
			// (2026-09-21, Ollama 0.33.3): toktape sent no model id because
			// the listing carried none to pick.
			matWriteStatus(w, http.StatusBadRequest, `{"error":{"message":"model is required","type":"invalid_request_error","param":null,"code":null}}`)
			return
		}
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
		promptTokens := matTokens(b.String())
		maxTokens := 0
		if in.MaxTokens != nil {
			maxTokens = *in.MaxTokens
		}
		if cfg.ctxLimit > 0 && promptTokens+maxTokens > cfg.ctxLimit {
			body := fmt.Sprintf(
				`{"error":{"message":"This model's maximum context length is %d tokens. However, you requested %d tokens (%d in your messages, %d in the completion). Please reduce the length of the messages or completion.","type":"BadRequestError","code":400}}`,
				cfg.ctxLimit, promptTokens+maxTokens, promptTokens, maxTokens)
			matWriteStatus(w, http.StatusBadRequest, body)
			return
		}
		reported := promptTokens
		if cfg.truncateTo > 0 && promptTokens > cfg.truncateTo {
			reported = cfg.truncateTo
		}
		perToken := time.Duration(0)
		if cfg.decodeTokPerSec != nil {
			if rate := cfg.decodeTokPerSec(promptTokens); rate > 0 {
				perToken = time.Duration(float64(time.Second) / rate)
			}
		}
		matWriteOpenAIChat(w, reported, cfg.chatTokens, maxTokens, perToken)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, f
}

func (f *matOpenAI) postsSeen() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.posts
}

// ---------------------------------------------------------------------------
// SSE emitters. The shapes are the recorded fixtures' (internal/server/
// testdata): timings ride every content chunk, the way llama-server sends
// them under timings_per_token, so a stream cut mid-answer still carries its
// last figures.

func matFlush(w http.ResponseWriter) {
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

func matChunk(w http.ResponseWriter, payload string) bool {
	_, err := fmt.Fprintf(w, "data: %s\n\n", payload)
	matFlush(w)
	return err == nil
}

// matWriteChat answers one llama-shaped chat request: a role chunk, nTok
// content deltas each carrying timings, the stop chunk, [DONE].
//
// The decode timings are measured, not planted: predicted_ms is the handler's
// own clock since the first chunk, the way a real server reports the rate it
// actually produced. A planted constant cannot agree with the client's clock
// through flush and loopback overhead, and the card's identity checks
// (streamsMultiply, 2 %) assume the two agree on a healthy stream.
func matWriteChat(w http.ResponseWriter, promptN int, promptMs float64, nTok int, sleep time.Duration) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)
	matChunk(w, `{"id":"m","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}`)
	start := time.Now()
	for i := 1; i <= nTok; i++ {
		if !matChunk(w, `{"id":"m","choices":[{"index":0,"delta":{"content":"word"},"finish_reason":null}],`+matTimings(promptN, promptMs, i, msSince(start))+`}`) {
			return
		}
		if sleep > 0 {
			time.Sleep(sleep)
		}
	}
	matChunk(w, `{"id":"m","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],`+matTimings(promptN, promptMs, nTok, msSince(start))+`}`)
	fmt.Fprint(w, "data: [DONE]\n\n")
	matFlush(w)
}

// matWriteReasoningForever emits reasoning_content deltas until the client
// stops reading — the reasoning model under a clock it cannot outpace. Every
// chunk carries timings, so the cut record keeps the server's own count. The
// wall guard is a backstop only: the client's clock cancels the request, the
// next write fails, and the handler returns long before it fires.
func matWriteReasoningForever(w http.ResponseWriter, promptN int, promptMs float64, sleep time.Duration) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)
	matChunk(w, `{"id":"m","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}`)
	start := time.Now()
	deadline := start.Add(10 * time.Second)
	for i := 1; i <= 100000; i++ {
		if time.Now().After(deadline) {
			return
		}
		if !matChunk(w, `{"id":"m","choices":[{"index":0,"delta":{"reasoning_content":"hm"},"finish_reason":null}],`+matTimings(promptN, promptMs, i, msSince(start))+`}`) {
			return
		}
		if sleep > 0 {
			time.Sleep(sleep)
		}
	}
}

// matCutMidAnswer writes 50 tokens and then cuts the connection without a
// finish chunk (llama-server dying, or the network doing so).
func matCutMidAnswer(w http.ResponseWriter, sleep time.Duration) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)
	matChunk(w, `{"id":"m","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}`)
	start := time.Now()
	for i := 1; i <= 50; i++ {
		if !matChunk(w, `{"id":"m","choices":[{"index":0,"delta":{"content":"word"},"finish_reason":null}],`+matTimings(4000, 100, i, msSince(start))+`}`) {
			return
		}
		if sleep > 0 {
			time.Sleep(sleep)
		}
	}
	panic(http.ErrAbortHandler)
}

// msSince is the handler's own clock, for measured decode timings.
func msSince(start time.Time) float64 {
	return float64(time.Since(start)) / float64(time.Millisecond)
}

// matTimings builds one chunk's timings object: the prompt side planted (the
// probe's fit reads it), the decode side measured since start.
func matTimings(promptN int, promptMs float64, predictedN int, predictedMs float64) string {
	pps := 0.0
	if promptMs > 0 {
		pps = float64(promptN) / (promptMs / 1000)
	}
	ppsTok := 0.0
	if predictedMs > 0 {
		ppsTok = float64(predictedN) / (predictedMs / 1000)
	}
	return fmt.Sprintf(`"timings":{"prompt_n":%d,"prompt_ms":%s,"prompt_per_second":%s,"predicted_n":%d,"predicted_ms":%s,"predicted_per_second":%s,"cache_n":0}`,
		promptN, strconv.FormatFloat(promptMs, 'f', -1, 64), strconv.FormatFloat(pps, 'f', -1, 64),
		predictedN, strconv.FormatFloat(predictedMs, 'f', -1, 64), strconv.FormatFloat(ppsTok, 'f', -1, 64))
}

// matWriteCompletion answers one probe /completion request: the raw
// endpoint's own chunk shape, the final timings on the stop chunk.
func matWriteCompletion(w http.ResponseWriter, promptN int, promptMs float64) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)
	time.Sleep(2 * time.Millisecond)
	pps := 0.0
	if promptMs > 0 {
		pps = float64(promptN) / (promptMs / 1000)
	}
	fmt.Fprintf(w, "data: {\"content\":\"ok\",\"stop\":false}\n\n")
	fmt.Fprintf(w, "data: {\"content\":\"\",\"stop\":true,\"stop_type\":\"limit\",\"timings\":{\"prompt_n\":%d,\"prompt_ms\":%s,\"prompt_per_second\":%s,\"predicted_n\":1,\"predicted_ms\":5.0,\"predicted_per_second\":200.0,\"cache_n\":0}}\n\n",
		promptN, strconv.FormatFloat(promptMs, 'f', -1, 64), strconv.FormatFloat(pps, 'f', -1, 64))
	matFlush(w)
}

// matWriteOpenAIChat answers one generic OpenAI chat request: content deltas,
// a finish chunk carrying usage, [DONE]. The stream ends at the request's own
// cap or the model's EOS (eosTok), whichever comes first — finish_reason says
// which — and usage rides only that closing chunk: a stream the client cut
// never says how many tokens it sent, the way real Ollama counts (measured
// 2026-09-21: only a closing usage message carries the count, so a cut costs
// the entire measurement, not part of it). perToken spaces the deltas
// (0 = as fast as the loop writes them).
func matWriteOpenAIChat(w http.ResponseWriter, promptTokens, eosTok, cap int, perToken time.Duration) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)
	matChunk(w, `{"id":"m","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}`)
	n, finish := eosTok, "stop"
	if cap > 0 && cap < eosTok {
		n, finish = cap, "length"
	}
	for i := 0; i < n; i++ {
		if !matChunk(w, `{"id":"m","choices":[{"index":0,"delta":{"content":"w"},"finish_reason":null}]}`) {
			return
		}
		if perToken > 0 {
			time.Sleep(perToken)
		}
	}
	matChunk(w, fmt.Sprintf(`{"id":"m","choices":[{"index":0,"delta":{},"finish_reason":"%s"}],"usage":{"prompt_tokens":%d,"completion_tokens":%d}}`, finish, promptTokens, n))
	fmt.Fprint(w, "data: [DONE]\n\n")
	matFlush(w)
}

// matWriteCtxRefused ends a stream with the server's own refusal, the shape
// the failed-stream fixtures use: an error frame inside the SSE body.
func matWriteCtxRefused(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, "data: {\"error\":{\"code\":500,\"message\":\"context_length_exceeded: the request exceeds the available context size\",\"type\":\"server_error\"}}\n\n")
	matFlush(w)
}

func matWriteStatus(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(body))
}

// ---------------------------------------------------------------------------
// Rows and verdicts.

// matOutcome is one scenario's evidence.
type matOutcome struct {
	exit   int
	stdout string
	stderr string
	// tapes holds every take's tape; empty when none was written.
	tapes []*tape.Tape
	// streamsSent says whether any request the run measured was sent — the
	// difference between a refusal and a failure.
	streamsSent bool
	// fakeKnowsPrompt says the fake reports real prompt figures, so the
	// USABLE rule's prompt_n clause applies to what the tape carries.
	fakeKnowsPrompt bool
}

// matRow is one scenario of the matrix.
type matRow struct {
	name string
	// wantUsable pins the USABLE verdict when non-empty.
	wantUsable string
	// wantExplained pins the EXPLAINED verdict when non-empty (and the row
	// must be one whose verdict is assessed at all).
	wantExplained string
	// gapWhy, when non-empty, puts the row in knownGaps with this reason
	// even if its pinned verdicts hold.
	gapWhy string
	// causeMarkers are the strings that must appear, user-visibly, for the
	// cause to count as explained: the server's own sentence, or the plain
	// statement of the observed fact. Empty means no wording this audit
	// would call recognisable ever reaches the user.
	causeMarkers []string
	// leverMismatch, when non-empty, downgrades EXPLAINED to partial: a
	// lever is printed but it answers a different problem than the one that
	// happened (the spec's "names a flag the user's server does not have").
	leverMismatch string
	run           func(t *testing.T) *matOutcome
}

// matLeverRe is what counts as a concrete next action: a toktape flag, one
// of llama-server's own settings, or a toktape command.
var matLeverRe = regexp.MustCompile(`--[a-z][a-z0-9-]+|-np\b|-c\b|toktape (card|play|compare|record|log|ls|render)`)

// usableVerdict applies the USABLE rule to one outcome.
func (o *matOutcome) usableVerdict() string {
	if o.exit == exitOK && len(o.tapes) > 0 {
		good := true
		for _, tp := range o.tapes {
			if tp.Summary.Aggregate.StreamsFailed != 0 {
				good = false
			}
			for _, rec := range tp.Requests {
				if rec.Timings.PredictedN < 64 {
					good = false
				}
				if o.fakeKnowsPrompt && rec.Timings.PromptN < 256 {
					good = false
				}
			}
		}
		if good {
			return "yes"
		}
	}
	if o.exit != exitOK && !o.streamsSent {
		return "refused-cleanly"
	}
	return "no"
}

// degraded reports whether any tape carries a caveat that changes what the
// headline figures mean — the audit's line between "nothing owed" and "the
// output owes the user an explanation".
func (o *matOutcome) degraded() bool {
	for _, tp := range o.tapes {
		for _, c := range card.Caveats(&tp.Summary) {
			if c.Severity == card.SeverityFigure {
				return true
			}
		}
	}
	return false
}

// explainedVerdict applies the EXPLAINED rule: the cause in recognisable
// words, and a concrete next action, both somewhere the user actually looks —
// the failure door's sentence and hint, the share block's failure lines, or
// the card's caveat sentences. The share block's other arrows ("Post it",
// "Attach the .tape") are not searched: they are about a good run, and would
// make every row pass vacuously.
func (o *matOutcome) explainedVerdict(row *matRow) (verdict, reason string) {
	scope := o.failureScope()
	hay := strings.Join(scope, "\n")
	causeOK := len(row.causeMarkers) > 0
	for _, m := range row.causeMarkers {
		if !strings.Contains(hay, m) {
			causeOK = false
		}
	}
	lever := matLeverRe.MatchString(hay)
	switch {
	case row.leverMismatch != "":
		return "partial", row.leverMismatch
	case causeOK && lever:
		return "yes", ""
	case causeOK:
		return "partial", "the cause is stated but no concrete next action (flag, command or server setting) accompanies it"
	default:
		return "no", "the cause never appears in words a user would recognise"
	}
}

// failureScope collects the sentences a user reads when a run went wrong.
func (o *matOutcome) failureScope() []string {
	var scope []string
	lines := strings.Split(o.stderr, "\n")
	// The failure door (cli.fail): the sentence, then its hint arrow.
	for i, l := range lines {
		if strings.HasPrefix(l, "toktape: ") {
			scope = append(scope, l)
			if i+1 < len(lines) && strings.HasPrefix(lines[i+1], "→ ") {
				scope = append(scope, lines[i+1])
			}
			break
		}
	}
	// The share block's failure lines (streamFailureBlock): the count with
	// the server's words, then the lever arrow.
	for i, l := range lines {
		if strings.HasPrefix(l, "✗") {
			scope = append(scope, l)
			if i+1 < len(lines) && strings.HasPrefix(lines[i+1], "→ ") {
				scope = append(scope, lines[i+1])
			}
		}
	}
	// The card's caveat sentences (stdout).
	for _, tp := range o.tapes {
		for _, c := range card.Caveats(&tp.Summary) {
			scope = append(scope, c.Text)
		}
	}
	return scope
}

// matRunOnce runs one invocation and reads back the newest tape it wrote.
func matRunOnce(t *testing.T, outDir string, args ...string) (int, string, string, *tape.Tape) {
	t.Helper()
	code, stdout, stderr := exec(t, args...)
	tapes, err := filepath.Glob(filepath.Join(outDir, "*"+tape.Ext))
	if err != nil {
		t.Fatalf("glob tapes: %v", err)
	}
	if len(tapes) == 0 {
		return code, stdout, stderr, nil
	}
	// Run IDs are <date>-<time>-<model>, so lexical order is chronological:
	// the last tape is the run that just finished.
	sort.Strings(tapes)
	tp, err := tape.Read(tapes[len(tapes)-1])
	if err != nil {
		t.Fatalf("read tape %s: %v", tapes[len(tapes)-1], err)
	}
	return code, stdout, stderr, tp
}

// matRecord runs one row's invocation the way a first-time user types it.
func matRecord(t *testing.T, url, outDir string, extra ...string) *matOutcome {
	t.Helper()
	code, stdout, stderr, tp := matRunOnce(t, outDir, append([]string{"--url", url, "--out", outDir}, extra...)...)
	return &matOutcome{
		exit:            code,
		stdout:          stdout,
		stderr:          stderr,
		tapes:           tapeOrNil(tp),
		fakeKnowsPrompt: true,
	}
}

func tapeOrNil(tp *tape.Tape) []*tape.Tape {
	if tp == nil {
		return nil
	}
	return []*tape.Tape{tp}
}

// matSight picks the lines the research doc quotes verbatim: what the run
// said about itself, in its own words, at most five.
func matSight(o *matOutcome) []string {
	var out []string
	add := func(s string) {
		s = strings.TrimSpace(s)
		if len(s) > 160 {
			s = s[:157] + "…"
		}
		if s != "" && len(out) < 5 {
			out = append(out, s)
		}
	}
	lines := strings.Split(o.stderr, "\n")
	for i, l := range lines {
		if strings.HasPrefix(l, "toktape: ") {
			add(l)
			if i+1 < len(lines) && strings.HasPrefix(lines[i+1], "→ ") {
				add(lines[i+1])
			}
			break
		}
	}
	for i, l := range lines {
		if strings.HasPrefix(l, "✗") {
			add(l)
			if i+1 < len(lines) && strings.HasPrefix(lines[i+1], "→ ") {
				add(lines[i+1])
			}
		}
		if strings.HasPrefix(l, "· plan:") {
			add(l)
		}
	}
	for _, l := range lines {
		if strings.HasPrefix(l, "→ llama") || strings.HasPrefix(l, "→ ik_") || strings.HasPrefix(l, "→ ?") || strings.HasPrefix(l, "→ openai") {
			add(l)
			break
		}
	}
	// The card draws its caveat line inside the box, after the border and
	// its padding, so the marker is matched mid-line rather than anchored.
	// When no caveat line exists (a clean card), the card's own first line
	// stands in, so the doc always quotes what the run looked like.
	sawCaveat := false
	for _, l := range strings.Split(o.stdout, "\n") {
		if strings.Contains(l, "! ") {
			add(l)
			sawCaveat = true
			break
		}
	}
	if !sawCaveat {
		for _, l := range strings.Split(o.stdout, "\n") {
			if strings.TrimSpace(l) != "" {
				add(l)
				break
			}
		}
	}
	return out
}

// matRowSummary is the one-line-per-figure block the table is built from.
func matRowSummary(o *matOutcome) string {
	if len(o.tapes) == 0 {
		return "(no tape written)"
	}
	var parts []string
	for i, tp := range o.tapes {
		s := tp.Summary
		minPrompt, minPred := -1, -1
		for _, rec := range tp.Requests {
			if minPrompt < 0 || rec.Timings.PromptN < minPrompt {
				minPrompt = rec.Timings.PromptN
			}
			if minPred < 0 || rec.Timings.PredictedN < minPred {
				minPred = rec.Timings.PredictedN
			}
		}
		plan := "no plan"
		if s.Plan != nil {
			plan = fmt.Sprintf("plan %s target %d slot %d tokenized %v", s.Plan.Binding, s.Plan.TargetTokens, s.Plan.SlotCtx, s.Plan.Tokenized)
		}
		var codes []string
		for _, c := range card.Caveats(&s) {
			codes = append(codes, c.Code)
		}
		label := ""
		if len(o.tapes) > 1 {
			label = fmt.Sprintf("take%d: ", i+1)
		}
		parts = append(parts, fmt.Sprintf("%sstreams %d failed %d · min prompt_n %d · min predicted_n %d · %s · caveats %v",
			label, s.Aggregate.Streams, s.Aggregate.StreamsFailed, minPrompt, minPred, plan, codes))
	}
	return strings.Join(parts, " | ")
}

// TestFirstRunMatrix is the audit itself. See the file's top comment for the
// two verdict rules; the rows below are named exactly as the research doc's
// table.
func TestFirstRunMatrix(t *testing.T) {
	// fast is the hero shape: four 8192 slots under a 32768 -c, a tokenizer,
	// a 64 000 tok/s prefill and an exact-text prefix cache. The streams are
	// 1000 tokens at 500 µs each — half a second of decode, an order of
	// magnitude more than the few-ms arrival stagger between four concurrent
	// posts, so a healthy row reads healthy (the card's ragged/identity
	// checks are 2 %; a 25 ms fake stream is stagger in disguise).
	fast := matLlamaCfg{
		totalSlots: 4, propsNCtx: 32768,
		slotNCtx:     []int{8192, 8192, 8192, 8192},
		tokenize:     true,
		perTokMs:     matFastPerTokMs,
		fixedMs:      matFastFixedMs,
		promptPerSec: 64000,
		enforceCtx:   8192,
		chatTokens:   1000,
		tokenSleep:   500 * time.Microsecond,
		cache:        true,
	}

	rows := []matRow{
		{
			name:         "llama-4slot-8k",
			wantUsable:   "yes",
			causeMarkers: []string{"context_length_exceeded"},
			run: func(t *testing.T) *matOutcome {
				srv, _ := matLlamaServer(t, fast)
				return matRecord(t, srv.URL, t.TempDir(), "--sessions", "4", "--for", "2s")
			},
		},
		{
			name:         "llama-1slot-4k",
			wantUsable:   "yes",
			causeMarkers: []string{"context_length_exceeded"},
			run: func(t *testing.T) *matOutcome {
				cfg := fast
				cfg.totalSlots, cfg.propsNCtx = 1, 4096
				cfg.slotNCtx = []int{4096}
				cfg.enforceCtx = 4096
				srv, _ := matLlamaServer(t, cfg)
				return matRecord(t, srv.URL, t.TempDir()) // the default command, one stream
			},
		},
		{
			name:         "llama-4slot-2k",
			wantUsable:   "yes",
			causeMarkers: []string{"context_length_exceeded"},
			run: func(t *testing.T) *matOutcome {
				cfg := fast
				cfg.slotNCtx = []int{2048, 2048, 2048, 2048}
				cfg.enforceCtx = 2048
				srv, _ := matLlamaServer(t, cfg)
				return matRecord(t, srv.URL, t.TempDir(), "--sessions", "4", "--for", "2s")
			},
		},
		{
			name:          "llama-sessions-gt-slots",
			wantUsable:    "refused-cleanly",
			wantExplained: "yes",
			causeMarkers:  []string{"would wait in its queue"},
			run: func(t *testing.T) *matOutcome {
				cfg := fast
				cfg.totalSlots, cfg.propsNCtx = 2, 32768
				cfg.slotNCtx = []int{8192, 8192}
				srv, f := matLlamaServer(t, cfg)
				o := matRecord(t, srv.URL, t.TempDir(), "--sessions", "4")
				o.streamsSent = f.postsSeen() > 0
				return o
			},
		},
		{
			name: "llama-no-slots-endpoint",
			// Re-pinned 2026-09-21: the recorder reads n_ctx from /props when
			// /slots is refused, so the run finishes (was exit 3, every
			// stream lost on context_length_exceeded).
			wantUsable:   "yes",
			causeMarkers: []string{"context_length_exceeded"},
			run: func(t *testing.T) *matOutcome {
				cfg := fast
				cfg.noSlots501 = true
				cfg.propsNCtx = 4096
				cfg.enforceCtx = 4096 // the fake enforces what the server knows and toktape cannot read
				srv, f := matLlamaServer(t, cfg)
				o := matRecord(t, srv.URL, t.TempDir())
				o.streamsSent = f.postsSeen() > 0
				return o
			},
		},
		{
			name:         "llama-no-tokenize",
			wantUsable:   "yes",
			causeMarkers: []string{"context_length_exceeded"},
			run: func(t *testing.T) *matOutcome {
				cfg := fast
				cfg.tokenize = false
				srv, _ := matLlamaServer(t, cfg)
				return matRecord(t, srv.URL, t.TempDir())
			},
		},
		{
			name:          "llama-thinking-default",
			wantUsable:    "yes",
			wantExplained: "yes",
			causeMarkers:  []string{"answer cut"},
			run: func(t *testing.T) *matOutcome {
				cfg := fast
				cfg.reasoningForever = true
				cfg.tokenSleep = 2 * time.Millisecond
				srv, _ := matLlamaServer(t, cfg)
				return matRecord(t, srv.URL, t.TempDir(), "--for", "2s")
			},
		},
		{
			name:         "llama-slow-box",
			wantUsable:   "yes",
			causeMarkers: []string{"context_length_exceeded"},
			run: func(t *testing.T) *matOutcome {
				cfg := fast
				cfg.perTokMs = 1000.0 / matSlowPerSec // 30 tok/s: prompt_ms = n/30 s
				cfg.promptPerSec = matSlowPerSec
				srv, _ := matLlamaServer(t, cfg)
				return matRecord(t, srv.URL, t.TempDir(), "--for", "2s")
			},
		},
		{
			name:         "llama-rerun-same-server",
			wantUsable:   "yes",
			causeMarkers: []string{"context_length_exceeded"},
			run: func(t *testing.T) *matOutcome {
				srv, _ := matLlamaServer(t, fast)
				out := t.TempDir()
				c1, _, se1, tp1 := matRunOnce(t, out, "--url", srv.URL, "--out", out, "--sessions", "4", "--for", "2s")
				c2, so2, se2, tp2 := matRunOnce(t, out, "--url", srv.URL, "--out", out, "--sessions", "4", "--for", "2s")
				exit := c2
				if c1 != exitOK {
					exit = c1
				}
				return &matOutcome{
					exit:            exit,
					stdout:          so2,
					stderr:          se1 + "\n-- second take --\n" + se2,
					tapes:           append(tapeOrNil(tp1), tapeOrNil(tp2)...),
					fakeKnowsPrompt: true,
				}
			},
		},
		{
			name: "openai-only-32k",
			// Re-pinned 2026-09-21: usage.prompt_tokens is recorded
			// (PromptNSource "usage") and the calibrated cap lets the stream
			// end before the clock, so the run has a count and a rate.
			wantUsable: "yes",
			run: func(t *testing.T) *matOutcome {
				srv, _ := matOpenAIServer(t, matOpenAICfg{chatTokens: 80})
				return matRecord(t, srv.URL, t.TempDir())
			},
		},
		{
			name:          "openai-only-4k",
			wantExplained: "yes", // pinned 2026-09-21: the exit-3 door names the context lever for an unknown engine (all three spellings)
			gapWhy:        "usable stays no: the OpenAI path never lowers the 8000-token runaway guard (no slots to read), so a small-context vLLM refuses every stream up front — a pre-flight cap for OpenAI-kind runs is the recorder's to write (owner: recorder track)",
			causeMarkers:  []string{"maximum context length"},
			run: func(t *testing.T) *matOutcome {
				srv, f := matOpenAIServer(t, matOpenAICfg{ctxLimit: 4096, chatTokens: 80})
				o := matRecord(t, srv.URL, t.TempDir())
				o.streamsSent = f.postsSeen() > 0
				return o
			},
		},
		{
			// Added 2026-09-21: the shape a real vLLM has — the listing
			// states the context, so the plan trims and caps inside it and
			// the default command finishes. openai-only-4k above stays the
			// server that says nothing until it refuses.
			name:       "vllm-4k-advertised",
			wantUsable: "yes",
			run: func(t *testing.T) *matOutcome {
				srv, _ := matOpenAIServer(t, matOpenAICfg{ctxLimit: 4096, chatTokens: 80, advertiseMaxLen: true})
				return matRecord(t, srv.URL, t.TempDir())
			},
		},
		{
			// 2026-09-21 (TTP-172): the fake now fails the way real Ollama
			// fails — usage only on a stream that ended on its own, decode
			// paced at the measured ~110 tok/s (short prompt) to ~85 tok/s
			// (~6k-token prompt) curve — where before it stopped at 80
			// tokens at full speed and so always ended on its own and could
			// never lose the race the real server loses. The model's EOS
			// (chatTokens) is past any clock: what ends the stream is the
			// request's cap or the run's clock, whichever comes first. The
			// row's verdict is whatever the recorder under test honestly
			// does with that.
			//
			// PINNED 2026-09-21, and the pin is the point (lead). Written
			// against the recorder as it then stood, this row FLIPPED: the
			// cap was 0.8 x the calibration's short-prompt rate x the whole
			// clock, which needs ~20.5 s of long-prompt decode against a
			// 20 s clock, so it was cut and lost 5 runs in 7. Real Ollama
			// lost the same race 3 of 6 on a real Mac the same day. The two
			// were measured from opposite ends — a fake written from the
			// real server's shape, and the real server — and they agreed,
			// which is what says the fake is honest rather than merely
			// harsh.
			//
			// TTP-170 then re-priced the cap (0.65 below the measured floor
			// of the long-context slowdown, a prefill reserve when no point
			// was measured, and a bounded grace on the clock so this kind's
			// cut cannot destroy the measurement). Pinned usable-yes after
			// 5 consecutive runs. If it ever flips back, the pricing
			// regressed — that is the whole reason this row is no longer a
			// gap but an assertion.
			name:       "ollama-shape",
			wantUsable: "yes",
			run: func(t *testing.T) *matOutcome {
				srv, _ := matOpenAIServer(t, matOpenAICfg{
					truncateTo: 2048, chatTokens: 100000, apiTags: true,
					decodeTokPerSec: matOllamaTokPerSec,
				})
				return matRecord(t, srv.URL, t.TempDir())
			},
		},
		{
			// Added 2026-09-21 (TTP-172): the server that is healthy but
			// empty — every Ollama reader's first five minutes. Measured on
			// real Windows 11 + Ollama 0.33.3 with no model pulled: the run
			// reached the chat endpoint, sent no model id (the listing had
			// none to pick), and the server answered the 400 quoted in the
			// fake. The old lever at this door named "its slot count", a
			// llama.cpp concept this server does not have.
			name:       "ollama-empty",
			wantUsable: "no",
			// Pinned 2026-09-21: the server's own sentence is quoted and the
			// lever names what to type — ollama pull, or --model — instead.
			wantExplained: "yes",
			causeMarkers:  []string{"model is required"},
			run: func(t *testing.T) *matOutcome {
				srv, f := matOpenAIServer(t, matOpenAICfg{emptyModels: true, apiTags: true})
				o := matRecord(t, srv.URL, t.TempDir())
				o.streamsSent = f.postsSeen() > 0
				return o
			},
		},
		{
			name:          "server-not-running",
			wantUsable:    "refused-cleanly",
			wantExplained: "yes",
			causeMarkers:  []string{"connection refused"},
			run: func(t *testing.T) *matOutcome {
				return matRecord(t, "http://127.0.0.1:1", t.TempDir())
			},
		},
		{
			name:          "server-loading",
			wantUsable:    "refused-cleanly",
			wantExplained: "yes",
			causeMarkers:  []string{"Loading model"},
			run: func(t *testing.T) *matOutcome {
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					matWriteStatus(w, http.StatusServiceUnavailable, `{"error":{"message":"Loading model","type":"server_error"}}`)
				}))
				t.Cleanup(srv.Close)
				// --wait 1s stands in for the default 10m patience so the
				// row stays fast; the wait, the give-up sentence and the
				// hint are the same shape.
				return matRecord(t, srv.URL, t.TempDir(), "--wait", "1s")
			},
		},
		{
			name:          "wrong-port-http-200-html",
			wantUsable:    "refused-cleanly",
			wantExplained: "yes",
			causeMarkers:  []string{"not JSON"},
			run: func(t *testing.T) *matOutcome {
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "text/html")
					_, _ = w.Write([]byte("<html><body><h1>It works!</h1></body></html>"))
				}))
				t.Cleanup(srv.Close)
				return matRecord(t, srv.URL, t.TempDir())
			},
		},
		{
			name: "auth-required",
			// Re-pinned 2026-09-21: 401/403 is server.ErrUnauthorized, and the
			// hint says the server wants a key and that toktape has no flag
			// for one yet — true, and the flag itself is the open half.
			wantUsable:   "refused-cleanly",
			gapWhy:       "explained honestly, but the lever is an absence: toktape has no --api-key/--header flag yet",
			causeMarkers: []string{"Invalid API key"},
			run: func(t *testing.T) *matOutcome {
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					matWriteStatus(w, http.StatusUnauthorized, `{"error":{"message":"Invalid API key"}}`)
				}))
				t.Cleanup(srv.Close)
				return matRecord(t, srv.URL, t.TempDir())
			},
		},
		{
			name:          "rate-limited",
			wantExplained: "yes", // pinned 2026-09-21: the server's words plus 'fewer --sessions' held through the share-block changes
			gapWhy:        "two of four streams are refused with 429 and the explanation is good (server words + 'fewer --sessions'), but the process exits 0 — a wrapper script cannot tell this run from a clean one; the exit code is the lead's call, not this track's",
			causeMarkers:  []string{"Too Many Requests"},
			run: func(t *testing.T) *matOutcome {
				cfg := fast
				cfg.chatGate = func(call int) matGate {
					if call >= 2 {
						return matGate429
					}
					return matGateNormal
				}
				srv, _ := matLlamaServer(t, cfg)
				return matRecord(t, srv.URL, t.TempDir(), "--sessions", "4")
			},
		},
		{
			name:   "stream-dies-midway",
			gapWhy: "one of four streams is cut mid-answer: exit 0; the ✗ line and the lever now both say the likely cause in plain words (the connection closed mid-answer — the server may have crashed or been killed, out of memory the usual one), but no toktape flag or command exists for a crashed server, so the verdict stays partial honestly — a lever the rule counts would have to be invented first, and whether one ever exists is the lead's call",
			// causeMarkers added 2026-09-21 with the lever that names the
			// cause in plain words; before it, no wording this audit would
			// call recognisable ever reached the user.
			causeMarkers: []string{"crashed or been killed"},
			run: func(t *testing.T) *matOutcome {
				cfg := fast
				cfg.chatGate = func(call int) matGate {
					if call == 0 {
						return matGateCut
					}
					return matGateNormal
				}
				srv, _ := matLlamaServer(t, cfg)
				return matRecord(t, srv.URL, t.TempDir(), "--sessions", "4")
			},
		},
		{
			// Pinned 2026-09-21: the short_generation caveat names --prompt and
			// the closing block says the run is not a usable measurement. The
			// row stays deliberately NOT usable — a model that answers in 8
			// tokens is honestly a sample, whatever the tool prints.
			name:          "model-answers-instantly",
			wantUsable:    "no",
			wantExplained: "yes",
			causeMarkers:  []string{"short generation"},
			run: func(t *testing.T) *matOutcome {
				cfg := fast
				cfg.chatTokens = 8
				srv, _ := matLlamaServer(t, cfg)
				return matRecord(t, srv.URL, t.TempDir())
			},
		},
	}

	type gap struct{ name, why string }
	var knownGaps []gap

	for _, row := range rows {
		row := row
		t.Run(row.name, func(t *testing.T) {
			hermetic(t)
			o := row.run(t)

			// Structural facts, every row: the invocation completed inside
			// the CLI's exit-code contract, and produced output.
			if o.exit != exitOK && o.exit != exitUsage && o.exit != exitUnreachable && o.exit != exitStreams {
				t.Errorf("exit %d is outside the CLI's contract", o.exit)
			}
			if o.stderr == "" && o.stdout == "" {
				t.Error("the scenario produced no output on either stream")
			}

			usable := o.usableVerdict()
			explained, whyE := "n/a", ""
			assessed := usable != "yes" || o.degraded()
			if assessed {
				explained, whyE = o.explainedVerdict(&row)
			}

			t.Logf("EXIT %d · USABLE %s · EXPLAINED %s %s", o.exit, usable, explained, reasonClause(whyE))
			if os.Getenv("MAT_RAW") != "" {
				t.Logf("RAW:\n%s", o.stderr)
			}
			t.Logf("  %s", matRowSummary(o))
			for _, l := range matSight(o) {
				t.Logf("  | %s", l)
			}

			// The pins: rows whose verdict is already good hold it.
			if row.wantUsable != "" && usable != row.wantUsable {
				t.Errorf("USABLE = %q, want %q (pinned verdict regressed)", usable, row.wantUsable)
			}
			if row.wantExplained != "" {
				if !assessed {
					t.Errorf("EXPLAINED was not assessed (usable %q, degraded %v) but the row pins %q",
						usable, o.degraded(), row.wantExplained)
				} else if explained != row.wantExplained {
					t.Errorf("EXPLAINED = %q (%s), want %q", explained, whyE, row.wantExplained)
				}
			}

			// Everything else is a gap, logged for the lead, not asserted.
			if row.gapWhy != "" || row.wantUsable == "" || (row.wantExplained == "" && assessed) {
				why := row.gapWhy
				if why == "" {
					why = fmt.Sprintf("usable %s, explained %s %s", usable, explained, reasonClause(whyE))
				}
				knownGaps = append(knownGaps, gap{row.name, why})
			}
		})
	}

	// The gap list is the audit's output, in row order, once more at the end
	// for the lead's table.
	if len(knownGaps) > 0 {
		t.Logf("knownGaps: %d rows", len(knownGaps))
		for _, g := range knownGaps {
			t.Logf("  knownGaps %s: %s", g.name, g.why)
		}
	} else {
		t.Logf("knownGaps: none")
	}
}

func reasonClause(why string) string {
	if why == "" {
		return ""
	}
	return "(" + why + ")"
}
