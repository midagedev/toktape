package bandwidth

// TTP-56 (2026-09-14). The card printed "Decode 20.3 tok/s · ≈ 155 GB/s" for a
// run of DeepSeek V4.1 Flash Q3_K_M on the ws box, and 155 GB/s is above that
// machine's 115.8 GB/s STREAM figure for host RAM. The figure was not
// over-counted: it is ActiveBytesPerToken (7.639 GB) × the decode rate, and
// every one of those bytes really is read. The defect is that the model's
// weights live on three buses at once — 3.220 GB/token in host RAM behind a
// 115.8 GB/s wall, 4.419 GB/token in two GPUs behind 768 and 936 GB/s — and one
// number summed across them is not a bandwidth against any ceiling that exists.
// Divided by bus the same run reads 65.5 GB/s from RAM, 34.5 GB/s from GPU0 and
// 38.7 GB/s from GPU1.
//
// This file owns the per-bus view. Ceiling() in bandwidth.go collapses the
// three into one harmonic mean and is the right figure for "how fast could this
// placement possibly go"; RAM() is the right figure for "is the host bus the
// wall", which is the question a partial CPU offload is always really asking.

import (
	"math"

	"github.com/midagedev/toktape/internal/tape"
)

// ExpertsClassBytes splits a placement's ClassExperts bytes into the part a
// token reads sparsely and the part it reads in full.
//
// The two are indistinguishable in tape.DevicePlacement.Classes, which carries
// bytes per class and no tensor names, and internal/placement/classify.go puts
// three different things in ClassExperts:
//
//   - the stacked per-expert weights, "ffn_{up,down,gate}_exps" and "_chexps":
//     a token routes through ExpertUsedCount of ExpertCount slices, so it reads
//     used/count of these. Sparse.
//   - the MoE router, "ffn_gate_inp": read in full for every token, because
//     choosing the experts is what it is for.
//   - the shared expert, "_shexp": read in full for every token, because every
//     token goes through it in addition to its routed experts.
//
// internal/placement/active.go knows the difference — it tests the tensor NAME
// against sparseExpertRe — and counts the router and the shared expert in full.
// A reader working from the class totals alone cannot, and scales the whole
// bucket. On the ws recording that is 0.818 GB of a 7.639 GB token, 10.71 %,
// which is over SplitTolerance, which is why Ceiling() reported no ratio on
// the very machine the card is meant to settle arguments about.
//
// Since TTP-68 the recorder writes DevicePlacement.ActiveBytesPerToken and a
// fresh tape needs none of this. It stays because every tape already recorded
// does: this is how much of such a tape's experts class was read in full, and
// DeviceExpertsSplit is the same question asked per device, which only a
// post-TTP-68 tape can answer.
type ExpertsClassBytes struct {
	// Sparse is the stacked per-expert weight bytes: read used/count per token.
	Sparse int64
	// Dense is the router and shared-expert bytes inside ClassExperts: read in
	// full per token.
	Dense int64
}

// splitSlack is how far the solved Dense may fall outside [0, experts] and
// still be accepted, as a fraction of the experts-class bytes.
//
// internal/placement/active.go truncates bytes×used/count per tensor, so the
// record it produces can be a few bytes under the exact rational value — one
// byte per stacked expert tensor, a few hundred on a large model. A slack of a
// millionth of the experts bytes is hundreds of kilobytes on a 260 GB stack,
// far more than that truncation and far less than one router tensor.
const splitSlack = 1e-6

