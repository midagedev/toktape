package main

import (
	"fmt"
	"strings"

	"github.com/midagedev/toktape/internal/card"
	"github.com/midagedev/toktape/internal/tape"
)

// nextStep is the one owner of "what to type next" after a run went wrong
// (first-run matrix, 2026-09-21).
//
// Two surfaces print advice under a failure and they used to decide it
// separately: failureLever under a run's failed-stream block, and the
// all-streams-failed door in reportRecordError — which answered a context
// overflow with "check its log and its slot count", words that name no flag,
// no command and no setting, for an error whose cause the server had stated
// in itself. Both call this function now, so there is one table, and the
// lever it returns names the setting the engine actually has: llama-server's
// context flag is -c, vLLM's is --max-model-len, Ollama's is
// OLLAMA_CONTEXT_LENGTH (measured on real servers, 2026-09-21).
//
// An error this table does not recognise gets no invented advice: "" is
// returned and each caller prints its own fallback sentence — a guess dressed
// as a fix is how a first-time user learns to distrust the tool. The same
// rule keeps the auth row honest: toktape has no flag for a key (grep for
// Authorization/Bearer/api-key over internal/server and cmd/toktape finds
// none), so the sentence says so instead of naming a flag that does not
// exist.
func nextStep(errText string, s *tape.RunSummary) string {
	low := strings.ToLower(errText)
	// The context-overflow class in both wordings this project has measured:
	// llama-server's "context_length_exceeded … exceeds the available context
	// size" and vLLM's "This model's maximum context length is N tokens…".
	// The vLLM wording carries "context" and "length" too, so one test covers
	// both; it is named separately for the reader.
	isContextOverflow := strings.Contains(low, "maximum context length") ||
		(strings.Contains(low, "context") &&
			(strings.Contains(low, "exceed") || strings.Contains(low, "length") || strings.Contains(low, "size")))
	switch {
	case isContextOverflow:
		if s == nil {
			// The failure preceded the tape (the attach died, or every stream
			// refused), so no summary exists and the engine is unknown even
			// on a server that would have said. Name all three spellings.
			s = &tape.RunSummary{}
		}
		return contextLever(s)
	case httpStatusIn(low, "401", "403") || strings.Contains(low, "credentials"):
		return "the server refused the request as unauthenticated: it wants a key, and toktape has no flag for one yet"
	case httpStatusIn(low, "429") || strings.Contains(low, "rate limit") || strings.Contains(low, "too many requests"):
		return "the server refused the load: fewer --sessions"
	case strings.Contains(low, "unexpected eof") ||
		strings.Contains(low, "connection reset") ||
		strings.Contains(low, "closed the connection"):
		return "the server may have crashed or been killed; its log says which, and out of memory is the usual cause"
	}
	return ""
}

// contextLever is the context-overflow lever for the engine the summary says
// answered. The engine claim wins over the kind (a claim is the user naming
// vLLM or Ollama on a server that speaks only the OpenAI protocol and cannot
// say); the llama kinds are llama-server's flag vocabulary; anything else —
// an OpenAI-kind server that did not say, an unknown kind, an empty summary —
// gets all three spellings in one sentence, because a lever naming a flag the
// server does not have is the exact shape the matrix audit calls partial.
// httpStatusIn reports whether one of the status codes stands in low as a
// number of its own. Lead, 2026-09-21: the first cut matched substrings, so
// "rate" caught "failed to generate" and "401" caught "14012 tokens", and a
// wrong lever is worse than none.
func httpStatusIn(low string, codes ...string) bool {
	isDigit := func(b byte) bool { return b >= '0' && b <= '9' }
	for _, c := range codes {
		for i := 0; ; {
			j := strings.Index(low[i:], c)
			if j < 0 {
				break
			}
			at := i + j
			end := at + len(c)
			if (at == 0 || !isDigit(low[at-1])) && (end == len(low) || !isDigit(low[end])) {
				return true
			}
			i = end
		}
	}
	return false
}

