package recorder

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/midagedev/toktape/internal/procmon"
	"github.com/midagedev/toktape/internal/server"
	"github.com/midagedev/toktape/internal/tape"
)

// Multi-prompt runs (TTP-31, 2026-09-13).
//
// Measured on the rig log: one DSpark draft model is accepted 13 % of the time
// on prose and 87 % on SQL. A card recorded from one prompt therefore settles
// nothing, so `record --prompts file.jsonl` sends each line as its own round of
// Concurrency streams, strictly one round after another, into one tape. The
// run-level figures are over every stream of every round, each round keeps
// its own reduction in RunSummary.PerRound, and RunSummary.Spread is the
// median with its range that the card prints.

// Round is one line of a prompts file: a name for the card and the prompts
// that round sends. Fewer prompts than Concurrency are cycled exactly as
// Options.Prompts are; no prompt at all means the default prompt set.
type Round struct {
	// Name is the line's "name", or "" and the card labels the round by its
	// 1-based number.
	Name    string
	Prompts []server.StreamRequest
}

// roundsConcurrency is the stream count a multi-round run defaults to: the
// longest round's prompt list, so no prompt of any round is left unsent.
func roundsConcurrency(rounds []Round) int {
	n := 1
	for _, rd := range rounds {
		if len(rd.Prompts) > n {
			n = len(rd.Prompts)
		}
	}
	return n
}

// roundRequests builds every round's Concurrency requests.
//
// A request's own max_tokens wins over Options.MaxTokens here, unlike in a
// single-round run: the CLI always passes --n-predict (its default included),
// and a prompts file that names a cap per line is the more specific of the
// two. A line without one gets Options.MaxTokens.
func roundRequests(o Options, activeBytesPerToken int64) [][]server.StreamRequest {
	out := make([][]server.StreamRequest, len(o.Rounds))
	for k, rd := range o.Rounds {
		ro := o
		ro.Prompts = rd.Prompts
		ro.MaxTokens = 0
		reqs := buildRequests(ro, activeBytesPerToken)
		for i := range reqs {
			if reqs[i].MaxTokens == 0 && o.MaxTokens > 0 {
				reqs[i].MaxTokens = o.MaxTokens
			}
		}
		out[k] = reqs
	}
	return out
}

// roundPrefill is a round's engine prefill from the server's own figures: the
// prompt tokens its streams evaluated over the prompt_ms the server took for
// them (TTP-64, 2026-09-14). A stream with no prompt timings is unknown and is
// left out of both sums rather than counted as a zero-time evaluation, which
// would inflate the rate.
func roundPrefill(rr []tape.RequestRecord) (promptN int, perSecond float64) {
	var ms float64
	for _, r := range rr {
		if r.Timings.PromptN <= 0 || r.Timings.PromptMs <= 0 {
			continue
		}
		promptN += r.Timings.PromptN
		ms += r.Timings.PromptMs
	}
	if ms > 0 {
		perSecond = float64(promptN) / (ms / 1000)
	}
	return promptN, perSecond
}

