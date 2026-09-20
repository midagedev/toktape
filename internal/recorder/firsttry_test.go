package recorder_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/midagedev/toktape/internal/gpu"
	"github.com/midagedev/toktape/internal/recorder"
	"github.com/midagedev/toktape/internal/tape"
)

// The first-try gate (lead, 2026-09-20). The default `toktape --sessions 4`
// against the day's real server (ik_llama.cpp, Qwen3.6-35B, -c 32768 -np 4,
// slots of 8192) produced no usable tape four times running: prompts were
// not trimmed, four streams prefilled one after another for 17.5 of the 20
// seconds, the densest prompt plus its answer overran its slot
// (context_length_exceeded, stream lost), and a second take on the same
// server was served whole from the prefix cache ("5 prompt tokens", a card
// that said 0% hit). The fake below models exactly that server; the gates
// assert the run that meets it is planned, not improvised.
//
// The cost model is planted so the whole gate stays under two seconds of
// wall clock: a 2 s clock with a 16 000 tok/s probe fit (0.0625 ms/token,
// binary-exact so the plan's arithmetic is too) stands in for the day's
// 20 s clock at 3 023 tok/s. The ratios the plan must survive are the same.

// firstTryCosts is the planted machine: the per-token and fixed prefill
// costs the fit recovers. 0.0625 ms/token is 16 000 tok/s at the margin.
const (
	firstTryPerTokMs = 0.0625
	firstTryFixedMs  = 25.0
	// firstTryNCtx is the slot context, -c 32768 divided by -np 4.
	firstTryNCtx = 8192
	// firstTryTemplate is what the fake counts the chat template as adding.
	firstTryTemplate = 100
)

// firstTryTokens is the fake tokenizer: ceil(bytes / 2.5). 2.5 bytes/token
// sits inside the published set's measured band (2.43 to 3.70 on the day's
// real tokenizer), dense enough that whole prompts overrun the slot.
func firstTryTokens(s string) int { return (2*len(s) + 4) / 5 }

// firstTry is the stateful half of the fake: a prefix cache keyed on the
// exact prompt text, and a mutex that serialises concurrent prefills the
// way the day's four streams queued behind each other. The cost is
// computed, never slept: prompt_ms is the server's own figure and the gate
// asserts on it, not on wall time.
type firstTry struct {
	mu         sync.Mutex
	served     map[string]bool
	enforceCtx bool
}