// ExpertsSplit recovers the sparse/dense split of the experts class from
// figures the tape already carries, without needing the tensor names back.
//
// The record and the placement are two views of one tensor list (see the
// package doc), so with
//
//	E      = Σ devices' ClassExperts bytes
//	other  = Σ everything else a token reads in full
//	f      = ExpertUsedCount / ExpertCount
//	record = Model.ActiveBytesPerToken
//
// the record is by definition Sparse×f + Dense + other, and Sparse + Dense = E.
// Two equations, two unknowns:
//
//	Dense  = (record − other − E×f) / (1 − f)
//	Sparse = E − Dense
//
// On the ws recording this returns Dense = 831,160,320 = 40 layers ×
// (3,932,160 router + 16,846,848 shared expert), and the shared expert
// decomposes as 5,070,848 + 5,070,848 + 6,705,152 — the three Q3_K/Q4_K tensor
// sizes rig-log measured independently for one expert of this model. The
// solution is exact, not fitted.
//
// ok is false when the model reports no active bytes, when the placement
// carries no experts, when the expert counts do not describe a sparse MoE
// (f outside (0,1) means every expert is routed or the counts were not
// observed, and then there is nothing to split), or when the solved Dense
// falls outside [0, E] by more than splitSlack — which means the record and
// the placement disagree about something other than this class rule, and
// guessing which is the CLAUDE.md sin of picking the nicer number.
func ExpertsSplit(s *tape.RunSummary) (ExpertsClassBytes, bool) {
	if s == nil || s.Model.ActiveBytesPerToken <= 0 {
		return ExpertsClassBytes{}, false
	}
	n, used := s.Model.NExperts, s.Model.NExpertsUsed
	if n <= 0 || used <= 0 || used >= n {
		return ExpertsClassBytes{}, false
	}
	experts := expertsClassBytes(s.Placement)
	if experts <= 0 {
		return ExpertsClassBytes{}, false
	}

	tied := tiedEmbeddings(s.Placement)
	var other int64
	for _, d := range s.Placement.Devices {
		for class, b := range d.Classes {
			if b <= 0 {
				continue
			}
			switch class {
			case tape.ClassNGram, tape.ClassExperts:
				// Not counted here: ngram is never read, experts are the
				// unknown we are solving for.
			case tape.ClassEmbed:
				if tied {
					other += b
				}
			default:
				other += b
			}
		}
	}

	f := float64(used) / float64(n)
	dense := (float64(s.Model.ActiveBytesPerToken) - float64(other) - float64(experts)*f) / (1 - f)
	slack := float64(experts) * splitSlack
	if dense < -slack || dense > float64(experts)+slack {
		return ExpertsClassBytes{}, false
	}
	d := int64(math.Round(dense))
	if d < 0 {
		d = 0
	}
	if d > experts {
		d = experts
	}
	return ExpertsClassBytes{Sparse: experts - d, Dense: d}, true
}

