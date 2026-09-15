package recorder

import (
	"encoding/json"
	"time"

	"github.com/midagedev/toktape/internal/gpu"
	"github.com/midagedev/toktape/internal/procmon"
	"github.com/midagedev/toktape/internal/server"
	"github.com/midagedev/toktape/internal/tape"
)

// reduce turns the raw records, the fault timeline and the periodic samples
// into the finished tape. It is the last step of a run and does no I/O apart
// from the one /proc read that sizes the model's mapping.
func (r *run) reduce(recs []tape.RequestRecord, st *state, startedAt, finishedAt time.Time) *tape.Tape {
	timeline, samples, _ := st.snapshot()

	for i := range recs {
		sentAt := startedAt.Add(recs[i].StartedAt)
		recs[i].Timings = server.Reduce(&recs[i], sentAt, r.model.ActiveBytesPerToken)
		recs[i].Cache = server.CacheVerdict(recs[i].Timings, st.decodeFaults(i), recs[i].Timings.PredictedN)
	}

	var (
		agg         tape.AggregateTimings
		perRound    []tape.RoundSummary
		spread      *tape.RoundSpread
		rounds      int
		concurrency = len(recs)
	)
	if st.perRound > 0 {
		// A multi-round run (TTP-31): the aggregate is over every round but
		// its windows are the rounds' own, and Concurrency is the streams sent
		// at once, never streams × rounds.
		agg, perRound, spread = reduceRounds(recs, r.roundNames, st.perRound)
		rounds = len(r.roundNames)
		concurrency = st.perRound
	} else {
		agg = server.Aggregate(recs)
	}
	agg.SlotsBusyMax = slotsBusyMax(samples)

	mem := procmon.Summarize(samples, timeline, st.firstTokenIndex())
	if st.perRound > 0 {
		st.roundFaults(&mem, timeline)
	}
	if r.pid > 0 && r.model.Path != "" {
		if b, err := procmon.MappedFileBytes(r.opts.FSRoot, r.pid, r.model.Path); err == nil {
			mem.MappedFileBytes = b
		}
	}

	timings := representativeTimings(recs)
	cache := server.CacheVerdict(timings, mem.MajFaultsDecode, agg.TotalPredictedN)

	gpusAtEnd := lastGPUs(samples)
	contention := r.contention(samples)

	summary := tape.RunSummary{
		ID:             runID(startedAt, r.model.FileName),
		ToktapeVersion: r.opts.Version,
		StartedAt:      startedAt,
		FinishedAt:     finishedAt,
		Server: tape.ServerInfo{
			Kind:    r.kind,
			URL:     r.client.BaseURL(),
			PID:     r.pid,
			Host:    r.host.Hostname,
			Args:    r.args,
			Flags:   r.flags,
			NSlots:  r.props.TotalSlots,
			CtxSize: r.props.CtxSize(),
		},
		Model:       r.model,
		Host:        r.host,
		Placement:   r.place,
		Memory:      mem,
		Concurrency: concurrency,
		Timings:     timings,
		Aggregate:   agg,
		Rounds:      rounds,
		PerRound:    perRound,
		Spread:      spread,
		Cache:       cache,
		Contention:  contention,
		// What was allowed to end this generation and what did (TTP-76). CutAt
		// is 0 unless the clock actually ended something, so a reader tells a
		// cut run from a completed one by that field alone, and CutAt > For
		// says the floor held the cut back past the budget.
		Limit:     limitOf(r.limit, r.cutAt),
		Template:  r.template,
		Sampling:  samplingOf(recs),
		GPUsAtEnd: gpusAtEnd,
		Warnings:  r.warnings,
	}
	summary.Server.Build, summary.Server.Commit = r.build, r.commit
	if st.perRound > 0 {
		// A --spec-n-max sweep groups its rounds by the value each was sent
		// with (TTP-35). It is filled here, not after Record returns, so the
		// summary EventDone carries is the one the tape holds.
		applySweep(&summary, recs)
	}

	return &tape.Tape{
		Schema:   tape.SchemaVersion,
		Summary:  summary,
		Requests: recs,
		Samples:  samples,
	}
}

