package recorder

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/midagedev/toktape/internal/server"
	"github.com/midagedev/toktape/internal/tape"
)

// The run plan (lead, 2026-09-20): one owner for prompt length, answer cap
// and clock.
//
// The three were each decided by their own code, each defensible alone, and
// the first default `--sessions 4` take after prompts@v2 proved what that
// buys when they meet a real server (ik_llama.cpp, -c 32768 -np 4, slots of
// 8192, Qwen3.6-35B, measured 2026-09-20):
//
//   - the fixed-share trim kept all 18116 characters of every prompt — the
//     probe had measured 3023 tok/s, so no budget the old rule knew said
//     stop — and four streams prefilled one after another for 17.5 of the
//     20 seconds, leaving 2.5 seconds and ~95 tokens of decode;
//   - the answer cap was priced from bytes at 3.5/token, but the prompts
//     tokenized at 2.43 to 3.70, so the densest one plus its answer overran
//     its slot and the stream died on context_length_exceeded;
//   - the set is fixed text, so the second take against the same server was
//     served whole from the prefix cache — "5 prompt tokens", a card that
//     said 0% hit on an engine that reports no cache_n.
//
// Every number above was chosen without reference to the others. This file
// is where they are chosen together, before the first request, and the
// choice is recorded (tape.RunPlan) so a reader can see which limit bound.

const (
	// planPrefillShare is the fraction of the clock the plan allows all the
	// streams' prefill together. A quarter leaves the decode phase the
	// majority of the budget while keeping prefill past the fixed-cost
	// share it needs to be a throughput (the old fixed-share rule targeted
	// 5% fixed cost, which a quarter-share prefill comfortably is on any
	// box fast enough to finish it): the day's 17.5-of-20-seconds take is
	// the shape this constant exists to make impossible.
	planPrefillShare = 0.25
	// planConcurrentPrefillFactor is how much of the probe's one-stream
	// prefill rate N concurrent streams get together. Measured 2026-09-20
	// on the day's server: 1425-1595 tok/s aggregate against 3023 probed
	// with four streams — about half, so half is what the plan assumes. The
	// probe cannot measure this itself (it runs on one stream by design),
	// and assuming the full rate is the error that filled the clock with
	// queue wait.
	planConcurrentPrefillFactor = 0.5
	// planFallbackBytesPerToken prices prompt bytes in tokens when the
	// server's /tokenize did not answer. 2.4 is the floor of the published
	// set's own measured band on the day's tokenizer — 2.43 to 3.70
	// bytes/token across four 18116-character prompts (2026-09-20) — so the
	// price errs toward more tokens: a prompt priced denser than it is gets
	// a shorter answer, never a refused stream. It replaces the 3.5 the
	// slot cap used to price at, which that same measurement showed was not
	// the floor it claimed to be.
	planFallbackBytesPerToken = 2.4
	// planMinAnswerTokens is the answer the plan must leave room for inside
	// the slot, whatever else it does to the prompt. 1024 is the default
	// clock's own arithmetic: 20 s at 50 tok/s per stream is 1000 tokens,
	// so a plan that squeezed the answer under a thousand would be trading
	// the run's second half for prompt it did not need. When the user named
	// a cap with no clock, their number is the room to leave; when a clock
	// binds, this floor caps even a larger runaway guard, because the
	// guard is not a target.
	planMinAnswerTokens = 1024
	// planFloorTokens is the shortest prompt the plan will trim to. Below
	// tape.MinPrefillPromptTokens a prompt is not a prefill measurement,
	// and 256 keeps two of them clear of it while still letting a very
	// slow box cut deep. The floor never lengthens a prompt — a set prompt
	// already shorter than it goes whole.
	planFloorTokens = 256
)

// minAnswerTokens is the answer room this run's plan leaves in the slot:
// the named cap when the user gave one and no clock overrules it, else the
// floor capped at the runaway guard when a clock binds.
func (r *run) minAnswerTokens() int {
	switch {
	case r.limit.For > 0 && r.limit.MaxTokens > 0:
		return min(r.limit.MaxTokens, planMinAnswerTokens)
	case r.limit.MaxTokensNamed && r.limit.MaxTokens > 0:
		return r.limit.MaxTokens
	default:
		return planMinAnswerTokens
	}
}