// DeviceExpertsSplit is ExpertsSplit asked of ONE device: how many of this
// device's ClassExperts bytes a token reads sparsely and how many it reads in
// full.
//
// ExpertsSplit can only answer for the whole model — it solves one equation
// against Model.ActiveBytesPerToken — and TTP-56 recorded that as the open
// end: "which DEVICE holds them is not recoverable". TTP-68 closed it, and
// this is where. With the device's own ActiveBytesPerToken recorded, the same
// two equations are available per device:
//
//	E      = d.Classes[ClassExperts]
//	other  = the device's other classes a token reads in full
//	f      = ExpertUsedCount / ExpertCount
//	active = d.ActiveBytesPerToken = Sparse×f + Dense + other,  Sparse+Dense = E
//
// so
//
//	Sparse = (E − (active − other)) / (1 − f)
//	Dense  = E − Sparse
//
// ok is false on a tape with no per-device figure (the whole point of the
// exercise is that the class totals cannot answer this), when the expert
// counts do not describe a sparse MoE, or when the solution falls outside
// [0, E] by more than splitSlack — which means the device's figure and its
// class totals disagree about something other than this class rule, and
// picking one would be picking the nicer number (CLAUDE.md).
//
// A device with no expert bytes at all solves trivially to {0, 0}, ok.
//
// tied is the caller's answer to the tied-embedding question and must come
// from tiedEmbeddings over the whole PLACEMENT, never from this device. It
// inherits that proxy's weakness (see tiedEmbeddings): on a genuinely tied
// model whose embedding matrix shares a device with expert bytes, "other"
// omits the matrix and the solution is wrong by that much while still landing
// inside [0, E], so the slack check will not catch it. Every model this has
// been exercised against is untied. A caller that needs certainty should read
// d.ActiveBytesPerToken directly, which is exact either way.
func DeviceExpertsSplit(d tape.DevicePlacement, m tape.ModelInfo, tied bool) (ExpertsClassBytes, bool) {
	if d.ActiveBytesPerToken <= 0 {
		return ExpertsClassBytes{}, false
	}
	experts := d.Classes[tape.ClassExperts]
	if experts <= 0 {
		return ExpertsClassBytes{}, true
	}
	n, used := m.NExperts, m.NExpertsUsed
	if n <= 0 || used <= 0 || used >= n {
		return ExpertsClassBytes{}, false
	}

	var other int64
	for class, b := range d.Classes {
		if b <= 0 {
			continue
		}
		switch class {
		case tape.ClassNGram, tape.ClassExperts:
			// ngram is never read; experts are the unknown.
		case tape.ClassEmbed:
			if tied {
				other += b
			}
		default:
			other += b
		}
	}

	f := float64(used) / float64(n)
	sparse := (float64(experts) - float64(d.ActiveBytesPerToken-other)) / (1 - f)
	slack := float64(experts) * splitSlack
	if sparse < -slack || sparse > float64(experts)+slack {
		return ExpertsClassBytes{}, false
	}
	sp := int64(math.Round(sparse))
	if sp < 0 {
		sp = 0
	}
	if sp > experts {
		sp = experts
	}
	return ExpertsClassBytes{Sparse: sp, Dense: experts - sp}, true
}

// expertsClassBytes is the placement's total ClassExperts bytes.
func expertsClassBytes(p tape.PlacementSummary) int64 {
	var n int64
	for _, d := range p.Devices {
		n += d.Classes[tape.ClassExperts]
	}
	return n
}

// device finds a placement device by name.
func device(p tape.PlacementSummary, name string) (tape.DevicePlacement, bool) {
	for _, d := range p.Devices {
		if d.Device == name {
			return d, true
		}
	}
	return tape.DevicePlacement{}, false
}

// RAMSide is what one run asked of the host memory bus: the weight bytes that
// live in system RAM rather than in VRAM, and the rate they were pulled at.
//
// This is the figure a partially offloaded MoE run is really about. On the ws
// recording the whole-model number is 155 GB/s, which reads as "above the
// 115.8 GB/s wall, so the accounting must be wrong"; the RAM side is 65.5 GB/s,
// which reads as "the host bus is at 57 % and something else is the limit".
// Only one of those two sentences can be argued with, and it is the true one.
type RAMSide struct {
	// ActiveBytesPerToken is what the CPU device reads to decode one token.
	ActiveBytesPerToken int64
	// BytesPerSec is ActiveBytesPerToken × the decode rate.
	BytesPerSec int64
	// OfPeak is BytesPerSec / HostBytesPerSec, or 0 when the host's RAM
	// bandwidth was not observed, in which case the caller must print no
	// percentage rather than a guessed one. internal/procmon/host.go leaves
	// RAMSpeed empty on every Linux run (the DMI tables are root-only), so
	// before TTP-45 this was 0 on every real recording; an operator can now
	// state the figure instead. See HostBandwidth.
	OfPeak float64
	// Streams is how many streams the rate covers: 1, or Concurrency for a
	// concurrent run, where BytesPerSec is the whole server's host traffic and
	// is an UPPER bound (see decodeRate).
	Streams int
	// Exact is true only when ActiveBytesPerToken is PROVABLY the whole of what
	// the CPU reads. False means it may be an under-count, never that it is
	// one.
	//
	// The doubt is the router and the shared expert: they are ClassExperts but
	// are read in full, a reader working from class totals scales them anyway,
	// and if any of them are on the CPU the figure is low by that much.
	// ExpertsSplit says how many such bytes the model has; which DEVICE holds
	// them is not in DevicePlacement.Classes at all.
	//
	// TTP-68 (2026-09-14) closed the doubt for tapes recorded since: the CPU
	// states its own ActiveBytesPerToken, counted from its own tensor names,
	// so there is no attribution left to have got wrong and Exact is true.
	//
	// On a tape without that field Exact is true in exactly two further cases:
	// the CPU carries no ClassExperts bytes, so there is nothing to have got
	// wrong; or ExpertsSplit succeeded AND found no such bytes in the model.
	// It is deliberately false when ExpertsSplit could not solve at all —
	// "we could not check" is not "we checked and it is fine", and collapsing
	// the two is how a figure gets trusted more than it has earned.
	//
	// The card's "≈" already marks the clause an estimate, so this is for the
	// reader of a report rather than a branch in a renderer.
	Exact bool
}

