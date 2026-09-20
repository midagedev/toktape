package recorder

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

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
	// prefill rate N concurrent streams are ASSUMED to get together, when
	// the run has no concurrent point to measure it. Two measurements on
	// the same box (ik_llama.cpp, Qwen3.6-35B, 2026-09-20) put the truth
	// far apart: 0.47 at ~6k-token prompts (1425-1595 tok/s aggregate
	// against 3023 probed) and about 0.2 at ~1.6k (574-716 aggregate
	// against 2963-3083 probed, TTFT 6.5-10.4 s of a 20 s clock). How an
	// engine shares one batch between slots is the engine's business and
	// moves with the prompt length, so 0.5 is a placeholder of the right
	// order, not a calibration: whenever the probe could measure the run's
	// own N-stream rate (tape.ProbeSummary.Concurrent) the plan uses the
	// measurement, and this factor is what is left for the runs where it
	// could not — a burst that did not fit its budget on a slow box, or one
	// whose measurement came back with a hole in it. Assuming the full rate
	// is the error that filled the clock with queue wait.
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
	// The clock: only when the run has one and something measured the box's
	// prefill. A run that measured no rate does not get to act on one.
	if r.limit.For > 0 && r.prefill != nil && r.prefill.PrefillPerSecond > 0 {
		n := len(counts) // streams per round: reqs is one round's requests
		budgetMs = float64(r.limit.For.Milliseconds()) * planPrefillShare
		rate, measured := planConcurrentRate(r.prefill, n)
		if measured {
			// The measured aggregate rate is the plan's own subject: it
			// already contains the fixed costs and the queueing of N
			// streams asking at once — that is what WallMs spans — so
			// subtracting FixedMs again would pay the queue twice and buy
			// prompts the clock cannot afford.
			ceilings = append(ceilings, planCeiling{
				int(budgetMs / 1000 * rate / float64(n)), tape.PlanBoundClock})
		} else {
			// The budget buys tokens for every stream's prompt and pays every
			// stream's fixed cost; what is left over, divided by N, is the
			// longest prompt one stream may carry.
			tClock := int((budgetMs/1000*rate - float64(n)*(r.prefill.FixedMs/1000)*rate) / float64(n))
			ceilings = append(ceilings, planCeiling{tClock, tape.PlanBoundClock})
		}
	} else if r.limit.For > 0 && r.kind == tape.ServerOpenAI {
		// The OpenAI-kind clock ceiling (TTP-168, 2026-09-21): the prefill
		// calibration prices what a prompt costs, and the ceiling is the
		// SERIAL prediction — N prompts of t tokens each take N x t /
		// PerSecond on a box that prefills one request at a time (Ollama at
		// parallel 1, measured; nothing measured this server's concurrent
		// prefill, so the serial figure is the one that cannot promise time
		// the box has not been heard to deliver — a batching server then
		// finishes early, which costs nothing). PerSecond is already
		// marginal: the fixed cost was subtracted when it was derived, so
		// there is no intercept to pay here.
		if pf := r.calibrationPrefill(); pf != nil && pf.PerSecond > 0 {
			n := len(counts)
			budgetMs = float64(r.limit.For.Milliseconds()) * planPrefillShare
			ceilings = append(ceilings, planCeiling{
				int(budgetMs / 1000 * pf.PerSecond / float64(n)), tape.PlanBoundClock})
		}
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

	// The longest length the plan will send: the trim caps every count at
	// the target, so the longest planned is the longest count unless the
	// target was under it. Recorded here because the plan knows it even when
	// no slot asks for the as-sent figure (capAnswersToSlot fills its own
	// only under a slot context — TTP-165 leaves the OpenAI-kind path
	// without one whenever the listing said nothing — and the plan line's
	// prefill figure reads this field on every kind).
	longestPlanned := longest
	if target > 0 && longestPlanned > target {
		longestPlanned = target
	}
	r.runPlan = &tape.RunPlan{
		TargetTokens: target,
		Binding:      binding,
		SlotCtx:      slotCtx,
		Tokenized:    counted,
		// LongestPromptTokens: the planned figure; capAnswersToSlot raises
		// it to the as-sent longest under a slot, and multi-round runs to
		// the longest any round sent.
		LongestPromptTokens: longestPlanned,
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

// planConcurrentRate is the aggregate prefill rate streams streams are
// planned against, and whether it was measured: the probe's own concurrent
// point when it made one for exactly this stream count, the fitted
// one-stream rate with planConcurrentPrefillFactor applied when it did not
// (and un-factored at one stream, where there is no concurrency to assume).
// planSet's ceiling and PlanLine's "~X s prefill" both read it here so the
// number the plan used and the number the run prints are the same number by
// construction, not by coincidence.
func planConcurrentRate(p *tape.ProbeSummary, streams int) (rate float64, measured bool) {
	if p == nil || p.PrefillPerSecond <= 0 {
		return 0, false
	}
	if c := p.Concurrent; streams > 1 && c != nil && c.Streams == streams && c.PerSecond > 0 {
		return c.PerSecond, true
	}
	if streams > 1 {
		return p.PrefillPerSecond * planConcurrentPrefillFactor, false
	}
	return p.PrefillPerSecond, false
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

// openAIPrice is the bytes-per-token this run prices an OpenAI-kind server's
// prompts at: the prefill calibration's own measurement first (TTP-168,
// 2026-09-21 — 4096 bytes of word-list text is a real sample of a tokenizer,
// where the decode calibration's 119-byte ask is mostly chat template), the
// decode calibration's short count when that is all the run made (TTP-156),
// and 0 when neither measured — the caller then prices at
// planFallbackBytesPerToken. One owner for one conversion: countSet and
// requestTokens both read it here, so a plan can never trim on one ruler and
// cap on another.
//
// Lead, 2026-09-21: the measured price may only make the estimate denser,
// never sparser than planFallbackBytesPerToken. The calibration text is one
// sample and a set prompt is another: the first-run matrix's vLLM row priced
// a prompt at 2822 tokens from a calibration that ran 4.0 bytes/token, the
// server counted 3815 (its tail ran 2.2), and prompt plus cap overran a
// 4096 context — every stream refused. On the real set the spread is just as
// wide (2.43–3.70 bytes/token measured 2026-09-20). Pricing too dense trims a
// little more than it had to; pricing too sparse loses the run.
func (r *run) openAIPrice() float64 {
	measured := 0.0
	if pf := r.calibrationPrefill(); pf != nil && pf.PromptN > 0 {
		measured = float64(pf.PromptBytes) / float64(pf.PromptN)
	} else if cal := r.calibration(); cal != nil && cal.PromptN >= calibrationPriceFloorPromptN {
		measured = float64(cal.PromptBytes) / float64(cal.PromptN)
	}
	if measured <= 0 {
		return 0
	}
	return math.Min(measured, planFallbackBytesPerToken)
}

// openAIPricedTokens prices content bytes at the run's own measured
// bytes-per-token, falling back to the band constant when nothing was
// measured. ceil, like pricedTokens: the tokenizer never undercounts, which
// is the safe direction for every ceiling that reads it.
func (r *run) openAIPricedTokens(bytes int) int {
	bpt := r.openAIPrice()
	if bpt <= 0 {
		return pricedTokens(bytes)
	}
	return int(math.Ceil(float64(bytes) / bpt))
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
		// OpenAI-compatible server is priced, never asked — at the
		// calibration's own measurement of this tokenizer when the run made
		// one (openAIPrice), else at the fallback band.
		for i := range reqs {
			if reqs[i].Set == server.PromptSetID {
				out[i] = r.openAIPricedTokens(len(*promptTextOf(&reqs[i])))
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

// The converging trim's two knobs.
const (
	// planTrimBand is how far under the target a converged trim may land:
	// 3% buys convergence in a handful of corrections while keeping the
	// streams' work equal to within what a card reader can see.
	planTrimBand = 0.97
	// planTrimTokenizations caps the re-counts one prompt may spend. Each
	// one is a round trip, and five covers a density that changes twice
	// between the guess and the answer.
	planTrimTokenizations = 5
)

// trimSetTo cuts every set prompt whose count exceeds target down towards
// target tokens, on rune boundaries, from the front.
//
// When the counts came from the server's own tokenizer the cut converges
// (2026-09-20): the proportional guess assumes the front of a prompt is as
// dense as its whole, and it is not — the set opens with task prose and
// continues with code and logs, so the first takes' cuts landed 25% under
// their target (target 1800, landed 1339–1700) while every stream reported
// a different miss. The cut is re-counted and corrected proportionally —
// shrunk when over, grown while under planTrimBand and runes remain — until
// it lands in [planTrimBand × target, target] or planTrimTokenizations
// re-counts have been spent, after which the largest candidate that fit
// under the target is sent: never over, because the slot behind this step
// refuses over, and as close to it as the ruler could get.
//
// When the lengths were priced there is no ruler to converge against, and
// the cut stays the single proportional one at 98% — a shrink-only era's
// safety, kept because a priced cut cannot be corrected, only guessed
// better.
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
		if !r.planTokenized {
			// The priced single cut, unchanged: no tokenizer, no iteration.
			chars := int(float64(len(runes)) * float64(target) / float64(counts[i]) * 0.98)
			if chars < 1 {
				chars = 1
			}
			if chars > len(runes) {
				chars = len(runes)
			}
			*p = string(runes[:chars])
			continue
		}
		*p = string(r.trimConverge(ctx, runes, counts[i], target))
	}
}

// trimConverge walks one prompt's cut onto the target: proportional guess,
// re-count, proportional correction, and the largest under-target candidate
// seen if the budget of re-counts runs out mid-walk. A tokenizer that stops
// answering ends the walk early the same way — the best candidate so far,
// or the guess itself when nothing better was counted, which is the priced
// cut's answer wearing the ruler it had.
func (r *run) trimConverge(ctx context.Context, runes []rune, count, target int) []rune {
	chars := int(float64(len(runes)) * float64(target) / float64(count))
	if chars < 1 {
		chars = 1
	}
	if chars > len(runes) {
		chars = len(runes)
	}
	cut := runes[:chars]
	bestLen, bestN := 0, 0
	for range planTrimTokenizations {
		n, err := r.client.Tokenize(ctx, string(cut))
		if err != nil {
			break // no ruler: keep the best candidate, or the guess
		}
		if n <= target && (n > bestN || (n == bestN && len(cut) > bestLen)) {
			bestLen, bestN = len(cut), n
		}
		switch {
		case n > target, float64(n) < planTrimBand*float64(target) && len(cut) < len(runes):
			next := int(float64(len(cut)) * float64(target) / float64(n))
			next = min(max(next, 1), len(runes))
			if next == len(cut) {
				return cut // the ruler cannot resolve a finer step
			}
			cut = runes[:next]
		default:
			return cut // in the band: [planTrimBand x target, target]
		}
	}
	if bestLen > 0 {
		return runes[:bestLen]
	}
	return cut
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
// same rate the plan used (the measured concurrent rate when the probe made
// one, the fitted rate with the fallback concurrency factor when it did
// not), the cap as resolved, the slot as reported. A prompt length that was
// never counted is omitted rather than printed as "0-token" (TTP-160,
// 2026-09-21: an OpenAI-kind run priced nothing and printed one anyway), and
// a cap the decode calibration computed says so in plain words, because why
// the cap is what it is — answers end before the clock, so the server's own
// count arrives — is the one question that cap begs (TTP-156). "" when the
// run planned nothing, which is the caller's cue to print nothing.
//
// On the OpenAI-kind prefill-calibrated path (TTP-168, 2026-09-21) the
// target is PRICED, not counted, and the line says so — "~800-token" with
// "priced" beside the binding — because a reader deciding whether to trust
// the length needs to know it came from a bytes-per-token measurement of
// this tokenizer rather than the tokenizer itself; the prefill clause on
// that path is the serial prediction the plan budgeted with (N x the
// longest prompt at the calibrated rate), for the same reason the llama
// path prints its own. The context figure is "ctx" there, not "slot":
// vLLM's max_model_len is per request, and a vLLM user has no slots to
// raise.
//
// The calibrated cap's explaining tail is dropped when it would push the
// line past 110 columns: the priced path has the prefill clause to carry,
// and a wrapped plan line serves nobody. The tail is the least dense clause
// — the binding word already says the clock decided — and the drop is
// deterministic in the figures, not a rendering guess.
func PlanLine(s *tape.RunSummary, streams int) string {
	if s == nil || s.Plan == nil {
		return ""
	}
	p := s.Plan
	n := p.TargetTokens
	if n == 0 {
		n = p.LongestPromptTokens
	}
	// priced says the lengths were measured in bytes at a calibrated
	// bytes-per-token, not counted by a tokenizer: the prefill calibration
	// is the only thing on an OpenAI-kind run that sizes prompts.
	priced := s.Probe != nil && s.Probe.Calibration != nil && s.Probe.Calibration.Prefill != nil
	var b strings.Builder
	if n > 0 {
		num := commaInt(n)
		if priced {
			num = "~" + num
		}
		binding := planBindingWord(p.Binding)
		if priced {
			binding += ", priced"
		}
		fmt.Fprintf(&b, "plan: %d × %s-token prompts (%s)", streams, num, binding)
	} else {
		// Unknown is no figure, never zero: the token count was not counted
		// on this run, and the count it never had must not be printed.
		word := "prompts"
		if streams == 1 {
			word = "prompt"
		}
		fmt.Fprintf(&b, "plan: %d %s (%s)", streams, word, planBindingWord(p.Binding))
	}
	if s.Probe != nil && p.LongestPromptTokens > 0 && streams > 0 {
		if s.Probe.PrefillPerSecond > 0 {
			rate, _ := planConcurrentRate(s.Probe, streams)
			sec := strconv.FormatFloat(float64(streams)*float64(p.LongestPromptTokens)/rate, 'f', 1, 64)
			fmt.Fprintf(&b, " · ~%s s prefill", strings.TrimSuffix(sec, ".0"))
		} else if pf := calibrationPrefillOf(s.Probe); pf != nil && pf.PerSecond > 0 {
			// The serial prediction the plan budgeted with: N prompts at the
			// calibrated rate, the honest figure on a box that prefills one
			// request at a time and a conservative one on a box that batches.
			sec := strconv.FormatFloat(float64(streams)*float64(p.LongestPromptTokens)/pf.PerSecond, 'f', 1, 64)
			fmt.Fprintf(&b, " · ~%s s prefill", strings.TrimSuffix(sec, ".0"))
		}
	}
	if s.Limit.MaxTokens > 0 {
		fmt.Fprintf(&b, " · cap %s", commaInt(s.Limit.MaxTokens))
		if capCameFromCalibration(s) {
			tail := fmt.Sprintf(" (so answers end before the %s clock and the server counts the tokens)",
				s.Limit.For.Round(time.Second))
			if b.Len()+len(tail) <= 110 {
				b.WriteString(tail)
			}
		}
	}
	if p.SlotCtx > 0 {
		word := "slot"
		if s.Server.Kind == tape.ServerOpenAI {
			word = "ctx"
		}
		fmt.Fprintf(&b, " · %s %s", word, commaInt(p.SlotCtx))
	}
	return b.String()
}

// calibrationPrefillOf is the prefill calibration a summary carries, nil
// when the run made none. PlanLine's reader-side twin of the recorder's
// calibrationPrefill.
func calibrationPrefillOf(p *tape.ProbeSummary) *tape.CalibrationPrefill {
	if p == nil || p.Calibration == nil {
		return nil
	}
	return p.Calibration.Prefill
}

// capCameFromCalibration reports whether the resolved answer cap was computed
// from the decode calibration against the clock: the calibration exists only
// on a run whose cap it lowered (calibrateOpenAI applies the cap and the
// binding in one decision), so its presence beside a clock binding is the
// mark of that path.
func capCameFromCalibration(s *tape.RunSummary) bool {
	return s.Plan.Binding == tape.PlanBoundClock &&
		s.Probe != nil && s.Probe.Calibration != nil
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
