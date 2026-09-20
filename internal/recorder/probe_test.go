package recorder_test

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/midagedev/toktape/internal/gpu"
	"github.com/midagedev/toktape/internal/recorder"
	"github.com/midagedev/toktape/internal/tape"
)

// The prefill probe gates (TTP-137, 2026-09-19). The fake /completion route
// plants a per-token and a fixed cost, so the two-point fit is asserted
// against numbers the test chose; its stateful half serves a repeat prompt
// from the prefix-cache shape, which is what the replay probe observes.

// probeCosts is the cost model the fake /completion answers with.
type probeCosts struct {
	fixedMs  float64
	perTokMs float64
	// inverted makes longer prompts answer faster, the shape a cache hit or
	// a broken measurement would produce: a fit the probe must refuse.
	inverted bool
	// nFor overrides the token count reported for a prompt. nil reports
	// len(prompt)/5, a planted conversion that cancels in the slope.
	nFor func(prompt string) int
	// longPartialCache is the cache_n reported for the long fit point's own
	// first send: a partial prefix-cache hit, prompt_n > 0 with cache_n > 0
	// together, the shape a scheduler routing the request to the slot with
	// the longest common prefix serves. The point costs only the tokens it
	// evaluated, so through the pair the cache's discount masquerades as the
	// machine's speed — which is why the fit must refuse on the flag, not on
	// the arithmetic (lead, 2026-09-19).
	longPartialCache int
}

// probeSeen records what the /completion route was asked for, so the gates
// can assert on the prompts the pass generated and the caps it sent.
type probeSeen struct {
	mu      sync.Mutex
	prompts []string
	caps    []int
}

func (s *probeSeen) add(prompt string, cap int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.prompts = append(s.prompts, prompt)
	s.caps = append(s.caps, cap)
}

func (s *probeSeen) snapshot() ([]string, []int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.prompts...), append([]int(nil), s.caps...)
}