// runID is <yyyymmdd>-<hhmmss>-<model slug>.
func runID(t time.Time, fileName string) string {
	return t.Format("20060102-150405") + "-" + tape.SlugFromModel(fileName)
}

// representativeTimings is tape.RunSummary.Timings: the single stream when
// there is one, otherwise the mean of the streams that produced tokens.
//
// The mean is over successful streams only. A stream that failed has no rate
// to average, and letting its zeros in would halve the reported figure of a
// run whose other half worked — the card would then under-report a server
// that was fine.
func representativeTimings(recs []tape.RequestRecord) tape.TimingsSummary {
	if len(recs) == 0 {
		return tape.TimingsSummary{}
	}
	if len(recs) == 1 {
		return recs[0].Timings
	}
	var (
		out tape.TimingsSummary
		n   float64
	)
	// The draft figures are pooled rather than averaged (tape.TimingsSummary,
	// TTP-30): accepted over drafted is a ratio, and the run's acceptance rate
	// is the sum of the numerators over the sum of the denominators. Averaging
	// the per-stream rates would give a stream that drafted twelve tokens the
	// same weight as one that drafted three hundred.
	var draftN, draftAccepted int
	draftReported := false
	for i := range recs {
		t := recs[i].Timings
		if recs[i].Error != "" || t.PredictedN <= 0 {
			continue
		}
		n++
		out.PromptN += t.PromptN
		out.CacheN += t.CacheN
		out.PredictedN += t.PredictedN
		// A thinking model's reasoning tokens are part of PredictedN, so they
		// are averaged with it; without this the card's Context row drops the
		// thinking clause for every concurrent run (TTP-20, 2026-09-13).
		out.ReasoningN += t.ReasoningN
		out.PromptMs += t.PromptMs
		out.PredictedMs += t.PredictedMs
		out.PromptPerSecond += t.PromptPerSecond
		out.PredictedPerSecond += t.PredictedPerSecond
		out.TTFTMs += t.TTFTMs
		out.ClientPromptPerSecond += t.ClientPromptPerSecond
		out.ClientPredictedPerSecond += t.ClientPredictedPerSecond
		out.ITLp50Ms += t.ITLp50Ms
		out.ITLp95Ms += t.ITLp95Ms
		out.ITLp99Ms += t.ITLp99Ms
		out.EffectiveBandwidthBytesPerSec += t.EffectiveBandwidthBytesPerSec
		if t.DraftN != nil {
			draftReported = true
			draftN += *t.DraftN
			if t.DraftNAccepted != nil {
				draftAccepted += *t.DraftNAccepted
			}
		}
	}
	if n == 0 {
		return tape.TimingsSummary{}
	}
	// New ints, never the records' own: the run-level pair must not alias a
	// stream's, or writing the sum would rewrite the request it came from.
	if draftReported {
		out.DraftN, out.DraftNAccepted = &draftN, &draftAccepted
	}
	out.PromptN = int(float64(out.PromptN)/n + 0.5)
	out.CacheN = int(float64(out.CacheN)/n + 0.5)
	out.PredictedN = int(float64(out.PredictedN)/n + 0.5)
	out.ReasoningN = int(float64(out.ReasoningN)/n + 0.5)
	out.PromptMs /= n
	out.PredictedMs /= n
	out.PromptPerSecond /= n
	out.PredictedPerSecond /= n
	out.TTFTMs /= n
	out.ClientPromptPerSecond /= n
	out.ClientPredictedPerSecond /= n
	out.ITLp50Ms /= n
	out.ITLp95Ms /= n
	out.ITLp99Ms /= n
	out.EffectiveBandwidthBytesPerSec = int64(float64(out.EffectiveBandwidthBytesPerSec)/n + 0.5)
	// The agreement flag describes the figures it is printed beside, and above
	// one stream those are the means, not the streams' own verdicts ANDed
	// together. The AND was wrong because a mean of disagreeing and agreeing
	// streams can itself agree: the two-stream take in
	// tape.AggregateTimings.DisagreeingStreams' doc read 11.79 against 11.49
	// on one stream (2.6 %) and 11.57 against 11.58 on the other, so the means
	// agreed to 1.3 % while the flag said they did not — a caveat that
	// contradicted the numbers it printed. The per-stream fact is now said as
	// the count that field carries. RatesAgree keeps the absent-rate case
	// false, as the AND did: a rate that was never measured is not an
	// agreement.
	out.ClientAgreesWithServer = server.RatesAgree(out.ClientPredictedPerSecond, out.PredictedPerSecond)
	out.DecodeLabel = "sample"
	if out.PredictedN >= tape.MinDecodeTokens {
		out.DecodeLabel = "decode"
	}
	return out
}

