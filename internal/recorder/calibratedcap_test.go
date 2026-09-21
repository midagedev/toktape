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

	"github.com/midagedev/toktape/internal/card"
	"github.com/midagedev/toktape/internal/gpu"
	"github.com/midagedev/toktape/internal/recorder"
	"github.com/midagedev/toktape/internal/tape"
)

// The calibrated-cap gate (TTP-170, 2026-09-21). Six real runs on one M1 Pro
// (Ollama 0.34.2, llama3.2:1b, ~6,150-token prompts, a 20 s clock, one
// stream) every one of whose caps landed above the clock: the time a cap
// actually needs is TTFT + cap / the decode rate the run really did, and
// that was 20.3–24.4 s against the 20 s clock on all six. The runs that
// produced a card were the ones where the model stopped on its own first.
//
// Three causes, all in the sizing, and one structural fact beneath them: an
// OpenAI-compatible server that reports no timings counts tokens ONLY in a
// closing usage message, and a stream the clock cancels never gets one — so
// on that kind of server a clock cut does not shorten the measurement, it
// destroys it (the card prints `Sample ?`; on llama.cpp the same cut still
// reports timings and costs nothing).
//
// The fake below is the measured shape of that box, not a generic one. It is
// a sibling of firstRunFakeCfg (openai_firstrun_test.go) rather than a
// variant of it because the two behaviours that make this defect reachable
// cannot be planted there without moving that file's pinned 1000 tok/s:
//
//   - decode runs SLOWER at the run's prompt length than at the
//     calibration's short one — ~110 tok/s at the ~60-token ask, ~85 tok/s at
//     the ~9.5k-token prompt (measured 106.8–115.9 against 84.5–86.3; the
//     ratio 0.729–0.808 is what a 0.8 factor sits in the middle of). Against
//     a one-rate fake the cap cannot lose the race and the gate is theatre;
//   - the first request the server serves pays a model load, so the decode
//     calibration's TTFT carries it and the prefill calibration's — sent
//     after, model already resident — does not. That is what used to drive
//     the prefill span negative and drop the point exactly on a cold box.
//
// The usage rule is real Ollama's, copied from firstRunFakeCfg: usage rides
// only a stream that ran to its own end; a cancelled stream gets nothing.

// The planted machine. Rates are the measured ones; the clock and the load
// are scaled to keep the four gates in this file inside ~12 s of wall.
const (
	capGateShortRate   = 110.0 // tok/s at a short prompt — the calibration's own figure
	capGateLongRate    = 85.0  // tok/s at a long prompt — the run's own figure
	capGateRateStep    = 2048  // prompt tokens at which decode slows to the long rate
	capGatePrefillTPS  = 25000.0
	capGateLoad        = 400 * time.Millisecond
	capGateTick        = 5 * time.Millisecond
	capGateFor         = 2 * time.Second
	capGateBytesPerTok = 2.0 // priced == counted, so the plan's ruler is the fake's own
)

// capGateCfg plants the one axis each gate varies.
type capGateCfg struct {
	// longRate is the decode rate at a prompt of capGateRateStep tokens or
	// more. 85 is the measured shape; 62 is a box worse than the derate
	// predicted, the residual case the grace exists for.
	longRate float64
	// stuckAfter, when > 0, makes the stream go silent after that many
	// tokens and never finish: the pathological server the grace bound
	// exists to survive.
	stuckAfter int
}