// probeMux is fakeMux plus the /completion route the probe pass sends to.
func probeMux(t *testing.T, costs probeCosts) (*http.ServeMux, *probeSeen) {
	t.Helper()
	mux := fakeMux(t)
	seen := &probeSeen{}
	served := map[string]bool{}
	first := true
	mux.HandleFunc("/completion", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Prompt   string `json:"prompt"`
			NPredict *int   `json:"n_predict"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		cap := 0
		if in.NPredict != nil {
			cap = *in.NPredict
		}
		seen.add(in.Prompt, cap)
		_, hit := served[in.Prompt]
		served[in.Prompt] = true

		n := len(in.Prompt) / 5
		if costs.nFor != nil {
			n = costs.nFor(in.Prompt)
		}
		ms := costs.fixedMs + costs.perTokMs*float64(n)
		if costs.inverted {
			ms = costs.fixedMs + costs.perTokMs*float64(2600-n)
		}
		cacheN := 0
		if hit {
			// The prefix-cache shape: everything reused, almost nothing
			// spent. timings.prompt_n is what the server evaluated.
			cacheN, ms, n = n, 2.0, 0
		} else if !first && costs.longPartialCache > 0 && n > costs.longPartialCache {
			// The long fit point's own first send, served partly from cache:
			// the point evaluates only what was not reused, and its timings
			// say so — the pair of figures a cold-only fit would misread.
			cacheN = costs.longPartialCache
			n -= cacheN
			ms = costs.fixedMs + costs.perTokMs*float64(n)
		}
		first = false
		writeCompletion(w, n, cacheN, ms)
	})
	return mux, seen
}

// writeCompletion answers one /completion request in llama-server's own
// stream shape: one content chunk, then the stop chunk that carries the
// final timings. The 2 ms pause gives the client a TTFT to measure.
func writeCompletion(w http.ResponseWriter, promptN, cacheN int, promptMs float64) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)
	time.Sleep(2 * time.Millisecond)
	pps := 0.0
	if promptMs > 0 {
		pps = float64(promptN) / (promptMs / 1000)
	}
	fmt.Fprintf(w, "data: {\"content\":\"ok\",\"stop\":false}\n\n")
	fmt.Fprintf(w, "data: {\"content\":\"\",\"stop\":true,\"stop_type\":\"limit\",\"timings\":{\"prompt_n\":%d,\"prompt_ms\":%s,\"prompt_per_second\":%s,\"predicted_n\":1,\"predicted_ms\":5.0,\"predicted_per_second\":200.0,\"cache_n\":%d}}\n\n",
		promptN, strconv.FormatFloat(promptMs, 'f', -1, 64), strconv.FormatFloat(pps, 'f', -1, 64), cacheN)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

// probeOpts is the plain one-stream run the probe gates record.
func probeOpts(t *testing.T, srv *httptest.Server) recorder.Options {
	t.Helper()
	return recorder.Options{
		BaseURL:        srv.URL,
		FSRoot:         t.TempDir(),
		GPU:            gpu.Null{},
		SampleInterval: 50 * time.Millisecond,
		Clock:          fixedClock{time.Date(2026, 9, 19, 8, 0, 0, 0, time.UTC)},
	}
}

// near is a relative-tolerance comparison for the planted figures.
func near(got, want, tol float64) bool {
	return math.Abs(got-want) <= tol*math.Abs(want)
}

// Gate 1: the slope through two planted points is the machine's prefill rate
// and the intercept is the server's fixed cost — not the single-point
// average either of them hides in.
func TestProbeFitsTheMachinesPrefillRate(t *testing.T) {
	mux, seen := probeMux(t, probeCosts{fixedMs: 30, perTokMs: 0.5})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	tp, err := recorder.Record(context.Background(), probeOpts(t, srv))
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	p := tp.Summary.Probe
	if p == nil {
		t.Fatal("Summary.Probe = nil, the pass did not record anything")
	}
	if len(p.Prefill) != 2 {
		t.Fatalf("Prefill points = %d, want 2: %+v", len(p.Prefill), p.Prefill)
	}
	short, long := p.Prefill[0], p.Prefill[1]
	if short.PromptN >= long.PromptN {
		t.Errorf("points not in send order (short first): %+v then %+v", short, long)
	}
	if short.PromptN < 100 || short.PromptN > 200 {
		t.Errorf("short point = %d tokens, want about 128", short.PromptN)
	}
	if long.PromptN < 1700 || long.PromptN > 2400 {
		t.Errorf("long point = %d tokens, want about 2048", long.PromptN)
	}
	if short.TTFTMs <= 0 || long.TTFTMs <= 0 {
		t.Errorf("TTFT not recorded beside the server figures: %+v %+v", short, long)
	}
	// perTokMs 0.5 is 2000 tok/s at the margin; fixedMs 30 is the intercept.
	if !near(p.PrefillPerSecond, 2000, 0.01) {
		t.Errorf("PrefillPerSecond = %v, want the slope 2000 (planted 0.5 ms/token)", p.PrefillPerSecond)
	}
	if !near(p.FixedMs, 30, 0.01) {
		t.Errorf("FixedMs = %v, want the planted intercept 30", p.FixedMs)
	}
	// The replay: the longer prompt a second time, reported as it happened.
	prompts, caps := seen.snapshot()
	if len(prompts) != 3 {
		t.Fatalf("probe sent %d requests, want 3 (short, long, replay)", len(prompts))
	}
	if prompts[2] != prompts[1] {
		t.Error("the replay did not resend the longer prompt")
	}
	if p.Replay == nil {
		t.Fatal("Replay = nil, the second send of the long prompt was not recorded")
	}
	if p.Replay.PromptN != 0 || p.Replay.CacheN != long.PromptN || !near(p.Replay.PromptMs, 2.0, 0.01) {
		t.Errorf("Replay = %+v, want the server's own words (0 evaluated, %d reused, 2 ms)", *p.Replay, long.PromptN)
	}
	// Every probe request asked for exactly one token.
	for i, c := range caps {
		if c != 1 {
			t.Errorf("probe request %d asked for n_predict %d, want 1", i, c)
		}
	}
	// The pass is silent on success.
	for _, w := range tp.Summary.Warnings {
		if strings.Contains(strings.ToLower(w), "probe") {
			t.Errorf("warning about the probe on a healthy pass: %q", w)
		}
	}
}

// Gate 2: a fit the probe cannot trust is refused — rates 0, points kept —
// never clamped into a plausible number.
func TestProbeRefusesAFitItCannotTrust(t *testing.T) {
	cases := []struct {
		name  string
		costs probeCosts
	}{
		{"longer prompt answered faster, the cache-hit shape", probeCosts{fixedMs: 30, perTokMs: 0.5, inverted: true}},
		{"both points the same length, no slope", probeCosts{fixedMs: 30, perTokMs: 0.5, nFor: func(string) int { return 100 }}},
		{"negative intercept", probeCosts{fixedMs: -30, perTokMs: 0.5}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mux, _ := probeMux(t, tc.costs)
			srv := httptest.NewServer(mux)
			t.Cleanup(srv.Close)

			tp, err := recorder.Record(context.Background(), probeOpts(t, srv))
			if err != nil {
				t.Fatalf("Record: %v", err)
			}
			p := tp.Summary.Probe
			if p == nil {
				t.Fatal("Summary.Probe = nil, the pass did not record anything")
			}
			if len(p.Prefill) != 2 {
				t.Fatalf("Prefill points = %d, a refused fit keeps them: %+v", len(p.Prefill), p.Prefill)
			}
			if p.PrefillPerSecond != 0 || p.FixedMs != 0 {
				t.Errorf("refused fit produced figures: %v tok/s, fixed %v ms", p.PrefillPerSecond, p.FixedMs)
			}
		})
	}
}

// Gate 2b, re-authored (lead, 2026-09-20). The 2026-09-19 version of this
// gate asserted that any cache_n > 0 must refuse the fit, on the premise
// that "the point costs only the tokens it evaluated, so through the pair
// the cache's discount masquerades as the machine's speed". That premise is
// false, and upstream says so: timings.prompt_n is n_prompt_processed and
// timings.cache_n is n_prompt_cached (llama.cpp tools/server/
// server-common.cpp, the timings object), two disjoint counts whose sum is
// the prompt. A partially served point therefore reports the honest cost of
// the tokens it did evaluate, at the x it did evaluate — which is why the
// fixture above computes its ms from the reduced n, and why a pair through
// it recovers the planted machine exactly. The old gate was refusing a
// correct measurement.
//
// It was also refusing every warm server. Both probe prompts render through
// the same opening — /completion tokenizes with add_special (server-context
// .cpp, the handler's tokenize_input_prompts call), so BOS alone is a shared
// first token — and the slot keeps its tokens between requests, so on any
// slot that has served anything the common prefix is at least 1 and
// n_prompt_cached is assigned from it with no floor. A second run against a
// warm server got no prefill fit at all.
//
// What a hit can actually break is the span: a pair needs two lengths far
// enough apart for a slope, and a hit that lands on the long point drags it
// toward the short one until the slope is noise over a small denominator. A
// shared prefix shaves the same k off both points and cancels; only a
// lopsided hit collapses the span. So the gate now pins the three cases
// separately — cold fits, partial-but-spanning fits, collapsed refuses —
// and the middle one is the clause the old code fails.
func TestProbeFitsThroughAPartialHitAndRefusesACollapsedSpan(t *testing.T) {
	record := func(longPartialCache int) *tape.ProbeSummary {
		mux, _ := probeMux(t, probeCosts{fixedMs: 30, perTokMs: 0.5, longPartialCache: longPartialCache})
		srv := httptest.NewServer(mux)
		t.Cleanup(srv.Close)
		tp, err := recorder.Record(context.Background(), probeOpts(t, srv))
		if err != nil {
			t.Fatalf("Record: %v", err)
		}
		if tp.Summary.Probe == nil {
			t.Fatal("Summary.Probe = nil, the pass did not record anything")
		}
		return tp.Summary.Probe
	}

	// Cold: both points cache_n == 0, and the fit is the planted machine —
	// 0.5 ms/token is 2000 tok/s at the margin, 30 ms the fixed cost.
	cold := record(0)
	if len(cold.Prefill) != 2 {
		t.Fatalf("cold probe: %d points, want 2: %+v", len(cold.Prefill), cold.Prefill)
	}
	for i, pt := range cold.Prefill {
		if pt.CacheN != 0 {
			t.Errorf("cold point %d carries cache_n %d, want 0", i, pt.CacheN)
		}
	}
	if !near(cold.PrefillPerSecond, 2000, 0.01) || !near(cold.FixedMs, 30, 0.01) {
		t.Errorf("cold probe fit = %v tok/s, fixed %v ms; want the planted 2000 and 30 (a refusal here would be a regression)",
			cold.PrefillPerSecond, cold.FixedMs)
	}

	// Partial hit on the long point, span intact: the point is an honest
	// measurement of the tokens it evaluated, so the pair recovers the same
	// planted machine as the cold run. This is the clause the 2026-09-19
	// code fails — it returned the refusal's 0/0 here.
	cached := record(300)
	if len(cached.Prefill) != 2 {
		t.Fatalf("cache-hit probe: %d points, want the pair kept: %+v", len(cached.Prefill), cached.Prefill)
	}
	if got := cached.Prefill[1].CacheN; got != 300 {
		t.Errorf("long point CacheN = %d, want the server's own 300: the hit was dropped on the floor", got)
	}
	if cached.Prefill[0].CacheN != 0 {
		t.Errorf("short point CacheN = %d, want 0: only the long point was served from cache", cached.Prefill[0].CacheN)
	}
	if !near(cached.PrefillPerSecond, 2000, 0.01) || !near(cached.FixedMs, 30, 0.01) {
		t.Errorf("partial hit with the span intact fitted %v tok/s, fixed %v ms; want the planted 2000 and 30 — the point's ms is the cost of the tokens it evaluated, at the x it evaluated",
			cached.PrefillPerSecond, cached.FixedMs)
	}

	// The hit large enough to collapse the long point onto the short one:
	// the intended long is probeLongTokens, and reusing all but ~150 of it
	// leaves the two points closer together than the floor probeLongLength
	// makes the intended pair clear. No slope worth reporting, so refuse.
	collapsed := record(1900)
	if len(collapsed.Prefill) != 2 {
		t.Fatalf("collapsed probe: %d points, want the pair kept: %+v", len(collapsed.Prefill), collapsed.Prefill)
	}
	lo, hi := collapsed.Prefill[0], collapsed.Prefill[1]
	if span := hi.PromptN - lo.PromptN; span >= 256 {
		t.Fatalf("the collapse fixture left a span of %d tokens (%d and %d); it is meant to fall under the floor, so this gate is not testing what it says",
			span, lo.PromptN, hi.PromptN)
	}
	if collapsed.PrefillPerSecond != 0 || collapsed.FixedMs != 0 {
		t.Errorf("a fit through a collapsed span produced figures: %v tok/s, fixed %v ms; want the refusal's 0/0",
			collapsed.PrefillPerSecond, collapsed.FixedMs)
	}
}

// Gates 3 and 4: the two probe prompts differ from their first tokens, and
// the pass leaves the run it measured alone — the run's own cache picture
// is built from the run's requests, probe requests included nowhere.
func TestProbePromptsAreTheirOwnAndLeaveTheRunsCacheAlone(t *testing.T) {
	mux, seen := probeMux(t, probeCosts{fixedMs: 30, perTokMs: 0.5})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	tp, err := recorder.Record(context.Background(), probeOpts(t, srv))
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	prompts, _ := seen.snapshot()
	if len(prompts) < 2 {
		t.Fatalf("probe sent %d prompts, want at least the two fit points", len(prompts))
	}
	const head = 160 // about 32 tokens at the set's own 5 characters a token

	// Gate 3: the two fit prompts do not share a prefix, so the second
	// point's prefill cannot be cache-served by the first one's.
	if firstChars(prompts[0], head) == firstChars(prompts[1], head) {
		t.Error("the two probe prompts share their first tokens; the long point's prefill would be cache-served and the slope meaningless")
	}

	// Gate 4: no probe prompt shares a prefix with any prompt the run sent,
	// so the pass cannot poison the set's prefix cache, and the summary's
	// cache picture is untouched by the probe's own cache hit.
	for i, rec := range tp.Requests {
		runPrompt := rec.Prompt.Messages[0].Content
		for j, pp := range prompts {
			if firstChars(runPrompt, head) == firstChars(pp, head) {
				t.Errorf("run prompt %d shares its first tokens with probe prompt %d", i, j)
			}
		}
	}
	if tp.Summary.Probe == nil || tp.Summary.Probe.Replay == nil || tp.Summary.Probe.Replay.CacheN == 0 {
		t.Fatal("the probe's replay did not observe a cache hit, so the check below has no teeth")
	}
	if tp.Summary.Cache.HitTokens != 0 || tp.Summary.Cache.HitRatio != 0 {
		t.Errorf("summary.Cache = %+v, want zero hits: the probe's replay must not land in the run's cache picture", tp.Summary.Cache)
	}
}

func firstChars(s string, n int) string {
	if len(s) < n {
		return s
	}
	return s[:n]
}

// Gate 5: a tape from before the probe field decodes unchanged — no Probe
// key, every other field exactly as it was.
func TestOldTapeWithoutProbeKeyDecodes(t *testing.T) {
	tp, err := tape.Read("testdata/no-probe.tape")
	if err != nil {
		t.Fatalf("tape.Read: %v", err)
	}
	if tp.Schema != 1 {
		t.Errorf("Schema = %d, want 1", tp.Schema)
	}
	if tp.Summary.Probe != nil {
		t.Errorf("Probe = %+v, want nil on a tape with no probe key", tp.Summary.Probe)
	}
	if tp.Summary.ID != "20260913-071500-old" || tp.Summary.Concurrency != 1 {
		t.Errorf("summary drifted: id %q, concurrency %d", tp.Summary.ID, tp.Summary.Concurrency)
	}
	if tp.Summary.Timings.PromptN != 283 || tp.Summary.Cache.Label != tape.CacheWarm {
		t.Errorf("pinned fields drifted: prompt_n %d, label %q", tp.Summary.Timings.PromptN, tp.Summary.Cache.Label)
	}
	// Round-trip: writing it back keeps the field absent.
	path := filepath.Join(t.TempDir(), "again.tape")
	if err := tape.Write(path, tp); err != nil {
		t.Fatalf("tape.Write: %v", err)
	}
	again, err := tape.Read(path)
	if err != nil {
		t.Fatalf("tape.Read: %v", err)
	}
	if again.Summary.Probe != nil {
		t.Errorf("Probe = %+v after a round trip, want nil", again.Summary.Probe)
	}
}

// An engine that reports its own KV size is the record, and it is now the
// only source (decision on TTP-136, 2026-09-19).
//
// Until today the recorder also scanned the server's stdout for llama.cpp's
// "KV self size = ..." line. That was dropped, and the gates that pinned it
// went with it rather than being re-pinned, because the behaviour is gone by
// design: the string does not exist in llama.cpp master (it became
// "llama_kv_cache: size = ... (N cells, N layers, ...)", the second rename
// since 2025), and no HTTP surface carries the figure at all — not
// /metrics, not /props, not /slots. A scraper for one number that tracks an
// upstream log format across renames is a dependency the card does not earn
// back. Where the engine says nothing, VRAMKVBytes stays 0 and the card
// prints "?", which is what "not observed" is supposed to look like.
func TestVRAMKVIsTheEnginesFigureOrNothing(t *testing.T) {
	inner, _ := probeMux(t, probeCosts{fixedMs: 30, perTokMs: 0.5})
	mux := http.NewServeMux()
	mux.HandleFunc("/props", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{
		  "model_path": "` + modelPath + `",
		  "total_slots": 4,
		  "engine": {"name": "shimx", "version": "1.0", "model": {"format": "exl3"},
		             "placement": {"devices": [], "vram_kv_bytes": 12345678}}
		}`))
	})
	mux.Handle("/", inner)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	tp, err := recorder.Record(context.Background(), probeOpts(t, srv))
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	if got := tp.Summary.Placement.VRAMKVBytes; got != 12345678 {
		t.Errorf("Placement.VRAMKVBytes = %d, want the engine's own 12345678", got)
	}
}