// RAM is the host-memory-bus view of a run, and whether it is worth printing.
//
// ok is false unless the placement is genuinely MIXED: the CPU carries bytes a
// token reads AND at least one other device does too. On an all-CPU or an
// all-GPU run there is only one bus, the whole-model effective bandwidth is
// already a figure against a real ceiling, and splitting it out would say the
// same thing twice. ok is also false without a decode rate to multiply by.
func RAM(s *tape.RunSummary) (RAMSide, bool) {
	if s == nil {
		return RAMSide{}, false
	}
	rate, streams, ok := decodeRate(s)
	if !ok {
		return RAMSide{}, false
	}
	cpu, found := device(s.Placement, tape.DeviceCPU)
	if !found {
		return RAMSide{}, false
	}
	tied := tiedEmbeddings(s.Placement)
	cpuActive := activeBytesOn(cpu, s.Model, tied)
	if cpuActive <= 0 {
		return RAMSide{}, false
	}

	// Mixed, or there is nothing to separate.
	var elsewhere int64
	for _, d := range s.Placement.Devices {
		if d.Device == tape.DeviceCPU {
			continue
		}
		elsewhere += activeBytesOn(d, s.Model, tied)
	}
	if elsewhere <= 0 {
		return RAMSide{}, false
	}

	out := RAMSide{
		ActiveBytesPerToken: cpuActive,
		BytesPerSec:         int64(math.Round(float64(cpuActive) * rate)),
		Streams:             streams,
	}
	if peak := HostBytesPerSec(s.Host); peak > 0 {
		out.OfPeak = float64(out.BytesPerSec) / float64(peak)
	}
	out.Exact = cpuActiveExact(s, cpu)
	return out, true
}

// cpuActiveExact reports whether the CPU's active-bytes figure is PROVABLY the
// whole of what the CPU reads, in the sense RAMSide.Exact documents — read
// that comment before changing this one; it is the reasoning, and this is only
// where the reasoning is executed.
//
// It is one function rather than one per caller because the question is about
// the placement and the model, not about which figure a caller went on to
// build out of them: RAMSide.BytesPerSec and Verify.RAMBytesPerStep are two
// arithmetics over the same attribution, so an answer that differed between
// them would be a bug by construction. Deciding it in each caller is how two
// renderers of one tape come to disagree about whether a ratio may be printed.
//
// False means the figure MAY be an under-count, never that it is one.
func cpuActiveExact(s *tape.RunSummary, cpu tape.DevicePlacement) bool {
	switch {
	case cpu.ActiveBytesPerToken > 0:
		// TTP-68 (2026-09-14): the recorder read this device's own tensor
		// names and wrote what they are read for, so there is no attribution
		// left to have got wrong. This is the case the field was waiting for —
		// "closing it means recording per-device active bytes at record time",
		// as RAMSide.Exact says.
		return true
	case cpu.Classes[tape.ClassExperts] <= 0:
		// Nothing of the doubtful class is here at all.
		return true
	}
	// The split solved and the model has no router or shared-expert bytes to
	// have misattributed. A split that did NOT solve leaves this false: not
	// knowing is not the same as knowing it is fine.
	split, ok := ExpertsSplit(s)
	return ok && split.Dense == 0
}