// capGateFake is the server the six runs met. One model listed, no
// max_model_len (Ollama sizes its context to the prompt), prefill serialized
// and real, the load paid once by the first request, decode self-correcting
// onto the planted rate so sleep stretch under test load cannot deliver less
// than was planted, and usage only on a stream that ended on its own.
func capGateFake(t *testing.T, cfg capGateCfg) (*httptest.Server, func() []int) {
	t.Helper()
	var (
		mu        sync.Mutex
		bodies    []int // the max_tokens of every request, in arrival order
		loaded    bool
		prefillMu sync.Mutex
	)
	chat := func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Model     string         `json:"model"`
			MaxTokens int            `json:"max_tokens"`
			Messages  []tape.Message `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		mu.Lock()
		first := !loaded
		loaded = true
		bodies = append(bodies, in.MaxTokens)
		mu.Unlock()
		var b strings.Builder
		for _, m := range in.Messages {
			b.WriteString(m.Content)
		}
		promptN := int(math.Ceil(float64(b.Len()) / capGateBytesPerTok))

		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		emit := func(payload string) bool {
			if _, err := fmt.Fprintf(w, "data: %s\n\n", payload); err != nil {
				return false
			}
			if flusher != nil {
				flusher.Flush()
			}
			return true
		}
		// The cancelled-request rule, real Ollama's: once the client is gone
		// nothing more is written — no finish chunk, no usage. It is the
		// whole defect.
		gone := func() bool { return r.Context().Err() != nil }

		// The model load, once, by the first request the server ever serves
		// — the decode calibration on a cold box. It lands in THAT request's
		// TTFT and in nobody else's, which is what used to drive the prefill
		// calibration's span negative (cause c).
		if first {
			time.Sleep(capGateLoad)
		}
		// The prefill: real time, serialized — the cost the prefill
		// calibration prices and the cap reserves.
		prefillMu.Lock()
		time.Sleep(time.Duration(float64(promptN) / capGatePrefillTPS * float64(time.Second)))
		prefillMu.Unlock()
		if gone() {
			return
		}
		if !emit(`{"id":"cg","model":"` + in.Model + `","choices":[{"index":0,"delta":{"role":"assistant"}}]}`) {
			return
		}
		rate := capGateShortRate
		if promptN >= capGateRateStep {
			rate = cfg.longRate
		}
		cap := in.MaxTokens
		if cap <= 0 {
			cap = 1 << 20
		}
		// The self-correcting rate clock (firstRunFakeCfg's): each tick tops
		// the emitted count up to what the planted rate says the decoding
		// time so far produced, so a stretched sleep cannot accumulate into
		// a delivered rate under the plant.
		var decoded time.Duration
		sent := 0
		for sent < cap {
			if gone() {
				return
			}
			if cfg.stuckAfter > 0 && sent >= cfg.stuckAfter {
				// The pathological server: the floor is satisfied, then
				// silence forever. Hold the stream open until the client
				// gives up — no finish chunk, no usage.
				<-r.Context().Done()
				return
			}
			t0 := time.Now()
			time.Sleep(capGateTick)
			decoded += time.Since(t0)
			want := int(rate * decoded.Seconds())
			if want > cap {
				want = cap
			}
			for ; sent < want; sent++ {
				if !emit(fmt.Sprintf(`{"id":"cg","model":%q,"choices":[{"index":0,"delta":{"content":"w%d"}}]}`, in.Model, sent)) {
					return
				}
			}
		}
		// The stream ran to its own end: the finish word, the usage both
		// calibration requests are read from, [DONE].
		if !emit(fmt.Sprintf(`{"id":"cg","model":%q,"choices":[{"index":0,"delta":{},"finish_reason":"length"}],"usage":{"prompt_tokens":%d,"prompt_tokens_details":{"cached_tokens":0},"completion_tokens":%d}}`,
			in.Model, promptN, sent)) {
			return
		}
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
		if flusher != nil {
			flusher.Flush()
		}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/props", func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) })
	mux.HandleFunc("/slots", func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) })
	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"cg:1b","object":"model"}]}`))
	})
	mux.HandleFunc("/apply-template", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "no template here", http.StatusNotFound)
	})
	mux.HandleFunc("/v1/chat/completions", chat)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, func() []int {
		mu.Lock()
		defer mu.Unlock()
		return append([]int(nil), bodies...)
	}
}

// capGateOpts is the first run itself: default options against the planted
// box, the clock scaled to 2 s, no cap named — the command whose six real
// counterparts every came back `Sample ?` or lucky.
func capGateOpts(t *testing.T, url string) recorder.Options {
	t.Helper()
	return recorder.Options{
		BaseURL:        url,
		Concurrency:    1,
		For:            capGateFor,
		FSRoot:         t.TempDir(),
		GPU:            gpu.Null{},
		SampleInterval: 50 * time.Millisecond,
		Clock:          fixedClock{time.Date(2026, 9, 21, 11, 0, 0, 0, time.UTC)},
		Version:        "0.1.0-test",
	}
}

