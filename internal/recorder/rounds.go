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

// recordRounds is Record from the first request on, for a multi-round run.
func (r *run) recordRounds(ctx context.Context) (*tape.Tape, error) {
	rounds := roundRequests(r.opts, r.model.ActiveBytesPerToken)

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
		// cancelledAfter is the round count sent before a cancellation, 0 when
		// the run was not cancelled. The warning is emitted after the sampler
		// stops: r.warn calls Progress, and Options.Progress promises calls
		// serialised with the sampler's own events (lead, 2026-09-13).
		cancelledAfter int
	)
	r.roundNames = nil
	for k, reqs := range rounds {
		if k > 0 && ctx.Err() != nil {
			cancelledAfter = k
			break
		}
		st.beginRound(k)
		for i := range reqs {
			st.notify(Event{
				Kind:      EventStreamStarted,
				Stream:    i,
				Round:     k,
				Streams:   n,
				MaxTokens: reqs[i].SentMaxTokens(),
			})
		}
		// server.RunConcurrent stamps StartedAt against its own call, so a
		// later round's records are shifted by how long after round 0 it
		// began: StartedAt is since the run start, never the round start.
		var offset time.Duration
		if k == 0 {
			stopSampling = r.startSampling(ctx, st)
			origin = time.Now()
		} else {
			offset = time.Since(origin)
		}
		base := k * n
		got, err := server.RunConcurrent(ctx, r.client, reqs, func(i int) server.StreamHooks {
			return st.hooks(base + i)
		})
		name := r.opts.Rounds[k].Name
		for i := range got {
			got[i].Round = k
			got[i].StartedAt += offset
			got[i].Prompt.Name = name
		}
		recs = append(recs, got...)
		r.roundNames = append(r.roundNames, name)
		if err != nil {
			failed++
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	stopSampling()
	if cancelledAfter > 0 {
		r.warn("run cancelled after round %d of %d", cancelledAfter, len(rounds))
	}

	if failed == len(r.roundNames) {
		return nil, nil, fmt.Errorf("%w: every round failed: %v", ErrAllStreamsFailed, firstErr)
	}
	st.applyDeltas(recs)
	return recs, st, nil
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
// windows replaced by the sums of the rounds' own windows. server.Aggregate
// stretches a window from the first round's start to the last round's last
// token, so the pauses between rounds, and every later round's prefill, would
// land in WallMs and in both rate denominators (TestReduceRoundsLeavesTheGapsOut).
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
	)
	per := make([]tape.RoundSummary, len(names))
	for k, rr := range byRound {
		a := server.Aggregate(rr)
		wallMs += a.WallMs
		if a.AggregatePredictedPerSecond > 0 {
			decodeSec += float64(a.TotalPredictedN) / a.AggregatePredictedPerSecond
			decodeTokens += a.TotalPredictedN
		}
		if a.AggregatePromptPerSecond > 0 {
			promptSec += float64(a.TotalPromptN) / a.AggregatePromptPerSecond
			promptToks += a.TotalPromptN
		}
		per[k] = tape.RoundSummary{
			Index:                       k,
			Name:                        names[k],
			Streams:                     len(rr),
			PerStreamPredictedPerSecond: a.PerStreamPredictedPerSecond,
			AggregatePredictedPerSecond: a.AggregatePredictedPerSecond,
			PredictedN:                  a.TotalPredictedN,
			TTFTp50Ms:                   a.TTFTp50Ms,
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
	agg.AggregatePredictedPerSecond, agg.AggregatePromptPerSecond = 0, 0
	if decodeSec > 0 {
		agg.AggregatePredictedPerSecond = float64(decodeTokens) / decodeSec
	}
	if promptSec > 0 {
		agg.AggregatePromptPerSecond = float64(promptToks) / promptSec
	}

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
