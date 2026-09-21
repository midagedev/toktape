package recorder_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/midagedev/toktape/internal/card"
	"github.com/midagedev/toktape/internal/recorder"
	"github.com/midagedev/toktape/internal/tape"
)

// The dropped-calibration gate (TTP-177, 2026-09-21). The cap and the whole
// plan behind it depend on the decode calibration, and until this change a
// dropped one recorded nothing and said nothing — which is exactly how a
// whole first run was lost to one unknown spelling: the calibration's 48
// server-counted tokens parsed to 0, the `len(Tokens) < 2` guard fired in
// silence, the cap fell back to the 8,000-token runaway guard, and the only
// sentence on the card was tokens_uncounted. A drop now names its guard in
// one note; the run still proceeds exactly as before.

// dialectFake is a server whose thinking deltas carry text under a key the
// parser does not know (content present and empty beside it — the Ollama
// capture's shape), with usage only on a stream that ran to its own end.
// key is the spelling; "thoughts" stands for the fourth engine's, the one
// after this fix teaches the parser the third.
//
// The phase order is the capture's, and it is load-bearing: the model thinks
// BEFORE it answers, so a short-capped stream (the calibration's 48) never
// leaves the thinking phase, while the run's own stream reaches its answer
// phase and the clock's floor — the exact shape the real first run had. The
// thinking phase is 60 deltas at 5 ms each, then 100 answer deltas, then
// thinking again until the cap.
func dialectFake(t *testing.T, key string, usage bool) *httptest.Server {
	t.Helper()
	const (
		tick       = 5 * time.Millisecond
		thinkTicks = 60
		answerN    = 100
	)
	mux := http.NewServeMux()
	mux.HandleFunc("/props", func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) })
	mux.HandleFunc("/slots", func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) })
	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"q:1.7b","object":"model"}]}`))
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
		gone := func() bool { return r.Context().Err() != nil }
		cap := in.MaxTokens
		if cap <= 0 {
			cap = 1 << 20
		}
		if !emit(`{"id":"dt","model":"` + in.Model + `","choices":[{"index":0,"delta":{"role":"assistant"}}]}`) {
			return
		}
		think, answer := 0, 0
		for i := 0; i < cap; i++ {
			if gone() {
				return
			}
			time.Sleep(tick)
			payload := fmt.Sprintf(`{"id":"dt","model":%q,"choices":[{"index":0,"delta":{"content":"","%s":"t%d"}}]}`, in.Model, key, think)
			// The answer phase sits inside the thinking timeline the way a
			// reasoning model's does: 100 content deltas once the thinking
			// has run its course, then back to thinking until the cap.
			if i >= thinkTicks && answer < answerN {
				payload = fmt.Sprintf(`{"id":"dt","model":%q,"choices":[{"index":0,"delta":{"content":"a%d"}}]}`, in.Model, answer)
				answer++
			} else {
				think++
			}
			if !emit(payload) {
				return
			}
		}
		final := fmt.Sprintf(`{"id":"dt","model":%q,"choices":[{"index":0,"delta":{},"finish_reason":"length"}]`, in.Model)
		if usage {
			final += `,"usage":{"prompt_tokens":37,"prompt_tokens_details":{"cached_tokens":4},"completion_tokens":` + fmt.Sprint(cap) + `}`
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
	return srv
}

// notesOf records every note event of one run.
func notesOf(t *testing.T, opts recorder.Options) ([]string, *tape.Tape, error) {
	t.Helper()
	var notes []string
	opts.Progress = func(ev recorder.Event) {
		if ev.Kind == recorder.EventNote {
			notes = append(notes, ev.Message)
		}
	}
	tp, err := recorder.Record(context.Background(), opts)
	return notes, tp, err
}

// calibrationNote is the one dropped-calibration note of a run, or "".
func calibrationNote(notes []string) string {
	for _, n := range notes {
		if strings.HasPrefix(n, "no decode calibration: ") {
			return n
		}
	}
	return ""
}

// TestADroppedCalibrationNamesItsGuard: every way the decode calibration can
// be dropped says which one of them dropped it, in one note. The three here
// are the ones the measured defects produced — a stream that never got its
// usage chunk (TTP-156's server), a request the server refused, and TTP-177's
// own: 48 tokens counted, none parsed, the guard nobody could see fire.
//
// FAIL-first (2026-09-21, unmodified sources, output in the round report):
// no note carried the drop on any of the three.
func TestADroppedCalibrationNamesItsGuard(t *testing.T) {
	t.Run("a stream that never sent usage", func(t *testing.T) {
		srv, _ := firstRunFake(t, false, false)
		notes, tp, err := notesOf(t, firstRunOpts(t, srv.URL, 1))
		if err != nil {
			t.Fatalf("Record: %v (a dropped calibration must never fail a run)", err)
		}
		if note := calibrationNote(notes); !strings.Contains(note, "usage") {
			t.Fatalf("the drop note = %q, want it to name the missing usage figure (notes %q)", note, notes)
		}
		if tp.Summary.Probe != nil && tp.Summary.Probe.Calibration != nil {
			t.Error("a calibration was recorded from a stream that sent no usage")
		}
	})

	t.Run("a request the server refused", func(t *testing.T) {
		mux := http.NewServeMux()
		mux.HandleFunc("/props", func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) })
		mux.HandleFunc("/slots", func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) })
		mux.HandleFunc("/v1/models", func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"m","object":"model"}]}`))
		})
		mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "no model loaded", http.StatusServiceUnavailable)
		})
		srv := httptest.NewServer(mux)
		t.Cleanup(srv.Close)
		notes, _, _ := notesOf(t, firstRunOpts(t, srv.URL, 1))
		if note := calibrationNote(notes); !strings.Contains(note, "failed") {
			t.Fatalf("the drop note = %q, want it to name the failed request (notes %q)", note, notes)
		}
	})

	t.Run("tokens counted the client never parsed (TTP-177's own shape)", func(t *testing.T) {
		srv := dialectFake(t, "thoughts", true)
		notes, tp, err := notesOf(t, firstRunOpts(t, srv.URL, 1))
		if err != nil {
			t.Fatalf("Record: %v (the run proceeds exactly as before)", err)
		}
		s := tp.Summary
		// The note names both counts: 48 server-counted, 0 parsed — the same
		// contradiction the card's caveat carries, said on the way past.
		note := calibrationNote(notes)
		if !strings.Contains(note, "parsed 0") || !strings.Contains(note, "48") {
			t.Fatalf("the drop note = %q, want it to name 0 parsed of 48 counted (notes %q)", note, notes)
		}
		if s.Probe != nil && s.Probe.Calibration != nil {
			t.Error("a calibration was recorded from a stream whose tokens were all invisible")
		}
		// And the run itself is exactly today's cut shape: the cap fell back,
		// the clock cut, no usage arrived, and the card says so through the
		// code that already owned it.
		if !hasCaveat(&s, card.CodeTokensUncounted) {
			t.Errorf("caveats lack tokens_uncounted: %v", card.Caveats(&s))
		}
	})
}