// recordRounds is Record from the first request on, for a multi-round run.
func (r *run) recordRounds(ctx context.Context) (*tape.Tape, error) {
	// The prefill probe pass (TTP-137), once per tape and first of all —
	// before round one's first request, so no round's cache picture or
	// fault baseline sees it, and before the rounds are built because its
	// fit is one of the ceilings the run plan takes (2026-09-20, plan.go).
	r.prefillProbe(ctx)

	rounds := roundRequests(r.opts, r.model.ActiveBytesPerToken)
	if err := r.chooseOpenAIModel(rounds[0]); err != nil {
		return nil, err
	}
	// Shaping precedes the plan here for the same reason it does in Record:
	// the calibration measures as the model the run will request, and the
	// plan prices with what the calibration counted (TTP-156/TTP-157,
	// 2026-09-21).
	for k := range rounds {
		if shaped, err := r.shapeOpenAIRequests(rounds[k]); err != nil {
			return nil, err
		} else {
			rounds[k] = shaped
		}
	}
	// One calibration pass for the whole tape, capping every round's
	// requests (TTP-156): the answer cap is a property of the clock and the
	// rate, neither of which changes between rounds. The cap itself lands
	// after the plan, as it does in Record, so the prefill the plan sizes
	// and the cap share one clock.
	r.calibrateOpenAI(ctx, rounds...)
	// The plan is one decision for the whole tape, made from the first
	// round and applied to every round (lead, 2026-09-20): a rounds run
	// sends the same set every round, and where it does not, the set's
	// rounds are still cut to one target so the recorded number stays the
	// truth for every one of them. Each round's prompts are their own size,
	// so the slot's room is asked once per round, after that round's trim
	// (TTP-148).
	if err := r.plan(ctx, rounds[0]); err != nil {
		return nil, err
	}
	for k := 1; k < len(rounds); k++ {
		if err := r.applyPlan(ctx, rounds[k]); err != nil {
			return nil, err
		}
	}
	r.applyCalibratedCap(rounds...)
	if len(rounds) > 0 {
		r.promptSet = promptSetOf(rounds[0])
	}

	// The template is fetched for every stream of every round, in one pass,
	// so every record carries its rendered prompt and one warning covers the
	// failures. The rounds are then re-sliced from the same backing array, so
	// the rendered prompts written into flat are the ones each round sends.
	n := r.opts.Concurrency
	flat := make([]server.StreamRequest, 0, n*len(rounds))
	for _, reqs := range rounds {
		flat = append(flat, reqs...)
	}
	r.collectTemplate(ctx, flat)
	for k := range rounds {
		rounds[k] = flat[k*n : (k+1)*n]
	}
	r.emitAttached()

	startedAt := r.opts.Clock.Now()
	recs, st, err := r.streamRounds(ctx, rounds)
	finishedAt := r.opts.Clock.Now()
	if err != nil {
		return nil, err
	}

	t := r.reduce(recs, st, startedAt, finishedAt)
	if r.opts.Progress != nil {
		r.opts.Progress(Event{Kind: EventDone, Stream: -1, Streams: r.opts.Concurrency, Summary: &t.Summary})
	}
	return t, nil
}