// firstTryMux is the whole server the default run meets: /props with eight
// slots, /slots reporting 8192 a slot, /tokenize, the raw /completion the
// probe pass uses, and a chat route that refuses a prompt whose answer cap
// cannot fit the slot — the refusal the day's takes died on — unless
// enforceCtx is false, the lenient variant that isolates the cache clause.
func firstTryMux(t *testing.T, enforceCtx bool) (*http.ServeMux, *firstTry) {
	t.Helper()
	ft := &firstTry{served: map[string]bool{}, enforceCtx: enforceCtx}
	mux := http.NewServeMux()
	mux.HandleFunc("/props", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Server", "llama.cpp")
		_, _ = w.Write([]byte(`{
		  "model_path": "` + modelPath + `",
		  "build_info": "b4321-abcdef12",
		  "chat_template": "chatml",
		  "total_slots": 8,
		  "default_generation_settings": {"n_ctx": 32768}
		}`))
	})
	mux.HandleFunc("/slots", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[
		  {"id":0,"is_processing":false,"n_ctx":8192,"n_past":0},
		  {"id":1,"is_processing":false,"n_ctx":8192,"n_past":0},
		  {"id":2,"is_processing":false,"n_ctx":8192,"n_past":0},
		  {"id":3,"is_processing":false,"n_ctx":8192,"n_past":0},
		  {"id":4,"is_processing":false,"n_ctx":8192,"n_past":0},
		  {"id":5,"is_processing":false,"n_ctx":8192,"n_past":0},
		  {"id":6,"is_processing":false,"n_ctx":8192,"n_past":0},
		  {"id":7,"is_processing":false,"n_ctx":8192,"n_past":0}
		]`))
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
	// The probe pass's route, in its raw /completion shape. A repeated
	// prompt is served whole and says so the way ik does: prompt_n 5,
	// cache_n 0.
	mux.HandleFunc("/completion", func(w http.ResponseWriter, r *http.Request) {
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
		n := firstTryTokens(in.Prompt)
		if ft.enforceCtx && n+firstTryTemplate+cap > firstTryNCtx {
			writeCtxRefused(w)
			return
		}
		ft.mu.Lock()
		_, hit := ft.served[in.Prompt]
		ft.served[in.Prompt] = true
		ms := firstTryFixedMs + firstTryPerTokMs*float64(n)
		if hit {
			n, ms = 5, 2.0
		}
		ft.mu.Unlock()
		writeCompletion(w, n, 0, ms)
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
		prompt := b.String()
		cap := 0
		if in.MaxTokens != nil {
			cap = *in.MaxTokens
		}
		n := firstTryTokens(prompt)
		if ft.enforceCtx && n+firstTryTemplate+cap > firstTryNCtx {
			writeCtxRefused(w)
			return
		}
		ft.mu.Lock()
		_, hit := ft.served[prompt]
		ft.served[prompt] = true
		ms := firstTryFixedMs + firstTryPerTokMs*float64(n)
		if hit {
			n, ms = 5, 2.0
		}
		ft.mu.Unlock()
		writeFirstTryChat(w, n, ms)
	})
	return mux, ft
}

// writeCtxRefused ends a stream with the server's own refusal, the shape
// the existing failed-stream fixtures use: an error frame inside the SSE
// body, no finish chunk after it.
func writeCtxRefused(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, "data: {\"error\":{\"code\":500,\"message\":\"context_length_exceeded: the request exceeds the available context size\",\"type\":\"server_error\"}}\n\n")
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

// writeFirstTryChat answers one chat request: two content tokens, then the
// stop chunk carrying the timings the tape records. The 2 ms pause gives
// the client a TTFT to measure; nothing else is slept.
func writeFirstTryChat(w http.ResponseWriter, promptN int, promptMs float64) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)
	time.Sleep(2 * time.Millisecond)
	pps := 0.0
	if promptMs > 0 {
		pps = float64(promptN) / (promptMs / 1000)
	}
	timings := fmt.Sprintf("\"timings\":{\"prompt_n\":%d,\"prompt_ms\":%s,\"prompt_per_second\":%s,\"predicted_n\":2,\"predicted_ms\":10.0,\"predicted_per_second\":200.0,\"cache_n\":0}",
		promptN, strconv.FormatFloat(promptMs, 'f', -1, 64), strconv.FormatFloat(pps, 'f', -1, 64))
	chunk := func(s string) {
		fmt.Fprintf(w, "data: %s\n\n", s)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}
	chunk(`{"id":"ft","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}`)
	chunk(`{"id":"ft","choices":[{"index":0,"delta":{"content":"The answer."},"finish_reason":null}]}`)
	chunk(`{"id":"ft","choices":[{"index":0,"delta":{"content":""},"finish_reason":"stop"}],` + timings + `}`)
}

// firstTryOpts is the day's command — the default prompt set, a clock, no
// cap named — with the clock shortened to 2 s and the clock pinned so the
// two takes against one server get different salts.
func firstTryOpts(t *testing.T, srv *httptest.Server, streams int, hour int) recorder.Options {
	t.Helper()
	return recorder.Options{
		BaseURL:        srv.URL,
		Concurrency:    streams,
		For:            2 * time.Second,
		FSRoot:         t.TempDir(),
		GPU:            gpu.Null{},
		SampleInterval: 50 * time.Millisecond,
		Clock:          fixedClock{time.Date(2026, 9, 20, hour, 0, 0, 0, time.UTC)},
	}
}

// TestTheFirstTryRunIsPlannedNotImprovised is the gate. For one, four and
// eight streams against the server above, twice against the same server:
// every stream answers, no prompt is served from the cache, the prefill the
// plan chose fits inside its share of the clock, the streams do equal work,
// and the tape names the limit that decided it.
//
// With this fixture the plan's arithmetic is exact (the planted costs are
// binary-exact), so the targets are too: the slot ceiling is
// 8192 - 192 - 1024 = 6976 and binds one stream (the whole first prompt is
// ~7619 tokens at this tokenizer, over the slot less its answer), and the
// clock ceiling is (500 ms x 8000 tok/s - N x 25 ms x 8000 tok/s) / N =
// 800 tokens at four streams and 300 at eight.
func TestTheFirstTryRunIsPlannedNotImprovised(t *testing.T) {
	for _, n := range []int{1, 4, 8} {
		t.Run(fmt.Sprintf("streams%d", n), func(t *testing.T) {
			mux, _ := firstTryMux(t, true)
			srv := httptest.NewServer(mux)
			t.Cleanup(srv.Close)

			for take, hour := range []int{10, 11} {
				tp, err := recorder.Record(context.Background(), firstTryOpts(t, srv, n, hour))
				if err != nil {
					t.Fatalf("take %d: Record: %v", take+1, err)
				}
				s := tp.Summary
				if s.Aggregate.StreamsFailed != 0 {
					t.Errorf("take %d: %d of %d streams failed; the first take against the day's server lost its densest stream to context_length_exceeded and the plan exists to stop exactly that",
						take+1, s.Aggregate.StreamsFailed, s.Aggregate.Streams)
				}
				if len(tp.Requests) != n {
					t.Fatalf("take %d: %d requests, want %d", take+1, len(tp.Requests), n)
				}

				// No prompt was served from the prefix cache: the run salted
				// the set, so even the second take prefilled real tokens.
				lo, hi := -1, -1
				for i, rec := range tp.Requests {
					got := rec.Timings.PromptN
					if got < 256 {
						t.Errorf("take %d stream %d: prompt_n %d, want >= 256 (a cache-served prompt reports 5 on this fake, the day's \"5 prompt tokens\")", take+1, i, got)
					}
					if lo < 0 || got < lo {
						lo = got
					}
					if got > hi {
						hi = got
					}
				}

				// The streams did equal work: equal tokens, not equal
				// characters (the day's four same-length prompts tokenized
				// to 4901..7458).
				if n >= 4 && hi > 0 && float64(hi-lo) > 0.05*float64(hi) {
					t.Errorf("take %d: prompt_n spread %d..%d exceeds 5%% of %d", take+1, lo, hi, hi)
				}

				// The plan is on the tape and names its binding limit.
				p := s.Plan
				if p == nil {
					t.Fatalf("take %d: Summary.Plan = nil, the run decided nothing", take+1)
				}
				if !p.Tokenized {
					t.Errorf("take %d: Plan.Tokenized = false, want the server's own /tokenize to have counted the prompts", take+1)
				}
				if p.SlotCtx != firstTryNCtx {
					t.Errorf("take %d: Plan.SlotCtx = %d, want %d", take+1, p.SlotCtx, firstTryNCtx)
				}
				switch {
				case n >= 4:
					if p.Binding != tape.PlanBoundClock {
						t.Errorf("take %d: Plan.Binding = %q, want %q (the prefill share of the clock is the tightest ceiling at %d streams)", take+1, p.Binding, tape.PlanBoundClock, n)
					}
					wantTarget := 800
					if n == 8 {
						wantTarget = 300
					}
					if p.TargetTokens != wantTarget {
						t.Errorf("take %d: Plan.TargetTokens = %d, want %d", take+1, p.TargetTokens, wantTarget)
					}
					if s.PromptTrimTokens != wantTarget {
						t.Errorf("take %d: PromptTrimTokens = %d, want %d", take+1, s.PromptTrimTokens, wantTarget)
					}
				case n == 1:
					// Slot-bound, and the tape says so: the whole first
					// prompt is ~7619 tokens at this tokenizer, over the
					// 6976 the slot leaves once its answer is reserved,
					// while the clock alone would have allowed 7600.
					if p.Binding != tape.PlanBoundSlot {
						t.Errorf("take %d: Plan.Binding = %q, want %q", take+1, p.Binding, tape.PlanBoundSlot)
					}
					if p.TargetTokens != 6976 {
						t.Errorf("take %d: Plan.TargetTokens = %d, want 6976 (8192 less the 192 template margin less the 1024 answer floor)", take+1, p.TargetTokens)
					}
				}
				if p.LongestPromptTokens <= 0 || p.LongestPromptTokens > firstTryNCtx-firstTryTemplate {
					t.Errorf("take %d: Plan.LongestPromptTokens = %d, want the longest prompt as sent to fit the slot less its template", take+1, p.LongestPromptTokens)
				}
				if s.PromptSalt == "" {
					t.Errorf("take %d: PromptSalt empty, the set went out unsalted", take+1)
				}

				// The prefill the plan chose fits its share of the clock:
				// at the concurrent rate the probe measured halved, N
				// prompts of the trimmed length cost at most 30% of For.
				if n >= 4 {
					if s.Probe == nil || s.Probe.PrefillPerSecond <= 0 {
						t.Fatalf("take %d: no probe fit to check the plan against", take+1)
					}
					rate := s.Probe.PrefillPerSecond * 0.5
					plannedSec := float64(n) * float64(hi) / rate
					if limit := 0.30 * firstTryOpts(t, srv, n, hour).For.Seconds(); plannedSec > limit {
						t.Errorf("take %d: planned prefill %v s exceeds the 30%% share (%v s) of the clock", take+1, plannedSec, limit)
					}
				}
			}
		})
	}
}

// TestTheFirstTrySaltDefeatsTheServersPrefixCache isolates the cache clause
// on the lenient variant of the same server (no context refusal): both
// takes must prefill real tokens. On the unplanned run the set is fixed
// text, the second take is byte-identical to the first, and the fake answers
// it the way the day's warm server did — prompt_n 5.
func TestTheFirstTrySaltDefeatsTheServersPrefixCache(t *testing.T) {
	mux, _ := firstTryMux(t, false)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	sent := [][]string{}
	for take, hour := range []int{10, 11} {
		tp, err := recorder.Record(context.Background(), firstTryOpts(t, srv, 4, hour))
		if err != nil {
			t.Fatalf("take %d: Record: %v", take+1, err)
		}
		var texts []string
		for i, rec := range tp.Requests {
			if got := rec.Timings.PromptN; got < 256 {
				t.Errorf("take %d stream %d: prompt_n %d, want >= 256 (the prefix cache served a prompt this run had already sent)", take+1, i, got)
			}
			texts = append(texts, rec.Prompt.Messages[0].Content)
		}
		sent = append(sent, texts)
	}
	for i := range sent[0] {
		if sent[0][i] == sent[1][i] {
			t.Errorf("stream %d sent byte-identical prompts on both takes; the second take cannot measure a prefill the cache already holds", i)
		}
	}
}

// PlanLine renders the summary's own figures, nothing invented: the target
// (or the longest prompt when the prompts went whole), the prefill the plan
// costs at the probe's rate halved for concurrency, the resolved cap, the
// slot. The first case is the lead's worked example, and its figures are
// the day's real ones (4 streams, 1,880-token clock-bound prompts, a 3,023
// tok/s probe, a 1,024 cap, an 8,192 slot).
func TestPlanLineRendersTheSummarysOwnFigures(t *testing.T) {
	clock := &tape.Tape{Summary: tape.RunSummary{
		Concurrency: 4,
		Limit:       tape.LimitSummary{MaxTokens: 1024},
		Probe:       &tape.ProbeSummary{PrefillPerSecond: 3023},
		Plan: &tape.RunPlan{
			TargetTokens: 1880, Binding: tape.PlanBoundClock,
			PrefillBudgetMs: 5000, SlotCtx: 8192, Tokenized: true,
			LongestPromptTokens: 1891,
		},
	}}
	want := "plan: 4 × 1,880-token prompts (clock-bound) · ~5 s prefill · cap 1,024 · slot 8,192"
	if got := recorder.PlanLine(&clock.Summary, 4); got != want {
		t.Errorf("PlanLine =\n  %q\nwant\n  %q", got, want)
	}

	// A whole run with no probe and no slot prints the prompt, the binding
	// and the cap only: every other figure is the schema's "not observed"
	// and the line says nothing rather than something derived.
	whole := &tape.Tape{Summary: tape.RunSummary{
		Limit: tape.LimitSummary{MaxTokens: 8000},
		Plan:  &tape.RunPlan{Binding: tape.PlanBoundWhole, LongestPromptTokens: 7247},
	}}
	want = "plan: 1 × 7,247-token prompts (whole) · cap 8,000"
	if got := recorder.PlanLine(&whole.Summary, 1); got != want {
		t.Errorf("PlanLine =\n  %q\nwant\n  %q", got, want)
	}

	// A run that planned nothing has no line at all.
	if got := recorder.PlanLine(&tape.RunSummary{}, 4); got != "" {
		t.Errorf("PlanLine = %q on a summary with no plan, want \"\"", got)
	}
	if got := recorder.PlanLine(nil, 4); got != "" {
		t.Errorf("PlanLine = %q on a nil summary, want \"\"", got)
	}
}
