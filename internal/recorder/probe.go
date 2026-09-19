package recorder

import (
	"context"
	"strings"

	"github.com/midagedev/toktape/internal/server"
	"github.com/midagedev/toktape/internal/tape"
)

// The prefill probe pass (TTP-137, 2026-09-19).
//
// The figure the card called "prefill" was not one: one prompt length at one
// concurrency carries the server's fixed cost inside it, so 4079 ms for 283
// tokens printed as 69 tok/s on a box whose honest marginal rate is an order
// higher. Two prompt lengths separate them — the slope through the points is
// the marginal cost of a prompt token, which is the machine's, and the
// intercept is what the server spends before it reads the first one.
//
// The pass runs before the prompt set, on one stream, so it measures the
// machine rather than the queueing a concurrent set adds. Its prompts are
// generated, never taken from the published set: a set prompt would leave
// its prefix in the server's cache and poison the very measurement the set
// exists to make (the warm-pass incident of 2026-09-19, cached_prefill).

// The prompt lengths the pass measures, in prompt tokens. The short point is
// a fixed 128: far enough past tape.MinPrefillPromptTokens that it is a
// prefill measurement and not the noise around one, and short enough that it
// costs about a tenth of a second at the reference rate. The long point is
// chosen per box (TTP-142) with probeLongTokens as its ceiling — 2048 is
// llama-bench's pp2048, the figure a reader can line up against the number
// every other tool on this box has already printed, and every box fast enough
// to afford it still sends exactly that.
const (
	probeShortTokens = 128
	probeLongTokens  = 2048
)

// The long point's cost is bounded (TTP-142). A fixed 2048-token point is
// about 1.7 s at the reference box's 1182 tok/s prefill, but 79 s on a box
// measured at 26 tok/s with experts on the CPU — and the replay sends the
// point again, so a default run there spent over two minutes probing,
// silently, before recording anything.
const (
	// probeBudgetMs is what one long point may cost, predicted from the short
	// point's own observed prompt_ms and prompt_n. A few seconds keeps the
	// whole pass in the noise next to DefaultFor (20 s) on any box, while
	// still buying the full 2048-token ceiling wherever prefill is faster
	// than about 410 tok/s.
	probeBudgetMs = 5000.0
	// probeLongFloorMultiple is how many times the short point the long one
	// must at least be for the slope through the pair to be a slope: at 3x,
	// the span between the two lengths is twice the short point (256 tokens
	// of Δn), enough that the long leg dominates Δms/Δn rather than the
	// short point's own noise. A long point that cannot be that long inside
	// the budget is not sent at all — a bad fit is worse than no fit, and
	// fitPrefill already refuses one point.
	probeLongFloorMultiple = 3
)

// probeCharsPerToken converts those token budgets into byte budgets for the
// generator. 4.9 is this repo's measured prose conversion
// (internal/server prompts_test.go); the exact token count is the server's
// to report, so the budget only has to land near the constant.
const probeCharsPerToken = 4.9

// The leads and cycle offsets of the two prompts. Different leads make the
// first token differ; different offsets over a 64-word list make every
// position after it differ too, so the two prompts share no prefix at all
// and neither one's prefill can be cache-served by the other's. Neither lead
// is the first word of any prompt in the published set (nor of the "Request"
// lead its cycled extras use), so the pass cannot poison the set's cache
// either.
const (
	probeLeadShort  = "toktape"
	probeLeadLong   = "probe"
	probeCycleShort = 0
	probeCycleLong  = 33
)

