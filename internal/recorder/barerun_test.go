package recorder_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/midagedev/toktape/internal/card"
	"github.com/midagedev/toktape/internal/gpu"
	"github.com/midagedev/toktape/internal/recorder"
)

// The bare run: what a stranger who typed `toktape --url …` and nothing else
// gets (TTP-139, lead, 2026-09-20).
//
// This file exists because nothing in this repo rendered that card on
// purpose, and that is why TTP-139 survived a month. It was found by running
// the tool the way the README tells a stranger to, and a defect a human has
// to stumble into is a defect with no gate under it. The card is the
// artifact; the assertions below are the few things that must be true of it
// whatever else changes.
//
// It deliberately names no options beyond what a run cannot start without —
// a URL, a temp dir, a null GPU collector and a fixed clock, so the card is
// reproducible. Every budget, every cap and every request parameter is the
// recorder's own default, which is the whole point: a test that passes
// options is testing something else.

// reasoningServer answers the chat path with a stream that is all reasoning
// and no answer — the shape a reasoning model produces when the budget ends
// before the thought does, which is the run TTP-139 was filed about.
func reasoningServer(t *testing.T) *httptest.Server {
	t.Helper()
	sse, err := os.ReadFile("../server/testdata/stream_reasoning_only.sse")
	if err != nil {
		t.Fatalf("read reasoning fixture: %v", err)
	}
	mux := fakeMux(t)
	// fakeMux's chat route serves the ordinary fixture; this one replaces it.
	// A ServeMux will not re-register a pattern, so the route is wrapped
	// rather than overwritten: the handler here answers first and the run
	// never reaches the original.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			mux.ServeHTTP(w, r)
			return
		}
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
			time.Sleep(time.Millisecond)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// bareOptions is every option a run cannot start without, and nothing else.
func bareOptions(t *testing.T, srv *httptest.Server) recorder.Options {
	t.Helper()
	return recorder.Options{
		BaseURL:        srv.URL,
		FSRoot:         t.TempDir(),
		GPU:            gpu.Null{},
		SampleInterval: 50 * time.Millisecond,
		Clock:          fixedClock{time.Date(2026, 9, 20, 8, 0, 0, 0, time.UTC)},
	}
}

// TestBareRunAgainstAReasoningModel renders the zero-flag card and holds it
// to what TTP-139 decided it owes its reader.
//
// The decision that ticket closed on is that the run itself does not change:
// a reasoning model's tokens are decode tokens at the decode rate, so the
// figures are a measurement whether the model thought or answered, and
// sending the thinking switch by default would have changed what this tool
// measures to make the card prettier. What the card owes is an honest
// account of what came back, and advice that names a flag which would
// actually help.
func TestBareRunAgainstAReasoningModel(t *testing.T) {
	tp, err := recorder.Record(context.Background(), bareOptions(t, reasoningServer(t)))
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	s := &tp.Summary
	text := card.Text(s)
	t.Logf("the card a stranger gets:\n%s", text)

	if s.Timings.PredictedN <= 0 {
		t.Fatalf("the run generated nothing, so this card is about nothing: %+v", s.Timings)
	}
	if s.Timings.ReasoningN != s.Timings.PredictedN {
		t.Fatalf("fixture is not all-reasoning (%d of %d), so the warning under test cannot fire",
			s.Timings.ReasoningN, s.Timings.PredictedN)
	}

	// The rate is a measurement, not a casualty. This is the half of TTP-139
	// that says the default stays: whatever the tokens were, they were
	// decoded, and a card that refused to report the rate would be hiding a
	// figure it observed.
	if s.Timings.PredictedPerSecond <= 0 {
		t.Errorf("no decode rate on a run that decoded %d tokens", s.Timings.PredictedN)
	}

	// The card says the run produced no answer, and says it once.
	if !strings.Contains(text, "answer cut") {
		t.Errorf("the card does not say the run never answered:\n%s", text)
	}

	// And the advice names a flag that would help. Not --n-predict: this run
	// named no cap, so it had a clock, and naming --n-predict turns the clock
	// off rather than lengthening the run (Options.limit's table, row three).
	if !strings.Contains(text, "--for") {
		t.Errorf("the advice does not name a flag that would help:\n%s", text)
	}
	if strings.Contains(text, "raise --n-predict") {
		t.Errorf("the card tells a default run to raise a cap it never named:\n%s", text)
	}

	// The cap a bare run carries is the runaway guard, at its documented
	// value, and is recorded as not the user's.
	if s.Limit.MaxTokensNamed {
		t.Error("a bare run reports its cap as the user's own")
	}
	if s.Limit.MaxTokens != recorder.DefaultMaxTokens {
		t.Errorf("bare run sent max_tokens %d, want the guard's %d",
			s.Limit.MaxTokens, recorder.DefaultMaxTokens)
	}
	// What this cannot assert (lead, 2026-09-20): that the guard did not END
	// the run. The fixture is a canned SSE, so its finish word is whatever
	// was recorded into the file — "length" here — no matter what n_predict
	// the run actually sent, and the card duly reports "1 of 1 hit the cap".
	// Asserting otherwise would be asserting against the fixture rather than
	// the code. The guard-versus-clock contract is held where it is real,
	// against the constants themselves: TestTheGuardCannotBeatTheClock.
}