// And a server that says nothing about its KV cache leaves the figure
// unobserved rather than derived. This is the half that used to be filled by
// the log scan: placement spreads tensors and never computes a KV size, and
// the arithmetic that looks like it should (layers x kv heads x head dim x
// ctx x 2) is wrong by a factor of 4 on a hybrid-attention model, where only
// every fourth layer holds a cache at all. 0 is the honest answer.
func TestVRAMKVStaysUnobservedWhenTheEngineIsSilent(t *testing.T) {
	mux, _ := probeMux(t, probeCosts{fixedMs: 30, perTokMs: 0.5})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	tp, err := recorder.Record(context.Background(), probeOpts(t, srv))
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	if got := tp.Summary.Placement.VRAMKVBytes; got != 0 {
		t.Errorf("Placement.VRAMKVBytes = %d, want 0: nothing observed it, and it is never computed", got)
	}
}

// The adaptive-probe gates (TTP-142). A fixed 2048-token long point is 1.7 s
// at the reference box's 1182 tok/s prefill but 79 s on a box measured at
// 26 tok/s with experts on the CPU — and the replay sends it again. The long
// point's length is chosen from the short point's own observed cost, against
// recorder.probeBudgetMs (5000 here).
func TestProbeAdaptsTheLongPointToTheBox(t *testing.T) {
	const budgetMs = 5000 // recorder.probeBudgetMs
	record := func(costs probeCosts) (*tape.ProbeSummary, []string) {
		mux, seen := probeMux(t, costs)
		srv := httptest.NewServer(mux)
		t.Cleanup(srv.Close)
		tp, err := recorder.Record(context.Background(), probeOpts(t, srv))
		if err != nil {
			t.Fatalf("Record: %v", err)
		}
		if tp.Summary.Probe == nil {
			t.Fatal("Summary.Probe = nil, the pass did not record anything")
		}
		prompts, _ := seen.snapshot()
		return tp.Summary.Probe, prompts
	}

	// Fast box (400 tok/s at the margin — planted 0.5 ms/token): the budget
	// admits the ceiling, and the ceiling is what llama-bench's pp2048 lines
	// up against, so it stays 2048.
	fast, fastPrompts := record(probeCosts{fixedMs: 30, perTokMs: 0.5})
	if len(fast.Prefill) != 2 {
		t.Fatalf("fast box: %d points, want 2: %+v", len(fast.Prefill), fast.Prefill)
	}
	if n := fast.Prefill[1].PromptN; n < 1950 || n > 2100 {
		t.Errorf("fast box chose a %d-token long point, want the 2048 ceiling (about 2007 at this fake's 5 bytes a token)", n)
	}
	if len(fastPrompts) != 3 {
		t.Errorf("fast box: %d probe requests, want 3 (short, long, replay)", len(fastPrompts))
	}

	// Slow box (planted 2.5 ms/token, 400 tok/s): a 2048-token point is about
	// 5.1 s, so a smaller one is chosen — the largest that fits the budget —
	// and the fit through it still recovers the planted machine.
	slow, slowPrompts := record(probeCosts{fixedMs: 30, perTokMs: 2.5})
	if len(slow.Prefill) != 2 {
		t.Fatalf("slow box: %d points, want 2: %+v", len(slow.Prefill), slow.Prefill)
	}
	long := slow.Prefill[1]
	if long.PromptN >= fast.Prefill[1].PromptN {
		t.Errorf("slow box chose %d tokens, not below the fast box's %d: the budget did not bind", long.PromptN, fast.Prefill[1].PromptN)
	}
	if long.PromptN < 1500 || long.PromptN > 1900 {
		t.Errorf("slow box long point = %d tokens, want about 1788 (the largest that fits %d ms at this box's short-point cost)", long.PromptN, budgetMs)
	}
	if long.PromptMs > budgetMs {
		t.Errorf("slow box long point cost %v ms, want it inside the %d ms budget", long.PromptMs, budgetMs)
	}
	if !near(slow.PrefillPerSecond, 400, 0.01) || !near(slow.FixedMs, 30, 0.01) {
		t.Errorf("adapted length broke the fit: %v tok/s, fixed %v ms; want the planted 400 and 30", slow.PrefillPerSecond, slow.FixedMs)
	}
	if len(slowPrompts) != 3 || slowPrompts[2] != slowPrompts[1] {
		t.Errorf("slow box replay: %d requests, replay==long is %v; the replay must resend the adapted prompt, not a 2048 one",
			len(slowPrompts), slowPrompts[2] == slowPrompts[1])
	}
}