// TestCalibratedCapFitsTheClockOnAUsageOnlyServer is the headline gate: the
// run against the measured box comes back a USABLE measurement — a
// server-counted token count and a decode rate — with every stream ended on
// its own. The assertion is the outcome, not the constant: a test that
// asserted 0.65 would pass a future implementation that is still broken.
//
// FAIL-first (2026-09-21, unmodified sources, output in the round report):
// on this fake the prefill calibration's span goes negative (the calibration
// paid the load, the prefill request did not), the point is dropped, the cap
// reserves no prefill and promises 0.8 x the short-prompt rate — 176 tokens
// that need 0.38 s of prefill and 2.07 s of decode under a 2 s clock. The
// clock cuts, the usage chunk never arrives, and every assertion of
// "counted" below fails with PredictedNSource "chunks".
func TestCalibratedCapFitsTheClockOnAUsageOnlyServer(t *testing.T) {
	srv, caps := capGateFake(t, capGateCfg{longRate: capGateLongRate})
	var notes []string
	opts := capGateOpts(t, srv.URL)
	opts.Progress = func(ev recorder.Event) {
		if ev.Kind == recorder.EventNote {
			notes = append(notes, ev.Message)
		}
	}
	tp, err := recorder.Record(context.Background(), opts)
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	s := tp.Summary
	if len(tp.Requests) != 1 {
		t.Fatalf("%d requests, want 1", len(tp.Requests))
	}
	rec := tp.Requests[0]

	// The usable measurement: the server's own count and a rate where the
	// card's Sample row reads one.
	if rec.Timings.PredictedNSource != "usage" {
		t.Errorf("PredictedNSource = %q, want \"usage\"; the clock cut the stream and the count never arrived",
			rec.Timings.PredictedNSource)
	}
	if rec.Timings.PredictedN <= 0 || rec.Timings.PredictedN != s.Limit.MaxTokens {
		t.Errorf("PredictedN = %d, want the cap %d the stream ran to, server-counted",
			rec.Timings.PredictedN, s.Limit.MaxTokens)
	}
	if s.Timings.PredictedNSource != "usage" || s.Timings.PredictedPerSecond <= 0 {
		t.Errorf("summary rate = %v (%q), want a rate over the server's count — the figure `Sample ?` stood in for",
			s.Timings.PredictedPerSecond, s.Timings.PredictedNSource)
	}
	if hasCaveat(&s, card.CodeTokensUncounted) {
		t.Errorf("caveats carry tokens_uncounted: %v", card.Caveats(&s))
	}
	if rec.Prompt.Cut || rec.Prompt.FinishReason == "" {
		t.Errorf("cut = %v, finish = %q; the cap existed so the stream could end on its own",
			rec.Prompt.Cut, rec.Prompt.FinishReason)
	}

	// The prompt was the long one: the fake only decodes at the long rate
	// past capGateRateStep prompt tokens, so a run that trimmed its way out
	// of the measured shape would pass this gate for a reason it was not
	// written to accept.
	if rec.Timings.PromptN < capGateRateStep {
		t.Fatalf("PromptN = %d, want >= %d (the measured shape is the long prompt's)",
			rec.Timings.PromptN, capGateRateStep)
	}

	// The sizing reserved real prefill and derated the rate below the
	// measured spread: the cold calibration no longer drops the prefill
	// point (the span clamp), and the cap is under the one that lost the
	// race on this very fake — 0.8 x the calibrated rate x the whole clock,
	// no prefill reserved.
	if s.Probe == nil || s.Probe.Calibration == nil {
		t.Fatal("Probe.Calibration = nil, want the decode calibration")
	}
	cal := s.Probe.Calibration
	if cal.Prefill == nil || cal.Prefill.PerSecond <= 0 {
		t.Errorf("Calibration.Prefill = %+v, want the point a cold decode calibration used to drop",
			cal.Prefill)
	}
	if losing := int(0.8 * cal.PerSecond * capGateFor.Seconds()); s.Limit.MaxTokens >= losing {
		t.Errorf("Limit.MaxTokens = %d, want under %d — the cap sized at the centre of the measured spread with no prefill reserved loses the race on this server",
			s.Limit.MaxTokens, losing)
	}
	if got := caps(); len(got) != 3 || got[2] != s.Limit.MaxTokens {
		t.Errorf("requests carried %v, want the third (the run's) to be the planned cap %d",
			got, s.Limit.MaxTokens)
	}

	// The grace allowance is on the tape even though nothing needed it:
	// Grace is the allowance, CutAt says whether it was spent (it was not).
	if s.Limit.Grace != capGateFor/2 {
		t.Errorf("Limit.Grace = %v, want the allowance %v", s.Limit.Grace, capGateFor/2)
	}
	if s.Limit.CutAt != 0 {
		t.Errorf("Limit.CutAt = %v, want 0; no stream was cut", s.Limit.CutAt)
	}

	// Debuggability (TTP-170): the run says what the cap predicted — the
	// calibrated rate, the reserved prefill, and the BAND the finish falls
	// in — where a reader reaches it in the same command. The run used to
	// print the cap it chose and never the prediction behind it.
	//
	// The band and not a single figure (lead, 2026-09-21): the cap is
	// chosen as share x rate x budget, so on a one-stream run
	// cap / (share x rate) is the budget back again and a lone "predicted
	// finish" prints the clock every time — a checksum of its own sum. The
	// two ends are what a reader can be wrong between, so the assertion is
	// that both are there.
	var capNote string
	for _, n := range notes {
		if strings.Contains(n, "the run finishes in") {
			capNote = n
		}
	}
	if capNote == "" {
		t.Fatalf("no note carries the cap's prediction; notes were %q", notes)
	}
	t.Logf("the note the run printed: %s", capNote)
	for _, want := range []string{
		strconv.FormatFloat(cal.PerSecond, 'f', 0, 64) + " tok/s", // the calibrated rate
		"prefill reserved",       // what the clock had left after the prompts
		"the run finishes in",    // the band's opening
		"of the calibrated rate", // the derate the high end assumes
	} {
		if !strings.Contains(capNote, want) {
			t.Errorf("the cap's note = %q, lacks %q", capNote, want)
		}
	}
}

