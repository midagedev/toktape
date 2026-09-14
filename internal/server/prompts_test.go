package server

import (
	"strings"
	"testing"

	"github.com/midagedev/toktape/internal/tape"
)

// The length a built-in prompt must have, in characters, because this package
// has no tokenizer (TTP-84, 2026-09-14).
//
// The conversion is the pessimistic one on each side. English prose runs about
// 4 characters to a token (0.75 words per token) on the Llama and Qwen
// tokenizers, and code, logs and numbers run denser, nearer 3. So a floor
// counted at 4 characters per token is a floor for every kind of prompt in the
// set, and a ceiling counted at 3 is a ceiling for every kind.
//
// The floor is twice tape.MinPrefillPromptTokens before the chat template adds
// a single token of its own: the template's share differs per model, so the
// prompt must clear the threshold without counting on it. The ceiling is five
// times the threshold, 500 tokens: at the 60 tok/s low end of honest prefill
// on this repo's hero rig that is about 8 s before the first token of a run
// whose default budget is 20 s, and a longer prompt starts spending the
// decode's time on prefill.
const (
	proseCharsPerToken = 4
	codeCharsPerToken  = 3
	minPromptChars     = proseCharsPerToken * 2 * tape.MinPrefillPromptTokens
	maxPromptChars     = codeCharsPerToken * 5 * tape.MinPrefillPromptTokens
)

// TestDefaultPromptsArePrefillMeasurements: every built-in prompt is long
// enough that the card presents its prefill as a prefill measurement, and
// short enough that a slow rig's prefill does not eat the run's budget.
func TestDefaultPromptsArePrefillMeasurements(t *testing.T) {
	if len(defaultPrompts) < 16 {
		t.Errorf("%d built-in prompts, want at least 16: a concurrent run needs one distinct prompt per stream before the numbered repetition starts", len(defaultPrompts))
	}
	shortest, longest := 0, 0
	for i, text := range defaultPrompts {
		if n := len(text); n < minPromptChars || n > maxPromptChars {
			t.Errorf("prompt %d is %d characters, want %d to %d (about %d to %d tokens): %.80q",
				i, n, minPromptChars, maxPromptChars, minPromptChars/proseCharsPerToken, maxPromptChars/codeCharsPerToken, text)
		}
		if len(text) < len(defaultPrompts[shortest]) {
			shortest = i
		}
		if len(text) > len(defaultPrompts[longest]) {
			longest = i
		}
	}
	for _, i := range []int{shortest, longest} {
		text := defaultPrompts[i]
		t.Logf("prompt %d: %d characters, %d words", i, len(text), len(strings.Fields(text)))
	}
}

// TestDefaultPromptsDistinctFromTheFirstWord: concurrent streams must not
// share a prefix, or one stream's prefill lands in another's prompt cache and
// the measured prefill means nothing. A word that differs is a token that
// differs, whatever the tokenizer.
func TestDefaultPromptsDistinctFromTheFirstWord(t *testing.T) {
	first := map[string]int{}
	for i, text := range defaultPrompts {
		fields := strings.Fields(text)
		if len(fields) == 0 {
			t.Fatalf("prompt %d is empty", i)
		}
		w := strings.ToLower(fields[0])
		if prev, dup := first[w]; dup {
			t.Errorf("prompt %d opens with %q, as prompt %d does", i, fields[0], prev)
		}
		first[w] = i
		// The numbered repetition beyond the set leads with this word; a
		// built-in prompt that did too would share its opening with them.
		if w == "request" {
			t.Errorf("prompt %d opens with %q, the word the numbered repetition leads with", i, fields[0])
		}
	}
}