// Gate 6: a box so slow that even the floor length would blow the budget gets
// no long point at all — the short point is recorded, the fit refuses (a bad
// fit is worse than no fit), and nothing is sent twice.
func TestProbeSkipsTheLongPointWhenTheFloorWouldBlowTheBudget(t *testing.T) {
	// Planted 40 ms/token = 25 tok/s at the margin, the measured CPU-experts
	// class: the 128-token short point itself costs about 5 s, and no length
	// past the floor fits the budget.
	mux, seen := probeMux(t, probeCosts{fixedMs: 30, perTokMs: 40})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	tp, err := recorder.Record(context.Background(), probeOpts(t, srv))
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	p := tp.Summary.Probe
	if p == nil {
		t.Fatal("Summary.Probe = nil, the pass did not record anything")
	}
	if len(p.Prefill) != 1 {
		t.Fatalf("Prefill points = %d, want 1 (the short point only): %+v", len(p.Prefill), p.Prefill)
	}
	if p.Prefill[0].PromptN < 100 {
		t.Errorf("short point = %d tokens, want it recorded (about 125)", p.Prefill[0].PromptN)
	}
	if p.PrefillPerSecond != 0 || p.FixedMs != 0 {
		t.Errorf("one-point probe produced a fit: %v tok/s, fixed %v ms; fitPrefill must refuse", p.PrefillPerSecond, p.FixedMs)
	}
	if p.Replay != nil {
		t.Errorf("Replay = %+v, want nil: there is no long prompt to resend", *p.Replay)
	}
	prompts, _ := seen.snapshot()
	if len(prompts) != 1 {
		t.Errorf("probe sent %d requests, want exactly 1 (the short point; no long point, no replay)", len(prompts))
	}
}

