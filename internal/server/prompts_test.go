package server

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/midagedev/toktape/internal/tape"
)

// The length a built-in prompt must have, in characters, because this package
// has no tokenizer (TTP-84, 2026-09-14).
//
// Both the conversion and the band were re-derived from measurement on this
// repo's hero rig (lead, 2026-09-14), replacing numbers that had been reasoned
// from published tokenizer averages. Two concurrent streams sent 714 prompt
// tokens for 2538 characters of the densest prompts in the set — code, a query
// plan, a log excerpt — which is 3.55 characters to a token with the chat
// template already counted in, against the 3 this file used to assume; the
// prose-only prompts of the same run ran 4.9. Each is the pessimistic end of
// one bound: a floor counted at 4.9 characters per token is a floor for every
// prompt in the set, a ceiling counted at 3.5 a ceiling for every one.
//
// The floor is 150 tokens, not the 2 x tape.MinPrefillPromptTokens this file
// claimed before. That claim was wrong in fact — at the measured 4.9, the 800
// characters it demanded are 163 tokens, never the 200 it named — and the
// honest restatement is a margin: half again the threshold the card tests, so
// a tokenizer that differs from this one still clears it without counting on
// the template's share, which differs per model.
//
// The ceiling is 300 tokens, and it is the half that moved. It used to be 500,
// justified at the 60 tok/s low end of ordinary prefill, where 500 tokens is
// about 8 s of a 20 s budget. But the run that produced the conversion above
// took 21 s to reach its first token: that rig pages a 445 GB model through
// 251 GB of RAM and prefills at 16.8 tok/s per stream, so 500 tokens would be
// half a minute before a single token of decode. A box whose prefill is bound
// by its memory is exactly the box this tool exists for, so the ceiling is set
// where such a box still gets a run: 300 tokens is about 5 s on the 60 tok/s
// rig and about 12 s on the paging one.
const (
	proseCharsPerToken = 4.9
	codeCharsPerToken  = 3.5
	minPromptTokens    = 150
	maxPromptTokens    = 300
	minPromptChars     = int(minPromptTokens * proseCharsPerToken) // 735
	maxPromptChars     = int(maxPromptTokens * codeCharsPerToken)  // 1050
)

// TestDefaultPromptsArePrefillMeasurements: every built-in prompt is long
// enough that the card presents its prefill as a prefill measurement, and
// short enough that a slow rig's prefill does not eat the run's budget.
func TestDefaultPromptsArePrefillMeasurements(t *testing.T) {
	// The floor is a margin over the threshold the card tests against, so the
	// two are pinned to each other here rather than only in the prose above:
	// raising tape.MinPrefillPromptTokens past the floor would leave this
	// file's numbers reading as a margin while they had stopped being one.
	if minPromptTokens <= tape.MinPrefillPromptTokens {
		t.Fatalf("the floor is %d tokens and the card calls a prompt short under %d: the built-in set would carry the caveat it exists to avoid",
			minPromptTokens, tape.MinPrefillPromptTokens)
	}
	if len(defaultPrompts) < 16 {
		t.Errorf("%d built-in prompts, want at least 16: a concurrent run needs one distinct prompt per stream before the numbered repetition starts", len(defaultPrompts))
	}
	shortest, longest := 0, 0
	for i, text := range defaultPrompts {
		if n := len(text); n < minPromptChars || n > maxPromptChars {
			t.Errorf("prompt %d is %d characters, want %d to %d (about %d to %d tokens): %.80q",
				i, n, minPromptChars, maxPromptChars, minPromptTokens, maxPromptTokens, text)
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

// promptSetHash is sha256 over the built-in prompts, NUL-joined in order.
//
// It exists to keep one promise the id makes: two tapes carrying
// prompts@v1 did the same work. Nothing else enforces it — the id is a
// constant and a prompt is a string, and the day someone fixes a typo in one
// of them, every tape already published under that id becomes a claim about
// a set that no longer exists.
//
// So when this fails, the set changed. Decide which happened and do the
// matching thing:
//
//   - a released set changed -> bump PromptSetID, then update this hash
//   - the set has never shipped in a release -> update this hash alone,
//     because there is no tape in the world carrying the old contents
//
// Updating the hash to make the test pass, without asking that question, is
// the one move this gate exists to prevent.
const promptSetHash = "886ccba10bf1c336c00f95d65aab0cfd98d1fdbc9946a334e883ebec9afd3c49"

func TestDefaultPromptsMatchTheirID(t *testing.T) {
	sum := sha256.Sum256([]byte(strings.Join(defaultPrompts, "\x00")))
	if got := hex.EncodeToString(sum[:]); got != promptSetHash {
		t.Errorf("the built-in set hashes to %s, not %s: the prompts changed.\n"+
			"%s is stamped on every tape this set records, so a reader is entitled to\n"+
			"assume two tapes carrying it ran the same prompts. See promptSetHash above\n"+
			"for which of the two things to do.", got, promptSetHash, PromptSetID)
	}
}

// TestDefaultPromptsCarryTheSetID: the set stamps its own requests, so a
// caller that did not go through DefaultPrompts cannot be mistaken for one
// that did.
func TestDefaultPromptsCarryTheSetID(t *testing.T) {
	// Past the end of the set too: a numbered repetition is still this set's
	// work, sent because the run asked for more streams than there are
	// prompts, and a run of 20 streams is no less comparable for it.
	for i, req := range DefaultPrompts(len(defaultPrompts) + 4) {
		if req.Set != PromptSetID {
			t.Errorf("prompt %d carries Set %q, want %q", i, req.Set, PromptSetID)
		}
	}
	if (StreamRequest{}).Set != "" {
		t.Error("a request nobody stamped carries a set")
	}
}

// TestPromptSetNeverReachesTheWire: Set says what the run can be compared
// against, which is the tape's question, not the server's.
func TestPromptSetNeverReachesTheWire(t *testing.T) {
	body := DefaultPrompts(1)[0].Body()
	for k, v := range body {
		if s, ok := v.(string); ok && s == PromptSetID {
			t.Errorf("body[%q] = %q: the set id went over the wire", k, s)
		}
	}
	if _, ok := body["set"]; ok {
		t.Error(`body carries a "set" key`)
	}
}