// plan is the one decision that sizes a run: it salts the set, counts every
// set prompt with the server's own tokenizer (or prices it when that route
// stays silent), chooses the target length from the tightest of the
// ceilings — the clock's prefill share, the slot's context less the answer
// it must hold — trims to it, and then lowers the answer cap into the room
// the prompts actually left. reqs is edited in place, and what it carries
// afterwards is what goes on the wire.
//
// A user's own prompts are never salted and never trimmed: the set is fixed
// text this tool chose, and the user's prompt is the experiment. The slot
// cap and its refusal still apply to them exactly as before — the server
// refuses on bytes it has not been told about, whoever wrote them.
//
// A multi-round run plans once, from its first round, and applyPlan carries
// the same salt and the same target to every later round: one run sends one
// shape, and the recorded number stays the truth for all of them.
func (r *run) plan(ctx context.Context, reqs []server.StreamRequest) error {
	if len(reqs) == 0 {
		return nil
	}
	slotCtx := r.smallestSlotCtx(ctx)
	if hasSetPrompts(reqs) {
		r.planSet(ctx, reqs, slotCtx)
	}
	return r.capAnswersToSlot(ctx, reqs, slotCtx)
}

// applyPlan carries an already-made plan to a later round's requests: the
// same salt in front, the same token target, and the slot cap asked again
// — each round's prompts are their own size (TTP-31), so the room they
// leave is too.
func (r *run) applyPlan(ctx context.Context, reqs []server.StreamRequest) error {
	if len(reqs) == 0 {
		return nil
	}
	if r.planSalt != "" {
		saltSetPrompts(reqs, r.planSalt)
	}
	if r.planTrimTokens > 0 {
		counts, _ := r.countSet(ctx, reqs)
		r.trimSetTo(ctx, reqs, counts, r.planTrimTokens)
	}
	return r.capAnswersToSlot(ctx, reqs, r.smallestSlotCtx(ctx))
}

// hasSetPrompts reports whether any request came from the published set.
func hasSetPrompts(reqs []server.StreamRequest) bool {
	for i := range reqs {
		if reqs[i].Set == server.PromptSetID {
			return true
		}
	}
	return false
}

// planCeiling is one limit the target length must fit under.
type planCeiling struct {
	t       int
	binding string
}

// planSet is steps (a) through (d) of the plan, over reqs' set prompts
// only: salt, count, choose the target, trim. It never fails — a server
// that will not count tokens is priced, not refused — and it records the
// decision in r.runPlan for the summary.
func (r *run) planSet(ctx context.Context, reqs []server.StreamRequest, slotCtx int) {
	// (a) The salt: one line, new every run, derived from the run's own
	// injected clock the way the probe's salts are, and recorded whole so
	// a verifier strips exactly the bytes that were sent. The set is fixed
	// text a warm server has cached whole; the salt moves the first token
	// and with it every prefix the cache could serve.
	r.planSalt = "[run " + strconv.FormatInt(r.opts.Clock.Now().UnixNano(), 36) + "]\n\n"
	saltSetPrompts(reqs, r.planSalt)

	// (b) The count, before any ceiling is taken: every ceiling is in
	// tokens, and only the server can count them — the day's four
	// same-length prompts spread 4901 to 7458 on one tokenizer.
	counts, counted := r.countSet(ctx, reqs)
	r.planTokenized = counted

	// (c) The target: the minimum of the ceilings, and which one won.
	longest := 0
	for _, n := range counts {
		if n > longest {
			longest = n
		}
	}
	target, binding := 0, tape.PlanBoundWhole
	var budgetMs float64
	var ceilings []planCeiling
	// The clock: only when the run has one and the probe fitted the box.
	// A run that measured no rate does not get to act on one.
	if r.limit.For > 0 && r.prefill != nil && r.prefill.PrefillPerSecond > 0 {
		n := len(counts) // streams per round: reqs is one round's requests
		rate := r.prefill.PrefillPerSecond
		if n > 1 {
			rate *= planConcurrentPrefillFactor
		}
		budgetMs = float64(r.limit.For.Milliseconds()) * planPrefillShare
		// The budget buys tokens for every stream's prompt and pays every
		// stream's fixed cost; what is left over, divided by N, is the
		// longest prompt one stream may carry.
		tClock := int((budgetMs/1000*rate - float64(n)*(r.prefill.FixedMs/1000)*rate) / float64(n))
		ceilings = append(ceilings, planCeiling{tClock, tape.PlanBoundClock})
	}
	// The slot: the context the request will land in, less the template's
	// markup, less the answer the run exists to measure.
	if slotCtx > 0 {
		ceilings = append(ceilings, planCeiling{slotCtx - slotCtxTemplateTokens - r.minAnswerTokens(), tape.PlanBoundSlot})
	}
	if best := tightestCeiling(ceilings); best != nil && best.t < longest {
		// The floor keeps the trim from eating the measurement: never
		// below planFloorTokens, and a prompt the floor already covers
		// was never the binding one.
		target = max(best.t, planFloorTokens)
		binding = best.binding
		if target >= longest {
			target, binding = 0, tape.PlanBoundWhole
		}
	}

	// (d) The trim, per prompt, from the front: the opening words survive
	// and the streams stay distinct from their first token.
	r.planTrimTokens = target
	r.trimSetTo(ctx, reqs, counts, target)

	r.runPlan = &tape.RunPlan{
		TargetTokens: target,
		Binding:      binding,
		SlotCtx:      slotCtx,
		Tokenized:    counted,
		// LongestPromptTokens is filled by capAnswersToSlot, which counts
		// the prompts as they were actually sent.
	}
	if binding == tape.PlanBoundClock {
		r.runPlan.PrefillBudgetMs = budgetMs
	}
}

