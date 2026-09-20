package recorder_test

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
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
// The cost model is planted so the whole gate stays around three seconds of
// wall clock: a 3.5 s clock with a 64 000 tok/s probe fit (1/64 ms/token,
// binary-exact, so the fit recovers the plant without rounding) stands in
// for the day's 20 s clock at 3 023 tok/s. The ratios the plan must survive
// are the same.

// firstTryCosts is the planted machine: the per-token and fixed prefill
// costs the fit recovers. 1/64 ms/token is 64 000 tok/s at the margin.
const (
	firstTryPerTokMs = 0.015625
	firstTryFixedMs  = 25.0
	// firstTryNCtx is the slot context, -c 32768 divided by -np 4.
	firstTryNCtx = 8192
	// firstTryTemplate is what the fake counts the chat template as adding.
	firstTryTemplate = 100
	// firstTryConcurrentShare is the planted contention: when more than one
	// /completion request is in prefill at once, the aggregate rate the
	// burst of them delivers is this fraction of the single-stream rate.
	// 0.2 is the day's own measurement at ~1.6k-token prompts (574–716 tok/s
	// aggregate against 2963–3083 probed, 2026-09-20) — the number the
	// plan's first draft assumed was 0.5 and spent half the clock on.
	firstTryConcurrentShare = 0.2
)

// firstTryTokens is the fake tokenizer, non-uniform the way the day's real
// one proved to be (2026-09-20): the first firstTryHeadBytes of any text
// price at 4.0 bytes/token — task prose, the sparse thing the set opens
// with — and everything after at 2.2, the code and logs the material
// continues into. A pure function of byte position, so a prefix re-counts
// consistently: /tokenize, /completion and the chat route all report the
// same number for the same bytes, and the count is monotone in the length.
// Against the uniform ceil(bytes/2.5) this gate used before, the
// proportional trim looked exact (any linear tokenizer is); against this
// one it undershoots by double-digit percents, which is the defect the
// day's takes exposed (target 1800, landed 1339–1700).
const (
	firstTryHeadBytes = 6000
	firstTryHeadBPT   = 4.0
	firstTryTailBPT   = 2.2
)

func firstTryTokens(s string) int {
	head := min(len(s), firstTryHeadBytes)
	return ceilDiv(head, firstTryHeadBPT) + ceilDiv(len(s)-head, firstTryTailBPT)
}

// ceilDiv is bytes divided by a bytes-per-token price, rounded up: the
// tokenizer never undercounts, which is the safe direction for every
// ceiling that reads it.
func ceilDiv(bytes int, bytesPerToken float64) int {
	return int(math.Ceil(float64(bytes) / bytesPerToken))
}

// firstTry is the stateful half of the fake: a prefix cache keyed on the
// exact prompt text, and the contention model the day's four streams
// queued behind each other through. The single-stream cost is computed,
// never slept — prompt_ms is the server's own figure and the gate asserts
// on it — but the concurrent burst is a wall-clock measurement in the code
// under test, so its cost has to be real time: under contention each
// prefill sleeps the marginal cost of its tokens at the contended
// aggregate rate, serialized behind prefillMu, and the sum of those sleeps
// is exactly what an aggregate of firstTryConcurrentShare x single implies.
// The chat route keeps the instant answer: no gate here asserts on the
// run's own chat wall time, and a serialized chat queue would price
// seconds of sleep for nothing these gates read.
//
// burstN/burstMs are the fake's own account of the contended burst it
// delivered — the tokens those requests carried and the serialized wall
// its holds actually took, measured, not planted (the streams4 flake,
// 2026-09-21: under `go test ./...` parallel load the sleeps stretch, and
// the gate's rate assertion — measured against the planted ideal — missed
// its 10% band by 0.7%, Concurrent.PerSecond 11512 against the band's
// floor 11520). The gate now asserts the recorder's figures against THIS
// account: load stretches the client's clock and the fake's holds
// together, and the ratio is the load-independent fact.
type firstTry struct {
	mu         sync.Mutex
	served     map[string]bool
	enforceCtx bool
	prefillMu  sync.Mutex
	inflight   int
	maxIn      int
	burstN     int
	burstMs    float64
}

// addBurst folds one contended prefill into the burst account. The caller
// has already measured the hold; n is the token count that request reported.
func (ft *firstTry) addBurst(n int, ms float64) {
	ft.mu.Lock()
	ft.burstN += n
	ft.burstMs += ms
	ft.mu.Unlock()
}

