package recorder_test

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/midagedev/toktape/internal/card"
	"github.com/midagedev/toktape/internal/gpu"
	"github.com/midagedev/toktape/internal/recorder"
	"github.com/midagedev/toktape/internal/tape"
)

// The OpenAI first-run gate (TTP-156, 2026-09-21). Measured that day on
// Ollama 0.34.2 at 127.0.0.1:11434 with llama3.2:1b and qwen3:1.7b: a
// ServerOpenAI server counts tokens only in the closing usage chunk
// (stream_options.include_usage), the default 20 s clock cancels the stream
// before that chunk exists, and the card printed "Sample ? · client-timed"
// with the tokens_uncounted caveat over a run that had streamed 1127 chunks.
// Counting chunks would have been a guess — a chunk is not a token — so the
// fix is a one-request decode calibration before the run, and an answer cap
// computed from it that every stream reaches before the clock does: each
// stream then ends on its own and the server's count arrives.
//
// The fake below is the OpenAI sibling of firstTry's llama fake: 404 on
// /props and /slots, a two-model /v1/models listing, and a chat route that
// decodes at a planted rate and sends the usage chunk ONLY when the stream
// ran to its own end — a request the client cancels gets none, which is the
// whole defect. It is a new fake rather than a variant of openaiFake because
// that one cannot plant a rate nor withhold usage on cancel, and both are the
// behaviour under test here.

// The planted machine, sized so the whole file runs in ~3 s: 1000 tok/s and
// a 600 ms clock standing in for the default 20 s one. The rate is delivered
// by a self-correcting clock (firstRunFake): every tick emits as many tokens
// as the planted rate says should exist by now, so a host whose time.Sleep
// overshoots — macOS timers run a millisecond and more over — still delivers
// the planted rate in the long run, and the calibration's few per cent of
// batching bias sit far inside the 20% headroom the cap's 0.8 factor buys.
const (
	firstRunTick = 5 * time.Millisecond
	firstRunFor  = 600 * time.Millisecond
)

// firstRunRate is the planted decode rate the fake's self-correcting clock
// delivers: 1000 tok/s. The gate's expectations are derived from the
// calibration the tape carries rather than from this ideal, because the
// measured figure honestly carries a few per cent of batching bias and
// timer slop around it.
const firstRunRate = 1000.0

// firstRunModels is a two-model listing: the first id is the default the run
// takes when no model is chosen, the second is the one --model / --param
// model= selects (TTP-157).
const firstRunModels = `{"object":"list","data":[` +
	`{"id":"alpha:1b","object":"model"},` +
	`{"id":"beta:2b","object":"model"}]}`

// firstRunCfg is the planted server a default first run meets, beyond the
// two switches the TTP-156 gate needed (TTP-168/TTP-165/TTP-169, 2026-09-21):
//
//   - prefill: prompts cost real time, proportional to their bytes at
//     firstRunPrefillRate, serialized behind one mutex — the Ollama/vLLM
//     behaviour the prefill calibration exists to price. A request's usage
//     prompt_tokens is then its real count (bytes / 4), not a constant.
//   - maxModelLen: listed per model in /v1/models and enforced up front with
//     vLLM's own 400 sentence when prompt tokens + max_tokens exceed it —
//     the refusal the context-planning path exists to never meet.
//   - fingerprint: the system_fingerprint on every chunk, the engine's own
//     statement of what it is.
type firstRunCfg struct {
	serial      bool
	usage       bool
	prefill     bool
	maxModelLen int
	fingerprint string
}

// The planted prefill rate and the fake's tokenizer: 4.0 bytes per token,
// so a 4096-byte prefill calibration counts 1024 tokens and the set's
// ~19 kB counts ~4.7k. 25 000 tok/s keeps the planted prefill of a whole
// gate run inside a few hundred milliseconds.
const (
	firstRunPrefillRate = 25000.0
	firstRunBytesPerTok = 4.0
)