// tightestCeiling is the smallest of the ceilings, nil when there are none.
func tightestCeiling(ceilings []planCeiling) *planCeiling {
	var best *planCeiling
	for i := range ceilings {
		if best == nil || ceilings[i].t < best.t {
			best = &ceilings[i]
		}
	}
	return best
}

// saltSetPrompts puts salt in front of every set prompt's content, the one
// place the text is edited before counting: the ceilings are in tokens and
// the salt is tokens too.
func saltSetPrompts(reqs []server.StreamRequest, salt string) {
	for i := range reqs {
		if reqs[i].Set != server.PromptSetID {
			continue
		}
		p := promptTextOf(&reqs[i])
		*p = salt + *p
	}
}

// countSet returns the token count of every set prompt in reqs, aligned to
// reqs (0 for the user's own), counted by the server's tokenizer when the
// route answered and priced from bytes for all of them when it did not:
// one price, one owner, never a mix — a plan half counted and half priced
// would trim two prompts on different rulers.
func (r *run) countSet(ctx context.Context, reqs []server.StreamRequest) ([]int, bool) {
	out := make([]int, len(reqs))
	if r.kind == tape.ServerOpenAI {
		// /tokenize is llama-server's route (Client.Tokenize doc); an
		// OpenAI-compatible server is priced, never asked.
		for i := range reqs {
			if reqs[i].Set == server.PromptSetID {
				out[i] = pricedTokens(len(*promptTextOf(&reqs[i])))
			}
		}
		return out, false
	}
	counted := true
	for i := range reqs {
		if reqs[i].Set != server.PromptSetID {
			continue
		}
		n, err := r.client.Tokenize(ctx, *promptTextOf(&reqs[i]))
		if err != nil {
			counted = false
			break
		}
		out[i] = n
	}
	if !counted {
		for i := range reqs {
			if reqs[i].Set == server.PromptSetID {
				out[i] = pricedTokens(len(*promptTextOf(&reqs[i])))
			}
		}
	}
	return out, counted
}

// trimSetTo cuts every set prompt whose count exceeds target to target
// tokens, on rune boundaries, from the front. The cut is sized at 98% of
// the proportional share so a tokenizer whose boundaries land denser than
// its average still comes in under; when the counts came from the server,
// the cut is re-counted once and shrunk proportionally once more if it is
// still over — one correction, not a loop, is the budget here, and the slot
// cap behind this step is the backstop that makes a single correction
// enough.
func (r *run) trimSetTo(ctx context.Context, reqs []server.StreamRequest, counts []int, target int) {
	if target <= 0 {
		return
	}
	for i := range reqs {
		if reqs[i].Set != server.PromptSetID || counts[i] <= target {
			continue
		}
		p := promptTextOf(&reqs[i])
		runes := []rune(*p)
		chars := int(float64(len(runes)) * float64(target) / float64(counts[i]) * 0.98)
		if chars < 1 {
			chars = 1
		}
		if chars > len(runes) {
			chars = len(runes)
		}
		cut := []rune(*p)[:chars]
		if r.planTokenized {
			if n, err := r.client.Tokenize(ctx, string(cut)); err == nil && n > target {
				shrink := int(float64(len(cut)) * float64(target) / float64(n))
				if shrink < 1 {
					shrink = 1
				}
				if shrink < len(cut) {
					cut = cut[:shrink]
				}
			}
		}
		*p = string(cut)
	}
}

