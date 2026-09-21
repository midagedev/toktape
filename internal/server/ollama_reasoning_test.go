package server

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/midagedev/toktape/internal/tape"
)

// The Ollama spelling of the thinking delta (TTP-177, 2026-09-21). Measured
// on the wire, Ollama 0.34.2 with qwen3:1.7b: every delta of a 48-token
// answer carried its text under `reasoning` with `content` present and empty
// beside it, the parser knew only vLLM's and exl3-serve's `reasoning_content`,
// and a thinking model's first run recorded no token at all — no calibration,
// no rate, no TTFT but the answer's (which never arrived). The fixture is the
// capture itself; the assertions are the outcome (a count, a rate, a TTFT at
// the first thinking delta), never the key: a test that asserted
// json:"reasoning" would pass a future implementation that is still broken
// for the fourth engine.

// TestReduceOllamaReasoningFixture replays the captured bytes: a
// thinking-only stream still produces a token count and a rate, and its TTFT
// is the arrival of the first reasoning delta, not of an answer token that
// never existed.
//
// FAIL-first (2026-09-21, unmodified sources, output in the round report):
// the parser recorded 0 tokens, PredictedNSource fell to "chunks", and every
// count-and-rate assertion below failed.
func TestReduceOllamaReasoningFixture(t *testing.T) {
	var reasoning, tokens []tape.TokenEvent
	rec, tim := loadFixture(t, "stream_ollama_reasoning", StreamHooks{
		OnReasoning: func(ev tape.TokenEvent) { reasoning = append(reasoning, ev) },
		OnToken:     func(ev tape.TokenEvent) { tokens = append(tokens, ev) },
	})

	// The count: 46 deltas that carried text, not 49 events and not 0.
	if got, want := len(rec.Tokens), 46; got != want {
		t.Fatalf("tokens = %d, want %d (the thinking deltas; the empty content, the finish chunk and the usage chunk are not tokens)", got, want)
	}
	for i, tk := range rec.Tokens {
		if !tk.Reasoning {
			t.Fatalf("Tokens[%d].Reasoning = false; this stream never produced an answer token", i)
		}
	}
	if got, want := len(tokens), 46; got != want {
		t.Errorf("OnToken fired %d times, want %d (a thinking delta is a decode token)", got, want)
	}
	if got, want := len(reasoning), 46; got != want {
		t.Errorf("OnReasoning fired %d times, want %d", got, want)
	}
	if got, want := rec.Prompt.ReasoningN, 46; got != want {
		t.Errorf("Prompt.ReasoningN = %d, want %d", got, want)
	}
	if got, want := rec.Timings.ReasoningN, 46; got != want {
		t.Errorf("Timings.ReasoningN = %d, want %d (the card's Context row reads this)", got, want)
	}
	if got := rec.Prompt.Completion; got != "" {
		t.Errorf("Completion = %q, want empty (content was present and empty on every chunk)", got)
	}
	if !strings.HasPrefix(rec.Prompt.Reasoning, "Okay, so I need to count upward from one, digits only, one number per line, and keep going until told to stop.") {
		t.Errorf("Reasoning = %q..., want the concatenated thinking text", clip(rec.Prompt.Reasoning, 60))
	}
	if got, want := rec.Prompt.FinishReason, "length"; got != want {
		t.Errorf("FinishReason = %q, want %q", got, want)
	}

	// The server's figures are the record: the usage chunk's counts.
	if got, want := rec.Timings.Source, "client"; got != want {
		t.Errorf("Source = %q, want %q (no timings object ever arrived)", got, want)
	}
	if got, want := rec.Timings.PredictedNSource, "usage"; got != want {
		t.Fatalf("PredictedNSource = %q, want %q (the server counted the answer)", got, want)
	}
	if got, want := rec.Timings.PredictedN, 48; got != want {
		t.Errorf("PredictedN = %d, want %d (usage.completion_tokens)", got, want)
	}
	if got, want := rec.Timings.PromptN, 37; got != want {
		t.Errorf("PromptN = %d, want %d (usage.prompt_tokens)", got, want)
	}
	if got, want := rec.Timings.PromptNSource, "usage"; got != want {
		t.Errorf("PromptNSource = %q, want %q", got, want)
	}
	if got, want := rec.Timings.CacheN, 4; got != want {
		t.Errorf("CacheN = %d, want %d (prompt_tokens_details.cached_tokens)", got, want)
	}
	// The returned server timings carry no count of their own — no timings
	// object ever arrived. The usage chunk's count above is the record, which
	// is exactly what PredictedNSource says.
	if got := tim.PredictedN; got != 0 {
		t.Errorf("server timings predicted_n = %d, want 0 (this server reports no timings object)", got)
	}

	// The client's own count, beside the server's: 46 parsed of 48 counted.
	// The two going apart is the signature of an unparsed dialect whatever
	// the key is called, and it is the figure the card's caveat reads.
	if got, want := rec.Timings.TokensObserved, 46; got != want {
		t.Errorf("TokensObserved = %d, want %d (the deltas the client parsed)", got, want)
	}

	// TTFT is the first REASONING delta's arrival. With the field unparsed
	// the first observed token would have been the first answer token —
	// which this stream never produced.
	if got, want := rec.Timings.TTFTMs, 612.0; got != want {
		t.Errorf("TTFTMs = %v, want %v (the first thinking delta's arrival)", got, want)
	}

	// The rate exists and is the server-counted figure over the observed
	// window: (48-1) tokens over the 990 ms the 46 parsed deltas span.
	if rec.Timings.PredictedPerSecond <= 0 || rec.Timings.ClientPredictedPerSecond <= 0 {
		t.Fatalf("rates = %v/%v, want both > 0 (a thinking-only stream still yields a rate)",
			rec.Timings.PredictedPerSecond, rec.Timings.ClientPredictedPerSecond)
	}
	window := 1602.0 - 612.0
	if want := 47.0 / (window / 1000.0); rec.Timings.PredictedPerSecond != want {
		t.Errorf("PredictedPerSecond = %v, want 47 over the observed window = %v", rec.Timings.PredictedPerSecond, want)
	}
	if rec.Timings.ClientPredictedPerSecond != rec.Timings.PredictedPerSecond {
		t.Errorf("client rate %v != the recalibrated %v on a one-clock stream",
			rec.Timings.ClientPredictedPerSecond, rec.Timings.PredictedPerSecond)
	}
	if rec.Timings.ClientAgreesWithServer {
		t.Error("ClientAgreesWithServer = true; one clock cannot agree with itself")
	}
	if got, want := rec.Timings.DecodeLabel, "decode"; got != want {
		t.Errorf("DecodeLabel = %q, want %q (48 counted tokens)", got, want)
	}
	if rec.Timings.ITLp50Ms <= 0 {
		t.Errorf("ITLp50Ms = %v, want the per-delta latency of a parsed timeline", rec.Timings.ITLp50Ms)
	}
}

