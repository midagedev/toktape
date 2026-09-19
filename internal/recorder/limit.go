package recorder

import (
	"context"
	"sync"
	"time"

	"github.com/midagedev/toktape/internal/tape"
)

// What may end a run's generation, decided in one place (TTP-76, 2026-09-14).
//
// A run stops for one of three reasons — the model stopped (EOS), the token
// cap was reached, or the clock ran out — and which of them is allowed depends
// on what the user named. That decision is a five-row table, and a table
// resolved in three places is three places to get a row wrong, so it is
// resolved exactly once, in Options.limit, and carried from there as data: the
// recorder reads tape.LimitSummary and the tape records it verbatim.
//
// The clock exists because a token count is the wrong unit for "how long".
// The same 256 tokens is 1.8 s on one box and two minutes on another, and the
// thing a first run is aimed at — a clip somebody will watch, a card that
// settles an argument — is measured in seconds. See DefaultFor.

const (
	// DefaultFor is the wall-clock budget a run gets when nobody named one.
	//
	// The arithmetic that chose it is also the argument that a token count
	// cannot be the default on this axis. 256 tokens is 1.8 s at 140 tok/s (a
	// 7B on a 3090), 19 s at 13.5 tok/s (this repo's own hero rig) and 128 s
	// at 2 tok/s (a large model with no GPU): one number is a blink at one end
	// of that range and a wait at the other. Twenty seconds of wall clock over
	// the same three machines gives roughly 2800, 270 and 40 tokens — every
	// one of them above tape.MinDecodeTokens, so every one of them is a decode
	// rate and not a sample. It is also a clip a reader sits through: a 20 s
	// run is a 25 s clip, 31 s with --open.
	DefaultFor = 20 * time.Second
	// DefaultMaxTokens is the per-stream cap a request carries when the user
	// named none. It is a runaway guard, not a target: what ends a default run
	// is the clock, and this is what is left if the clock ever fails to.
	//
	// Sending nothing is not the alternative. llama-server with no n_predict
	// generates until the context is full, so a run whose clock failed would
	// then have nothing behind it at all — which is why
	// tape.LimitSummary.MaxTokens is documented as always sent.
	//
	// It is sized so that the sentence above can be true (TTP-145, 2026-09-20).
	// At 2048 it was not: the guard fires before the clock on any box past
	// 2048/20 = 102 tok/s per stream, so on most machines the cap was the
	// terminator and every sentence this tool writes about the clock being
	// the default's budget was false for that reader. The arithmetic was
	// already written down two constants above — DefaultFor's own worked
	// example computes 2800 tokens for twenty seconds on a 7B at 140 tok/s —
	// and 2800 > 2048 went unnoticed.
	//
	// guardRate is the per-stream decode rate above which the guard would
	// start terminating default runs again, and it is deliberately far past
	// anything DefaultFor's example cites: a small model on current hardware
	// can pass 140 by a wide margin, and a guard that has to be re-argued
	// every time somebody buys a GPU is not a guard.
	//
	// The slow-box cost is real and is accepted: if the clock fails on a 2
	// tok/s box this is now 66 minutes rather than 17. That is a choice
	// between two failures of a mechanism that is not supposed to fire at
	// all, and the one that happens every day is worse than the one that
	// needs the clock to break first.
	DefaultMaxTokens = int(DefaultFor/time.Second) * guardRate
	// guardRate is DefaultMaxTokens' divisor, named so the relationship
	// cannot drift: the guard is the clock's length times a decode rate no
	// stream is expected to reach.
	guardRate = 400
	// NoClock is the Options.For value that means "this run has no wall-clock
	// budget"; it is what `--for 0` sets. Zero cannot mean it, because zero is
	// the zero value and the zero value has to be the sane default. Same shape
	// as NoWait, for the same reason.
	NoClock = -1 * time.Nanosecond
	// floorPoll is how often the clock re-asks whether the streams it wants to
	// cut have reached the floor. It is short next to a decode step on any
	// machine, so the cut lands within a token of the moment it becomes legal.
	floorPoll = 10 * time.Millisecond
)

