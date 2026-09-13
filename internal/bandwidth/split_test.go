package bandwidth

import (
	"math"
	"testing"

	"github.com/midagedev/toktape/internal/tape"
)

// The ws recording of DeepSeek V4.1 Flash Q3_K_M, verbatim from
// scratch/wsreal/20260913-205310-deepseek-v4-1-flash-q3-k.tape. These are the
// figures TTP-56 was opened about, so the tests below are the recording rather
// than a model of it.
const (
	wsActiveBytesPerToken = 7_639_161_280 // model.active_bytes_per_token
	wsDecodeRate          = 20.346424970619694
	wsEffectiveBW         = 155_429_621_821 // timings.effective_bw_bps, 155.4 GB/s

	wsCPUEmbed   = 1_323_827_200
	wsCPUNGram   = 209_236_612_640
	wsCPUExperts = 206_108_098_560

	wsGPU0Experts = 33_774_612_480
	wsGPU0Attn    = 1_150_986_496
	wsGPU0FFN     = 430_080
	wsGPU0Other   = 18_346_936

	wsGPU1Experts = 19_716_034_560
	wsGPU1Attn    = 1_034_227_456
	wsGPU1FFN     = 389_120
	wsGPU1Other   = 17_380_872
	wsGPU1Output  = 542_996_480

	// The answers, hand-derived in the TTP-56 report and reproduced there from
	// rig-log's independent per-tensor measurements.
	wsExpertsDense  = 831_160_320     // 40 × (3,932,160 router + 16,846,848 shared expert)
	wsExpertsSparse = 258_767_585_280 // the stacked ffn_*_exps
	wsCPUActive     = 3_220_439_040   // 206,108,098,560 × 6/384
)

// wsSummary is the ws prose recording reduced to what this package reads.
func wsSummary() *tape.RunSummary {
	draftN, accepted := 2636, 1168
	return &tape.RunSummary{
		Model: tape.ModelInfo{
			NLayers:             40,
			NExperts:            384,
			NExpertsUsed:        6,
			ActiveBytesPerToken: wsActiveBytesPerToken,
		},
		Host: tape.HostInfo{
			// RAMSpeed and RAMChannels are empty exactly as they are on the
			// real recording: procmon cannot read the DMI tables unprivileged.
			GPUs: []tape.GPUInfo{
				{Index: 0, Name: "NVIDIA RTX A6000", PeakBandwidthBytesPerSec: 768_000_000_000},
				{Index: 1, Name: "NVIDIA GeForce RTX 3090", PeakBandwidthBytesPerSec: 936_200_000_000},
			},
		},
		Placement: tape.PlacementSummary{
			Source: "gguf+args",
			Devices: []tape.DevicePlacement{
				{Device: tape.DeviceCPU, Bytes: 416_668_538_400, Classes: map[tape.TensorClass]int64{
					tape.ClassEmbed:   wsCPUEmbed,
					tape.ClassNGram:   wsCPUNGram,
					tape.ClassExperts: wsCPUExperts,
				}},
				{Device: "GPU0", Bytes: 34_944_375_992, Classes: map[tape.TensorClass]int64{
					tape.ClassExperts:   wsGPU0Experts,
					tape.ClassAttention: wsGPU0Attn,
					tape.ClassFFN:       wsGPU0FFN,
					tape.ClassOther:     wsGPU0Other,
				}},
				{Device: "GPU1", Bytes: 21_311_028_488, Classes: map[tape.TensorClass]int64{
					tape.ClassExperts:   wsGPU1Experts,
					tape.ClassAttention: wsGPU1Attn,
					tape.ClassFFN:       wsGPU1FFN,
					tape.ClassOther:     wsGPU1Other,
					tape.ClassOutput:    wsGPU1Output,
				}},
			},
		},
		Concurrency: 1,
		Timings: tape.TimingsSummary{
			PredictedN:                    2048,
			PredictedMs:                   100607.355,
			PredictedPerSecond:            wsDecodeRate,
			DraftN:                        &draftN,
			DraftNAccepted:                &accepted,
			EffectiveBandwidthBytesPerSec: wsEffectiveBW,
		},
	}
}