func contextLever(s *tape.RunSummary) string {
	claim := strings.ToLower(s.Server.EngineClaim)
	switch {
	case strings.Contains(claim, "vllm"):
		return "the prompt plus the answer did not fit the context: lower --n-predict, or raise the server's --max-model-len"
	case strings.Contains(claim, "ollama"):
		return "the prompt plus the answer did not fit the context: lower --n-predict, or raise the server's context (OLLAMA_CONTEXT_LENGTH)"
	case s.Server.Kind == tape.ServerLlamaCPP || s.Server.Kind == tape.ServerIKLlama:
		return "the prompt plus the answer did not fit the slot: lower --n-predict, or raise the server's -c (which -np divides)"
	default:
		return "the prompt plus the answer did not fit the context: lower --n-predict, or raise the server's context — --max-model-len on vLLM, OLLAMA_CONTEXT_LENGTH on Ollama, -c on llama-server"
	}
}

// unusableRunLever is the → line under the share block's "not a usable
// measurement" sentence: the action half of the caveat that fired, so the
// block does not restate the finding and stop. "" when the code has no
// honest lever — the block then prints the sentence alone rather than invent
// one.
func unusableRunLever(code string) string {
	switch code {
	case card.CodeShortGeneration:
		return "a longer answer needs a prompt that asks for one: --prompt, or a model that is not terse"
	case card.CodeTokensUncounted:
		// Verified: every stream request already asks for a usage figure
		// (internal/server/stream.go sends stream_options.include_usage), so
		// a server that sent none is a property of the server, not a flag
		// away — and the sentence says that instead of naming a lever that
		// does not exist.
		return "the stream ended before the server sent its token count: name a cap the clock does not reach, e.g. --n-predict 256"
	}
	return ""
}

// unusableRunCodes is the card's own verdict for "this run is not a usable
// measurement", reused rather than re-derived so the closing block and the
// card cannot disagree about which runs are worthless:
//
//   - streams_failed: the run lost streams (its block already exists —
//     streamFailureBlock — so this set is not consulted for it);
//   - short_generation: every stream was cut under tape.MinDecodeTokens, so
//     the decode figure is a sample (isSample, the same predicate the Decode
//     row's label asks);
//   - tokens_uncounted: the server sent no usage figure, so no rate was
//     derived at all (recorder/reduce.go withholds it rather than printing
//     chunks as tokens).
//
// Severity alone is deliberately not the test: client_timed is a run-severity
// caveat on a run that IS a measurement (comparable with other client-timed
// runs), and answer_cut's rate is real decode tokens the server counted.
var unusableRunCodes = map[string]bool{
	card.CodeStreamsFailed:   true,
	card.CodeShortGeneration: true,
	card.CodeTokensUncounted: true,
}

// unusableMeasurementBlock is the block a finished run that is not a usable
// measurement opens the share block with, or "" for a run worth posting.
//
// A run can exit 0, save its tape and still be worthless as a figure — a
// terse model's 8-token answers, a server that never said how many tokens it
// sent — and before this the share block ended with the ✓ lines and the
// Markdown invitation as if the run were a result (matrix rows 19 and 10's
// shape, 2026-09-21). Failed streams already have their own block, so this
// one stands aside when any died: one ✗ block, not two.
func unusableMeasurementBlock(tp *tape.Tape) string {
	if tp == nil || tp.Summary.Aggregate.StreamsFailed > 0 {
		return ""
	}
	cs := card.Caveats(&tp.Summary)
	var top *card.Caveat
	for i := range cs {
		// Caveats is ranked most serious first, so the first hit is the top
		// one: short_generation outranks tokens_uncounted, as the figure
		// beside it outranks its absence.
		if unusableRunCodes[cs[i].Code] {
			top = &cs[i]
			break
		}
	}
	if top == nil {
		return ""
	}
	b := fmt.Sprintf("✗ this run is not a usable measurement: %s\n", top.Text)
	if lever := unusableRunLever(top.Code); lever != "" {
		b += fmt.Sprintf("→ %s\n", lever)
	}
	return b
}