// limit resolves the five rows of the table `--help` prints, once per run.
//
//	given                | what ends the generation
//	---------------------|-------------------------------------------------
//	neither              | the clock at DefaultFor, DefaultMaxTokens, or EOS
//	--for D only         | the clock at D, DefaultMaxTokens, or EOS
//	--n-predict N only   | the cap at N, or EOS. No clock.
//	both                 | whichever comes first
//	--for 0              | no clock
//
// The third row is the one with a reason behind it: naming a token count is an
// explicit choice about how long the run should be, and a default that then
// cut the run short would be overruling the user on the axis they just spoke
// on. It is also what keeps a command somebody recorded a card with — this
// repo's own hero is `--n-predict 240` — behaving exactly as it was recorded.
//
// MinTokens is the floor, and it is in force whenever there is a clock at all:
// a budget that lands a stream under tape.MinDecodeTokens produces the "sample"
// first run the budget exists to prevent.
func (o Options) limit() tape.LimitSummary {
	// Named is decided before the defaults are applied: afterwards a cap the
	// user gave and the runaway guard are indistinguishable, and the tape has
	// to be able to tell them apart to reproduce the "both" row.
	out := tape.LimitSummary{For: o.For, MaxTokens: o.MaxTokens, MaxTokensNamed: o.MaxTokens > 0}
	switch {
	case out.For < 0: // NoClock: the user said no clock, and that is a choice
		out.For = 0
	case out.For == 0 && out.MaxTokens <= 0: // neither: the default budget
		out.For = DefaultFor
	}
	if out.MaxTokens <= 0 {
		out.MaxTokens = DefaultMaxTokens
	}
	if out.For > 0 {
		out.MinTokens = tape.MinCutTokens
	}
	return out
}

// limitOf is the run's limit as the tape records it: what was asked for,
// when the clock actually cut, and what the streams' own finish words say
// ended them (TTP-135). They are one struct because a reader needs the whole
// of it to say anything — a CutAt without a For is unreadable, a For without
// a CutAt is a budget nothing spent, and a CappedStreams without an
// EndingsObserved beside it cannot tell an observed none from a tape that
// cannot say.
//
// The endings are counted here, from the records, because the finish word
// has been in every tape since PromptRecord existed and the summary is all a
// renderer reads. Which case a record is in is not decided here (lead,
// 2026-09-19): PromptRecord.EndedOnCap and EndedOnContextExhaustion own that,
// because the transcript labels the same records one at a time and a
// predicate authored in two packages is how the ragged clause ended up with
// two renderers saying opposite things about one run. The two counts are
// disjoint and the streams that finished on their own complete
// EndingsObserved. A failed stream is not an answered one and counts toward
// neither; a cut stream carries no word (the server never said one) and so
// counts toward neither as well.
func limitOf(asked tape.LimitSummary, cutAt time.Duration, recs []tape.RequestRecord) tape.LimitSummary {
	asked.CutAt = cutAt
	for i := range recs {
		r := &recs[i]
		if r.Error != "" || r.Prompt.FinishReason == "" {
			continue
		}
		asked.EndingsObserved++
		switch {
		case r.Prompt.EndedOnContextExhaustion():
			asked.ContextExhaustedStreams++
		case r.Prompt.EndedOnCap():
			asked.CappedStreams++
		}
	}
	return asked
}

// clock is a run's wall-clock budget: the one thing that ends a generation
// early, and the only owner of that decision.
//
// It cancels a context derived from the caller's, so a cut is indistinguishable
// from a cancellation down in internal/server — which is the point: the stream
// loop needs no case for it. Telling the two apart is done here, by the
// snapshot of who was still live at the instant of the cut.
//
// The floor can make the run longer than the budget, and on a slow box it will:
// the cut waits until every live stream has tape.MinCutTokens or has ended on
// its own. It can in principle wait indefinitely, on a server that has accepted
// a request and generates nothing — but that is the package's existing
// contract ("the only deadline is ctx", internal/server Client.Stream), not a
// new hazard, and the caller's own context is still the way out of it.
type clock struct {
	cancel  context.CancelFunc
	stopped chan struct{} // closed by stop
	exited  chan struct{} // closed by the goroutine

	mu    sync.Mutex
	cutAt time.Duration
	live  []bool
}