// probeWords is the material the probe prompts are made of: 64 plain words,
// no sentence in them, cycled deterministically so every run of the same
// version sends the same two prompts. filler words instead of real material
// is deliberate here — the prompt exists to cost prefill, not to be
// answered, and one generated token is all that is ever read of it.
var probeWords = []string{
	"harbor", "cinder", "plume", "granite", "thistle", "marble", "ember", "fjord",
	"lantern", "quartz", "meadow", "spruce", "cobalt", "dune", "ivory", "jasper",
	"kelp", "lagoon", "mosaic", "nettle", "opal", "pastel", "quiver", "reed",
	"saffron", "tundra", "umber", "velvet", "willow", "yarrow", "zephyr", "anchor",
	"bramble", "chalk", "dahlia", "echo", "flint", "gable", "hazel", "indigo",
	"juniper", "kayak", "lichen", "mullein", "noble", "onyx", "pumice", "ridge",
	"sedge", "trellis", "vessel", "walnut", "yarn", "amber", "birch", "clover",
	"drift", "elm", "fern", "glen", "heath", "iris", "knot", "cord",
}

// probePrompt builds one probe prompt: the lead word, then the word list
// cycled from offset, up to a byte budget of about tokens prompt tokens.
func probePrompt(lead string, offset, tokens int) string {
	budget := int(float64(tokens) * probeCharsPerToken)
	var b strings.Builder
	b.WriteString(lead)
	for i := 0; b.Len() < budget; i++ {
		b.WriteByte(' ')
		b.WriteString(probeWords[(offset+i)%len(probeWords)])
	}
	return b.String()
}

// prefillProbe measures the machine's own prefill rate before the run: two
// raw /completion requests of different prompt lengths, one token of
// generation each, then the longer prompt a second time to see what the
// server's prefix cache does with a prompt it has already read. It runs
// after the run's requests are built and before the first one is sent, so
// the run's own timeline — startedAt, the sampler's fault baseline, the
// clock's budget — starts clean after it.
//
// The long point's length is not fixed: it is chosen from the short point's
// own observed cost, as the largest that fits probeBudgetMs under the
// ceiling probeLongTokens (probeLongLength, TTP-142). A reader comparing
// runs from different boxes will therefore see different prompt_n values in
// Prefill[1] — that is deliberate, it is the budget doing its job, and the
// fit's slope does not depend on which length was sent. On a box too slow
// for even the floor length to fit, no long point and no replay are sent at
// all: the short point is recorded and fitPrefill refuses, which is the
// honest outcome — one point cannot separate a machine's rate from a
// server's fixed cost.
//
// The pass never fails a run and never warns: a request that errors or a
// server that reports no timings is a figure that was not observed, and nil
// Probe is the schema's "this run did not probe". A ServerOpenAI server is
// skipped whole — it has no /completion route and no timings object, so
// there is nothing to observe through this pass.
func (r *run) prefillProbe(ctx context.Context) {
	if !r.kind.SpeaksLlamaProtocol() {
		return
	}
	p := &tape.ProbeSummary{}
	st, ttft, ok := r.probeSend(ctx, probePrompt(probeLeadShort, probeCycleShort, probeShortTokens))
	if ok && st.PromptN > 0 {
		// A point with no evaluated tokens is not a cost measurement
		// (a cache-served prompt), so it is not recorded as one.
		p.Prefill = append(p.Prefill, tape.PrefillPoint{
			PromptN:  st.PromptN,
			PromptMs: st.PromptMs,
			TTFTMs:   ttft,
		})
	}
	if long := probeLongLength(st); long > 0 {
		if st, ttft, ok = r.probeSend(ctx, probePrompt(probeLeadLong, probeCycleLong, long)); ok && st.PromptN > 0 {
			p.Prefill = append(p.Prefill, tape.PrefillPoint{
				PromptN:  st.PromptN,
				PromptMs: st.PromptMs,
				TTFTMs:   ttft,
			})
		}
		// The replay resends the prompt the long point actually used, never a
		// fixed one: the prefix cache is being asked about that prompt.
		if st, _, ok = r.probeSend(ctx, probePrompt(probeLeadLong, probeCycleLong, long)); ok {
			p.Replay = &tape.ReplayProbe{
				PromptN:  st.PromptN,
				CacheN:   st.CacheN,
				PromptMs: st.PromptMs,
			}
		}
	}
	if len(p.Prefill) == 2 {
		p.PrefillPerSecond, p.FixedMs = fitPrefill(p.Prefill)
	}
	if len(p.Prefill) > 0 || p.Replay != nil {
		r.prefill = p
	}
}