func TestDefaultPromptsAreDistinctAndDeterministic(t *testing.T) {
	n := 2*len(defaultPrompts) + 3 // past the end of the set twice, to cover the wrap
	a := DefaultPrompts(n)
	b := DefaultPrompts(n)
	if len(a) != n {
		t.Fatalf("DefaultPrompts(%d) returned %d", n, len(a))
	}

	seen := map[string]int{}
	openings := map[string]int{}
	for i, req := range a {
		if len(req.Messages) != 1 || req.Messages[0].Role != "user" {
			t.Fatalf("prompt %d is not a single user message: %+v", i, req.Messages)
		}
		text := req.Messages[0].Content
		if prev, dup := seen[text]; dup {
			t.Errorf("prompt %d repeats prompt %d", i, prev)
		}
		seen[text] = i

		// The opening is the first two words. A built-in prompt is already
		// distinct on the first; a repeated one reads "Request 17." and is
		// distinct on the number, so the only thing two streams can share is
		// the one "Request" token in front of it.
		fields := strings.Fields(text)
		if len(fields) < 2 {
			t.Fatalf("prompt %d has fewer than two words: %q", i, text)
		}
		opening := fields[0] + " " + fields[1]
		if prev, dup := openings[opening]; dup {
			t.Errorf("prompt %d shares its opening %q with prompt %d", i, opening, prev)
		}
		openings[opening] = i

		if text != b[i].Messages[0].Content {
			t.Errorf("prompt %d is not deterministic", i)
		}
		if !strings.HasSuffix(strings.TrimSpace(text), ".") {
			t.Errorf("prompt %d does not end in a full stop: %.80q", i, text[max(0, len(text)-80):])
		}
	}
}

// TestDefaultPromptsCarryNoCap: the cap is the run's, set by the recorder, and
// a prompt that named its own would be a second number on the same axis, one
// that wins wherever a request's own cap is allowed to.
func TestDefaultPromptsCarryNoCap(t *testing.T) {
	for i, req := range DefaultPrompts(len(defaultPrompts) + 1) {
		if req.MaxTokens != 0 {
			t.Errorf("prompt %d carries MaxTokens %d, want none of its own", i, req.MaxTokens)
		}
		if got := req.SentMaxTokens(); got != 0 {
			t.Errorf("prompt %d would send max_tokens %d, want none", i, got)
		}
	}
}

func TestDefaultPromptsEdgeCounts(t *testing.T) {
	if got := DefaultPrompts(0); got != nil {
		t.Errorf("DefaultPrompts(0) = %v, want nil", got)
	}
	if got := DefaultPrompts(-3); got != nil {
		t.Errorf("DefaultPrompts(-3) = %v, want nil", got)
	}
	if got := DefaultPrompts(1); len(got) != 1 {
		t.Errorf("DefaultPrompts(1) returned %d", len(got))
	}
}

// TestDefaultPromptBodyShape: the zero-config run must produce a request the
// recorder can use, with the streaming fields always asked for and no cap
// until the recorder sets the run's.
func TestDefaultPromptBodyShape(t *testing.T) {
	body := DefaultPrompts(1)[0].Body()
	for _, k := range []string{"timings_per_token", "return_progress", "stream"} {
		if body[k] != true {
			t.Errorf("body[%q] = %v, want true", k, body[k])
		}
	}
	if v, ok := body["max_tokens"]; ok {
		t.Errorf("max_tokens = %v, want absent until the recorder sets the run's cap", v)
	}
}

// TestStreamRequestBodyParamsCannotBreakTheStream: Params may override the
// defaults, but not the two fields that make this a recordable stream.
func TestStreamRequestBodyParamsCannotBreakTheStream(t *testing.T) {
	req := StreamRequest{
		Params: map[string]any{
			"stream":            false,
			"timings_per_token": false,
			"temperature":       0.7,
		},
	}
	body := req.Body()
	if body["stream"] != true {
		t.Errorf("stream = %v, want true", body["stream"])
	}
	if body["timings_per_token"] != false {
		t.Errorf("timings_per_token = %v; Params are allowed to turn it off", body["timings_per_token"])
	}
	if body["temperature"] != 0.7 {
		t.Errorf("temperature = %v, want 0.7", body["temperature"])
	}
}
