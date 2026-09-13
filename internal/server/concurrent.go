package server

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/midagedev/toktape/internal/tape"
)

// RunConcurrent sends every request at once and records each stream
// separately. A run is N concurrent requests; N == 1 is the special case, not
// the design (docs/toktape-spec.ko.md §1 decision 9).
//
// Each returned record carries its Index and its StartedAt relative to the
// moment this call began, which is the common clock Aggregate needs: a token's
// absolute time in the run is StartedAt + TokenEvent.T.
//
// A stream that fails lands in its own record's Error and does not abort the
// others; the returned error is non-nil only when every stream failed, or when
// there was nothing to send. Records are returned in request order either way.
//
// hooks may be nil; otherwise it is called once per stream index before the
// stream starts. The hooks it returns run concurrently with the hooks of the
// other streams, so shared state must be synchronised.
func RunConcurrent(ctx context.Context, c *Client, reqs []StreamRequest, hooks func(i int) StreamHooks) ([]tape.RequestRecord, error) {
	if len(reqs) == 0 {
		return nil, errors.New("server: run: no requests")
	}
	if c == nil {
		return nil, errors.New("server: run: nil client")
	}
	recs := make([]tape.RequestRecord, len(reqs))
	errs := make([]error, len(reqs))
	runStart := time.Now()

	var wg sync.WaitGroup
	for i := range reqs {
		// hooks is called here rather than inside the goroutine: a caller that
		// builds per-stream state in it would otherwise be doing so from N
		// goroutines at once.
		var h StreamHooks
		if hooks != nil {
			h = hooks(i)
		}
		wg.Add(1)
		go func(i int, h StreamHooks) {
			defer wg.Done()
			startedAt := time.Since(runStart)
			rec, _, err := c.Stream(ctx, reqs[i], h)
			if rec == nil {
				rec = &tape.RequestRecord{Slot: -1}
				rec.Prompt.Messages = reqs[i].Messages
				rec.Prompt.Params = reqs[i].Params
				rec.Prompt.MaxTokens = reqs[i].SentMaxTokens()
			}
			rec.Index = i
			rec.StartedAt = startedAt
			if err != nil {
				errs[i] = err
				if rec.Error == "" {
					rec.Error = err.Error()
				}
			}
			recs[i] = *rec
		}(i, h)
	}
	wg.Wait()

	failed := 0
	var first error
	for _, err := range errs {
		if err != nil {
			failed++
			if first == nil {
				first = err
			}
		}
	}
	if failed == len(reqs) {
		return recs, fmt.Errorf("server: run: all %d streams failed: %w", failed, first)
	}
	return recs, nil
}

// Aggregate reduces the streams of one run into the server-wide view. It is
// pure.
//
// The aggregate decode rate is every generated token over the window in which
// the server was generating: from the first content token of any stream to the
// last content token of any stream. Streams that failed or produced no token
// contribute to Streams and StreamsFailed but not to any rate.
//
// Note the deliberate difference from the per-stream rate in Reduce, which
// divides n-1 tokens by the window between the first and the last of them: the
// aggregate divides all n. Across overlapping streams the window is not one
// stream's token spacing, and the first token of every stream but the earliest
// falls inside the window rather than starting it.
func Aggregate(recs []tape.RequestRecord) tape.AggregateTimings {
	out := tape.AggregateTimings{Streams: len(recs)}
	if len(recs) == 0 {
		return out
	}

	minStart := recs[0].StartedAt
	var ok []*tape.RequestRecord
	for i := range recs {
		r := &recs[i]
		if r.StartedAt < minStart {
			minStart = r.StartedAt
		}
		if r.Error != "" {
			out.StreamsFailed++
		}
		if r.Error == "" && len(r.Tokens) > 0 {
			ok = append(ok, r)
		}
	}
	if len(ok) == 0 {
		return out
	}

	var (
		firstContent = ok[0].StartedAt + ok[0].Tokens[0].T
		lastFirst    = firstContent
		lastToken    = ok[0].StartedAt + ok[0].Tokens[len(ok[0].Tokens)-1].T
		rates        []float64
		ttfts        []float64
	)
	for _, r := range ok {
		first := r.StartedAt + r.Tokens[0].T
		last := r.StartedAt + r.Tokens[len(r.Tokens)-1].T
		if first < firstContent {
			firstContent = first
		}
		if first > lastFirst {
			lastFirst = first
		}
		if last > lastToken {
			lastToken = last
		}
		n := r.Timings.PredictedN
		if n == 0 {
			n = len(r.Tokens) // build without timings: what we saw is all we have
		}
		out.TotalPredictedN += n
		out.TotalPromptN += r.Timings.PromptN
		rate := r.Timings.PredictedPerSecond
		if rate == 0 {
			rate = r.Timings.ClientPredictedPerSecond
		}
		if rate > 0 {
			rates = append(rates, rate)
		}
		if r.Timings.TTFTMs > 0 {
			ttfts = append(ttfts, r.Timings.TTFTMs)
		}
	}

	out.WallMs = msOf(lastToken - minStart)
	if w := lastToken - firstContent; w > 0 {
		out.AggregatePredictedPerSecond = float64(out.TotalPredictedN) / w.Seconds()
	}
	// Prefill of the run ends when the last stream produced its first token.
	if w := lastFirst - minStart; w > 0 && out.TotalPromptN > 0 {
		out.AggregatePromptPerSecond = float64(out.TotalPromptN) / w.Seconds()
	}
	if len(rates) > 0 {
		sum := 0.0
		for _, r := range rates {
			sum += r
		}
		out.PerStreamPredictedPerSecond = sum / float64(len(rates))
	}
	if len(ttfts) > 0 {
		sort.Float64s(ttfts)
		out.TTFTp50Ms = percentile(ttfts, 0.50)
		out.TTFTp95Ms = percentile(ttfts, 0.95)
	}
	// SlotsBusyMax comes from /slots polling in tape.RunSample and Scaling
	// needs a single-stream baseline from the same run; neither is derivable
	// from the records alone, so both stay 0 rather than being guessed.
	return out
}
