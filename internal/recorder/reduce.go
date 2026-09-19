package recorder

import (
	"encoding/json"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/midagedev/toktape/internal/gpu"
	"github.com/midagedev/toktape/internal/placement"
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
	// server.Aggregate reads the records' own figures; the client-timed
	// corrections below are the recorder's, because they depend on the run's
	// mode rather than on the streams alone.
	fixClientAggregate(recs, &agg)
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
	r.narrowPlacement(gpusAtEnd)
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
		PromptSet:   r.promptSet,
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
		Limit:    limitOf(r.limit, r.cutAt),
		Template: r.template,
		// The prefill pass's own figures, whole as it measured them
		// (TTP-137). nil on a run that did not probe, which is every tape
		// from before the pass existed.
		Probe:     r.prefill,
		Sampling:  samplingOf(recs),
		GPUsAtEnd: gpusAtEnd,
		Warnings:  r.warnings,
	}
	summary.Server.Build, summary.Server.Commit = r.build, r.commit
	// The user's word about the engine, kept only where the server did not
	// name one itself (TTP-99): on a ServerOpenAI run it is the claim every
	// surface prints as one, everywhere else it is ignored.
	if r.kind == tape.ServerOpenAI {
		summary.Server.EngineClaim = r.opts.EngineClaim
	}
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
		// allClient is whether every averaged stream was client-timed, and
		// anyChunks whether any of them counted chunks instead of tokens
		// (TTP-99): one uncounted stream poisons the run-level headline too.
		allClient = true
		anyChunks = false
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
		if t.Source != "client" {
			allClient = false
		}
		if t.PredictedNSource == "chunks" {
			anyChunks = true
		}
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
	// The client-timed provenance travels to run level with the figures
	// (TTP-99): a run of client-timed streams is Source "client", and one
	// uncounted stream poisons the run-level decode headline to "?" — its
	// chunk count must not average into a token rate. Agreement is forced
	// false on a client run: the means are two readings of one clock, and
	// calling that agreement would claim a server confirmed them.
	if n > 0 && allClient {
		out.Source = "client"
		out.ClientAgreesWithServer = false
		if anyChunks {
			out.PredictedNSource = "chunks"
			out.PredictedPerSecond = 0
			out.ClientPredictedPerSecond = 0
			out.DecodeLabel = "sample"
			out.EffectiveBandwidthBytesPerSec = 0
		} else {
			out.PredictedNSource = "usage"
		}
	}
	return out
}

