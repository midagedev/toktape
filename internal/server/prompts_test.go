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
// The floor is a margin over the threshold the card tests against, so the two
// are pinned to each other here rather than only in the prose: raising
// tape.MinPrefillPromptTokens past the floor would leave this file's numbers
// reading as a margin while they had stopped being one. The floor's token
// reading is counted at the least dense 5.2 bytes a token, so the margin
// holds for every prompt in the set, not just the dense ones.
//
// The band was moved to bytes and raised by an order of magnitude for TTP-144
// (lead, 2026-09-20). The published set sent about 283 tokens a stream, which
// on the reference box is 240 ms of prefill against a 28 ms per-request fixed
// cost — 12% of the figure the card calls "prefill" was not prefill. A
// throughput needs the fixed cost to be a small share of what it measures,
// and the only honest way there is a longer input: at 18,000 bytes (roughly
// 4,500 tokens at the 4.0 bytes-per-token midband) the same box spends about
// 3.8 s in prefill and the fixed cost is under one percent of it. The floor
// is pinned in bytes because that is the unit the set measures itself in.
//
// The ceiling is 30,000 bytes, and it changed jobs. It used to be 300 tokens
// because a paging box had to send the whole prompt: that rig prefilled at
// 16.8 tok/s per stream, and 300 tokens was where it still got a run. The run
// now trims each prompt to a length it sizes for the machine it is on
// (RunSummary.PromptTrimChars), so a slow box is protected by the trim, not
// by the band — and the ceiling keeps the two jobs it still has on a fast
// box that sends whole prompts: bounding one stream's prefill at about six
// seconds on the reference rig, and refusing the padding a prompt past
// 30,000 bytes almost always is. The no-filler contract the set is grown
// under says it outright: real material runs out before 30,000 bytes, and
// past it a prompt is words about material, not material.
// The set gained Korean and Japanese prompts on 2026-09-18 (TTP-112), and the
// band survived them unchanged — which was measured, not assumed, because
// len(text) counts BYTES and a Korean character is three of them.
//
// What makes one band work for four scripts is that these are byte-level BPE
// tokenizers, so bytes per token barely moves with the script. Measured that
// day against three real vocabularies (Qwen3-0.6B, Llama 3.1 8B, gpt-oss-20b),
// tokenizer.json only, no chat template:
//
//	this set's densest code   3.3 - 3.5 B/token
//	its most prose-like       4.9 - 5.0
//	English prose             5.1 - 5.2
//	Korean prose              3.4 - 4.0
//	Japanese prose            4.1 - 4.6
//
// Characters per token, by contrast, is 1.4 to 1.7 for Korean and Japanese
// against 5.1 for English prose: a band in characters would have been three
// different bands. The names below say chars because Go's len says bytes and
// this file's inputs are UTF-8; for the Latin-script prompts they are the same
// number, and for the CJK ones the byte reading is the one that transfers.
//
// The conversion is a conversion, not a proof. At the measured extremes the
// new band's token reach is about 3,500 (18,000 bytes at the least dense 5.2)
// to about 9,100 (30,000 at the densest 3.3), so it catches a prompt that is
// wrong by a factor and not one that is five percent over — the same honesty
// the 150-to-300 band carried, moved up with it.
const (
	// The band is bytes now (TTP-144, 2026-09-20); the token readings are
	// kept for the margin check below and the error message. The old
	// per-script conversions (4.9, 3.5) retired with the derivation they
	// served: a byte floor needs no conversion to reach bytes.
	minPromptTokens = 3500
	maxPromptTokens = 9100
	minPromptChars  = 18000 // bytes: Go's len, and this file's inputs are UTF-8
	maxPromptChars  = 30000
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
			t.Errorf("prompt %d is %d bytes, want %d to %d (about %d to %d tokens): %.80q",
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
		t.Logf("prompt %d: %d bytes, %d words", i, len(text), len(strings.Fields(text)))
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
		// "。" joins "." on 2026-09-18, with the set (TTP-112). A Japanese
		// prompt ends in the ideographic full stop and nothing else — it was
		// red under the ASCII-only check, which is how this extension was
		// confirmed to be needed rather than assumed. The check is still that
		// a prompt ends in a sentence, not in a truncation.
		if !endsInFullStop(text) {
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
//   - PromptSetID has shipped in a release -> bump it, then update this hash,
//     because tapes carrying the old id are already out there
//   - it has not -> update this hash alone, since no tape in the world claims
//     the old contents
//
// Updating the hash to make the test pass, without asking that question, is
// the one move this gate exists to prevent.
const promptSetHash = "f02836757a468b51cf92a3f4a41326882bf7a3698450b35cc9451257c30dbe8a"

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

// endsInFullStop accepts both stops the set uses: "." for the Latin-script
// prompts and "。" for the Japanese one.
func endsInFullStop(text string) bool {
	t := strings.TrimSpace(text)
	return strings.HasSuffix(t, ".") || strings.HasSuffix(t, "。")
}

// TestDefaultPromptsCoverTheirAxes: the set is the comparison set, so what it
// leaves out is what nobody can compare (TTP-112, spec §9.5).
//
// Two axes, both measured rather than asserted from taste:
//
//   - Prose against code. They are different loads — measured 2026-09-18 with
//     three real tokenizers (Qwen3, Llama 3.1, gpt-oss), this set's densest
//     code runs 3.3 characters to a token and its most prose-like prompt 5.0.
//     A decode rate measured on one does not transfer to the other, and a set
//     that is all code invites exactly that misreading.
//   - CJK against Latin. Korean and Japanese cost about 1.5 characters to a
//     token against Latin prose's 5.0, so the same token budget is a very
//     different amount of screen — and the card's east-asian width contract
//     is only exercised when a run actually generates CJK.
func TestDefaultPromptsCoverTheirAxes(t *testing.T) {
	prose, cjk := 0, 0
	for _, text := range defaultPrompts {
		// A fenced block is how every code, config and log prompt in this set
		// carries its material; a prompt without one is prose.
		if !strings.Contains(text, "~~~") {
			prose++
		}
		for _, r := range text {
			if r > 0x2E7F {
				cjk++
				break
			}
		}
	}
	if prose < 5 {
		t.Errorf("%d of %d prompts are prose, want at least 5: a set that is all code invites a decode rate measured on code being read as one for prose",
			prose, len(defaultPrompts))
	}
	if cjk < 3 {
		t.Errorf("%d prompts contain CJK, want at least 3: Korean and Japanese cost about a third the characters per token, and the card's east-asian width contract is only exercised by a run that generates them",
			cjk)
	}
}