// decodeRate is the tokens per second the HOST BUS had to feed, and over how
// many streams.
//
// One stream: Timings.PredictedPerSecond, which is the same rate
// Timings.EffectiveBandwidthBytesPerSec is defined against, so the two clauses
// stay comparable.
//
// More than one: the bus carries every stream, so the per-stream mean would
// describe one slot's share and not the machine. The server-wide
// Aggregate.AggregatePredictedPerSecond is the right multiplier, and the
// product is an UPPER bound: llama.cpp batches the busy slots into one forward
// pass, so B slots read the dense weights once rather than B times and their
// routed experts overlap — the same batching that makes a speculative verify
// step cheaper than its tokens (see Speculative). Without an aggregate rate a
// concurrent run is not derivable rather than approximated by one slot's.
func decodeRate(s *tape.RunSummary) (rate float64, streams int, ok bool) {
	if s.Concurrency > 1 {
		if s.Aggregate.AggregatePredictedPerSecond <= 0 {
			return 0, 0, false
		}
		return s.Aggregate.AggregatePredictedPerSecond, s.Concurrency, true
	}
	if s.Timings.PredictedPerSecond <= 0 {
		return 0, 0, false
	}
	return s.Timings.PredictedPerSecond, 1, true
}

// ---------------------------------------------------- speculative decoding ---

// Verify is one run's speculative-decoding arithmetic: what the target model
// actually did, as opposed to what the token counter says.
//
// With a draft model the engine does not run one forward pass per token. It
// drafts up to n_max tokens, runs the target model ONCE over the batch of
// (1 + drafted) and keeps the longest accepted prefix. So "bytes per accepted
// token" and "bytes per verify step" are different quantities and the gap is
// the acceptance rate. Worse, they move in opposite directions with the batch:
// the dense weights are read once per step however big the batch, while the
// routed experts grow with it, because B tokens route to up to B×used distinct
// experts in a layer instead of used.
//
// On the ws prose recording this is the difference between "65.5 GB/s from RAM,
// 57 % of the wall" and "109.9 GB/s from RAM, 95 % of the wall". The second is
// the true description of the machine.
type Verify struct {
	// Steps is how many times the target model ran.
	Steps int
	// StepsPerSec is Steps over the decode window.
	StepsPerSec float64
	// Batch is the tokens handed to the target model per step, 1 + the drafts.
	Batch float64
	// DistinctExpertsPerLayer is how many of ExpertCount a batch of Batch
	// tokens is expected to touch in one layer.
	DistinctExpertsPerLayer float64
	// RAMBytesPerStep is the host-RAM weight bytes one verify step reads.
	RAMBytesPerStep int64
	// RAMBytesPerSec is RAMBytesPerStep × StepsPerSec: the rate the host bus
	// really carried, as opposed to the per-accepted-token figure in RAMSide.
	RAMBytesPerSec int64
	// OfPeak is RAMBytesPerSec / HostBandwidth, or 0 when the host's RAM
	// bandwidth was not observed, in which case the caller must print no
	// percentage rather than a guessed one. It is the same quantity RAMSide
	// carries under the same name, over the same host figure, and it is
	// decided here rather than in a renderer because the numerator is one this
	// package built out of its own per-device active bytes: a caller that
	// divided it by a host figure of its own choosing would be deciding this
	// package's provenance from outside it.
	OfPeak float64
	// Exact means what RAMSide.Exact means, decided by the same rule over the
	// same inputs (cpuActiveExact): the CPU's active bytes are provably the
	// whole of what the CPU reads, so the step this is built from is not an
	// under-count. False means it may be one, never that it is.
	//
	// Unlike RAMSide.Exact this IS a branch in a renderer: the card gates the
	// "of peak" clause on it, because a ratio over an under-count claims
	// headroom the tape cannot support, and headroom is the one thing a reader
	// acts on. The rate itself is printed either way, marked "≈" — what the
	// approximation costs is a percentage, not the figure.
	//
	// It says nothing about the batch model. DistinctExpertsPerLayer is an
	// upper bound on every run, exact or not (see Speculative), which is why
	// the clause carries its "≈" even here.
	Exact bool
	// AcceptRate is DraftNAccepted / DraftN.
	AcceptRate float64
	// Streams is how many streams the figures cover: 1, or Concurrency on a
	// concurrent run, where Steps is the SERVER's verify steps and
	// RAMBytesPerSec is an UPPER bound (see verifyWindow).
	Streams int
}

