package server

import (
	"strings"
	"testing"
)

func TestDefaultPromptsAreDistinctAndDeterministic(t *testing.T) {
	const n = 24 // past the end of the built-in set, to cover the wrap
	a := DefaultPrompts(n)
	b := DefaultPrompts(n)
	if len(a) != n {
		t.Fatalf("DefaultPrompts(%d) returned %d", n, len(a))
	}

	seen := map[string]int{}
	prefixes := map[string]int{}
	for i, req := range a {
		if len(req.Messages) != 1 || req.Messages[0].Role != "user" {
			t.Fatalf("prompt %d is not a single user message: %+v", i, req.Messages)
		}
		text := req.Messages[0].Content
		if prev, dup := seen[text]; dup {
			t.Errorf("prompt %d repeats prompt %d", i, prev)
		}
		seen[text] = i

		// A shared opening would let one stream's prefill land in another
		// stream's prompt cache and make the measured prefill meaningless.
		head := text
		if len(head) > 24 {
			head = head[:24]
		}
		if prev, dup := prefixes[head]; dup {
			t.Errorf("prompt %d shares its opening %q with prompt %d", i, head, prev)
		}
		prefixes[head] = i

		if req.Messages[0].Content != b[i].Messages[0].Content {
			t.Errorf("prompt %d is not deterministic", i)
		}
		if req.MaxTokens <= 0 {
			t.Errorf("prompt %d has no answer cap", i)
		}
		// Roughly forty tokens of English: measured in words, since this
		// package does not tokenise.
		words := len(strings.Fields(text))
		if words < 22 || words > 40 {
			t.Errorf("prompt %d is %d words, want roughly 30 (about forty tokens): %q", i, words, text)
		}
		if !strings.HasSuffix(strings.TrimSpace(text), ".") {
			t.Errorf("prompt %d does not end in a full stop: %q", i, text)
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
// recorder can use, with the streaming fields always asked for.
func TestDefaultPromptBodyShape(t *testing.T) {
	body := DefaultPrompts(1)[0].Body()
	for _, k := range []string{"timings_per_token", "return_progress", "stream"} {
		if body[k] != true {
			t.Errorf("body[%q] = %v, want true", k, body[k])
		}
	}
	if body["max_tokens"] != 320 {
		t.Errorf("max_tokens = %v, want 320", body["max_tokens"])
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
