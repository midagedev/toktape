package recorder

import (
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

	agg := server.Aggregate(recs)
	agg.SlotsBusyMax = slotsBusyMax(samples)

	mem := procmon.Summarize(samples, timeline, st.firstTokenIndex())
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
		Concurrency: len(recs),
		Timings:     timings,
		Aggregate:   agg,
		Cache:       cache,
		Contention:  contention,
		Template:    r.template,
		GPUsAtEnd:   gpusAtEnd,
		Warnings:    r.warnings,
	}
	summary.Server.Build, summary.Server.Commit = server.BuildFromProps(r.props.BuildInfo)

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
	agrees := true
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
		if !t.ClientAgreesWithServer {
			agrees = false
		}
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
	out.ClientAgreesWithServer = agrees
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
	// The GPU side counts as observed whenever a real backend is open, even
	// when it reported no foreign process: that zero is a reading. Only the
	// Null collector means nothing was looked at.
	_, noGPUBackend := r.gpus.(gpu.Null)
	if load == 0 && (r.gpus == nil || noGPUBackend) {
		r.warn("host load unknown, contention not judged")
		return tape.ContentionInfo{}
	}
	return gpu.Contention(load, r.host.CPUThreads, other, serverThreads(r.flags))
}