// TestExpertsSplitWS is the discriminating computation of TTP-56 made
// permanent: the 0.818 GB by which activeBytesOn and Model.ActiveBytesPerToken
// disagree on the ws recording is the router plus the shared expert, and the
// split is recoverable exactly from the tape.
func TestExpertsSplitWS(t *testing.T) {
	s := wsSummary()

	// The gap this ticket is about, stated before it is explained. These two
	// literals PIN A KNOWN DEFECT rather than a contract: they are what
	// activeBytesOn loses today because it cannot see tensor names. When
	// per-device active bytes are recorded exactly (the TTP-56 proposal), the
	// gap becomes 0 and this block must be updated with a dated comment — it
	// failing is the fix landing, not a regression.
	tied := tiedEmbeddings(s.Placement)
	var perDevice int64
	for _, d := range s.Placement.Devices {
		perDevice += activeBytesOn(d, s.Model, tied)
	}
	if want := int64(6_820_987_840); perDevice != want {
		t.Fatalf("per-device active sum = %d, want %d", perDevice, want)
	}
	gap := s.Model.ActiveBytesPerToken - perDevice
	if want := int64(818_173_440); gap != want {
		t.Fatalf("gap = %d, want %d", gap, want)
	}
	if ratio := float64(gap) / float64(s.Model.ActiveBytesPerToken); ratio <= SplitTolerance {
		t.Fatalf("gap is %.4f of the record, expected it to exceed SplitTolerance %.2f", ratio, SplitTolerance)
	}

	split, ok := ExpertsSplit(s)
	if !ok {
		t.Fatal("ExpertsSplit not ok on the ws recording")
	}
	if split.Dense != wsExpertsDense {
		t.Errorf("Dense = %d, want %d", split.Dense, wsExpertsDense)
	}
	if split.Sparse != wsExpertsSparse {
		t.Errorf("Sparse = %d, want %d", split.Sparse, wsExpertsSparse)
	}

	// The split must reconstruct the record exactly, which is what makes it a
	// solution rather than a fit.
	other := int64(wsGPU0Attn + wsGPU0FFN + wsGPU0Other + wsGPU1Attn + wsGPU1FFN + wsGPU1Other + wsGPU1Output)
	got := split.Sparse*6/384 + split.Dense + other
	if got != wsActiveBytesPerToken {
		t.Errorf("reconstructed record = %d, want %d", got, wsActiveBytesPerToken)
	}

	// And it must decompose into rig-log's independently measured tensor
	// sizes: 40 layers of one router and one shared expert, the shared expert
	// being up + gate (Q3_K, 5,070,848 each) + down (6,705,152).
	const router, shexp = 3_932_160, 5_070_848 + 5_070_848 + 6_705_152
	if want := int64(40 * (router + shexp)); split.Dense != want {
		t.Errorf("Dense = %d, want 40 × (router %d + shared expert %d) = %d", split.Dense, router, shexp, want)
	}
}

func TestExpertsSplitUnknown(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*tape.RunSummary)
	}{
		{"no active bytes recorded", func(s *tape.RunSummary) { s.Model.ActiveBytesPerToken = 0 }},
		{"no expert count", func(s *tape.RunSummary) { s.Model.NExperts = 0 }},
		{"no used count", func(s *tape.RunSummary) { s.Model.NExpertsUsed = 0 }},
		{"every expert routed leaves nothing to split", func(s *tape.RunSummary) { s.Model.NExpertsUsed = s.Model.NExperts }},
		{"placement carries no experts", func(s *tape.RunSummary) {
			for _, d := range s.Placement.Devices {
				delete(d.Classes, tape.ClassExperts)
			}
		}},
		{"a record the class rule cannot explain", func(s *tape.RunSummary) {
			// Twice the whole experts class: no sparse/dense split produces it.
			s.Model.ActiveBytesPerToken = 600_000_000_000
		}},
		{"a record below the dense floor", func(s *tape.RunSummary) { s.Model.ActiveBytesPerToken = 1 }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := wsSummary()
			c.mut(s)
			if _, ok := ExpertsSplit(s); ok {
				t.Error("ExpertsSplit reported a split it cannot know")
			}
		})
	}
	if _, ok := ExpertsSplit(nil); ok {
		t.Error("ExpertsSplit(nil) reported a split")
	}
}

