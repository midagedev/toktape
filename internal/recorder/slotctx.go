package recorder

import (
	"context"
	"errors"
	"fmt"
	"math"

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
// number too. Two rules, in the order they bite:
//
//   - a prompt the slot cannot hold at all is refused here, with both numbers
//     and the lever that moves them, before a stream is wasted on the
//     server's generic 500;
//   - a cap the slot cannot hold is lowered to one it can, on every request
//     of the run (one run sends one shape), with a warning that names the
//     asked number, the cap sent, the slot and the priced prompt.
//
// Unknown is not a limit, in this repo's first rule: an OpenAI-compatible
// server numbers no slots (TTP-99), and a llama-server started with
// --no-slots answers 501 — both leave the cap as it was.

const (
	// slotCtxBytesPerToken prices a prompt in tokens before the server has
	// counted it. 3.5 is the floor of the published set's own measured band
	// (3.0 bytes/token dense code to 5.2 English prose, internal/server/
	// prompts.go, 2026-09-20), so the estimate errs toward more tokens and a
	// tighter cap: a tokenizer that prices this prompt denser still gets a
	// shorter answer, never a refused one.
	slotCtxBytesPerToken = 3.5
	// slotCtxTemplateTokens covers what the chat template adds around the
	// messages — the server counts the templated text, and the recorder
	// prices only the content it sends. 192 is generous against the GLM and
	// Qwen templates this tool has recorded (~80–150 tokens with their system
	// lines and generation prompts), because the margin's only job is to keep
	// the capped request inside the slot.
	slotCtxTemplateTokens = 192
)

// ErrPromptOverflowsSlot is returned when the longest prompt cannot fit a
// slot's context even with no answer at all. The CLI maps it to exit code 1.
var ErrPromptOverflowsSlot = errors.New("recorder: a prompt will not fit its slot's context")

// SlotCtxError is ErrPromptOverflowsSlot with the numbers that name the fix.
type SlotCtxError struct {
	// PromptTokens is the estimate the refusal is based on, priced from
	// PromptBytes at slotCtxBytesPerToken plus the template margin.
	PromptTokens int
	// PromptBytes is the content the run would have sent.
	PromptBytes int
	// SlotCtx is the smallest n_ctx the server's own /slots reported.
	SlotCtx int
	// URL is the server that reported it.
	URL string
}

func (e *SlotCtxError) Error() string {
	return fmt.Sprintf("the prompt is ~%d tokens (%d bytes at %.1f bytes/token plus the template) and the slot's context is %d; send a shorter prompt, or raise the slot's context (llama-server's -c, which -np divides) — %s",
		e.PromptTokens, e.PromptBytes, slotCtxBytesPerToken, e.SlotCtx, e.URL)
}

// Is makes errors.Is(err, ErrPromptOverflowsSlot) true for a *SlotCtxError.
func (e *SlotCtxError) Is(target error) bool { return target == ErrPromptOverflowsSlot }

// capTokensToSlotCtx lowers the answer cap into the slot the requests will
// land in, or refuses a run whose prompt cannot land at all. It runs after
// the trim has settled the prompt sizes and before the requests are shaped,
// so the cap meets the bytes as they will be sent.
func (r *run) capTokensToSlotCtx(ctx context.Context, reqs []server.StreamRequest) error {
	if r.kind == tape.ServerOpenAI || len(reqs) == 0 {
		return nil
	}
	slots, err := r.client.Slots(ctx)
	if err != nil || len(slots) == 0 {
		return nil // unknown is not a limit: --no-slots answers 501
	}
	slotCtx := 0
	for _, s := range slots {
		if s.NCtx > 0 && (slotCtx == 0 || s.NCtx < slotCtx) {
			slotCtx = s.NCtx
		}
	}
	if slotCtx <= 0 {
		return nil // no slot named a context; nothing observed to act on
	}
	// One run sends one shape, so the longest prompt decides for all of them:
	// a per-request cap would give the streams different budgets and the
	// tape one recorded number.
	longest, longestBytes := 0, 0
	for i := range reqs {
		if est, b := pricedPrompt(reqs[i]); est > longest {
			longest, longestBytes = est, b
		}
	}
	allowed := slotCtx - longest
	if allowed < tape.MinCutTokens {
		return &SlotCtxError{
			PromptTokens: longest, PromptBytes: longestBytes,
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

// pricedPrompt estimates the tokens a request's prompt will occupy in the
// slot: its content bytes at the measured floor, plus the template's own.
func pricedPrompt(q server.StreamRequest) (tokens, bytes int) {
	b := len(q.Prompt)
	if !q.IsCompletion() {
		b = 0
		for _, m := range q.Messages {
			b += len(m.Content)
		}
	}
	return int(math.Ceil(float64(b)/slotCtxBytesPerToken)) + slotCtxTemplateTokens, b
}