// streamRounds sends the rounds one after another under one sampler and one
// periodic reader, and returns every record in run order: round 0's N streams,
// then round 1's, and so on.
//
// A round in which every stream failed is kept — its records carry the error —
// and the run goes on to the next round. Only a run in which every round failed
// is ErrAllStreamsFailed. A cancelled context stops the run between rounds:
// the rounds already sent are reduced and a warning says where it stopped.
//
// The run's clock (TTP-76) spans every round, not each round separately,
// because the budget is a statement about the run: `--for 20s` over a
// four-round prompts file is twenty seconds of run, not eighty. It cuts the
// round that is live and the rounds after it are never sent, which is a cut of
// the run and not a cancellation — they get different sentences.
func (r *run) streamRounds(ctx context.Context, rounds [][]server.StreamRequest) ([]tape.RequestRecord, *state, error) {
	sampler, closeSampler := r.openSampler()
	defer closeSampler()

	n := r.opts.Concurrency
	st := newState(n*len(rounds), sampler, r.opts.Progress)
	st.perRound, st.streams = n, n

	var (
		recs         = make([]tape.RequestRecord, 0, n*len(rounds))
		stopSampling = func() {}
		origin       time.Time
		failed       int
		firstErr     error
		runCtx       = ctx
		clk          *clock
		// marked is how many streams the clock actually ended, and cutAfter
		// the round count sent before it stopped the run. Either one makes the
		// run a cut one; a budget that ran out between two rounds ends the run
		// without ending a stream.
		marked   int
		cutAfter int
		// cancelledAfter is the round count sent before a cancellation, 0 when
		// the run was not cancelled. The warning is emitted after the sampler
		// stops: r.warn calls Progress, and Options.Progress promises calls
		// serialised with the sampler's own events (lead, 2026-09-13).
		cancelledAfter int
	)
	r.roundNames = nil
	for k, reqs := range rounds {
		if k > 0 && runCtx.Err() != nil {
			// The clock's own cut reads as a cancelled context down here, so
			// it is asked about first: "the budget ran out" and "you pressed
			// ^C" are different things to tell a reader.
			if at, _ := clk.cut(); at > 0 {
				cutAfter = k
			} else {
				cancelledAfter = k
			}
			break
		}
		base := k * n
		name := r.opts.Rounds[k].Name
		got, err := r.sendRound(runCtx, st, k, reqs, len(rounds), name, &origin, func() context.Context {
			stopSampling = r.startSampling(ctx, st)
			origin = time.Now()
			runCtx, clk = r.startClock(ctx, st, origin)
			return runCtx
		})
		// Before the round is judged: a round every one of whose streams the
		// clock cut is a round that answered, not one that failed.
		if at, live := clk.cut(); at > 0 && base <= len(live) {
			// live is indexed run-wide (g = round*N + i); got is this round's.
			marked += markCut(got, live[base:])
		}
		recs = append(recs, got...)
		r.roundNames = append(r.roundNames, name)
		if err != nil && allFailed(got) {
			failed++
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	if clk != nil {
		clk.stop()
		if at, _ := clk.cut(); at > 0 && (marked > 0 || cutAfter > 0) {
			r.cutAt = at
		}
	}
	stopSampling()
	r.warnTreeProcesses(st)
	if cutAfter > 0 {
		r.warn("the run's budget ran out after round %d of %d", cutAfter, len(rounds))
	}
	if cancelledAfter > 0 {
		r.warn("run cancelled after round %d of %d", cancelledAfter, len(rounds))
	}

	if failed == len(r.roundNames) {
		return nil, nil, fmt.Errorf("%w: every round failed: %v", ErrAllStreamsFailed, firstErr)
	}
	st.applyDeltas(recs)
	return recs, st, nil
}

// sendRound sends round k — its requests all at once — and returns their
// records stamped with the round, its name and their start since the run's
// origin. It is the one owner of a round's send for both callers: a
// benchmark run's streamRounds, and a chat session's Send, whose every turn
// is a round of one stream (2026-09-24).
//
// The run-wide stream index of request i is k*st.perRound+i (state doc). The
// state is grown to hold it — a no-op for a benchmark run, which sized it up
// front, and how a session's state grows by one stream per turn. rounds is
// the run's round count for EventStreamStarted, 0 when it is open-ended.
//
// origin is the instant server.RunConcurrent's StartedAt is stamped against
// for round 0. On round 0 it is still zero, and first is called between the
// round's announcement and its first request: it starts whatever the caller
// starts with the run (the sampler, the clock), sets *origin, and returns the
// context the streams run under. A later round sends under ctx, and its
// records are shifted by how long after round 0 it began: StartedAt is since
// the run's start, never the round's.
func (r *run) sendRound(ctx context.Context, st *state, k int, reqs []server.StreamRequest, rounds int, name string,
	origin *time.Time, first func() context.Context) ([]tape.RequestRecord, error) {
	n := st.perRound
	base := k * n
	st.grow(base + len(reqs))
	st.beginRound(k)
	for i := range reqs {
		st.beginStream(base + i)
		st.notify(Event{
			Kind:      EventStreamStarted,
			Stream:    i,
			Round:     k,
			Rounds:    rounds,
			RoundName: name,
			SpecNMax:  specNMaxOf(reqs[i].Params),
			Streams:   n,
			MaxTokens: reqs[i].SentMaxTokens(),
		})
	}
	r.observe(sinceOrigin(*origin), k+1, witnessStart)
	var offset time.Duration
	if origin.IsZero() {
		ctx = first()
	} else {
		offset = time.Since(*origin)
	}
	got, err := server.RunConcurrent(ctx, r.client, reqs, func(i int) server.StreamHooks {
		// The engine-fingerprint latch rides every run stream, as it does in
		// a single-round run (stream, TTP-169): a run that calibrated
		// nothing learns what served it from its own streams.
		h := st.hooks(base + i)
		h.OnFingerprint = r.noteFingerprint
		return h
	})
	r.observe(time.Since(*origin), k+1, witnessEnd)
	for i := range got {
		got[i].Round = k
		got[i].StartedAt += offset
		got[i].Prompt.Name = name
	}
	return got, err
}

// beginRound marks where round k's tokens start in the fault timeline.
func (st *state) beginRound(k int) {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.roundStart = append(st.roundStart, len(st.timeline))
	st.round = k
}

// roundFaults replaces the major-fault split of mem with one taken round by
// round.
//
// firstTokenIndex splits the run's timeline once, at the last stream's first
// token. Over sequential rounds that is the last round's first token, and every
// fault the earlier rounds took while decoding would be counted as prompt
// faults — a cold run would read warm. Each round is its own prefill followed
// by its own decode, so the split is made inside each round, at that round's
// last first token, and the two halves are summed.
func (st *state) roundFaults(mem *tape.MemorySummary, timeline []uint64) {
	st.mu.Lock()
	defer st.mu.Unlock()
	// No timeline means no token arrived at all: keep procmon.Summarize's
	// sample-based totals rather than overwriting them with zeros.
	if st.perRound <= 0 || len(timeline) == 0 {
		return
	}
	var prompt, decode uint64
	decoded := 0
	for k, start := range st.roundStart {
		end := len(timeline)
		if k+1 < len(st.roundStart) {
			end = st.roundStart[k+1]
		}
		if end > len(timeline) {
			end = len(timeline)
		}
		if start >= end {
			continue
		}
		cut := start - 1
		for g := k * st.perRound; g < (k+1)*st.perRound && g < len(st.firstIdx); g++ {
			if st.firstIdx[g] > cut {
				cut = st.firstIdx[g]
			}
		}
		m := procmon.Summarize(nil, timeline[start:end], cut-start)
		prompt += m.MajFaultsPrompt
		decode += m.MajFaultsDecode
		if d := (end - start) - (cut - start + 1); d > 0 {
			decoded += d
		}
	}
	mem.MajFaultsPrompt = prompt
	mem.MajFaultsDecode = decode
	mem.MajFaultsTotal = prompt + decode
	mem.MajFaultsPerToken = 0
	if decoded > 0 {
		mem.MajFaultsPerToken = float64(decode) / float64(decoded)
	}
}

// reduceRounds is the aggregate, the per-round summaries and the spread of a
// multi-round run. It is pure: recs must already carry their reduced Timings
// and their Round, names has one entry per round sent, and streams is N.
//
// The aggregate is server.Aggregate over every record — totals, failures, the
// per-stream mean and the TTFT percentiles need no correction — with its three
// windows replaced by the sums of the rounds' own windows, and its peak
// decoding streams by the lowest per-round peak. server.Aggregate stretches a
// window from the first round's start to the last round's last token, so the
// pauses between rounds, and every later round's prefill, would land in WallMs
// and in both rate denominators (TestReduceRoundsLeavesTheGapsOut); its peak
// counts two rounds' decoding as one instant, which rounds never share
// (TestReduceRoundsPeakIsTheLowestRoundPeak).
// A round's window is recovered from its own rate as tokens / rate, which is
// exactly how server.Aggregate formed that rate. Streams is N, the streams sent
// at once, because the card prints "N × per-stream = aggregate".
//
// PerRound and Spread follow the schema: both are empty for a single round.
func reduceRounds(recs []tape.RequestRecord, names []string, streams int) (tape.AggregateTimings, []tape.RoundSummary, *tape.RoundSpread) {
	agg := server.Aggregate(recs)
	agg.Streams = streams

	byRound := make([][]tape.RequestRecord, len(names))
	for _, rec := range recs {
		if rec.Round >= 0 && rec.Round < len(byRound) {
			byRound[rec.Round] = append(byRound[rec.Round], rec)
		}
	}

	var (
		wallMs                   float64
		decodeSec, promptSec     float64
		decodeTokens, promptToks int
		peakStreams              int
		concurrentMs             float64
		concurrentN              int
	)
	per := make([]tape.RoundSummary, len(names))
	for k, rr := range byRound {
		a := server.Aggregate(rr)
		fixClientAggregate(rr, &a)
		wallMs += a.WallMs
		// The run's peak is the lowest per-round peak above zero: one serial
		// round already makes the aggregate a queue, and 0 when no round had
		// a window is the schema's unknown.
		if p := a.PeakDecodingStreams; p > 0 && (peakStreams == 0 || p < peakStreams) {
			peakStreams = p
		}
		if a.AggregatePredictedPerSecond > 0 {
			decodeSec += float64(a.TotalPredictedN) / a.AggregatePredictedPerSecond
			decodeTokens += a.TotalPredictedN
		}
		if a.AggregatePromptPerSecond > 0 {
			promptSec += float64(a.TotalPromptN) / a.AggregatePromptPerSecond
			promptToks += a.TotalPromptN
		}
		// The concurrent window is summed the same way the decode seconds
		// are: per round from server.Aggregate — the one owner of that
		// arithmetic (TTP-138) — never re-derived here. Across serial rounds
		// the whole-record window inverts (the latest first token of the last
		// round is after the earliest last token of the first), so the
		// naive figure the loop's agg started from is replaced wholesale.
		concurrentMs += a.ConcurrentWindowMs
		concurrentN += a.ConcurrentPredictedN
		per[k] = tape.RoundSummary{
			Index:                       k,
			Name:                        names[k],
			Streams:                     len(rr),
			PerStreamPredictedPerSecond: a.PerStreamPredictedPerSecond,
			AggregatePredictedPerSecond: a.AggregatePredictedPerSecond,
			PredictedN:                  a.TotalPredictedN,
			TTFTp50Ms:                   a.TTFTp50Ms,
			// The n_max a sweep sent this round with (TTP-35); 0 otherwise.
			SpecNMax: roundSpecNMax(rr),
		}
		per[k].PromptN, per[k].PromptPerSecond = roundPrefill(rr)
		// The prompt tokens the server took from its prefix cache instead of
		// evaluating (TTP-66). Summed over every stream, including one with no
		// prompt timing: cache_n is a count the server reported, and a stream
		// whose prefix was cached is exactly one whose evaluation may be tiny.
		for _, r := range rr {
			per[k].CacheN += r.Timings.CacheN
		}
		// representativeTimings returns a lone record's own Timings, whose
		// draft pointers are that record's; the round gets new ints so writing
		// one can never rewrite the other.
		t := representativeTimings(rr)
		if t.DraftN != nil {
			d := *t.DraftN
			per[k].DraftN = &d
			acc := 0
			if t.DraftNAccepted != nil {
				acc = *t.DraftNAccepted
			}
			per[k].DraftNAccepted = &acc
		}
	}

	agg.WallMs = wallMs
	agg.PeakDecodingStreams = peakStreams
	agg.ConcurrentWindowMs = concurrentMs
	agg.ConcurrentPredictedN = concurrentN
	agg.ConcurrentPredictedPerSecond = 0
	if concurrentMs > 0 && concurrentN > 0 {
		agg.ConcurrentPredictedPerSecond = float64(concurrentN) / (concurrentMs / 1000)
	}
	agg.AggregatePredictedPerSecond, agg.AggregatePromptPerSecond = 0, 0
	if decodeSec > 0 {
		agg.AggregatePredictedPerSecond = float64(decodeTokens) / decodeSec
	}
	if promptSec > 0 {
		agg.AggregatePromptPerSecond = float64(promptToks) / promptSec
	}
	// The recombined rate sums per-round totals, which may mix chunk counts
	// in — so the correction is re-applied over every record (idempotent),
	// not only over the rounds' own already-corrected rates.
	fixClientAggregate(recs, &agg)

	if len(names) < 2 {
		return agg, nil, nil
	}
	return agg, per, roundSpread(per)
}

// roundSpread is the median and range of the rounds' per-stream rates and
// acceptance rates.
//
// A round with no rate — every stream failed — is left out rather than counted
// as 0 tok/s: zero is this schema's "unobserved", and a failed round is not a
// slow one. A round that reported no draft, or drafted nothing, has no
// acceptance rate for the same reason; when no round has one the spread stays
// all-zero, which the schema defines as unobserved.
func roundSpread(per []tape.RoundSummary) *tape.RoundSpread {
	var rates, accepts []float64
	for _, p := range per {
		if p.PerStreamPredictedPerSecond > 0 {
			rates = append(rates, p.PerStreamPredictedPerSecond)
		}
		if p.DraftN != nil && *p.DraftN > 0 {
			acc := 0
			if p.DraftNAccepted != nil {
				acc = *p.DraftNAccepted
			}
			accepts = append(accepts, float64(acc)/float64(*p.DraftN))
		}
	}
	return &tape.RoundSpread{
		PerStreamPredictedPerSecond: spreadOf(rates),
		DraftAcceptRate:             spreadOf(accepts),
	}
}

// spreadOf is the median, minimum and maximum of vals; the zero Spread for no
// values. The median of an even count is the mean of the two middle values.
func spreadOf(vals []float64) tape.Spread {
	if len(vals) == 0 {
		return tape.Spread{}
	}
	s := append([]float64(nil), vals...)
	sort.Float64s(s)
	n := len(s)
	med := s[n/2]
	if n%2 == 0 {
		med = (s[n/2-1] + s[n/2]) / 2
	}
	return tape.Spread{Median: med, Min: s[0], Max: s[n-1]}
}