// TestRAMWS is the ticket's headline: the card printed one 155 GB/s figure over
// three buses, and the host bus — the one with the 115.8 GB/s wall — carried
// 65.5 GB/s of it.
func TestRAMWS(t *testing.T) {
	s := wsSummary()
	r, ok := RAM(s)
	if !ok {
		t.Fatal("RAM not ok on the ws recording")
	}
	if r.ActiveBytesPerToken != wsCPUActive {
		t.Errorf("ActiveBytesPerToken = %d, want %d", r.ActiveBytesPerToken, wsCPUActive)
	}
	if want := int64(math.Round(wsCPUActive * wsDecodeRate)); r.BytesPerSec != want {
		t.Errorf("BytesPerSec = %d, want %d", r.BytesPerSec, want)
	}
	// 65.5 GB/s, against the 155.4 the card prints today.
	if r.BytesPerSec < 65_000_000_000 || r.BytesPerSec > 66_000_000_000 {
		t.Errorf("BytesPerSec = %d, want ≈ 65.5 GB/s", r.BytesPerSec)
	}
	if r.BytesPerSec >= s.Timings.EffectiveBandwidthBytesPerSec {
		t.Errorf("the RAM side (%d) must be below the whole-model figure (%d)",
			r.BytesPerSec, s.Timings.EffectiveBandwidthBytesPerSec)
	}
	// procmon cannot read the DMI tables, so there is no host peak to divide
	// by and the caller must print no percentage.
	if r.OfPeak != 0 {
		t.Errorf("OfPeak = %v, want 0 with an unobserved RAM speed", r.OfPeak)
	}
	// -ot "...exps=CPU" is the only rule that moved experts to the CPU and it
	// cannot match _shexp or ffn_gate_inp, so on this run every CPU expert byte
	// really is sparse and the figure really is right. The PLACEMENT cannot
	// prove that — only the -ot string can — so Exact stays false. The field
	// claims proof, not correctness.
	if r.Exact {
		t.Error("Exact should be false: the model has router/shexp bytes and the CPU carries experts")
	}
}

func TestRAMOfPeak(t *testing.T) {
	s := wsSummary()
	s.Host.RAMSpeed = "DDR5-4800"
	s.Host.RAMChannels = 8 // 307.2 GB/s
	r, ok := RAM(s)
	if !ok {
		t.Fatal("RAM not ok")
	}
	want := float64(r.BytesPerSec) / 307_200_000_000
	if math.Abs(r.OfPeak-want) > 1e-12 {
		t.Errorf("OfPeak = %v, want %v", r.OfPeak, want)
	}
}

func TestRAMNotMixed(t *testing.T) {
	t.Run("every byte on the GPUs", func(t *testing.T) {
		s := wsSummary()
		// The CPU keeps only what a token never reads.
		s.Placement.Devices[0].Classes = map[tape.TensorClass]int64{
			tape.ClassEmbed: wsCPUEmbed,
			tape.ClassNGram: wsCPUNGram,
		}
		if _, ok := RAM(s); ok {
			t.Error("RAM should say nothing when the CPU reads nothing per token")
		}
	})
	t.Run("every byte on the CPU", func(t *testing.T) {
		s := wsSummary()
		s.Placement.Devices = s.Placement.Devices[:1]
		if _, ok := RAM(s); ok {
			t.Error("RAM should say nothing when there is only one bus")
		}
	})
	t.Run("no decode rate", func(t *testing.T) {
		s := wsSummary()
		s.Timings.PredictedPerSecond = 0
		if _, ok := RAM(s); ok {
			t.Error("RAM should say nothing without a rate to multiply by")
		}
	})
	t.Run("no CPU device", func(t *testing.T) {
		s := wsSummary()
		s.Placement.Devices = s.Placement.Devices[1:]
		if _, ok := RAM(s); ok {
			t.Error("RAM should say nothing without a CPU device")
		}
	})
	if _, ok := RAM(nil); ok {
		t.Error("RAM(nil) reported a figure")
	}
}