// slotsBusyMax is the highest busy-slot count any sample saw.
func slotsBusyMax(samples []tape.RunSample) int {
	max := 0
	for _, s := range samples {
		if s.SlotsBusy > max {
			max = s.SlotsBusy
		}
	}
	return max
}

// lastGPUs is the final device reading of the run.
func lastGPUs(samples []tape.RunSample) []tape.GPUSample {
	for i := len(samples) - 1; i >= 0; i-- {
		if len(samples[i].GPUs) > 0 {
			return samples[i].GPUs
		}
	}
	return nil
}

// contention labels the run busy or not, or declines to label it.
//
// Declining matters. With no /proc to read the load average from and no GPU
// backend to count foreign processes with, the inputs to the verdict are both
// missing — and gpu.Contention would then return its zero value, which prints
// as "contended: no". That is a claim about a machine nobody measured, and it
// is exactly the kind of invented default the card exists to avoid. In that
// case the summary keeps a zero ContentionInfo, which both cards render as
// "?", and the run says so in a warning.
//
// otherGPUProcs is the highest count any single device reported, not the sum
// over devices: one foreign process that holds memory on four GPUs is one
// process, and summing would report it as four and make a quiet host look
// four times as contended as it is.
//
// Witnesses (TTP-36) are readings too: their load averages count toward the
// peak, a run that has any is judged even with no load and no GPU backend,
// and gpu.Witnessed adds their IO-pressure and llama-process reasons.
func (r *run) contention(samples []tape.RunSample) tape.ContentionInfo {
	var load float64
	other := 0
	for _, s := range samples {
		if s.LoadAvg1 > load {
			load = s.LoadAvg1
		}
		for _, g := range s.GPUs {
			if g.OtherProcs > other {
				other = g.OtherProcs
			}
		}
	}
	for _, w := range r.witnesses {
		if w.LoadAvg1 > load {
			load = w.LoadAvg1
		}
	}
	// The GPU side counts as observed whenever a real backend is open, even
	// when it reported no foreign process: that zero is a reading. Only the
	// Null collector means nothing was looked at.
	_, noGPUBackend := r.gpus.(gpu.Null)
	if load == 0 && (r.gpus == nil || noGPUBackend) && len(r.witnesses) == 0 {
		r.warn("host load unknown, contention not judged")
		return tape.ContentionInfo{}
	}
	info := gpu.Contention(load, r.host.CPUThreads, other, serverThreads(r.flags))
	return gpu.Witnessed(info, r.witnesses)
}

// samplingOf lifts the request shape onto the summary (TTP-55, 2026-09-14).
//
// One run sends one shape — the flags apply to every request it builds — so
// the first record speaks for all of them, and the renderers, which read a
// RunSummary and nothing else, get the three figures that decide what the
// rates mean. Only what was actually sent: a request that carried no
// temperature leaves it nil, which is "the server's own default", never a
// number nobody chose.
func samplingOf(recs []tape.RequestRecord) tape.SamplingSummary {
	if len(recs) == 0 {
		return tape.SamplingSummary{}
	}
	p := recs[0].Prompt
	out := tape.SamplingSummary{Thinking: p.Thinking, Endpoint: p.Endpoint}
	if v, ok := p.Params["temperature"]; ok {
		if f, ok := floatOf(v); ok {
			out.Temperature = &f
		}
	}
	return out
}

// floatOf reads a number out of a params value. The params are JSON-shaped —
// --param parses its value as JSON when it can — so a temperature arrives as a
// float64 from a decoded tape and as whatever the flag produced in the process
// that recorded it.
func floatOf(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	}
	return 0, false
}
