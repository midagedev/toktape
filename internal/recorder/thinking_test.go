package recorder

import (
	"testing"

	"github.com/midagedev/toktape/internal/tape"
)

// TTP-106 (2026-09-17): a request that asked for thinking off is not evidence
// that the server honoured it. llama-server drops chat_template_kwargs unless
// it was started with --jinja and reports nothing, so the run's own first
// tokens are the only witness — and the card, which reads the summary and
// never the token text, can only know what the recorder wrote down here.
//
// FAIL-first: before samplingOf counted this, every case below returned 0 and
// the "four streams thought" case was indistinguishable from a run that
// obeyed. The shipped v0.2.4 hero is that run: sampling.thinking "off", all
// four streams opening "<think>", no </think> anywhere, cut at the cap.
func TestSamplingThoughtAnyway(t *testing.T) {
	rec := func(thinking string, opens ...string) []tape.RequestRecord {
		var out []tape.RequestRecord
		for i, text := range opens {
			r := tape.RequestRecord{Index: i}
			r.Prompt.Thinking = thinking
			r.Prompt.Endpoint = tape.EndpointChat
			if text != "" {
				r.Tokens = []tape.TokenEvent{{Text: text}, {Text: " and then some answer"}}
			}
			out = append(out, r)
		}
		return out
	}
	// reasoning streams like an OpenAI-compatible server: no tag in the
	// content, the thinking carried in a field of its own and already counted
	// into the record (TTP-149, 2026-09-20).
	reason := func(thinking string, tokens ...int) []tape.RequestRecord {
		var out []tape.RequestRecord
		for i, n := range tokens {
			r := tape.RequestRecord{Index: i}
			r.Prompt.Thinking = thinking
			r.Prompt.Endpoint = tape.EndpointChat
			r.Prompt.ReasoningN = n
			r.Tokens = []tape.TokenEvent{{Text: "an answer from the start"}}
			out = append(out, r)
		}
		return out
	}

	for _, c := range []struct {
		name string
		recs []tape.RequestRecord
		want int
	}{
		{"all four thought", rec("off", "<think>", "<think>", "<think>", "<think>"), 4},
		{"one of four thought", rec("off", "an answer", "<think>", "an answer", "an answer"), 1},
		{"none thought", rec("off", "an answer", "an answer"), 0},
		{"whitespace before the tag", rec("off", "\n  <think>"), 1},
		{"the other spelling", rec("off", "<thinking>"), 1},
		// The tag is not the only witness. A server that splits reasoning into
		// its own field (exl3-serve and vLLM's reasoning_content) never puts a
		// tag in the content, and every thinking token is already counted in
		// ReasoningN. The 2026-09-20 GLM tapes are this shape: sampling
		// "thinking off", 783 of 783 predicted tokens reasoning, and the count
		// here read 0 — so the card said "thinking off" over its own Context
		// row. FAIL-first: before ReasoningN counted here, both cases below
		// returned 0.
		{"reasoning in a field, not a tag", reason("off", 783, 0), 1},
		{"all four reasoned in a field", reason("off", 12, 12, 12, 12), 4},
		{"a field count of zero is no witness", reason("off", 0, 0), 0},
		// Not asked for: the field says nothing about a run that left the
		// decision with the server, because there is no request to contradict.
		{"thinking not requested off", rec("", "<think>", "<think>"), 0},
		// A model that mentions a thinking tag inside an answer is quoting,
		// not reasoning. Only the opening counts.
		{"tag later in the answer", rec("off", "here is how a <think> block works"), 0},
		{"no streams", nil, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := samplingOf(c.recs).ThoughtAnyway; got != c.want {
				t.Errorf("ThoughtAnyway = %d, want %d", got, c.want)
			}
		})
	}
}
