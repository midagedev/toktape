package recorder

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

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
	// probeMinSpanTokens is that same Δn, named so the fit can check it on
	// the way back as well: probeLongLength enforces the floor on the length
	// the probe asks for, and fitPrefill enforces it on the span the server
	// actually reported, which a lopsided prefix-cache hit can be narrower
	// than (lead, 2026-09-20). One number, one derivation, two ends.
	probeMinSpanTokens = (probeLongFloorMultiple - 1) * probeShortTokens
)

// probeCharsPerToken converts those token budgets into byte budgets for the
// generator. 4.9 is this repo's measured prose conversion
// (internal/server prompts_test.go); the exact token count is the server's
// to report, so the budget only has to land near the constant.
const probeCharsPerToken = 4.9

// The concurrent point (lead, 2026-09-20). The run plan's first draft
// assumed N concurrent streams prefill at half the one-stream rate; the
// first real takes measured a fifth — 574–716 tok/s aggregate against
// 2963–3083 probed, TTFT 6.5–10.4 s of a 20 s clock — and a run with no
// measurement has to assume. How an engine shares one batch between slots
// is the engine's business and moves with the prompt length (the same box
// ran at 0.47 of its one-stream rate with ~6k-token prompts and ~0.2 with
// ~1.6k ones), so when the run itself will send several streams at once the
// pass asks the server what several prefills at once actually cost, the way
// the run will load it.
const (
	// probeConcurrentTokensMax caps each burst prompt's nominal length. A
	// burst of N x 1024 is enough tokens for the rate to be a rate on any
	// box that can afford the burst at all, and the cap keeps a fast box
	// from buying a longer one for nothing.
	probeConcurrentTokensMax = 1024
	// probeConcurrentTokensMin is the burst's floor. A burst too short to
	// contend meaningfully measures the fixed cost N times, not the shared
	// batch, and 256 is the same floor the plan's own trim holds.
	probeConcurrentTokensMin = 256
	// probeConcurrentSlowdown is how much slower N prefills at once are
	// ASSUMED to be, for budgeting the burst only — never for a figure. The
	// day's box measured 5x at 1.6k tokens and about 2x at 6k, so 4x is a
	// pessimistic middle: a burst that costs more than this against its
	// budget is not sent, which is the honest outcome on a box whose
	// contention is worse than the assumption — no figure beats a wrong one.
	probeConcurrentSlowdown = 4.0
)

// The leads and cycle offsets of the two prompts. With the per-run salts in
// front, the salts make the first token differ and the leads and offsets
// keep every position after it differing too, so the two prompts share no
// prefix at all and neither one's prefill can be cache-served by the
// other's. Neither lead nor any salt shape is the first word of any prompt
// in the published set (nor of the "Request" lead its cycled extras use), so
// the pass cannot poison the set's cache either.
const (
	probeLeadShort  = "toktape"
	probeLeadLong   = "probe"
	probeCycleShort = 0
	probeCycleLong  = 33
)

// probeWords is the material the probe prompts are made of: 64 plain words,
// no sentence in them, cycled deterministically. filler words instead of
// real material is deliberate here — the prompt exists to cost prefill, not
// to be answered, and one generated token is all that is ever read of it.
//
// Deterministic material under a per-run salt (lead, 2026-09-20): the words
// are the same in every run, and what made them so — "every run of the same
// version sends the same two prompts" — was the defect, not the design.
// A server this binary probed before holds both prompts whole in its prefix
// cache, so the re-measurement, the run this pass exists for, came back with
// prompt_n 0 on both points, recorded nothing, and carried no fit. The salt
// is derived from the run's own clock and recorded on the summary, so a
// reader with the tape re-derives the exact prompt bytes: same material,
// never the same prompt twice.
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

// probeSalts is the pair of per-run nonces the two probe prompts open with.
// One number from the clock — twice the unix second, so the short prompt's
// salt is always even and the long prompt's its successor — gives both
// prompts a first token no previous run sent while keeping the pair from
// sharing one with each other: the two salts differ, so the two prompts
// share no prefix, and the even split keeps one run's long salt from ever
// being another run's short salt. The short salt is what the summary
// records; base36 at seven characters covers unix seconds past the year
// 4000.
func probeSalts(now time.Time) (short, long string) {
	twice := now.Unix() * 2
	return strconv.FormatInt(twice, 36), strconv.FormatInt(twice+1, 36)
}

// probeBurstSalts are the per-run nonces the concurrent burst's n prompts
// open with: the fit points take twice and twice+1, and burst i takes
// twice+2+i, so no two prompts of one run share a first token with each
// other or with the fit points — a burst prompt served from another burst
// prompt's prefix would measure the cache, not the batch.
//
// The even split the fit salts keep does not extend here: a later run's
// short salt (even) can equal an earlier run's burst salt (twice+2+i is
// even for even i). What that shares is the salt and the lead — a prefix of
// about two tokens — which is the same size of hit every warm server
// already gives both fit prompts through BOS alone, and the fit is built to
// tolerate it (the span is what it checks, lead, 2026-09-20).
func probeBurstSalts(now time.Time, n int) []string {
	twice := now.Unix() * 2
	out := make([]string, n)
	for i := range out {
		out[i] = strconv.FormatInt(twice+2+int64(i), 36)
	}
	return out
}