// Speculative is the verify-step view of a run, and whether it is derivable.
//
// The step count comes out of the timings with no new schema. Every step emits
// exactly one token the target model chose itself, plus however many of its
// drafts were accepted, so
//
//	steps = predicted_n − draft_n_accepted
//	batch = draft_n / steps + 1
//
// Checked against the four independent requests of the ws code recording, each
// of which lands on the configured --spec-draft-n-max of 3:
//
//	predicted_n 250 − accepted 182 = 68 steps, 204/68 = 3.00 drafts a step
//	            262 − 189          = 73 steps, 216/73 = 2.96
//	            244 − 174          = 70 steps, 210/70 = 3.00
//	            242 − 174          = 68 steps, 204/68 = 3.00
//
// On a CONCURRENT run the same arithmetic holds, but only against the
// server-wide token count. At run level DraftN and DraftNAccepted are the SUM
// over streams while Timings.PredictedN is the per-stream MEAN (tape.go,
// TTP-30), so subtracting the one from the other mixes a sum with a mean and
// returns nonsense — on the ws code recording it gives 250 − 719 = −469 steps.
// Aggregate.TotalPredictedN is the matching sum, and with it the subtraction
// is between two figures of the same kind again. This matters because the
// project's README hero is a two-stream run: a version that refused N > 1
// would never appear on the card anyone actually sees (TTP-67, 2026-09-14).
//
// ok is false without both draft figures, without an expert count to expand
// the batch over, without a decode window, on a concurrent run with no
// Aggregate.TotalPredictedN or no aggregate rate, or when the arithmetic does
// not describe a real run (no steps, a batch under one token).
func Speculative(s *tape.RunSummary) (Verify, bool) {
	if s == nil || s.Timings.DraftN == nil || s.Timings.DraftNAccepted == nil {
		return Verify{}, false
	}
	t := s.Timings
	draftN, accepted := *t.DraftN, *t.DraftNAccepted
	if draftN <= 0 || accepted < 0 {
		return Verify{}, false
	}
	predictedN, window, streams, ok := verifyWindow(s)
	if !ok {
		return Verify{}, false
	}
	steps := predictedN - accepted
	if steps <= 0 {
		return Verify{}, false
	}
	batch := float64(draftN)/float64(steps) + 1
	if batch < 1 {
		return Verify{}, false
	}

	n, used := s.Model.NExperts, s.Model.NExpertsUsed
	if n <= 0 || used <= 0 || used > n {
		return Verify{}, false
	}
	// A token picks used of n experts, so a given expert is missed by one
	// token with probability (1 − used/n) and by all B with that to the Bth.
	// This assumes the B tokens route independently and uniformly, which they
	// do not — consecutive tokens of one sentence share experts — so it is an
	// upper bound on distinct experts and therefore on the bytes. For the ws
	// recording (B = 3.995, used 6, n 384) it gives 23.42 against the 23.4
	// rig-log derived by hand.
	distinct := float64(n) * (1 - math.Pow(1-float64(used)/float64(n), batch))
	if distinct < float64(used) {
		distinct = float64(used)
	}

	cpu, found := device(s.Placement, tape.DeviceCPU)
	if !found {
		return Verify{}, false
	}
	tied := tiedEmbeddings(s.Placement)
	cpuActive := activeBytesOn(cpu, s.Model, tied)

	// Only the STACKED expert weights re-expand with the batch: B tokens route
	// to up to B×used distinct slices of the stack instead of used. The
	// router and the shared expert are ClassExperts too, but every token runs
	// both, so a batch reads them exactly once per step — the same as every
	// other dense weight. Scaling the whole class would inflate the step by
	// the router and the shared expert all over again.
	//
	// DeviceExpertsSplit separates the two when the tape says what this device
	// is read for (TTP-68). On a tape that does not, the whole class is
	// treated as the stack, which is what this function did before the field
	// existed and is what activeBytesOn's own fallback assumed — the two must
	// make the same assumption or the subtraction below leaves a residue.
	sparse := cpu.Classes[tape.ClassExperts]
	if split, ok := DeviceExpertsSplit(cpu, s.Model, tied); ok {
		sparse = split.Sparse
	}
	// Everything the CPU reads once per step: its non-expert weights, plus the
	// router and shared expert, which is whatever is left when the sparse
	// stack's per-token share is taken out of the per-token figure.
	cpuOncePerStep := cpuActive - sparse*int64(used)/int64(n)
	perStep := cpuOncePerStep + int64(math.Round(float64(sparse)*distinct/float64(n)))
	if perStep <= 0 {
		return Verify{}, false
	}

	stepsPerSec := float64(steps) / window
	out := Verify{
		Steps:                   steps,
		StepsPerSec:             stepsPerSec,
		Batch:                   batch,
		DistinctExpertsPerLayer: distinct,
		RAMBytesPerStep:         perStep,
		RAMBytesPerSec:          int64(math.Round(float64(perStep) * stepsPerSec)),
		Exact:                   cpuActiveExact(s, cpu),
		AcceptRate:              float64(accepted) / float64(draftN),
		Streams:                 streams,
	}
	// The ratio is decided here because both of its terms are: the numerator
	// is the step this function just built, and HostBandwidth is this
	// package's single owner of the host's bandwidth and its provenance. It is
	// derivable on an all-CPU placement, where RAM() reports nothing because
	// there is no second bus to separate out — the host bus is still the one
	// this rate was carried on, and it is still the wall.
	if peak := HostBytesPerSec(s.Host); peak > 0 {
		out.OfPeak = float64(out.RAMBytesPerSec) / float64(peak)
	}
	return out, true
}