// probeLongLength is the long point's token budget for this box: the short
// point's observed cost, projected onto longer lengths, admits the largest
// length that fits probeBudgetMs, capped at probeLongTokens (TTP-142).
//
// The projection is linear from the origin — L x the short point's ms-per-
// token — which overestimates any affine cost (the fixed part is counted
// pro rata rather than once), so staying inside the budget errs on the
// conservative side; only a superlinear cost curve, which prefill at these
// lengths is not, could outrun it. The returned budget is in the nominal
// tokens probePrompt builds from; the server's own prompt_n of the point
// that comes back is what lands in the tape.
//
// 0 means no long point: the short point was not observed (nothing to
// predict from), or no length past the floor fits the budget — and a fit
// through a too-narrow pair would be noise, not a rate.
func probeLongLength(short server.ServerTimings) int {
	if short.PromptN <= 0 || short.PromptMs <= 0 {
		return 0
	}
	fits := int(probeBudgetMs * float64(short.PromptN) / short.PromptMs)
	if fits > probeLongTokens {
		fits = probeLongTokens
	}
	if fits < probeLongFloorMultiple*probeShortTokens {
		return 0
	}
	return fits
}

// probeSend sends one probe request — the prompt verbatim to /completion,
// capped at a single generated token, generation being cost with no figure
// behind it — and reports the server's own figures for it. ok is false when
// the request failed or the server reported nothing observable about the
// prompt; both mean "not observed", silently.
func (r *run) probeSend(ctx context.Context, prompt string) (st server.ServerTimings, ttftMs float64, ok bool) {
	rec, st, err := r.client.Stream(ctx, server.StreamRequest{
		Endpoint:  tape.EndpointCompletion,
		Prompt:    prompt,
		MaxTokens: 1,
	}, server.StreamHooks{})
	if err != nil || rec == nil {
		return st, 0, false
	}
	// Server figures are the record: prompt_ms from the timings object,
	// never a client stopwatch. The client's TTFT rides along as the third
	// figure of a point, the cross-check it is everywhere else.
	if st.PromptMs <= 0 || (st.PromptN <= 0 && st.CacheN <= 0) {
		return st, 0, false
	}
	return st, rec.Timings.TTFTMs, true
}

// fitPrefill is the two-point fit: the slope is the marginal cost of a
// prompt token — the machine's prefill rate — and the intercept is what the
// server spends per request before it reads the first one.
//
// A fit it cannot trust is refused, never clamped into a plausible number:
// a longer prompt that answered faster is a cache hit or a broken
// measurement, not a rate; two prompts of one length have no slope; a
// negative intercept is a fixed cost the points do not support. Every
// refusal leaves both figures 0 — the schema's "not observed" — while the
// points stay recorded, so a suspicious probe can be taken apart without
// re-running it.
func fitPrefill(points []tape.PrefillPoint) (perSecond, fixedMs float64) {
	if len(points) != 2 {
		return 0, 0
	}
	lo, hi := points[0], points[1]
	if hi.PromptN < lo.PromptN {
		lo, hi = hi, lo
	}
	if lo.PromptN == hi.PromptN {
		return 0, 0 // one length: the slope is undefined
	}
	if hi.PromptMs <= lo.PromptMs {
		return 0, 0 // the longer prompt did not cost more: not a cost curve
	}
	msPerTok := (hi.PromptMs - lo.PromptMs) / float64(hi.PromptN-lo.PromptN)
	fixed := lo.PromptMs - msPerTok*float64(lo.PromptN)
	if msPerTok <= 0 || fixed < 0 {
		return 0, 0
	}
	return 1000 / msPerTok, fixed
}