// probeBurstCycle is burst i's offset into probeWords: the two fit points
// cycle from 0 and 33, and the burst cycles from just past the long point's
// offset, each prompt one word further, so the material after the salt is
// its own too.
func probeBurstCycle(i int) int {
	return (probeCycleLong + 1 + i) % len(probeWords)
}

// probePrompt builds one probe prompt: the salt, the lead word, then the
// word list cycled from offset, up to a byte budget of about tokens prompt
// tokens.
func probePrompt(salt, lead string, offset, tokens int) string {
	budget := int(float64(tokens) * probeCharsPerToken)
	var b strings.Builder
	b.WriteString(salt)
	b.WriteByte(' ')
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
// server's prefix cache does with a prompt it has already read — and, when
// the run will send several streams at once, a burst of n probe prompts
// together (2026-09-20), because the rate the plan budgets against is the
// one under that load, not the one-stream slope. It runs
// before the run's requests are built — its fit is one of the ceilings the
// run plan takes (plan.go) — and before the first one is sent, so
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
//
// The pass is bracketed by its own fault sampler (TTP-143, 2026-09-19): it
// sends the first prompts a cold server ever sees, so it pays the faults of
// loading the weights — and the run's sampler latches only after the pass
// returns, so without this bracket that cost lands in nobody's figures and
// a genuinely cold server reads warm. The bracket's figure is also the
// better measurement of the two: one stream, prompts of a known length,
// nothing else in flight, where the run's reading was inferred from a decode
// window that was also busy generating. No sampler is no observation,
// silently, like every other miss here.
func (r *run) prefillProbe(ctx context.Context) {
	if !r.kind.SpeaksLlamaProtocol() {
		return
	}
	// The pass is silent startup time — up to ~7.5 s measured on the
	// reference box (TTP-163, 2026-09-21): two fit points, a replay and a
	// concurrent burst, all before the first run request. One note before
	// it starts says what the wait is; a note qualifies no figure, so it
	// reaches the CLI's stderr and the TUI ignores it.
	r.emit(Event{Kind: EventNote, Stream: -1,
		Message: "measuring this server's prefill (a few short requests)…"})
	p := &tape.ProbeSummary{}
	now := r.opts.Clock.Now()
	saltShort, saltLong := probeSalts(now)
	p.Salt = saltShort
	sampler, _ := r.newFaultSampler()
	if sampler != nil {
		defer sampler.Close()
	}
	shortPrompt := probePrompt(saltShort, probeLeadShort, probeCycleShort, probeShortTokens)
	st, ttft, ok := r.probeSend(ctx, shortPrompt)
	if ok && st.PromptN > 0 {
		// A point with no evaluated tokens is not a cost measurement
		// (a fully cache-served prompt), so it is not recorded as one.
		// A partial hit is recorded — with its CacheN, for the reader
		// taking the probe apart, not for the fit to refuse on: the
		// point is the honest cost of the tokens it did evaluate, and
		// what a hit can break is the span, which fitPrefill checks on
		// the pair (its doc, 2026-09-20).
		p.Prefill = append(p.Prefill, tape.PrefillPoint{
			PromptN:     st.PromptN,
			PromptMs:    st.PromptMs,
			PromptBytes: len(shortPrompt),
			TTFTMs:      ttft,
			CacheN:      st.CacheN,
		})
	}
	if long := probeLongLength(st); long > 0 {
		longPrompt := probePrompt(saltLong, probeLeadLong, probeCycleLong, long)
		if st, ttft, ok = r.probeSend(ctx, longPrompt); ok && st.PromptN > 0 {
			p.Prefill = append(p.Prefill, tape.PrefillPoint{
				PromptN:     st.PromptN,
				PromptMs:    st.PromptMs,
				PromptBytes: len(longPrompt),
				TTFTMs:      ttft,
				CacheN:      st.CacheN,
			})
		}
		// The replay resends the prompt the long point actually used, never a
		// fixed one: the prefix cache is being asked about that prompt.
		if st, _, ok = r.probeSend(ctx, longPrompt); ok {
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
	// The concurrent point, after the fit and the replay: the burst's length
	// is budgeted from the fit, so it needs one, and it goes out under the
	// same sampler bracket as the rest of the pass — its faults are the
	// pass's own, and its tokens join the per-token denominator below.
	if n := r.opts.Concurrency; n > 1 && len(p.Prefill) > 0 {
		if tokens := probeConcurrentTokens(n, p.PrefillPerSecond); tokens > 0 {
			p.Concurrent = r.probeConcurrent(ctx, n, tokens, now)
		}
	}
	if sampler != nil {
		// The denominator is the tokens the pass evaluated — both fit
		// points, the replay's own count and the burst's. A cache-served
		// replay reports PromptN 0 in the server's own words, so it
		// contributes nothing and the sum needs no special case: faults of a
		// hit are page-table walks, not prefills.
		if maj, _, err := sampler.FaultDelta(); err == nil {
			tokens := 0
			for _, pt := range p.Prefill {
				tokens += pt.PromptN
			}
			if p.Replay != nil {
				tokens += p.Replay.PromptN
			}
			if p.Concurrent != nil {
				tokens += p.Concurrent.PromptN
			}
			p.MajFaults = maj
			if tokens > 0 {
				p.MajFaultsPerToken = float64(maj) / float64(tokens)
			}
		}
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

// probeConcurrentTokens is the nominal length of each prompt in the
// concurrent burst: the largest up to probeConcurrentTokensMax whose
// predicted cost — n x L at the fitted rate, slowed probeConcurrentSlowdown
// times for budgeting — still fits probeBudgetMs. Like probeLongLength, the
// prediction errs on the conservative side and a length that cannot fit is
// not sent; unlike it, the floor here refuses rather than shrinks, because
// a burst under probeConcurrentTokensMin measures fixed cost N times over
// and calls it a batch.
//
// 0 means no burst: fewer than two streams (nothing concurrent to measure),
// no fit to predict from (the same honesty as a refused fit — no
// measurement, no action), or a box whose contention is worse than the
// assumption even at the floor.
func probeConcurrentTokens(n int, prefillPerSecond float64) int {
	if n <= 1 || prefillPerSecond <= 0 {
		return 0
	}
	fits := int(probeBudgetMs / 1000 * prefillPerSecond / (float64(n) * probeConcurrentSlowdown))
	if fits > probeConcurrentTokensMax {
		fits = probeConcurrentTokensMax
	}
	if fits < probeConcurrentTokensMin {
		return 0
	}
	return fits
}

// probeConcurrent sends n probe prompts at once and records what the server
// did with them: the aggregate prefill rate under the load the run itself
// will apply, fixed costs and queueing inside it, deliberately — the plan
// budgets wall time, not marginal cost. WallMs is the client's clock, from
// the instant before the first send to the last of the first tokens, each
// request's TTFT taken against that common start the way probeSend takes it
// against its own; it is the one client-timed figure in the probe because
// no single server figure spans requests.
//
// nil is "not observed", silently like every miss in the pass: one request
// that failed, one that the cache served whole (prompt_n 0), or a wall the
// clock could not measure — the burst is one measurement, and a measurement
// with a hole in it is not a smaller measurement.
func (r *run) probeConcurrent(ctx context.Context, n, tokens int, now time.Time) *tape.ConcurrentPrefill {
	prompts := make([]string, n)
	for i, salt := range probeBurstSalts(now, n) {
		prompts[i] = probePrompt(salt, probeLeadShort, probeBurstCycle(i), tokens)
	}
	type burst struct {
		ttftMs float64
		n      int
		ok     bool
	}
	out := make([]burst, n)
	start := time.Now()
	var wg sync.WaitGroup
	for i := range prompts {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			// The send happens a moment after this reading; that moment is
			// the request's own launch overhead and belongs to the wall.
			fromStart := time.Since(start)
			st, ttft, ok := r.probeSend(ctx, prompts[i])
			out[i] = burst{fromStart.Seconds()*1000 + ttft, st.PromptN, ok && st.PromptN > 0}
		}(i)
	}
	wg.Wait()

	var sum int
	var wallMs float64
	for _, b := range out {
		if !b.ok {
			return nil
		}
		sum += b.n
		if b.ttftMs > wallMs {
			wallMs = b.ttftMs
		}
	}
	if wallMs <= 0 {
		return nil
	}
	return &tape.ConcurrentPrefill{
		Streams:   n,
		PromptN:   sum,
		WallMs:    wallMs,
		PerSecond: float64(sum) / (wallMs / 1000),
	}
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
// a longer prompt that answered faster is a broken measurement, not a rate;
// two prompts too close in length have no slope worth reporting; and a
// negative intercept is a fixed cost the points do not support. Every
// refusal leaves both figures 0 — the schema's "not observed" — while the
// points stay recorded, so a suspicious probe can be taken apart without
// re-running it; tapefigures prints each point's cache_n for exactly that
// question.
//
// A cache hit is not on that list any more (lead, 2026-09-20, correcting
// 2026-09-19). The refusal used to fire on CacheN > 0, on the premise that
// a served point "measures the cache's discount as if it were the machine's
// speed". That premise was wrong about the wire: timings.prompt_n is
// n_prompt_processed and timings.cache_n is n_prompt_cached — disjoint
// counts, summing to the prompt — so a partially served point is the honest
// cost of the tokens it did evaluate, plotted at the x it did evaluate. The
// pair through it recovers the same slope and the same intercept.
//
// Refusing on it was also refusing every warm server. Both prompts go to
// /completion, which tokenizes with add_special, so BOS alone is a shared
// first token; a slot keeps its tokens between requests and the common
// prefix is assigned to n_prompt_cached with no floor. On a slot that had
// served anything at all, CacheN was 1 or more and there was no fit.
//
// What a hit can genuinely ruin is the span. The slope is a difference
// quotient, and a hit that lands on the long point alone drags it toward
// the short one until the denominator is small enough for noise in
// PromptMs to dominate. A prefix both prompts share shaves the same count
// off both and cancels; only the lopsided hit collapses the span. So the
// span is what is checked, and it is checked against the floor the probe
// already imposes on the length it asks for: probeLongLength refuses to
// send a long point under probeLongFloorMultiple short points, which is an
// intended span of probeMinSpanTokens. A pair that comes back spanning
// less than that did not measure what it set out to measure.
func fitPrefill(points []tape.PrefillPoint) (perSecond, fixedMs float64) {
	if len(points) != 2 {
		return 0, 0
	}
	lo, hi := points[0], points[1]
	if hi.PromptN < lo.PromptN {
		lo, hi = hi, lo
	}
	if hi.PromptN-lo.PromptN < probeMinSpanTokens {
		return 0, 0 // too close together for a slope: see the span paragraph
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

// The decode calibration (TTP-156, 2026-09-21): the OpenAI-kind server's own
// answer to "how fast does this box decode", asked with one request before
// the run.
//
// Measured that day on Ollama 0.34.2 with llama3.2:1b: a ServerOpenAI server
// counts tokens only in the closing usage chunk of a stream that ended on its
// own, the default 20 s clock cancels the stream instead, and a cancelled
// stream's usage chunk never arrives — so the run that streamed 1127 chunks
// printed "Sample ?" and the tokens_uncounted caveat. Counting chunks would
// have been the guess lesson 1 forbids. The calibration measures the rate
// once, the run plan turns it into an answer cap every stream reaches before
// the clock, and the clock stays armed behind it as the guard it always was.
const (
	// calibrationDecodeShare is the share of the calibration's decode rate
	// the cap may promise, because decode at the run's own prompt length
	// runs slower than the calibration measured at its short one — the KV
	// cache the prompt leaves behind is paid on every token. Five runs on
	// one unchanged box (M1 Pro, Ollama 0.34.2, llama3.2:1b, ~6,150-token
	// prompts, 2026-09-21) measured actual/calibrated at 0.798, 0.808,
	// 0.786, 0.787 and 0.729, and the 0.8 this replaces sat at the centre
	// of that spread: wrong half the time, and all five caps landed above
	// their 20 s clock (TTP-170). A factor set to a centre loses the race
	// on every run slower than the middle; 0.65 is below the slowest run
	// measured, with the margin a wider spread on another box still has to
	// get through (and the clock's grace behind it when one does).
	calibrationDecodeShare = 0.65
	// calibrationTokens is the answer cap the calibration request carries.
	// Long enough that (n-1)/decode_ms is not one chunk's jitter, short
	// enough to cost about a second at 50 tok/s — the two bounds of the
	// decision, and the same order as the real Ollama take (40 tokens
	// counted for a 48 cap, 2026-09-21).
	calibrationTokens = 48
	// calibrationTimeout bounds the whole calibration request. It is its own
	// budget, not the run's: the run's clock has not started yet, and a
	// calibration that eats into the run's budget would be trading the thing
	// it exists to protect.
	calibrationTimeout = 30 * time.Second
	// calibrationMinPredicted is the least server-counted answer a
	// calibration may be used from. Below it the rate is one or two chunks'
	// jitter wearing a unit.
	calibrationMinPredicted = 8
	// calibrationPriceFloorPromptN is the least server-counted prompt a
	// calibration may price this tokenizer at, in prompt tokens: the
	// calibration's own prompt is short, and a chat template dominates a
	// short one — the real Ollama take counted 51 tokens for a 119-byte
	// ask, most of them template — so below this floor the bytes-per-token
	// ratio is the template's, not the tokenizer's, and the fallback price
	// is the more honest ruler.
	calibrationPriceFloorPromptN = 16
)

// The prefill half of the calibration (TTP-168, 2026-09-21). The decode cap
// alone budgeted no prefill: on Ollama a 7.9k-token prompt cost 4.1 s the cap
// had not reserved (the default 20 s clock then cut the stream and the usage
// chunk never arrived), and on CPU vLLM TTFT ran to 74 s under the same
// clock. A second short request — a long salted prompt, a one-token answer,
// left to end on its own — prices prefill on a server that times nothing
// itself, and the plan spends that price before it spends the decode budget.
const (
	// calibrationPrefillBytes is the byte length of that prompt: long enough
	// that its prefill is a rate and not the fixed cost (the same order as
	// the 4096-token prompts the fixtures measure), short enough to cost
	// about a second at 4k tok/s and under the 60 s timeout at 70 tok/s —
	// a box slower than that refuses the point, which is the honest outcome.
	calibrationPrefillBytes = 4096
	// calibrationPrefillTimeout is the point's own budget, twice the decode
	// request's: prefill is the thing being measured, and a box that cannot
	// read 4 kB of prompt in a minute has no prefill rate to budget with.
	calibrationPrefillTimeout = 60 * time.Second
	// calibrationPrefillMinPromptN is the least server-counted prompt the
	// point may be used from. A 4096-byte prompt counts ~800–1700 tokens on
	// the tokenizers this tool has met; a count far under that says the
	// server truncated the prompt (Ollama shapes do) or answered it from a
	// cache, and either way the rate is not this box's prefill.
	calibrationPrefillMinPromptN = 256
	// calibrationPrefillMinMs is the least prefill span the point may be
	// used from. The rate divides by (this prompt's TTFT less the short
	// request's), and when that difference is a timer tick or two the
	// quotient is noise wearing four significant digits — a server whose
	// 4 kB prefill really is that cheap needs no prefill budget, and the
	// decode-only cap is already the right plan for it.
	calibrationPrefillMinMs = 20.0
)

// calibrationAsk is the fixed question the calibration sends. It asks for a
// long mechanical answer, so the 48-token cap is what ends the stream and
// not the model finishing: a stream ended by EOS measures how long the model
// felt like answering, not how fast the box decodes.
const calibrationAsk = "Count upward from one, digits only, one number per line, and do not stop until you are told to."

// calibrateOpenAI sends the two calibration requests and records what they
// measured (TTP-156, and TTP-168 for the second one). rounds is every slice
// of requests the run will send — one slice for a plain run — but only to
// read the first request's model: the cap the measurements buy is applied
// after the plan sizes the prompts it must fit beside, by
// applyCalibratedCap.
//
// It runs only on a ServerOpenAI server, only when the run has a clock, and
// only when the user named no cap of their own (--n-predict is an answer on
// the same axis, and the recorder does not overrule it). It never fails a
// run; a calibration that is dropped says so in ONE note naming the guard
// that dropped it (TTP-177, 2026-09-21). Until that change a drop recorded
// nothing and said nothing, which was right when the calibration was an
// optimisation and wrong now that the cap and the plan behind it depend on
// it: the whole first-run defect of TTP-177 — 48 server-counted tokens, 0
// parsed, cap fallen back to the runaway guard — was invisible for exactly
// that silence. The run itself still proceeds exactly as it did before this
// existed — clock, cut and caveat included.
func (r *run) calibrateOpenAI(ctx context.Context, rounds ...[]server.StreamRequest) {
	if r.kind != tape.ServerOpenAI || r.limit.For <= 0 || r.limit.MaxTokensNamed {
		return
	}
	var first []server.StreamRequest
	for _, rd := range rounds {
		if len(rd) > 0 {
			first = rd
			break
		}
	}
	if first == nil {
		return
	}
	// Both requests are silent startup time the user watches a still screen
	// through (TTP-163's sibling): one note before them says what is
	// happening. A note qualifies no figure, so it reaches the CLI's stderr
	// and the TUI ignores it.
	r.emit(Event{Kind: EventNote, Stream: -1,
		Message: "measuring this server's speed (2 short requests)…"})
	// The salt is derived from the run's own clock the way the plan's and
	// the probe's salts are (planSet, probeSalts), with a word of its own so
	// no run's prompt, probe or calibration ever shares a first token with
	// another: a calibration answered from a prefix cache would measure the
	// cache, not the box.
	prompt := "[cal " + strconv.FormatInt(r.opts.Clock.Now().UnixNano(), 36) + "] " + calibrationAsk

	cctx, cancel := context.WithTimeout(ctx, calibrationTimeout)
	defer cancel()
	rec, _, err := r.client.Stream(cctx, server.StreamRequest{
		Protocol:  tape.ServerOpenAI,
		Model:     requestModelOf(&first[0]),
		MaxTokens: calibrationTokens,
		Messages:  []tape.Message{{Role: "user", Content: prompt}},
	}, server.StreamHooks{OnFingerprint: r.noteFingerprint})
	// The conditions of a usable calibration, each named when it fails: a
	// drop says which guard dropped it, in one note — never a warning, the
	// run still proceeds exactly as before. A timeout arrives here as the
	// request's own error.
	drop := func(why string) {
		r.emit(Event{Kind: EventNote, Stream: -1,
			Message: "no decode calibration: " + why + " — the run proceeds without one"})
	}
	// The stream ended on its own (no error), the server counted the answer
	// (usage, never chunks), the count is more than jitter, the client parsed
	// enough of the stream to time a window, and the window and the count
	// together yield a rate. rec.Timings.PromptN is the usage chunk's
	// prompt_tokens — the server's own count, on the record since TTP-167.
	if err != nil {
		drop(fmt.Sprintf("the request failed (%v)", err))
		return
	}
	if rec == nil {
		drop("the request returned no record")
		return
	}
	if rec.Timings.PredictedNSource != "usage" {
		drop("the server's token count never arrived (no usage figure)")
		return
	}
	if rec.Timings.PredictedN < calibrationMinPredicted {
		drop(fmt.Sprintf("the server counted only %d tokens, under %d",
			rec.Timings.PredictedN, calibrationMinPredicted))
		return
	}
	if rec.Timings.PromptN <= 0 {
		drop("the usage figure carried no prompt count")
		return
	}
	if len(rec.Tokens) < 2 {
		// TTP-177's own guard: the server counted its answer and the client
		// parsed none of it, so there is no client window at any rate — the
		// unparsed-dialect signature, named with both counts.
		drop(fmt.Sprintf("the client parsed %d of the %d counted tokens, too few to time",
			len(rec.Tokens), rec.Timings.PredictedN))
		return
	}
	window := rec.Tokens[len(rec.Tokens)-1].T - rec.Tokens[0].T
	if window <= 0 {
		drop("every parsed token arrived at one instant, so there is no window to time")
		return
	}
	cal := &tape.DecodeCalibration{
		PromptN:     rec.Timings.PromptN,
		PredictedN:  rec.Timings.PredictedN,
		PromptBytes: len(prompt),
		TTFTMs:      rec.Timings.TTFTMs,
		DecodeMs:    float64(window) / float64(time.Millisecond),
		// Server-counted tokens over a client-timed span: the same figure
		// every client-timed rate on the tape is (reduceClientTimed), one
		// stream, nothing else running.
		PerSecond: float64(rec.Timings.PredictedN-1) / window.Seconds(),
	}
	if cal.PerSecond <= 0 {
		drop("the counted window yields no rate")
		return
	}
	cal.Prefill = r.calibratePrefill(ctx, first, cal)
	if r.prefill == nil {
		r.prefill = &tape.ProbeSummary{}
	}
	r.prefill.Calibration = cal
}

// calibratePrefill is the second calibration request: a long salted prompt,
// one generated token, its own timeout, left to end on its own so the usage
// chunk arrives. nil is "not observed", silently like every miss in the
// pass — the run then plans with the decode-only cap exactly as it did
// before the prefill had a price.
//
// The prompt is built from the probe's own deterministic word list under a
// per-run salt (probePrompt), so it is not the published set's text and
// cannot warm the set's prefix: the salt moves the first token, and the
// words are no prompt's opening. Its length is calibrationPrefillBytes,
// shrunk to fit the model's context when the server reported one — a probe
// the server would refuse measures a refusal.
func (r *run) calibratePrefill(ctx context.Context, first []server.StreamRequest, cal *tape.DecodeCalibration) *tape.CalibrationPrefill {
	bytes := calibrationPrefillBytes
	if mml := r.openaiModelCtx(requestModelOf(&first[0])); mml > 0 {
		// Price the probe at the calibration's own bytes-per-token: the
		// honest ruler this tokenizer has been measured with so far.
		room := int(float64(mml-slotCtxTemplateTokens-1) * float64(cal.PromptBytes) / float64(cal.PromptN))
		if room < bytes {
			bytes = max(room, 0)
		}
	}
	// A context so small the probe would count under the floor prompt
	// length sends nothing: there is no prefill rate to measure in it.
	if bytes*cal.PromptN < calibrationPrefillMinPromptN*cal.PromptBytes {
		return nil
	}
	// probePrompt takes a nominal token budget and converts at
	// probeCharsPerToken; the byte target converts back, and PromptBytes
	// records whatever length it actually built.
	prompt := probePrompt(strconv.FormatInt(r.opts.Clock.Now().UnixNano(), 36),
		probeLeadShort, probeCycleShort, int(float64(bytes)/probeCharsPerToken))

	pctx, cancel := context.WithTimeout(ctx, calibrationPrefillTimeout)
	defer cancel()
	rec, _, err := r.client.Stream(pctx, server.StreamRequest{
		Protocol:  tape.ServerOpenAI,
		Model:     requestModelOf(&first[0]),
		MaxTokens: 1,
		Messages:  []tape.Message{{Role: "user", Content: prompt}},
	}, server.StreamHooks{OnFingerprint: r.noteFingerprint})
	if err != nil || rec == nil || rec.Timings.PredictedNSource != "usage" ||
		rec.Timings.PromptNSource != "usage" {
		return nil
	}
	// The span removes the fixed request overhead BOTH requests pay — queue,
	// HTTP, tokenize — so the quotient below is the prompt's own marginal
	// cost. The subtraction is clamped at subtracting nothing: its purpose
	// is to remove what the two requests share, and a decode calibration
	// that paid the model's load (the first request a cold server serves)
	// carries that load in its TTFT while this request, sent after it, did
	// not pay it — the raw difference went negative and silently dropped
	// the point exactly on the coldest box (measured 2026-09-21, six runs:
	// cal.ttft_ms 1094.2 and 1611.7 on the two cold runs, no point; 72.8,
	// 82.8 and 107.6 on the warm ones, point present, spans 462–494 ms —
	// TTP-170). When the clamp fires, the overhead this request DID pay
	// stays inside the span, which under-states the rate and so reserves
	// more clock than the prompt needs — the safe side for a budget. A warm
	// run's span is positive and is subtracted exactly as before.
	span := rec.Timings.TTFTMs - cal.TTFTMs
	if span < 0 {
		span = rec.Timings.TTFTMs
	}
	if rec.Timings.PromptN < calibrationPrefillMinPromptN || span < calibrationPrefillMinMs {
		return nil
	}
	if rec.Timings.PromptN <= rec.Timings.CacheN {
		return nil // served whole from the prefix cache: a cache lookup, not a prefill
	}
	perSecond := 1000 * float64(rec.Timings.PromptN-rec.Timings.CacheN) / span
	if perSecond <= 0 {
		return nil
	}
	return &tape.CalibrationPrefill{
		PromptN:     rec.Timings.PromptN,
		CachedN:     rec.Timings.CacheN,
		PromptBytes: len(prompt),
		TTFTMs:      rec.Timings.TTFTMs,
		PerSecond:   perSecond,
	}
}

// applyCalibratedCap lowers the run's answer cap onto what the calibration
// measured, once the plan has sized the prompts (TTP-156's cap with
// TTP-168's prefill subtracted; it absorbs markCalibrationPlan, whose
// binding half it ends with). rounds is every slice the run will send; one
// run sends one shape, so the cap lands on all of them.
//
// The cap: calibrationDecodeShare (0.65, below the measured floor of the
// long-context slowdown — its constant) x the measured decode rate x
// whatever clock is left after the prefill the plan chose is predicted to
// take, divided over the streams. A server that batches gives each of N
// streams at least rate/N; one that serialises (Ollama with
// OLLAMA_NUM_PARALLEL=1) finishes them one after another in N x cap/rate —
// both land inside the clock, so every stream ends on its own and its usage
// chunk arrives. The prefill is predicted SERIALLY (N x tokens / rate):
// nothing measured this server's concurrent prefill, and Ollama at parallel
// 1 is literally serial, so the serial prediction is the one that cannot
// promise time the box has not been heard to deliver (a batching server
// then finishes early, which costs nothing).
//
// When no prefill point was observed, planPrefillShare of the clock is
// reserved instead of nothing (TTP-170, 2026-09-21): both cold runs of the
// day's six lost their point to a negative span (calibratePrefill's clamp
// is the fix) and sized their caps as if TTFT were zero — 4.1 s of a 20 s
// clock given away. The plan's own share is the reserve because it is the
// same fraction the plan would have budgeted prefill with had it been able
// to price it.
//
// When the derate and the reserve still lose the race — a box with a wider
// spread than the five runs the share was set from — the clock's grace
// (limit.go) waits, bounded, for the streams to end on their own rather
// than destroying the measurement; and if the grace runs out too, the run
// is cut, cut and caveat included, exactly as it was before the cap existed.
//
// The floor is tape.MinCutTokens, the same minimum the clock's own cut
// respects: below tape.MinDecodeTokens a rate is not a rate at all, and
// MinCutTokens is twice that with room to spare. Only a lowering — a cap
// some other rule already chose smaller keeps its own.
func (r *run) applyCalibratedCap(rounds ...[]server.StreamRequest) {
	cal := r.calibration()
	if cal == nil || cal.PerSecond <= 0 || r.limit.For <= 0 {
		return
	}
	var first []server.StreamRequest
	for _, rd := range rounds {
		if len(rd) > 0 {
			first = rd
			break
		}
	}
	if first == nil {
		return
	}
	// The prefill the plan's own lengths are predicted to cost, serial over
	// the streams: the longest prompt as sent, priced on the same ruler the
	// plan priced it with. With no point to price it, the plan's own share
	// of the clock is reserved instead — the prompts still cost prefill the
	// clock pays before the first token, and reserving nothing is how the
	// day's cold runs gave 4.1 s of a 20 s clock away (TTP-170).
	budget := r.limit.For.Seconds()
	reserved := 0.0
	if pf := r.calibrationPrefill(); pf != nil && pf.PerSecond > 0 {
		longest := 0
		for i := range first {
			if est, _, _ := r.requestTokens(context.Background(), &first[i]); est > longest {
				longest = est
			}
		}
		reserved = float64(max(r.opts.Concurrency, 1)) * float64(longest) / pf.PerSecond
	} else {
		reserved = planPrefillShare * r.limit.For.Seconds()
	}
	budget -= reserved
	if budget < 0 {
		budget = 0
	}
	streams := max(r.opts.Concurrency, 1)
	cap := int(calibrationDecodeShare * cal.PerSecond * budget / float64(streams))
	cap = max(cap, tape.MinCutTokens)
	if cap < r.limit.MaxTokens {
		for _, rd := range rounds {
			for i := range rd {
				if rd[i].MaxTokens > cap {
					rd[i].MaxTokens = cap
				}
			}
		}
		// Recorded where an unnamed cap is recorded today — LimitSummary,
		// with MaxTokensNamed left false — so the tape reproduces the run's
		// shape without crediting the user with a number they did not type.
		r.limit.MaxTokens = cap
		r.opts.MaxTokens = cap
		r.calibCapped = true
		// What the cap predicted, said where a reader reaches it (TTP-170):
		// the run used to print the cap it chose and never the prediction
		// behind it, and diagnosing a cut run was a session of guessing. A
		// note rather than a schema field because every input of the
		// prediction is already on the tape — the calibration's rate and
		// prefill point (Probe.Calibration), the reserved share's fallback
		// rule, the cap (Limit.MaxTokens) — so `-o json` carries them all
		// and the note is the same arithmetic spoken beside the plan line.
		//
		// The figure is a BAND, and the reason is arithmetic (lead,
		// 2026-09-21): the cap is chosen as share x rate x budget, so
		// cap / (share x rate) is the budget back again and a single
		// "predicted finish" would print the clock every time on a
		// one-stream run — a checksum of its own sum, not a fact about the
		// box. The two ends are the ones a reader can be wrong between: at
		// the rate the calibration actually measured, and at the derated
		// rate the cap was bought at. A run that lands inside the band went
		// as planned; one that is cut anyway came in slower than the
		// derate, which is the sentence the next diagnosis needs and could
		// not get.
		fast := reserved + float64(cap)/cal.PerSecond
		slow := reserved + float64(cap)/(calibrationDecodeShare*cal.PerSecond)
		r.emit(Event{Kind: EventNote, Stream: -1, Message: fmt.Sprintf(
			"cap %s: %s tok/s calibrated, %s s prefill reserved, so the run finishes in %s-%s s of the %s s clock (the high end assumes %s of the calibrated rate, which is what the cap was bought at)",
			commaInt(cap),
			strconv.FormatFloat(cal.PerSecond, 'f', 0, 64),
			strconv.FormatFloat(reserved, 'f', 1, 64),
			strconv.FormatFloat(fast, 'f', 1, 64),
			strconv.FormatFloat(slow, 'f', 1, 64),
			strconv.FormatFloat(r.limit.For.Seconds(), 'f', 0, 64),
			strconv.FormatFloat(calibrationDecodeShare, 'f', 2, 64))})
	}
	// The plan names its limit when the cap landed: markCalibrationPlan
	// guards on calibCapped, exactly as it did when the cap was applied
	// inside the calibration — a cap some other rule already chose smaller
	// keeps that rule's binding.
	r.markCalibrationPlan()
}

// calibration is this run's decode calibration, nil when none was recorded.
func (r *run) calibration() *tape.DecodeCalibration {
	if r.prefill == nil {
		return nil
	}
	return r.prefill.Calibration
}

// calibrationPrefill is the calibration's prefill point, nil when the second
// request was not made or was refused. planSet's clock ceiling and the price
// countSet prices with both read it here, so the guards have one reader's
// worth of surface.
func (r *run) calibrationPrefill() *tape.CalibrationPrefill {
	if cal := r.calibration(); cal != nil {
		return cal.Prefill
	}
	return nil
}

// markCalibrationPlan puts the calibrated cap on the run plan: the plan is
// the one place a reader sees which limit decided the run's shape, and on
// this path the limit was the clock the cap was computed against. It runs
// after plan(), which may have created the plan for the set's own reasons,
// and creates one on a path that planned nothing else (a user's own prompts)
// so the decision is never left off the tape.
func (r *run) markCalibrationPlan() {
	if !r.calibCapped {
		return
	}
	if r.runPlan == nil {
		r.runPlan = &tape.RunPlan{}
	}
	r.runPlan.Binding = tape.PlanBoundClock
}

// resolvePromptTrim and trimSetPrompts lived here until 2026-09-20, when
// the run plan (plan.go) became the single owner of prompt length: the
// fixed-share character trim they implemented kept every same-length prompt
// whole on a fast box and cut four different tokenizers' worth of work to
// one character count on the rest. The reasoning that outlived them — that
// a prefill figure is a throughput only when the per-request fixed cost is
// a small share of it — is what planPrefillShare's budget carries now.