func TestRAMExactWhenNoRouterOrSharedExpert(t *testing.T) {
	s := wsSummary()
	// A model whose experts class is all stacked weights: the record is then
	// exactly sparse×used/count + the rest, and nothing is unattributable.
	other := int64(wsGPU0Attn + wsGPU0FFN + wsGPU0Other + wsGPU1Attn + wsGPU1FFN + wsGPU1Other + wsGPU1Output)
	experts := int64(wsCPUExperts + wsGPU0Experts + wsGPU1Experts)
	s.Model.ActiveBytesPerToken = experts*6/384 + other
	split, ok := ExpertsSplit(s)
	if !ok || split.Dense != 0 {
		t.Fatalf("ExpertsSplit = %+v, ok %v; want Dense 0", split, ok)
	}
	r, ok := RAM(s)
	if !ok {
		t.Fatal("RAM not ok")
	}
	if !r.Exact {
		t.Error("Exact should be true on a model with no router or shared-expert bytes")
	}
}

// TestRAMNotExactWhenTheSplitCannotSolve is the distinction the field exists
// for. The hand-written card fixtures (internal/card/example_sharded.go,
// example_speculative.go) carry a record their own placement cannot produce
// under any sparse/dense split, so ExpertsSplit refuses. A figure whose
// premises could not be checked must not claim to be exact.
func TestRAMNotExactWhenTheSplitCannotSolve(t *testing.T) {
	s := wsSummary()
	// A record far below the dense floor: no split of the experts class
	// reaches it, so ExpertsSplit says nothing. This is the shape of
	// example_speculative.go, whose record is 6.72 GB against 7.79 GB of
	// floor.
	s.Model.ActiveBytesPerToken = 3_000_000_000
	if _, ok := ExpertsSplit(s); ok {
		t.Fatal("ExpertsSplit solved a record it should refuse")
	}
	r, ok := RAM(s)
	if !ok {
		t.Fatal("RAM not ok")
	}
	if r.Exact {
		t.Error("Exact claimed on a run whose split could not be solved")
	}
}

// TestRAMExactWhenTheCPUHoldsNoExperts: with no ClassExperts bytes on the CPU
// there is nothing of the doubtful class to have misattributed, whatever the
// rest of the model looks like.
func TestRAMExactWhenTheCPUHoldsNoExperts(t *testing.T) {
	s := wsSummary()
	s.Placement.Devices[0].Classes = map[tape.TensorClass]int64{
		tape.ClassEmbed:     wsCPUEmbed,
		tape.ClassNGram:     wsCPUNGram,
		tape.ClassAttention: 500_000_000,
	}
	r, ok := RAM(s)
	if !ok {
		t.Fatal("RAM not ok")
	}
	if !r.Exact {
		t.Error("Exact should be true when the CPU carries no expert bytes")
	}
}