// TestReasoningSpellingIsNotTheContract: the two spellings this repo has
// measured are one fact. The same text under either key must reduce to the
// same record, and a delta that carries both must count once — the server
// counts one decode step, and the shared Index sequence must not split it in
// two.
//
// FAIL-first (2026-09-21, unmodified sources): the `reasoning` column
// recorded no token and the identical-record assertion failed 0 against 3.
func TestReasoningSpellingIsNotTheContract(t *testing.T) {
	// Two three-delta streams, same text, same arrivals, one spelling apart.
	build := func(key string) []byte {
		var b strings.Builder
		words := []string{"Count", " upward", " slowly"}
		b.WriteString(fmt.Sprintf("data: {\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"\",\"%s\":\"%s\"}}]}\n\n", key, words[0]))
		b.WriteString(fmt.Sprintf("data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"\",\"%s\":\"%s\"}}]}\n\n", key, words[1]))
		b.WriteString(fmt.Sprintf("data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"\",\"%s\":\"%s\"}}]}\n\n", key, words[2]))
		b.WriteString("data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"length\"}],\"usage\":{\"prompt_tokens\":9,\"completion_tokens\":4}}\n\n")
		b.WriteString("data: [DONE]\n\n")
		return []byte(b.String())
	}
	arrivals := []time.Duration{40 * time.Millisecond, 60 * time.Millisecond, 80 * time.Millisecond, 84 * time.Millisecond, 86 * time.Millisecond}
	recA, _, err := ReplayStream(build("reasoning_content"), arrivals, StreamHooks{})
	if err != nil {
		t.Fatalf("replay reasoning_content: %v", err)
	}
	recB, _, err := ReplayStream(build("reasoning"), arrivals, StreamHooks{})
	if err != nil {
		t.Fatalf("replay reasoning: %v", err)
	}
	if len(recA.Tokens) != 3 || len(recB.Tokens) != 3 {
		t.Fatalf("tokens = %d and %d, want 3 and 3 (the spelling is not the contract)", len(recA.Tokens), len(recB.Tokens))
	}
	if recA.Timings != recB.Timings {
		t.Errorf("the two spellings reduced differently:\n%+v\n%+v", recA.Timings, recB.Timings)
	}
	if recA.Tokens[0] != recB.Tokens[0] || recA.Prompt.Reasoning != recB.Prompt.Reasoning {
		t.Errorf("the token events or the transcript differ between spellings: %+v vs %+v", recA.Tokens[0], recB.Tokens[0])
	}

	// Both spellings on one delta: one decode step, one token, one text.
	both := []byte("data: {\"choices\":[{\"index\":0,\"delta\":{" +
		"\"content\":\"\",\"reasoning\":\"thought\",\"reasoning_content\":\"thought\"}}]}\n\n" +
		"data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":5,\"completion_tokens\":1}}\n\n" +
		"data: [DONE]\n\n")
	recC, _, err := ReplayStream(both, []time.Duration{30 * time.Millisecond, 33 * time.Millisecond, 35 * time.Millisecond}, StreamHooks{})
	if err != nil {
		t.Fatalf("replay both spellings: %v", err)
	}
	if got, want := len(recC.Tokens), 1; got != want {
		t.Fatalf("a delta carrying both spellings recorded %d tokens, want %d (one decode step, not two)", got, want)
	}
	if got, want := recC.Prompt.Reasoning, "thought"; got != want {
		t.Errorf("Reasoning = %q, want %q (the text once, not doubled)", got, want)
	}
	if got, want := recC.Timings.TokensObserved, 1; got != want {
		t.Errorf("TokensObserved = %d, want %d", got, want)
	}
}

// TestReduceFillsTokensObserved: the client's own count is a function of the
// timeline, like every client figure — a hand-assembled record gets the
// count of the tokens it supplied, and a second Reduce does not change it.
func TestReduceFillsTokensObserved(t *testing.T) {
	tok := func(ms ...int) (out []tape.TokenEvent) {
		for i, m := range ms {
			out = append(out, tape.TokenEvent{T: time.Duration(m) * time.Millisecond, Index: i, Text: "x"})
		}
		return out
	}
	rec := tape.RequestRecord{Tokens: tok(100, 120, 140, 160)}
	s := Reduce(&rec, time.Time{}, 0)
	if got, want := s.TokensObserved, 4; got != want {
		t.Fatalf("TokensObserved = %d, want %d (the timeline's own count)", got, want)
	}
	if again := Reduce(&rec, time.Time{}, 0); again.TokensObserved != s.TokensObserved {
		t.Errorf("a second Reduce changed TokensObserved from %d to %d", s.TokensObserved, again.TokensObserved)
	}
	empty := tape.RequestRecord{}
	if got := Reduce(&empty, time.Time{}, 0).TokensObserved; got != 0 {
		t.Errorf("TokensObserved = %d on no tokens, want 0 (measured zero, not unknown)", got)
	}
}