// verifyWindow is the token count the verify-step arithmetic must be done
// against, the seconds it happened in, and how many streams that covers.
//
// One stream: Timings.PredictedN over Timings.PredictedMs, the same window
// Timings.PredictedPerSecond and EffectiveBandwidthBytesPerSec are defined
// against, so every clause on the card stays comparable.
//
// More than one: the server-wide Aggregate.TotalPredictedN over the window
// Aggregate.AggregatePredictedPerSecond is itself defined against
// (TotalPredictedN / decode window), which is the only place that window is
// recorded. Timings.PredictedN cannot be used here — it is the per-stream
// MEAN while the draft figures are SUMS (tape.go, TTP-30).
//
// The concurrent result is an UPPER bound on forward passes, for the reason
// decodeRate gives: llama.cpp batches the busy slots into one pass, so two
// streams verifying at the same instant are one pass and not two. Steps
// counts what each stream asked for; the hardware may have done fewer.
func verifyWindow(s *tape.RunSummary) (predictedN int, seconds float64, streams int, ok bool) {
	if s.Concurrency > 1 {
		a := s.Aggregate
		if a.TotalPredictedN <= 0 || a.AggregatePredictedPerSecond <= 0 {
			return 0, 0, 0, false
		}
		return a.TotalPredictedN, float64(a.TotalPredictedN) / a.AggregatePredictedPerSecond, s.Concurrency, true
	}
	if s.Timings.PredictedMs <= 0 {
		return 0, 0, 0, false
	}
	return s.Timings.PredictedN, s.Timings.PredictedMs / 1000, 1, true
}