// TTP-143 (FAIL-first, 2026-09-19): the probe pass pays the major faults a
// cold server takes for its weights — it sends the first prompts the server
// ever sees — and the run's sampler latches only after the pass, so without
// these fields the cost is simply gone and a genuinely cold server reads
// warm. The fake /completion route advances the fixture counter five faults
// a request, so the three probe requests cost fifteen; the run's own
// requests go to /v1/chat/completions and cost none, which is what makes the
// run's zero the honest reading rather than a fixture artefact.
func TestProbePaysItsOwnMajorFaults(t *testing.T) {
	proc := newLivingProc(t)
	mux := fakeMux(t)
	served := map[string]bool{}
	mux.HandleFunc("/completion", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Prompt string `json:"prompt"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		// Five faults a request: the page-ins a cold server takes while it
		// reads the prompt's weights. The replay takes them too — a cache
		// hit still walks the page tables — but evaluates no tokens.
		for i := 0; i < 5; i++ {
			proc.bump(t)
		}
		n := len(in.Prompt) / 5
		cacheN := 0
		if served[in.Prompt] {
			cacheN, n = n, 0
		}
		served[in.Prompt] = true
		ms := 30 + 0.5*float64(n)
		if cacheN > 0 {
			ms = 2.0
		}
		writeCompletion(w, n, cacheN, ms)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	opts := probeOpts(t, srv)
	opts.FSRoot = proc.root
	tp, err := recorder.Record(context.Background(), opts)
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	if tp.Summary.Server.PID != fixturePID {
		t.Fatalf("PID = %d, the writable fixture was not used", tp.Summary.Server.PID)
	}
	p := tp.Summary.Probe
	if p == nil {
		t.Fatal("Summary.Probe = nil, the pass did not record anything")
	}
	if len(p.Prefill) != 2 {
		t.Fatalf("Prefill points = %d, want 2: %+v", len(p.Prefill), p.Prefill)
	}
	if p.MajFaults != 15 {
		t.Errorf("MajFaults = %d, want 15 (three requests x five faults)", p.MajFaults)
	}
	// The denominator is the tokens the pass evaluated: both fit points,
	// and the replay's none — the cache served it, so its tokens were not
	// prefilled and its faults are not per-prefill-token faults.
	tokens := p.Prefill[0].PromptN + p.Prefill[1].PromptN
	if p.Replay != nil {
		tokens += p.Replay.PromptN
	}
	if !near(p.MajFaultsPerToken, 15.0/float64(tokens), 0.01) {
		t.Errorf("MajFaultsPerToken = %v, want 15 over the %d evaluated tokens (%v)", p.MajFaultsPerToken, tokens, 15.0/float64(tokens))
	}
	// The run's own counters start after the pass: the probe's faults are
	// not the run's, which is precisely why the probe has to carry them.
	if got := tp.Summary.Memory.MajFaultsTotal; got != 0 {
		t.Errorf("Memory.MajFaultsTotal = %d, want 0: the run's sampler latched after the probe", got)
	}
}

// And where no counter can be read — no /proc view of the server — both
// figures stay 0: not observed, never invented.
func TestProbeFaultsAreZeroWithoutAProcView(t *testing.T) {
	mux, _ := probeMux(t, probeCosts{fixedMs: 30, perTokMs: 0.5})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	// probeOpts leaves FSRoot an empty temp dir: no /proc under it, so no
	// sampler for the probe and none for the run.
	tp, err := recorder.Record(context.Background(), probeOpts(t, srv))
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	p := tp.Summary.Probe
	if p == nil {
		t.Fatal("Summary.Probe = nil, the pass did not record anything")
	}
	if p.MajFaults != 0 || p.MajFaultsPerToken != 0 {
		t.Errorf("fault figures without a counter: %d faults, %v per token; both want 0", p.MajFaults, p.MajFaultsPerToken)
	}
}