// pricedTokens prices content bytes in tokens at the fallback price. It is
// the only pricing left in the run: the template margin is added where the
// room is computed (capAnswersToSlot), never here, so a priced figure is
// never accidentally double-margined.
func pricedTokens(bytes int) int {
	return int(math.Ceil(float64(bytes) / planFallbackBytesPerToken))
}

// promptTextOf is the editable prompt text of a request: the raw prompt on
// the completion path (applyEndpoint put it there and cleared the
// messages), the single user message on the chat path — every prompt source
// in this recorder builds a one-message conversation, which is
// applyEndpoint's own rule for which text is the prompt.
func promptTextOf(q *server.StreamRequest) *string {
	if q.IsCompletion() || len(q.Messages) == 0 {
		return &q.Prompt
	}
	return &q.Messages[0].Content
}

// PlanLine renders a run's plan as the one line the CLI prints beside the
// run it planned, e.g.
//
//	plan: 4 × 1,880-token prompts (clock-bound) · ~5 s prefill · cap 1,024 · slot 8,192
//
// Every figure is the summary's own — the target (or the longest prompt,
// when the prompts went whole), the prefill the plan's lengths cost at the
// probe's measured rate halved for concurrency, the cap as resolved, the
// slot as reported. "" when the run planned nothing, which is the caller's
// cue to print nothing.
func PlanLine(s *tape.RunSummary, streams int) string {
	if s == nil || s.Plan == nil {
		return ""
	}
	p := s.Plan
	n := p.TargetTokens
	if n == 0 {
		n = p.LongestPromptTokens
	}
	var b strings.Builder
	fmt.Fprintf(&b, "plan: %d × %s-token prompts (%s)", streams, commaInt(n), planBindingWord(p.Binding))
	if s.Probe != nil && s.Probe.PrefillPerSecond > 0 && p.LongestPromptTokens > 0 && streams > 0 {
		rate := s.Probe.PrefillPerSecond
		if streams > 1 {
			rate *= planConcurrentPrefillFactor
		}
		sec := strconv.FormatFloat(float64(streams)*float64(p.LongestPromptTokens)/rate, 'f', 1, 64)
		fmt.Fprintf(&b, " · ~%s s prefill", strings.TrimSuffix(sec, ".0"))
	}
	if s.Limit.MaxTokens > 0 {
		fmt.Fprintf(&b, " · cap %s", commaInt(s.Limit.MaxTokens))
	}
	if p.SlotCtx > 0 {
		fmt.Fprintf(&b, " · slot %s", commaInt(p.SlotCtx))
	}
	return b.String()
}

// planBindingWord is the binding's clause for PlanLine: the same four words
// the schema's constants name, in the line a reader skims.
func planBindingWord(binding string) string {
	switch binding {
	case tape.PlanBoundClock:
		return "clock-bound"
	case tape.PlanBoundSlot:
		return "slot-bound"
	case tape.PlanBoundFixedShare:
		return "fixed-share-bound"
	default:
		return tape.PlanBoundWhole
	}
}

// commaInt groups a number the way the card's figures do: 8192 prints as
// 8,192. Local, because the card renders from its own helpers and the
// recorder does not import it.
func commaInt(n int) string {
	s := strconv.Itoa(n)
	sign := ""
	if strings.HasPrefix(s, "-") {
		sign, s = "-", s[1:]
	}
	if len(s) <= 3 {
		return sign + s
	}
	var groups []string
	for len(s) > 3 {
		groups = append([]string{s[len(s)-3:]}, groups...)
		s = s[:len(s)-3]
	}
	groups = append([]string{s}, groups...)
	return sign + strings.Join(groups, ",")
}