// TestTheClockWaitsInGraceForTheStreamItWouldHaveDestroyed is the structural
// half: sizing alone cannot close the class — any constant can still lose on
// a box with a wider spread. Here the box decodes at 62 tok/s, worse than
// the derate predicted, so the cap's answer needs ~2.2 s under a 2 s clock:
// the clock fires, and instead of destroying the measurement it waits —
// bounded — for the stream to end on its own, which it does ~0.2 s later.
// The tape reads the trade: Grace says the allowance, CutAt 0 says nothing
// was cut.
//
// FAIL-first (2026-09-21, unmodified sources): the clock cut at ~2.0 s, no
// usage arrived, and the PredictedNSource assertion failed with "chunks".
func TestTheClockWaitsInGraceForTheStreamItWouldHaveDestroyed(t *testing.T) {
	srv, _ := capGateFake(t, capGateCfg{longRate: 62})
	tp, err := recorder.Record(context.Background(), capGateOpts(t, srv.URL))
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	s := tp.Summary
	rec := tp.Requests[0]
	if rec.Timings.PredictedNSource != "usage" || rec.Timings.PredictedN <= 0 {
		t.Errorf("PredictedN = %d (%q), want the server's count: the grace existed so the stream could end on its own",
			rec.Timings.PredictedN, rec.Timings.PredictedNSource)
	}
	if rec.Timings.PromptN < capGateRateStep {
		t.Fatalf("PromptN = %d, want >= %d (the long prompt's shape)", rec.Timings.PromptN, capGateRateStep)
	}
	if s.Timings.PredictedPerSecond <= 0 {
		t.Errorf("summary rate = %v, want a rate — the measurement the cut would have destroyed", s.Timings.PredictedPerSecond)
	}
	if rec.Prompt.Cut {
		t.Error("the stream was cut inside its grace allowance")
	}
	if s.Limit.Grace != capGateFor/2 {
		t.Errorf("Limit.Grace = %v, want the allowance %v (the run went long on purpose)",
			s.Limit.Grace, capGateFor/2)
	}
	if s.Limit.CutAt != 0 {
		t.Errorf("Limit.CutAt = %v, want 0; every stream ended on its own and nothing was cut", s.Limit.CutAt)
	}
}