// TestAnUnparsedDialectRunCarriesTheContradiction: the general gate of
// TTP-177, end to end. A run whose streams end on their own against an
// engine whose thinking field this toktape does not read — the fourth
// engine, whatever its key is called — gets the server's count AND the
// client's own on the summary, and the caveat that says the two disagree.
// The named cap is 64 and the thinking phase is 300 ms of deltas, so the
// model answers its last 4: the partial shape, 4 parsed of 64 counted.
//
// FAIL-first (2026-09-21, unmodified sources): the summary carried the
// server's 64 and no observed count, and no caveat said a word.
func TestAnUnparsedDialectRunCarriesTheContradiction(t *testing.T) {
	srv := dialectFake(t, "thoughts", true)
	opts := firstRunOpts(t, srv.URL, 1)
	opts.MaxTokens = 64 // a named cap: the stream ends on its own, so usage arrives
	tp, err := recorder.Record(context.Background(), opts)
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	s := tp.Summary
	if s.Timings.PredictedNSource != "usage" || s.Timings.PredictedN != 64 {
		t.Fatalf("PredictedN = %d (%q), want 64 (usage) — the server's count is the record",
			s.Timings.PredictedN, s.Timings.PredictedNSource)
	}
	if s.Timings.TokensObserved != 4 {
		t.Fatalf("TokensObserved = %d, want 4 (the answer deltas; the 60 thinking ones were invisible)",
			s.Timings.TokensObserved)
	}
	if s.Timings.TTFTMs <= 0 {
		t.Fatalf("TTFTMs = %v, want the first answer delta — the thinking was invisible", s.Timings.TTFTMs)
	}
	if !hasCaveat(&s, "tokens_unseen") {
		t.Fatalf("caveats lack tokens_unseen: %v — 4 parsed of 64 counted is the code's whole reason",
			card.Caveats(&s))
	}
	for _, c := range card.Caveats(&s) {
		if c.Code == "tokens_unseen" {
			if !strings.Contains(c.Text, "64") || !strings.Contains(c.Text, "4") {
				t.Errorf("the sentence does not name both counts: %q", c.Text)
			}
		}
	}
	if hasCaveat(&s, card.CodeTokensUncounted) {
		t.Errorf("caveats carry tokens_uncounted beside a server that counted: %v", card.Caveats(&s))
	}
}