// TestSpeculativeWS pins the verify-step accounting: the prose run decoded at
// 20.3 tok/s but ran only 8.7 forward passes a second, each pulling 12.6 GB off
// host RAM, which is 95 % of the machine's 115.8 GB/s STREAM figure. The run is
// at the RAM wall, which neither the printed 155 GB/s nor the per-token 65.5
// GB/s says.
func TestSpeculativeWS(t *testing.T) {
	s := wsSummary()
	v, ok := Speculative(s)
	if !ok {
		t.Fatal("Speculative not ok on the ws recording")
	}
	if v.Steps != 880 {
		t.Errorf("Steps = %d, want 880 (predicted_n 2048 − accepted 1168)", v.Steps)
	}
	// --spec-draft-n-max was 3, so the target model saw batches of four.
	if math.Abs(v.Batch-3.995) > 0.001 {
		t.Errorf("Batch = %v, want ≈ 3.995", v.Batch)
	}
	// rig-log derived 23.4 distinct experts a layer for this batch by hand.
	if math.Abs(v.DistinctExpertsPerLayer-23.42) > 0.01 {
		t.Errorf("DistinctExpertsPerLayer = %v, want ≈ 23.42", v.DistinctExpertsPerLayer)
	}
	if math.Abs(v.StepsPerSec-8.747) > 0.01 {
		t.Errorf("StepsPerSec = %v, want ≈ 8.747", v.StepsPerSec)
	}
	if v.RAMBytesPerStep < 12_500_000_000 || v.RAMBytesPerStep > 12_650_000_000 {
		t.Errorf("RAMBytesPerStep = %d, want ≈ 12.57 GB", v.RAMBytesPerStep)
	}
	if v.RAMBytesPerSec < 109_000_000_000 || v.RAMBytesPerSec > 111_000_000_000 {
		t.Errorf("RAMBytesPerSec = %d, want ≈ 109.9 GB/s", v.RAMBytesPerSec)
	}
	// The whole point: the per-verify-step rate is far above the
	// per-accepted-token rate, and the gap is what the draft bought.
	perToken, ok := RAM(s)
	if !ok {
		t.Fatal("RAM not ok")
	}
	if v.RAMBytesPerSec <= perToken.BytesPerSec {
		t.Errorf("per-step %d should exceed per-token %d", v.RAMBytesPerSec, perToken.BytesPerSec)
	}
	if math.Abs(v.AcceptRate-1168.0/2636.0) > 1e-9 {
		t.Errorf("AcceptRate = %v, want %v", v.AcceptRate, 1168.0/2636.0)
	}
}

// TestSpeculativePerRequestShapes checks the step formula against the four
// independent requests of the ws code recording, each configured with
// --spec-draft-n-max 3. A formula that is right on one run is a coincidence;
// four is the claim.
func TestSpeculativePerRequestShapes(t *testing.T) {
	cases := []struct{ predictedN, draftN, accepted, wantSteps int }{
		{250, 204, 182, 68},
		{262, 216, 189, 73},
		{244, 210, 174, 70},
		{242, 204, 174, 68},
	}
	for _, c := range cases {
		s := wsSummary()
		s.Timings.PredictedN = c.predictedN
		dn, da := c.draftN, c.accepted
		s.Timings.DraftN, s.Timings.DraftNAccepted = &dn, &da
		v, ok := Speculative(s)
		if !ok {
			t.Fatalf("predicted_n %d: not ok", c.predictedN)
		}
		if v.Steps != c.wantSteps {
			t.Errorf("predicted_n %d: Steps = %d, want %d", c.predictedN, v.Steps, c.wantSteps)
		}
		// Every one of these was run with n_max 3, so the batch must land on 4.
		if v.Batch < 3.9 || v.Batch > 4.05 {
			t.Errorf("predicted_n %d: Batch = %v, want ≈ 4 (n_max 3)", c.predictedN, v.Batch)
		}
	}
}