// startClock derives the context the streams run under and starts the budget
// against origin, which is the instant the first request goes out.
//
// The origin is the send and not the first token deliberately. A clip replays
// the run at 1:1, so the number that makes a clip's length predictable is
// measured from the same place the clip starts; and under concurrency the time
// to the first token is queue wait plus prefill, which can be seconds. That
// those seconds are inside the budget is exactly why the floor exists.
//
// With no budget it returns ctx unchanged and a clock that never fires, so the
// caller has no second code path.
func (r *run) startClock(ctx context.Context, st *state, origin time.Time) (context.Context, *clock) {
	c := &clock{stopped: make(chan struct{}), exited: make(chan struct{})}
	if r.limit.For <= 0 {
		close(c.exited)
		return ctx, c
	}
	cctx, cancel := context.WithCancel(ctx)
	c.cancel = cancel
	min := r.limit.MinTokens
	go func() {
		defer close(c.exited)
		timer := time.NewTimer(r.limit.For)
		defer timer.Stop()
		select {
		case <-c.stopped:
			return
		case <-cctx.Done():
			return
		case <-timer.C:
		}
		// The budget is up; the floor decides when the cut is legal.
		tick := time.NewTicker(floorPoll)
		defer tick.Stop()
		for !st.atFloor(min) {
			select {
			case <-c.stopped:
				return
			case <-cctx.Done():
				return
			case <-tick.C:
			}
		}
		// The snapshot and the cancel are one decision: state takes it under
		// the same mutex OnEnd takes, so a stream that ends from here on was
		// ended by this cancel and a stream that had already ended is not in
		// the snapshot.
		c.mu.Lock()
		c.live, c.cutAt = st.cutSnapshot(), time.Since(origin)
		c.mu.Unlock()
		cancel()
	}()
	return cctx, c
}

// cut reports when the clock fired and which streams were still live then.
// cutAt is 0 until it fires, and never changes afterwards.
func (c *clock) cut() (time.Duration, []bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cutAt, c.live
}

// stop ends the budget. It is called once, after the streams are done, and
// releases the derived context whether the clock fired or not.
func (c *clock) stop() {
	close(c.stopped)
	<-c.exited
	if c.cancel != nil {
		c.cancel()
	}
}

// markCut turns the streams the clock ended into records of a cut, and reports
// how many it marked.
//
// A cut stream is not a failed one. Its tokens arrived, and its timings are
// the server's own up to the last chunk received — internal/server/sse.go
// documents that they ride every chunk under timings_per_token, which both
// request paths set — so the rate over it is a measurement. The only thing
// missing is the final chunk, which is precisely what tape.PromptRecord.Cut
// says. FinishReason is left empty because the server never said a word, and
// the read loop's "context canceled" is cleared: an Error here makes every
// renderer, and tape.AggregateTimings.StreamsFailed, read a cut run as a
// broken one.
//
// live[i] was taken the instant before the cancel, so a stream can have reached
// its own finish chunk in between. One that did carries a FinishReason and is
// left alone: the server's word outranks our snapshot.
func markCut(recs []tape.RequestRecord, live []bool) int {
	n := 0
	for i := range recs {
		if i >= len(live) || !live[i] || recs[i].Prompt.FinishReason != "" {
			continue
		}
		recs[i].Prompt.Cut = true
		recs[i].Error = ""
		n++
	}
	return n
}

// allFailed reports whether no record is usable: every stream carries an error.
// A run with one answered stream out of four is evidence and is not a failure
// (internal/server RunConcurrent), and after markCut a cut stream is answered.
func allFailed(recs []tape.RequestRecord) bool {
	for i := range recs {
		if recs[i].Error == "" {
			return false
		}
	}
	return true
}