// fixClientAggregate adjusts an aggregate server.Aggregate formed, for the
// client-timed mode (TTP-99). It reads the same answered population
// Aggregate does — streams with no error and at least one token:
//
//   - DisagreeingStreams is recounted without client-timed streams: their
//     rate is the recorder's own clock, so there is no server figure to
//     disagree with. On a run with no client-timed stream the recount is
//     identical to Aggregate's, by construction.
//   - One uncounted stream (PredictedNSource "chunks") poisons the aggregate
//     rate to 0: its PredictedN is a chunk count, and a rate over the summed
//     total would print chunks as if they were tokens.
func fixClientAggregate(recs []tape.RequestRecord, agg *tape.AggregateTimings) {
	d := 0
	poisoned := false
	for i := range recs {
		r := &recs[i]
		if r.Error != "" || len(r.Tokens) == 0 {
			continue
		}
		t := r.Timings
		if t.Source == "client" {
			if t.PredictedNSource == "chunks" {
				poisoned = true
			}
			continue
		}
		if t.PredictedPerSecond > 0 && t.ClientPredictedPerSecond > 0 && !t.ClientAgreesWithServer {
			d++
		}
	}
	agg.DisagreeingStreams = d
	if poisoned {
		agg.AggregatePredictedPerSecond = 0
	}
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

// procBytesFloor is the smallest ProcBytes a device may hold and still count
// as holding the server's weights (gpusInPlay, below). A card with the
// server's context on it but not its weights reports a few MiB; the floor
// leaves room for that while keeping one MiB — what the dark card of a
// CUDA_VISIBLE_DEVICES take holds — decisively out.
const procBytesFloor = 256 << 20

// gpusInPlay is the devices the run's own reading shows the server holding
// weights on, or nil when the reading cannot say.
//
// ProcBytes is the one figure that is this server's: it is the VRAM the
// server's PID holds, read per device, so a card carrying another process's
// 20 GB and none of this server's is not in play. UsedBytes is deliberately
// not consulted — another process's allocation is not this server's
// placement, and this is the one place that distinction decides where the
// weights are claimed to be. When no device reported ProcBytes at all the
// server's process was never identified and the column is absent, so the
// answer is "cannot say" (nil) rather than a guess in either direction.
func gpusInPlay(gpusAtEnd []tape.GPUSample) (inPlay []int) {
	var anyProc bool
	for _, g := range gpusAtEnd {
		if g.ProcBytes <= 0 {
			continue
		}
		anyProc = true
		if g.ProcBytes > procBytesFloor {
			inPlay = append(inPlay, g.Index)
		}
	}
	if !anyProc {
		return nil
	}
	return inPlay
}

// narrowPlacement re-estimates the placement over only the devices the run's
// own readings show holding the server's weights (lead, 2026-09-16).
//
// collectPlacement runs before the run does, so all it can do is spread the
// model over every device the box has — and a server launched with
// CUDA_VISIBLE_DEVICES=0 on a two-GPU box then had half of a 29 GB model
// claimed for a card that held one MiB, which bandwidth.Contradiction
// correctly refused to derive anything from. The samples that exist by reduce
// time name the devices the server's PID actually holds VRAM on, so the
// estimate is replayed over exactly those and the safety net stays for the
// cases it was built for: a placement the readings contradict still derives
// nothing.
//
// Only an estimate is narrowed. An engine's placement is its own report and
// an unknown one is not a placement; re-estimating either would put a guess
// where a record was. An in-play set that is empty, or spans every device the
// reading saw, changes nothing, and so does one nobody could compute.
func (r *run) narrowPlacement(gpusAtEnd []tape.GPUSample) {
	if r.place.Source != placement.SourceGGUFArgs {
		return
	}
	inPlay := gpusInPlay(gpusAtEnd)
	if len(inPlay) == 0 || len(inPlay) == len(gpusAtEnd) {
		return
	}
	// lazy stays false as collectPlacement left it, and WithModel rides along
	// so the per-device active bytes are filled the same way, over the
	// narrowed device set.
	sum, warns := placement.EstimateVerbose(r.tensors, r.flags, len(r.host.GPUs), false,
		placement.WithModel(r.model),
		placement.WithGPUIndices(inPlay))
	// The re-estimate spreads tensors and knows nothing about the KV cache —
	// placement never derives one — so whatever collectVRAMKV read from the
	// server's own log must survive the replace below (TTP-137).
	sum.VRAMKVBytes = r.place.VRAMKVBytes
	r.place = sum
	// The re-estimate runs over the same tensors and flags as the first one,
	// so its warnings are the sentences collectPlacement already appended; a
	// warning that is genuinely new (the estimator grew one) still rides
	// through, but a repeat must not print twice.
	for _, w := range warns {
		if !slices.Contains(r.warnings, w) {
			r.warn("%s", w)
		}
	}
	r.warn("placement estimated over %s, the only %s holding the server's weights",
		gpuNames(inPlay), pluralDevices(len(inPlay)))
}

// gpuNames lists GPU indices the way a warning names them: "GPU0" alone,
// "GPU0 and GPU2", "GPU0, GPU1 and GPU3".
func gpuNames(indices []int) string {
	names := make([]string, len(indices))
	for i, idx := range indices {
		names[i] = "GPU" + strconv.Itoa(idx)
	}
	if len(names) == 0 {
		return ""
	}
	if len(names) == 1 {
		return names[0]
	}
	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}

// pluralDevices is the warning's noun, in the number the list beside it is.
func pluralDevices(n int) string {
	if n == 1 {
		return "device"
	}
	return "devices"
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
	// The request is not the outcome (TTP-106, 2026-09-17). A server that
	// drops the switch reports nothing about having dropped it, so the run's
	// own first tokens are the only witness left. It is counted here because
	// the card reads this summary and never the token text.
	if out.Thinking == "off" {
		out.ThoughtAnyway = thoughtAnyway(recs)
	}
	if v, ok := p.Params["temperature"]; ok {
		if f, ok := floatOf(v); ok {
			out.Temperature = &f
		}
	}
	return out
}

// thoughtAnyway counts the answered streams whose output opens a thinking
// block.
//
// Only the opening counts. A reasoning model emits its tag as the very first
// thing it writes, whereas a "<think>" further into an answer is the model
// quoting one — and a run is not evidence that a switch was ignored because
// the model mentioned thinking.
func thoughtAnyway(recs []tape.RequestRecord) int {
	n := 0
	for _, r := range recs {
		var b strings.Builder
		for _, tk := range r.Tokens {
			b.WriteString(tk.Text)
			if b.Len() >= 32 {
				break
			}
		}
		if opensThinking(b.String()) {
			n++
		}
	}
	return n
}

// opensThinking reports whether text opens a reasoning block. The tags are the
// ones the engines this tool records actually emit; an unknown tag is no
// evidence rather than a guess, which is the direction this field must err in
// — it only ever speaks to contradict.
func opensThinking(s string) bool {
	s = strings.TrimLeft(s, " \t\r\n")
	return strings.HasPrefix(s, "<think>") || strings.HasPrefix(s, "<thinking>")
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
