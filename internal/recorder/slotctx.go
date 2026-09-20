package recorder

import (
	"context"
	"errors"
	"fmt"

	"github.com/midagedev/toktape/internal/server"
	"github.com/midagedev/toktape/internal/tape"
)

// The slot's context caps the answer before the first request goes out
// (TTP-148, 2026-09-20).
//
// The day's evidence, four runs the server refused with
// context_length_exceeded: an offload box where trim's own economics kept the
// whole 7246-token prompt, and a -np 4 server whose slots each held
// -c / -np = 8192 — every one with the default n-predict of 24000 riding on
// the request. The refusal names none of that usefully. The recorder already
// reads the slot count to refuse more sessions than slots, and /slots carries
// each slot's n_ctx; the missing act was to let the answer cap meet that
// number too.
//
// Since the run plan (2026-09-20, plan.go) this is step (e) of one decision:
// the plan trimmed the prompts, and the room they left is what the cap is
// lowered into. Two rules, in the order they bite:
//
//   - a prompt the slot cannot hold at all is refused here, with both numbers
//     and the lever that moves them, before a stream is wasted on the
//     server's generic 500;
//   - a cap the slot cannot hold is lowered to one it can, on every request
//     of the run (one run sends one shape), with a warning that names the
//     asked number, the cap sent, the slot and the longest prompt.
//
// Unknown is not a limit, in this repo's first rule: an OpenAI-compatible
// server numbers no slots (TTP-99), and a llama-server started with
// --no-slots answers 501 — both leave the cap as it was.

// slotCtxTemplateTokens covers what the chat template adds around the
// messages — the server counts the templated text, and the recorder
// prices or counts only the content it sends. 192 is generous against the GLM
// and Qwen templates this tool has recorded (~80–150 tokens with their system
// lines and generation prompts), because the margin's only job is to keep
// the capped request inside the slot.
const slotCtxTemplateTokens = 192

// ErrPromptOverflowsSlot is returned when the longest prompt cannot fit a
// slot's context even with no answer at all. The CLI maps it to exit code 1.
var ErrPromptOverflowsSlot = errors.New("recorder: a prompt will not fit its slot's context")

// SlotCtxError is ErrPromptOverflowsSlot with the numbers that name the fix.
type SlotCtxError struct {
	// PromptTokens is the figure the refusal is based on: counted by the
	// server's own tokenizer when Counted, priced from PromptBytes at
	// planFallbackBytesPerToken otherwise. The template's own margin is
	// named beside it, not folded in.
	PromptTokens int
	// PromptBytes is the content the run would have sent.
	PromptBytes int
	// Counted says PromptTokens came from /tokenize rather than the price.
	Counted bool
	// SlotCtx is the smallest n_ctx the server's own /slots reported.
	SlotCtx int
	// URL is the server that reported it.
	URL string
}

func (e *SlotCtxError) Error() string {
	if e.Counted {
		return fmt.Sprintf("the prompt is ~%d tokens (counted by the server's own tokenizer) and with the template's %d the slot's context is %d; send a shorter prompt, or raise the slot's context (llama-server's -c, which -np divides) — %s",
			e.PromptTokens, slotCtxTemplateTokens, e.SlotCtx, e.URL)
	}
	return fmt.Sprintf("the prompt is ~%d tokens (%d bytes priced at %.1f bytes/token) and with the template's %d the slot's context is %d; send a shorter prompt, or raise the slot's context (llama-server's -c, which -np divides) — %s",
		e.PromptTokens, e.PromptBytes, planFallbackBytesPerToken, slotCtxTemplateTokens, e.SlotCtx, e.URL)
}

// Is makes errors.Is(err, ErrPromptOverflowsSlot) true for a *SlotCtxError.
func (e *SlotCtxError) Is(target error) bool { return target == ErrPromptOverflowsSlot }

// smallestSlotCtx is the smallest n_ctx the server's /slots reported, 0 when
// it reported none or was never asked: an OpenAI-compatible server has no
// /slots (TTP-99), and unknown is not a limit.
func (r *run) smallestSlotCtx(ctx context.Context) int {
	if r.kind == tape.ServerOpenAI {
		return 0
	}
	slots, err := r.client.Slots(ctx)
	if err != nil {
		return 0
	}
	slotCtx := 0
	for _, s := range slots {
		if s.NCtx > 0 && (slotCtx == 0 || s.NCtx < slotCtx) {
			slotCtx = s.NCtx
		}
	}
	return slotCtx
}

// capAnswersToSlot lowers the answer cap into the slot the requests will
// land in, or refuses a run whose prompt cannot land at all (TTP-148, now
// step (e) of the plan). It runs after the trim has settled the prompt
// sizes, so the cap meets the prompts as they will be sent — counted by the
// server when the plan counted, priced when it priced.
func (r *run) capAnswersToSlot(ctx context.Context, reqs []server.StreamRequest, slotCtx int) error {
	if slotCtx <= 0 || len(reqs) == 0 {
		return nil // unknown is not a limit: --no-slots answers 501
	}
	// One run sends one shape, so the longest prompt decides for all of them:
	// a per-request cap would give the streams different budgets and the
	// tape one recorded number.
	longest, longestBytes, counted := 0, 0, false
	for i := range reqs {
		if est, b, c := r.requestTokens(ctx, &reqs[i]); est > longest {
			longest, longestBytes, counted = est, b, c
		}
	}
	if r.runPlan != nil && longest > r.runPlan.LongestPromptTokens {
		// A multi-round run caps per round and records the longest prompt
		// any round sent — the number the run's cap was set against.
		r.runPlan.LongestPromptTokens = longest
	}
	allowed := slotCtx - longest - slotCtxTemplateTokens
	if allowed < tape.MinCutTokens {
		return &SlotCtxError{
			PromptTokens: longest, PromptBytes: longestBytes, Counted: counted,
			SlotCtx: slotCtx, URL: r.client.BaseURL(),
		}
	}
	if r.limit.MaxTokens > 0 && r.limit.MaxTokens <= allowed {
		return nil // the budget already fits; the slot is not the binding one
	}
	asked := r.limit.MaxTokens
	for i := range reqs {
		if reqs[i].MaxTokens > allowed {
			reqs[i].MaxTokens = allowed
		}
	}
	r.limit.MaxTokens = allowed
	r.opts.MaxTokens = allowed
	r.warn("n-predict %d capped to %d: the slot's context is %d and the longest prompt is ~%d tokens",
		asked, allowed, slotCtx, longest)
	return nil
}

// requestTokens is one request's prompt as the slot will hold it: counted by
// the server's tokenizer when the plan counted the set, priced otherwise —
// the user's own prompts are never sent to /tokenize, so they are priced
// exactly as they always were. counted reports which ruler measured.
func (r *run) requestTokens(ctx context.Context, q *server.StreamRequest) (tokens, bytes int, counted bool) {
	b := len(*promptTextOf(q))
	if q.Set == server.PromptSetID && r.planTokenized {
		if n, err := r.client.Tokenize(ctx, *promptTextOf(q)); err == nil {
			return n, b, true
		}
	}
	return pricedTokens(b), b, false
}