// TestSpeculativeRefusesConcurrentRuns is the one that matters for honesty: at
// run level DraftN is the SUM over streams while PredictedN is the per-stream
// MEAN (tape.TimingsSummary, TTP-30), so the subtraction is meaningless. On the ws
// code recording it gives 250 − 719 = −469 steps.
func TestSpeculativeRefusesConcurrentRuns(t *testing.T) {
	s := wsSummary()
	s.Concurrency = 2
	s.Timings.PredictedN = 250
	dn, da := 834, 719
	s.Timings.DraftN, s.Timings.DraftNAccepted = &dn, &da
	if v, ok := Speculative(s); ok {
		t.Errorf("Speculative returned %+v for a 2-stream run; the figures are a sum and a mean", v)
	}

	// And the same figures on a single-stream run are refused too, because the
	// step count comes out negative rather than wrong-but-plausible.
	s.Concurrency = 1
	if _, ok := Speculative(s); ok {
		t.Error("Speculative accepted a negative step count")
	}
}

func TestSpeculativeUnknown(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*tape.RunSummary)
	}{
		{"no draft figures", func(s *tape.RunSummary) { s.Timings.DraftN, s.Timings.DraftNAccepted = nil, nil }},
		{"drafted nothing", func(s *tape.RunSummary) { n := 0; s.Timings.DraftN = &n }},
		{"no decode window", func(s *tape.RunSummary) { s.Timings.PredictedMs = 0 }},
		{"no expert count", func(s *tape.RunSummary) { s.Model.NExperts = 0 }},
		{"no CPU device", func(s *tape.RunSummary) { s.Placement.Devices = s.Placement.Devices[1:] }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := wsSummary()
			c.mut(s)
			if _, ok := Speculative(s); ok {
				t.Error("Speculative reported a figure it cannot know")
			}
		})
	}
	if _, ok := Speculative(nil); ok {
		t.Error("Speculative(nil) reported a figure")
	}
}

// TestRAMConcurrent pins that a multi-stream run is measured against the
// SERVER's token rate, not one slot's: the host bus carries every stream, so
// the per-stream mean would report a third of the traffic on a 3-stream run.
func TestRAMConcurrent(t *testing.T) {
	s := wsSummary()
	s.Concurrency = 3
	s.Aggregate.AggregatePredictedPerSecond = 51.3 // ≈ 3 × the per-stream 17.1

	r, ok := RAM(s)
	if !ok {
		t.Fatal("RAM not ok on a concurrent run")
	}
	if r.Streams != 3 {
		t.Errorf("Streams = %d, want 3", r.Streams)
	}
	if want := int64(math.Round(wsCPUActive * 51.3)); r.BytesPerSec != want {
		t.Errorf("BytesPerSec = %d, want %d (the aggregate rate)", r.BytesPerSec, want)
	}
	// The mistake this guards against: using the per-stream rate would report
	// roughly a third of what the bus carried.
	perStream := int64(math.Round(wsCPUActive * s.Timings.PredictedPerSecond))
	if r.BytesPerSec <= perStream {
		t.Errorf("BytesPerSec %d should exceed the per-stream figure %d", r.BytesPerSec, perStream)
	}

	// Without a server-wide rate a concurrent run is not derivable. Falling
	// back to one slot's rate would be a number picked for existing.
	s.Aggregate.AggregatePredictedPerSecond = 0
	if _, ok := RAM(s); ok {
		t.Error("RAM reported a concurrent figure with no aggregate rate")
	}
}

// TestRAMSingleStreamUsesTheServerPerStreamRate keeps the one-stream clause
// comparable with Timings.EffectiveBandwidthBytesPerSec, which is defined
// against the same rate.
func TestRAMSingleStreamUsesTheServerPerStreamRate(t *testing.T) {
	s := wsSummary()
	s.Aggregate.AggregatePredictedPerSecond = 20.404941657631618 // the wall-window rate
	r, ok := RAM(s)
	if !ok {
		t.Fatal("RAM not ok")
	}
	if r.Streams != 1 {
		t.Errorf("Streams = %d, want 1", r.Streams)
	}
	if want := int64(math.Round(wsCPUActive * wsDecodeRate)); r.BytesPerSec != want {
		t.Errorf("BytesPerSec = %d, want %d (predicted_per_second, not the aggregate)", r.BytesPerSec, want)
	}
}