func firstRunFakeCfg(t *testing.T, cfg firstRunCfg) (*httptest.Server, func() []map[string]any) {
	t.Helper()
	var (
		mu        sync.Mutex
		bodies    []map[string]any
		decodeMu  sync.Mutex
		prefillMu sync.Mutex
	)
	tokensOf := func(content string) int {
		return int(math.Ceil(float64(len(content)) / firstRunBytesPerTok))
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/props", func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	})
	mux.HandleFunc("/slots", func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	})
	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if cfg.maxModelLen > 0 {
			_, _ = w.Write([]byte(`{"object":"list","data":[` +
				fmt.Sprintf(`{"id":"alpha:1b","object":"model","max_model_len":%d},`, cfg.maxModelLen) +
				`{"id":"beta:2b","object":"model"}]}`))
			return
		}
		_, _ = w.Write([]byte(firstRunModels))
	})
	mux.HandleFunc("/apply-template", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "no template here", http.StatusNotFound)
	})
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
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
		bodies = append(bodies, map[string]any{"model": in.Model, "max_tokens": in.MaxTokens})
		mu.Unlock()
		promptN := tokensOf(strings.Join(func() []string {
			out := make([]string, len(in.Messages))
			for i, m := range in.Messages {
				out[i] = m.Content
			}
			return out
		}(), ""))
		if cfg.maxModelLen > 0 && promptN+in.MaxTokens > cfg.maxModelLen {
			// vLLM's own sentence, with the real numbers in it.
			http.Error(w, fmt.Sprintf(`{"error":{"message":"This model's maximum context length is %d tokens. However, you requested %d tokens (%d in your messages, %d in the completion). Please reduce the length of the messages or the completion.","type":"BadRequestError"}}`,
				cfg.maxModelLen, promptN+max(in.MaxTokens, 0), promptN, max(in.MaxTokens, 0)), http.StatusBadRequest)
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		fp := ""
		if cfg.fingerprint != "" {
			fp = `,"system_fingerprint":"` + cfg.fingerprint + `"`
		}
		emit := func(payload string) bool {
			if _, err := fmt.Fprintf(w, "data: %s\n\n", payload); err != nil {
				return false
			}
			if flusher != nil {
				flusher.Flush()
			}
			return true
		}
		// The cancelled-request rule: once the client is gone nothing more is
		// written — no finish chunk, no usage chunk. That is the server
		// behaviour the whole calibration exists to accommodate.
		gone := func() bool { return r.Context().Err() != nil }
		stop := func(held bool) {
			if held {
				decodeMu.Unlock()
			}
		}
		cap := in.MaxTokens
		if cap <= 0 {
			cap = 1 << 20
		}
		// The prefill: real time, proportional to the prompt, serialized —
		// the cost the calibration's second request prices and the plan
		// budgets. Paid before the first token, so TTFT carries it the way
		// a real server's does.
		if cfg.prefill {
			prefillMu.Lock()
			time.Sleep(time.Duration(float64(promptN) / firstRunPrefillRate * float64(time.Second)))
			prefillMu.Unlock()
			if gone() {
				return
			}
		}
		if !emit(`{"id":"fr","model":"` + in.Model + `"` + fp + `,"choices":[{"index":0,"delta":{"role":"assistant"}}]}`) {
			return
		}
		sent := 0
		// The self-correcting rate clock. decoded accumulates the time this
		// stream actually spent decoding — under the serial mutex only the
		// time it held the lock, so a stream queued behind another banks
		// none of the queue — and each tick tops the emitted count up to
		// what the planted rate says that much decoding produced. Sleep
		// overshoot therefore cannot accumulate into a delivered rate below
		// the plant, and the count is monotone in the decoding time.
		var decoded time.Duration
		for sent < cap {
			if cfg.serial {
				decodeMu.Lock()
			}
			if gone() {
				stop(cfg.serial)
				return
			}
			t0 := time.Now()
			time.Sleep(firstRunTick)
			decoded += time.Since(t0)
			want := int(firstRunRate * decoded.Seconds())
			if want > cap {
				want = cap
			}
			for ; sent < want; sent++ {
				if !emit(fmt.Sprintf(`{"id":"fr","model":%q%s,"choices":[{"index":0,"delta":{"content":"w%d"}}]}`, in.Model, fp, sent)) {
					stop(cfg.serial)
					return
				}
			}
			stop(cfg.serial)
		}
		// The stream ran to its own end: the finish chunk, the usage chunk
		// when this server sends one, and [DONE]. The prompt count is the
		// tokenizer's own figure for what arrived — the number whose absence
		// used to leave every OpenAI-kind card saying "? in".
		final := fmt.Sprintf(`{"id":"fr","model":%q%s,"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]`, in.Model, fp)
		if cfg.usage {
			final += fmt.Sprintf(`,"usage":{"prompt_tokens":%d,"prompt_tokens_details":{"cached_tokens":0},"completion_tokens":%d}`, promptN, sent)
		}
		final += "}"
		if !emit(final) {
			return
		}
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
		if flusher != nil {
			flusher.Flush()
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, func() []map[string]any {
		mu.Lock()
		defer mu.Unlock()
		return append([]map[string]any(nil), bodies...)
	}
}

// firstRunFake is the two-switch spelling the TTP-156 gate calls: no planted
// prefill, no context limit, no fingerprint — the server that gate met.
func firstRunFake(t *testing.T, serial, usage bool) (*httptest.Server, func() []map[string]any) {
	t.Helper()
	return firstRunFakeCfg(t, firstRunCfg{serial: serial, usage: usage})
}

// firstRunOpts is the first run itself: default options, a short clock, no
// cap named — the shape whose default clock is the thing that cancelled the
// usage chunk on the real Ollama.
func firstRunOpts(t *testing.T, url string, streams int) recorder.Options {
	t.Helper()
	return recorder.Options{
		BaseURL:        url,
		Concurrency:    streams,
		For:            firstRunFor,
		FSRoot:         t.TempDir(),
		GPU:            gpu.Null{},
		SampleInterval: 50 * time.Millisecond,
		Clock:          fixedClock{time.Date(2026, 9, 21, 9, 0, 0, 0, time.UTC)},
		Version:        "0.1.0-test",
	}
}

// TestOpenAIFirstRunCalibratedCapBeatsTheClock is the TTP-156 gate. For one
// and two streams, on a server that batches and one that serialises, with
// default options: every request carries a server-counted token figure, the
// summary carries a rate where the card's Sample row reads one, no
// tokens_uncounted caveat fires, the calibration is on the tape, the cap the
// calibration planned is the one the requests carried, and the whole run fit
// inside the clock that stays armed behind it.
func TestOpenAIFirstRunCalibratedCapBeatsTheClock(t *testing.T) {
	for _, mode := range []struct {
		name   string
		serial bool
	}{{"parallel", false}, {"serial", true}} {
		for _, n := range []int{1, 2} {
			t.Run(fmt.Sprintf("%s%d", mode.name, n), func(t *testing.T) {
				srv, _ := firstRunFake(t, mode.serial, true)
				opts := firstRunOpts(t, srv.URL, n)
				start := time.Now()
				tp, err := recorder.Record(context.Background(), opts)
				wall := time.Since(start)
				if err != nil {
					t.Fatalf("Record: %v", err)
				}
				s := tp.Summary
				if len(tp.Requests) != n {
					t.Fatalf("%d requests, want %d", len(tp.Requests), n)
				}

				// The cap the calibration planned: 0.8 x its own measured
				// rate x For / N, floored at tape.MinCutTokens, never above
				// what was there. The expectation is taken from the
				// calibration the tape carries — the planted rate is
				// firstRunRate's, and the measured one carries this fake's
				// few per cent of real wall around it, exactly as the
				// firsttry gate takes its target from the probe's own fit.
				if s.Probe == nil || s.Probe.Calibration == nil {
					t.Fatalf("Probe.Calibration = nil, want the one-request decode calibration")
				}
				cal := s.Probe.Calibration
				wantCap := int(0.8 * cal.PerSecond * firstRunFor.Seconds() / float64(n))
				wantCap = max(wantCap, tape.MinCutTokens)
				if s.Limit.MaxTokens != wantCap {
					t.Errorf("Limit.MaxTokens = %d, want %d (0.8 x the calibrated %.0f tok/s x %s / %d)",
						s.Limit.MaxTokens, wantCap, cal.PerSecond, firstRunFor, n)
				}
				if s.Limit.MaxTokensNamed {
					t.Error("Limit.MaxTokensNamed = true; the planned cap is the recorder's, not the user's")
				}

				// Every stream ended on its own — finish word, no cut — and
				// what it ended with is the server's own count, not a chunk
				// count: PredictedN equals the cap the fake counted out.
				for i, rec := range tp.Requests {
					if rec.Error != "" {
						t.Errorf("stream %d: Error %q", i, rec.Error)
					}
					if rec.Prompt.Cut {
						t.Errorf("stream %d: cut by the clock; the cap existed so the clock never needed to fire", i)
					}
					if rec.Prompt.FinishReason == "" {
						t.Errorf("stream %d: no finish reason; the stream did not end on its own", i)
					}
					if rec.Timings.PredictedNSource != "usage" {
						t.Errorf("stream %d: PredictedNSource = %q, want \"usage\" (the server counted the tokens)", i, rec.Timings.PredictedNSource)
					}
					if rec.Timings.PredictedN != wantCap {
						t.Errorf("stream %d: PredictedN = %d, want %d (the cap, server-counted)", i, rec.Timings.PredictedN, wantCap)
					}
				}

				// The summary's rate: the field the card's Sample row reads.
				if s.Timings.PredictedNSource != "usage" || s.Timings.PredictedPerSecond <= 0 {
					t.Errorf("summary rate = %v (%q), want a rate over the server's count",
						s.Timings.PredictedPerSecond, s.Timings.PredictedNSource)
				}
				if hasCaveat(&s, card.CodeTokensUncounted) {
					t.Errorf("caveats carry tokens_uncounted: %v", card.Caveats(&s))
				}

				// The calibration itself, on the tape.
				if cal.PredictedN < 8 || cal.PerSecond <= 0 || cal.DecodeMs <= 0 {
					t.Errorf("Calibration = %+v, want a counted, timed figure", *cal)
				}
				if cal.PromptN <= 0 || cal.PromptBytes <= 0 {
					t.Errorf("Calibration = %+v, want the usage prompt count and the bytes sent", *cal)
				}

				// The tape says the cap was planned against the clock.
				if s.Plan == nil {
					t.Fatal("Summary.Plan = nil; the calibrated cap was never planned onto the tape")
				}
				if s.Plan.Binding != tape.PlanBoundClock {
					t.Errorf("Plan.Binding = %q, want %q", s.Plan.Binding, tape.PlanBoundClock)
				}

				// The clock behind the cap never had to fire, and the whole
				// run — calibration included — stayed inside its budget.
				if wall > 1250*firstRunFor/1000 {
					t.Errorf("wall = %v, want <= 1.25 x %v", wall, firstRunFor)
				}
			})
		}
	}
}

// TestOpenAIFirstRunWithoutACalibrationRunsAsToday: a server whose
// calibration gets no usage records nothing, warns nothing, and the run
// behaves exactly as it did before the calibration existed — the clock cuts
// the streams, the usage chunk never arrives, and the card says so through
// the tokens_uncounted caveat.
func TestOpenAIFirstRunWithoutACalibrationRunsAsToday(t *testing.T) {
	srv, _ := firstRunFake(t, false, false)
	tp, err := recorder.Record(context.Background(), firstRunOpts(t, srv.URL, 1))
	if err != nil {
		t.Fatalf("Record: %v (a failed calibration must never fail a run)", err)
	}
	s := tp.Summary
	if s.Probe != nil && s.Probe.Calibration != nil {
		t.Errorf("Calibration = %+v, want nil from a stream that sent no usage", *s.Probe.Calibration)
	}
	if s.Limit.MaxTokensNamed {
		t.Error("MaxTokensNamed = true; nothing the user named changed")
	}
	if !hasCaveat(&s, card.CodeTokensUncounted) {
		t.Errorf("caveats lack tokens_uncounted: %v (the clock cut the stream, as it always did)", card.Caveats(&s))
	}
}

// TestPlanLineNeverPrintsACountItDoesNotHave is the TTP-160 gate: an
// OpenAI-kind run has no tokenized prompt length, and the plan line printed
// "1 × 0-token prompts (whole)" — a count that was never counted. The line
// omits the figure instead, and when the cap came from the decode calibration
// it says why in plain words (TTP-156), on one line.
func TestPlanLineNeverPrintsACountItDoesNotHave(t *testing.T) {
	// FAIL-first (2026-09-21): on the pre-change PlanLine this printed
	// "plan: 1 × 0-token prompts (whole) · cap 8,000".
	whole := &tape.Tape{Summary: tape.RunSummary{
		Limit: tape.LimitSummary{MaxTokens: 8000},
		Plan:  &tape.RunPlan{Binding: tape.PlanBoundWhole},
	}}
	want := "plan: 1 prompt (whole) · cap 8,000"
	if got := recorder.PlanLine(&whole.Summary, 1); got != want {
		t.Errorf("PlanLine =\n  %q\nwant\n  %q", got, want)
	}
	if strings.Contains(recorder.PlanLine(&whole.Summary, 4), "0-token") {
		t.Error("PlanLine printed a 0-token figure; 0 means unknown and prints as nothing")
	}

	// The calibrated cap names its reason: answers end before the clock, so
	// the server's own count arrives. One line, within 110 columns.
	calibrated := &tape.Tape{Summary: tape.RunSummary{
		Limit: tape.LimitSummary{For: 20 * time.Second, MaxTokens: 800},
		Probe: &tape.ProbeSummary{Calibration: &tape.DecodeCalibration{PerSecond: 90}},
		Plan:  &tape.RunPlan{Binding: tape.PlanBoundClock},
	}}
	line := recorder.PlanLine(&calibrated.Summary, 1)
	want = "plan: 1 prompt (clock-bound) · cap 800 (so answers end before the 20s clock and the server counts the tokens)"
	if line != want {
		t.Errorf("PlanLine =\n  %q\nwant\n  %q", line, want)
	}
	if len(line) > 110 {
		t.Errorf("the calibrated plan line is %d columns, over the 110 budget", len(line))
	}
}

// TestOpenAIFirstRunModelIsWhatWasSent is the TTP-157 gate: the tape's model
// stamp is the model the requests actually carried — the --param spelling
// included, which before this change stamped the run with the first listing
// id while the server ran another model entirely.
func TestOpenAIFirstRunModelIsWhatWasSent(t *testing.T) {
	for _, tc := range []struct {
		name string
		give func(o recorder.Options) recorder.Options
	}{
		{"flag", func(o recorder.Options) recorder.Options { o.Model = "beta:2b"; return o }},
		{"param", func(o recorder.Options) recorder.Options {
			o.Params = map[string]any{"model": "beta:2b"}
			return o
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// The param spelling is the FAIL-first case (2026-09-21): with
			// --param model=beta:2b the server ran beta:2b while the tape
			// said alpha:1b — the stamp came from /v1/models FirstID, not
			// from the request.
			srv, bodies := firstRunFake(t, false, true)
			opts := tc.give(firstRunOpts(t, srv.URL, 1))
			opts.MaxTokens = 64 // a named cap: no calibration, one request to read
			tp, err := recorder.Record(context.Background(), opts)
			if err != nil {
				t.Fatalf("Record: %v", err)
			}
			for i, b := range bodies() {
				if b["model"] != "beta:2b" {
					t.Errorf("request %d carried model %v, want beta:2b", i, b["model"])
				}
			}
			if tp.Summary.Model.FileName != "beta:2b" {
				t.Errorf("Model.FileName = %q, want the model the requests carried (beta:2b)", tp.Summary.Model.FileName)
			}
			if text := card.Text(&tp.Summary); !strings.Contains(text, "beta:2b") {
				t.Errorf("the card does not name the model that ran:\n%s", text)
			}
			if want := "beta-2b"; !strings.Contains(tp.Summary.ID, want) {
				t.Errorf("run ID = %q, want the slug of the model that ran (%q)", tp.Summary.ID, want)
			}
		})
	}

	// Both spellings on one command: the flag wins, and the raw param does
	// not ride along to override it on the wire.
	srv, bodies := firstRunFake(t, false, true)
	opts := firstRunOpts(t, srv.URL, 1)
	opts.Model, opts.Params = "beta:2b", map[string]any{"model": "alpha:1b"}
	opts.MaxTokens = 64
	if _, err := recorder.Record(context.Background(), opts); err != nil {
		t.Fatalf("Record: %v", err)
	}
	for i, b := range bodies() {
		if b["model"] != "beta:2b" {
			t.Errorf("request %d carried model %v, want the flag's beta:2b over the param's", i, b["model"])
		}
	}
}

// TestOpenAIFirstRunModelChoiceAndNote: the two ways the listing is used —
// a --model the listing does not carry is a usage error naming the ids, and a
// server that lists several, with nothing chosen, gets one note before the
// run saying which one it took. A single-model server notes nothing.
func TestOpenAIFirstRunModelChoiceAndNote(t *testing.T) {
	srv, _ := firstRunFake(t, false, true)

	opts := firstRunOpts(t, srv.URL, 1)
	opts.Model = "gamma:3b"
	opts.MaxTokens = 64
	_, err := recorder.Record(context.Background(), opts)
	if err == nil || !strings.Contains(err.Error(), "alpha:1b") || !strings.Contains(err.Error(), "beta:2b") {
		t.Errorf("Record err = %v, want a refusal naming both listed ids", err)
	}

	var notes []recorder.Event
	opts = firstRunOpts(t, srv.URL, 1)
	opts.MaxTokens = 64
	opts.Progress = func(ev recorder.Event) {
		if ev.Kind == recorder.EventNote {
			notes = append(notes, ev)
		}
	}
	if _, err := recorder.Record(context.Background(), opts); err != nil {
		t.Fatalf("Record: %v", err)
	}
	if len(notes) != 1 {
		t.Fatalf("%d note events, want exactly one", len(notes))
	}
	if msg := notes[0].Message; !strings.Contains(msg, "lists 2 models") ||
		!strings.Contains(msg, "using alpha:1b") || !strings.Contains(msg, "--model") {
		t.Errorf("note = %q, want the count, the default and the flag", msg)
	}
}

// TestOpenAIFirstRunPricesPrefillAndReadsTheServer is the TTP-168 / TTP-165
// / TTP-169 / TTP-167 gate (2026-09-21), against a fake shaped like the
// servers the evidence measured: prefill costs real time proportional to
// the prompt (serial), a vLLM-shaped 400 waits for any request whose prompt
// plus cap exceeds the listed max_model_len, every chunk names the engine's
// fingerprint, and usage counts the prompt as well as the answer.
//
// For one and four streams, default options with the clock scaled short:
// every stream ends on its own with BOTH counts server-stated, the summary
// carries a rate, the whole run fits the clock, the prefill the plan chose
// fits its share, the vLLM variant stamps the context it read, and the
// engine names itself.
//
// FAIL-first (2026-09-21, pre-change source, same fake): every case failed
// with lost streams and no figures — one stream against the vLLM variant
// died whole ("all streams failed: … 400 Bad Request: This model's maximum
// context length is 4096 tokens…"), the no-context variant ran the whole
// ~4.7k-token prompt and the clock cut it (PredictedNSource "chunks",
// PromptNSource "", no rate), and CtxSize and EngineClaim were 0 and "".
func TestOpenAIFirstRunPricesPrefillAndReadsTheServer(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  firstRunCfg
	}{
		{"vllm", firstRunCfg{serial: true, usage: true, prefill: true, maxModelLen: 4096, fingerprint: "vllm-0.29.0-b6a310fe"}},
		{"nocxt", firstRunCfg{serial: true, usage: true, prefill: true, fingerprint: "fp_ollama"}},
	} {
		for _, n := range []int{1, 4} {
			t.Run(fmt.Sprintf("%s%d", tc.name, n), func(t *testing.T) {
				srv, _ := firstRunFakeCfg(t, tc.cfg)
				opts := firstRunOpts(t, srv.URL, n)
				start := time.Now()
				tp, err := recorder.Record(context.Background(), opts)
				wall := time.Since(start)
				if err != nil {
					t.Fatalf("Record: %v", err)
				}
				s := tp.Summary
				if len(tp.Requests) != n {
					t.Fatalf("%d requests, want %d", len(tp.Requests), n)
				}

				// No stream lost: not to the clock (the cap budgeted for it)
				// and not to a context the run could have read.
				if s.Aggregate.StreamsFailed != 0 {
					t.Fatalf("%d of %d streams failed; the plan existed to keep every stream",
						s.Aggregate.StreamsFailed, s.Aggregate.Streams)
				}
				for i, rec := range tp.Requests {
					if rec.Error != "" {
						t.Errorf("stream %d: Error %q", i, rec.Error)
					}
					if rec.Prompt.Cut {
						t.Errorf("stream %d: cut by the clock; the cap budgeted the prefill the clock no longer has to absorb", i)
					}
					if rec.Timings.PredictedNSource != "usage" {
						t.Errorf("stream %d: PredictedNSource = %q, want \"usage\"", i, rec.Timings.PredictedNSource)
					}
					// TTP-167: the prompt count is the server's own statement
					// and rides the record, so the Context row says a number.
					if rec.Timings.PromptNSource != "usage" {
						t.Errorf("stream %d: PromptNSource = %q, want \"usage\" (the usage chunk counted the prompt)", i, rec.Timings.PromptNSource)
					}
					if rec.Timings.PromptN < 256 {
						t.Errorf("stream %d: PromptN = %d, want the server's count of the trimmed prompt", i, rec.Timings.PromptN)
					}
				}

				// The summary's figures: a rate, a prompt total, the source
				// both came from.
				if s.Timings.PredictedPerSecond <= 0 {
					t.Errorf("summary rate = %v, want a rate over the server's count", s.Timings.PredictedPerSecond)
				}
				if s.Timings.PromptNSource != "usage" || s.Timings.PromptN <= 0 {
					t.Errorf("summary prompt figure = %d (%q), want the usage count", s.Timings.PromptN, s.Timings.PromptNSource)
				}
				if text := card.Text(&s); strings.Contains(text, "? in /") {
					t.Errorf("the card's Context row still reads \"? in\":\n%s", text)
				}

				// The calibration priced prefill and the plan spent it: the
				// prefill calibration is on the tape, the plan is
				// clock-bound with a priced target, and the prefill the
				// plan's own lengths predict fits its share of the clock.
				if s.Probe == nil || s.Probe.Calibration == nil || s.Probe.Calibration.Prefill == nil {
					t.Fatalf("Probe.Calibration.Prefill = nil, want the second calibration request's figures")
				}
				pf := s.Probe.Calibration.Prefill
				if pf.PromptN < 256 || pf.PerSecond <= 0 || pf.TTFTMs <= 0 {
					t.Errorf("Calibration.Prefill = %+v, want a counted, timed figure", *pf)
				}
				if s.Plan == nil || s.Plan.Binding != tape.PlanBoundClock {
					t.Fatalf("Plan = %+v, want a clock-bound plan", s.Plan)
				}
				if s.Plan.Tokenized {
					t.Error("Plan.Tokenized = true; nothing tokenized this path, it was priced")
				}
				if s.Plan.TargetTokens <= 0 || s.PromptTrimTokens != s.Plan.TargetTokens {
					t.Errorf("Plan.TargetTokens = %d, PromptTrimTokens = %d, want the priced target on both",
						s.Plan.TargetTokens, s.PromptTrimTokens)
				}
				predicted := float64(n) * float64(s.Plan.LongestPromptTokens) / pf.PerSecond
				if limit := 0.30 * firstRunFor.Seconds(); predicted > limit {
					t.Errorf("predicted prefill %v s exceeds the 30%% share (%v s) of the %v clock", predicted, limit, firstRunFor)
				}

				// The vLLM variant read its context: stamped on the server,
				// planned against, never met as a 400.
				if tc.cfg.maxModelLen > 0 {
					if s.Server.CtxSize != tc.cfg.maxModelLen {
						t.Errorf("Server.CtxSize = %d, want the listed max_model_len %d", s.Server.CtxSize, tc.cfg.maxModelLen)
					}
					if s.Plan.SlotCtx != tc.cfg.maxModelLen {
						t.Errorf("Plan.SlotCtx = %d, want %d", s.Plan.SlotCtx, tc.cfg.maxModelLen)
					}
				}

				// The engine said who it is (TTP-169): the fingerprint,
				// verbatim, where the claim goes when the user claimed
				// nothing.
				if s.Server.EngineClaim != tc.cfg.fingerprint {
					t.Errorf("EngineClaim = %q, want the chunks' system_fingerprint %q", s.Server.EngineClaim, tc.cfg.fingerprint)
				}

				// The whole run — both calibration requests included — fit
				// the clock it planned inside.
				if wall > 1250*firstRunFor/1000 {
					t.Errorf("wall = %v, want <= 1.25 x %v", wall, firstRunFor)
				}
			})
		}
	}
}