// TestTheGraceIsBounded: a server that satisfies the floor and then goes
// silent forever cannot hang the run. The cut lands when the allowance runs
// out — at most For x 1.5 — and what comes out is exactly what a cut run
// always was: Cut set, no finish word, the tokens_uncounted caveat.
//
// FAIL-first (2026-09-21, unmodified sources): Grace stayed 0 — the grace
// did not exist — and this failed on the Grace assertion.
func TestTheGraceIsBounded(t *testing.T) {
	srv, _ := capGateFake(t, capGateCfg{longRate: capGateLongRate, stuckAfter: 70})
	start := time.Now()
	tp, err := recorder.Record(context.Background(), capGateOpts(t, srv.URL))
	wall := time.Since(start)
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	s := tp.Summary
	rec := tp.Requests[0]
	if s.Limit.Grace != capGateFor/2 {
		t.Errorf("Limit.Grace = %v, want the allowance %v", s.Limit.Grace, capGateFor/2)
	}
	if s.Limit.CutAt <= s.Limit.For || s.Limit.CutAt > s.Limit.For*3/2+150*time.Millisecond {
		t.Errorf("Limit.CutAt = %v, want inside (%v, %v]: the floor was satisfied, so the grace's expiry decides the cut",
			s.Limit.CutAt, s.Limit.For, s.Limit.For*3/2)
	}
	if !rec.Prompt.Cut || rec.Prompt.FinishReason != "" {
		t.Errorf("cut = %v, finish = %q; the allowance ran out and the cut is exactly today's",
			rec.Prompt.Cut, rec.Prompt.FinishReason)
	}
	if !hasCaveat(&s, card.CodeTokensUncounted) {
		t.Errorf("caveats lack tokens_uncounted: %v (the pathological server never sent usage)", card.Caveats(&s))
	}
	// The wall bound is deliberately loose beside CutAt's: the tight bound
	// is the cut's own figure (measured from the first request out, above),
	// while the wall also carries the calibration's load and prefill before
	// the clock ever starts — stretches under parallel test load, not grace
	// behaviour. What this line asserts is that Record RETURNED: an
	// unbounded grace would hang on this server forever.
	if bound := capGateFor*3/2 + 2500*time.Millisecond; wall > bound {
		t.Errorf("wall = %v, want <= %v: the grace is bounded so a silent server cannot hang the run", wall, bound)
	}
}

// TestLlamaKindGetsNoGrace: the allowance exists for the one kind whose cut
// destroys the measurement — a server that counts tokens only in a closing
// usage message. On a llama.cpp-kind server a cut stream still reports its
// timings, the same cut costs nothing, and the clock's behaviour must be
// byte-identical to before: no grace recorded, the cut at the budget (the
// floor already satisfied), exactly as TestBudgetCutSavesAValidTape has
// always read. This is the regression guard, not a FAIL-first gate: it fails
// the moment the grace leaks onto the kinds that never asked for it.
func TestLlamaKindGetsNoGrace(t *testing.T) {
	const (
		interval = 6 * time.Millisecond
		budget   = 700 * time.Millisecond
	)
	srv := pacedServer(t, interval, 100000)
	opts := clockOptions(t, srv)
	opts.Concurrency, opts.For = 2, budget

	tp, err := recorder.Record(context.Background(), opts)
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	s := tp.Summary
	if s.Limit.Grace != 0 {
		t.Errorf("Limit.Grace = %v, want 0; a llama.cpp-kind cut costs no measurement, so no allowance exists", s.Limit.Grace)
	}
	if s.Limit.CutAt < budget || s.Limit.CutAt > budget+200*time.Millisecond {
		t.Errorf("Limit.CutAt = %v, want the budget %v (the floor is satisfied long before it) with no grace hold",
			s.Limit.CutAt, budget)
	}
	for i, r := range tp.Requests {
		if !r.Prompt.Cut {
			t.Errorf("stream %d: not cut; the budget ended it exactly as before", i)
		}
	}
}