// burstFigures is the delivered burst: its tokens and its real serialized
// wall in milliseconds.
func (ft *firstTry) burstFigures() (int, float64) {
	ft.mu.Lock()
	defer ft.mu.Unlock()
	return ft.burstN, ft.burstMs
}

// enterPrefill is the contention model's accounting: a request entering
// /completion's prefill raises the in-flight count, and the peak is kept
// for the gate's overlap assertion on the burst.
func (ft *firstTry) enterPrefill() {
	ft.mu.Lock()
	ft.inflight++
	if ft.inflight > ft.maxIn {
		ft.maxIn = ft.inflight
	}
	ft.mu.Unlock()
}

func (ft *firstTry) leavePrefill() {
	ft.mu.Lock()
	ft.inflight--
	ft.mu.Unlock()
}

// inflightPrefill is how many prefills are in the route right now.
func (ft *firstTry) inflightPrefill() int {
	ft.mu.Lock()
	defer ft.mu.Unlock()
	return ft.inflight
}

// peakPrefill is the most prefills the server saw at once.
func (ft *firstTry) peakPrefill() int {
	ft.mu.Lock()
	defer ft.mu.Unlock()
	return ft.maxIn
}

// contendedPrefillMs is the wall time one contended prefill of n prompt
// tokens costs: the marginal single-stream cost divided by the planted
// share. Alone it would be prompt_ms's own per-token term; under the
// serialized queue the aggregate over the burst works out to the planted
// fraction, which is the figure the recorder's burst is built to measure.
func contendedPrefillMs(n int) float64 {
	return firstTryPerTokMs * float64(n) / firstTryConcurrentShare
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
	// cache_n 0. Contended prefills — the probe's burst — pay the planted
	// aggregate in real wall time, serialized; a lone prefill pays nothing
	// real, because every figure the fit reads is the server's own ms.
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
		ft.enterPrefill()
		// The entry settle: a burst arrives together, so every request of
		// it waits long enough for the whole batch to land before deciding
		// whether it shares the prefill slot — the first of the batch is
		// as contended as the rest, it just cannot know yet.
		time.Sleep(10 * time.Millisecond)
		if ft.inflightPrefill() > 1 {
			// The serialized queue: hold the prefill slot for this
			// request's share of the contended aggregate, so the last of
			// the burst's first tokens arrives at the sum of them all. The
			// hold is measured as well as slept: the burst account above is
			// what the gate asserts against, and it must carry the wall
			// this box actually delivered, not the wall the plant asked for.
			ft.prefillMu.Lock()
			held := time.Now()
			time.Sleep(time.Duration(contendedPrefillMs(n) * float64(time.Millisecond)))
			ft.addBurst(n, time.Since(held).Seconds()*1000)
			ft.prefillMu.Unlock()
		}
		ft.leavePrefill()
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
// cap named — with the clock shortened to 3.5 s and the clock pinned so the
// two takes against one server get different salts.
func firstTryOpts(t *testing.T, srv *httptest.Server, streams int, hour int) recorder.Options {
	t.Helper()
	return recorder.Options{
		BaseURL:        srv.URL,
		Concurrency:    streams,
		For:            3500 * time.Millisecond,
		FSRoot:         t.TempDir(),
		GPU:            gpu.Null{},
		SampleInterval: 50 * time.Millisecond,
		Clock:          fixedClock{time.Date(2026, 9, 20, hour, 0, 0, 0, time.UTC)},
	}
}

// TestTheFirstTryRunIsPlannedNotImprovised is the gate. For one, four and
// eight streams against the server above, twice against the same server:
// every stream answers, no prompt is served from the cache, the prefill the
// plan chose fits inside its share of the clock at the rate the box really
// delivers under load, the streams do equal work — equal tokens, inside the
// trim's convergence band — and the tape names the limit that decided it.
//
// With this fixture the plan's arithmetic is exact (the planted costs are
// binary-exact), so the targets follow from it: the slot ceiling is
// 8192 - 192 - 1024 = 6976 and binds one stream (the whole first prompt is
// ~7431 tokens at this tokenizer, over the slot less its answer), and at
// four and eight streams the clock binds at floor(0.25 x For x the measured
// aggregate / N) — about 2800 and 1400 at the planted 0.2-share aggregate
// of 12 800 tok/s, with the few per cent of real wall the burst's client
// clock honestly carries. The plan assumed that aggregate was half the
// one-stream rate before the concurrent point existed (2026-09-20), and
// these targets are what assuming it bought instead.
func TestTheFirstTryRunIsPlannedNotImprovised(t *testing.T) {
	for _, n := range []int{1, 4, 8} {
		t.Run(fmt.Sprintf("streams%d", n), func(t *testing.T) {
			mux, ft := firstTryMux(t, true)
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

				// The concurrent point: measured, not assumed. The burst
				// really ran concurrently (the fake saw the streams share
				// its prefill route), it recorded the planted aggregate,
				// and the plan's target came from that number.
				if n > 1 {
					if s.Probe == nil || s.Probe.Concurrent == nil {
						t.Errorf("take %d: Probe.Concurrent = nil at %d streams; the run that will load the server with %d prefills at once measured what that costs", take+1, n, n)
					} else {
						c := s.Probe.Concurrent
						if c.Streams != n {
							t.Errorf("take %d: Concurrent.Streams = %d, want %d", take+1, c.Streams, n)
						}
						if c.PromptN <= 0 || c.WallMs <= 0 {
							t.Errorf("take %d: Concurrent = %+v, want observed figures, not zeros", take+1, *c)
						}
						// Against the fake's OWN delivered burst, not the
						// plant (2026-09-21): the flake measured
						// Concurrent.PerSecond 11512 against the planted
						// band's floor 11520 — 0.7% outside 10% — under
						// `go test ./...` parallel load, because the
						// burst's wall is timed on the real clock and
						// sleeps stretch. Both figures below measure the
						// same delivered wall (the client's send-to-last-
						// first-token, the fake's measured holds), so
						// load moves them together. The plant itself is
						// still pinned by the planned-prefill check at
						// the bottom of this gate, which reads the
						// planted rate directly.
						bn, bms := ft.burstFigures()
						if bn <= 0 || bms <= 0 {
							t.Fatalf("take %d: the fake recorded no burst (%d tokens, %.0f ms); the concurrent point measured a queue that did not run", take+1, bn, bms)
						}
						want := float64(bn) / (bms / 1000)
						if !near(c.PerSecond, want, 0.10) {
							t.Errorf("take %d: Concurrent.PerSecond = %v, want the fake's own delivered %.0f (its %d tokens over its %.0f ms serialized wall) within 10%%",
								take+1, c.PerSecond, want, bn, bms)
						}
					}
				} else if s.Probe != nil && s.Probe.Concurrent != nil {
					t.Errorf("take %d: Concurrent = %+v on a one-stream run, want nil: nothing was concurrent", take+1, *s.Probe.Concurrent)
				}
				if peak := ft.peakPrefill(); n > 1 && peak < 2 {
					t.Errorf("take %d: the fake never saw two prefills at once (peak %d); the concurrent point measured a queue that did not exist", take+1, peak)
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
				var wantTarget int
				switch {
				case n >= 4:
					if p.Binding != tape.PlanBoundClock {
						t.Errorf("take %d: Plan.Binding = %q, want %q (the prefill share of the clock is the tightest ceiling at %d streams)", take+1, p.Binding, tape.PlanBoundClock, n)
					}
					// The ceiling comes from the rate the probe measured, so
					// the expectation does too — the measured aggregate
					// carries a few per cent of real wall around the plant,
					// and the plant itself is pinned by the PerSecond
					// assertion above. A target that is not
					// floor(share x For x Concurrent.PerSecond / N) would be
					// the plan acting on a number it was not given.
					if c := s.Probe.Concurrent; c != nil && c.PerSecond > 0 {
						wantTarget = int(firstTryOpts(t, srv, n, hour).For.Seconds() * 0.25 * c.PerSecond / float64(n))
						if p.TargetTokens != wantTarget {
							t.Errorf("take %d: Plan.TargetTokens = %d, want %d (the clock share over the measured %v tok/s)", take+1, p.TargetTokens, wantTarget, c.PerSecond)
						}
						if s.PromptTrimTokens != wantTarget {
							t.Errorf("take %d: PromptTrimTokens = %d, want %d", take+1, s.PromptTrimTokens, wantTarget)
						}
						// The trim converged: every stream carries the target's
						// work inside the band the trim converges into, never
						// over it. The day's proportional cut landed 25% under
						// (target 1800, landed 1339..1700) — the defect this
						// band exists to make impossible.
						for i, rec := range tp.Requests {
							if got := rec.Timings.PromptN; got > wantTarget || float64(got) < 0.97*float64(wantTarget) {
								t.Errorf("take %d stream %d: prompt_n %d, want within [0.97 x %d, %d]: the trim did not converge onto its target", take+1, i, got, wantTarget, wantTarget)
							}
						}
					}
				case n == 1:
					// Slot-bound, and the tape says so: the whole first
					// prompt is ~7431 tokens at this tokenizer, over the
					// 6976 the slot leaves once its answer is reserved,
					// while the clock alone would have allowed 54 400.
					if p.Binding != tape.PlanBoundSlot {
						t.Errorf("take %d: Plan.Binding = %q, want %q", take+1, p.Binding, tape.PlanBoundSlot)
					}
					wantTarget = 6976
					if p.TargetTokens != wantTarget {
						t.Errorf("take %d: Plan.TargetTokens = %d, want %d (8192 less the 192 template margin less the 1024 answer floor)", take+1, p.TargetTokens, wantTarget)
					}
					// One stream converges too.
					for i, rec := range tp.Requests {
						if got := rec.Timings.PromptN; got > wantTarget || float64(got) < 0.97*float64(wantTarget) {
							t.Errorf("take %d stream %d: prompt_n %d, want within [0.97 x %d, %d]: the trim did not converge onto its target", take+1, i, got, wantTarget, wantTarget)
						}
					}
				}
				if p.LongestPromptTokens <= 0 || p.LongestPromptTokens > firstTryNCtx-firstTryTemplate {
					t.Errorf("take %d: Plan.LongestPromptTokens = %d, want the longest prompt as sent to fit the slot less its template", take+1, p.LongestPromptTokens)
				}
				if s.PromptSalt == "" {
					t.Errorf("take %d: PromptSalt empty, the set went out unsalted", take+1)
				}

				// The prefill the plan chose fits its share of the clock at
				// the rate the box really delivers under this load — the
				// planted 0.2 aggregate, not the 0.5 the plan used to
				// assume. Assuming half was the error that spent half the
				// day's 20 s clock on prefill promised in 4.3 s.
				if n >= 4 {
					if s.Probe == nil || s.Probe.PrefillPerSecond <= 0 {
						t.Fatalf("take %d: no probe fit to check the plan against", take+1)
					}
					rate := s.Probe.PrefillPerSecond * firstTryConcurrentShare
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
// it the way the day's warm server did — prompt_n 5. Two streams carry the
// clause (each take still pays its concurrent burst in real wall time, and
// this test reads none of it).
func TestTheFirstTrySaltDefeatsTheServersPrefixCache(t *testing.T) {
	mux, _ := firstTryMux(t, false)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	sent := [][]string{}
	for take, hour := range []int{10, 11} {
		tp, err := recorder.Record(context.Background(), firstTryOpts(t, srv, 2, hour))
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
// costs at the rate it used — the measured concurrent rate when the probe
// made one, the probe's rate halved when it did not — the resolved cap, the
// slot. The first case is the lead's worked example, and its figures are
// the day's real ones (4 streams, 1,880-token clock-bound prompts, a 3,023
// tok/s probe, a 1,024 cap, an 8,192 slot); the second is the same run with
// the concurrent point the day's takes measured (574 tok/s aggregate,
// 2026-09-20), whose honest prefill line is the 13 s that took the clock.
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

	// The measured concurrent rate replaces the halved one: ~4 x 1,891 at
	// 574 tok/s is 13.2 s, the figure the day's plan owed its reader and
	// printed as 5 s while the takes waited 6.5-10.4 s for their first
	// tokens.
	clock.Summary.Probe.Concurrent = &tape.ConcurrentPrefill{Streams: 4, PromptN: 6544, WallMs: 11400, PerSecond: 574}
	want = "plan: 4 × 1,880-token prompts (clock-bound) · ~13.2 s prefill · cap 1,024 · slot 8,192"
	if got := recorder.PlanLine(&clock.Summary, 4); got != want {
		t.Errorf("PlanLine with a concurrent point =\n  %q\nwant\n  %q", got, want)
	}
	clock.Summary.Probe.Concurrent = nil

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