// TestOpenAIFirstRunPlanLineIsPriced pins the line the priced path prints
// (TTP-168): the target marked "~" and "priced", the prefill the plan
// budgeted, the cap, and "ctx" — not "slot" — for the listed max_model_len.
// One line, inside 110 columns.
func TestOpenAIFirstRunPlanLineIsPriced(t *testing.T) {
	// FAIL-first (2026-09-21, pre-change PlanLine): the same summary printed
	// "plan: 4 × 800-token prompts (clock-bound) · cap 210 · slot 4,096" —
	// an unmarked count, no prefill figure, and a vLLM user's slots.
	s := &tape.RunSummary{
		Concurrency: 4,
		Server:      tape.ServerInfo{Kind: tape.ServerOpenAI},
		Limit:       tape.LimitSummary{For: 20 * time.Second, MaxTokens: 210},
		Probe: &tape.ProbeSummary{Calibration: &tape.DecodeCalibration{
			PerSecond: 52,
			Prefill:   &tape.CalibrationPrefill{PromptN: 1024, PromptBytes: 4096, PerSecond: 640},
		}},
		Plan: &tape.RunPlan{
			TargetTokens: 800, Binding: tape.PlanBoundClock,
			PrefillBudgetMs: 5000, SlotCtx: 4096,
			LongestPromptTokens: 800,
		},
	}
	want := "plan: 4 × ~800-token prompts (clock-bound, priced) · ~5 s prefill · cap 210 · ctx 4,096"
	if got := recorder.PlanLine(s, 4); got != want {
		t.Errorf("PlanLine =\n  %q\nwant\n  %q", got, want)
	}
	if line := recorder.PlanLine(s, 4); len(line) > 110 {
		t.Errorf("the priced plan line is %d columns, over the 110 budget", len(line))
	}

	// Without the prefill calibration the old shape holds byte for byte —
	// a decode-only cap on a whole-prompt run.
	old := &tape.RunSummary{
		Concurrency: 1,
		Limit:       tape.LimitSummary{For: 20 * time.Second, MaxTokens: 800},
		Probe:       &tape.ProbeSummary{Calibration: &tape.DecodeCalibration{PerSecond: 90}},
		Plan:        &tape.RunPlan{Binding: tape.PlanBoundClock},
	}
	want = "plan: 1 prompt (clock-bound) · cap 800 (so answers end before the 20s clock and the server counts the tokens)"
	if got := recorder.PlanLine(old, 1); got != want {
		t.Errorf("PlanLine =\n  %q\nwant\n  %q", got, want)
	}
}
