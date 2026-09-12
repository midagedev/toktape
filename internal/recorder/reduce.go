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
	contention := gpuContention(samples, r.host, r.flags)

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
	for i := range recs {
		t := recs[i].Timings
		if recs[i].Error != "" || t.PredictedN <= 0 {
			continue
		}
		n++
		out.PromptN += t.PromptN
		out.CacheN += t.CacheN
		out.PredictedN += t.PredictedN
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
		if t.DraftN != nil && out.DraftN == nil {
			out.DraftN, out.DraftNAccepted = t.DraftN, t.DraftNAccepted
		}
	}
	if n == 0 {
		return tape.TimingsSummary{}
	}
	out.PromptN = int(float64(out.PromptN)/n + 0.5)
	out.CacheN = int(float64(out.CacheN)/n + 0.5)
	out.PredictedN = int(float64(out.PredictedN)/n + 0.5)
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

// gpuContention labels the run busy or not.
//
// otherGPUProcs is the highest count any single device reported, not the sum
// over devices: one foreign process that holds memory on four GPUs is one
// process, and summing would report it as four and make a quiet host look
// four times as contended as it is.
func gpuContention(samples []tape.RunSample, host tape.HostInfo, flags tape.ServerFlags) tape.ContentionInfo {
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
	return gpu.Contention(load, host.CPUThreads, other, serverThreads(flags))
}
