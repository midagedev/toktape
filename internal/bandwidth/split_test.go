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

	// Per-device active bytes as a post-TTP-68 recorder writes them, from the
	// tensor names themselves (tape.DevicePlacement.ActiveBytesPerToken).
	//
	// The CPU figure is unchanged and MEASURED: the -ot rule that moved
	// experts there is "...exps=CPU", which matches neither ffn_gate_inp nor
	// _shexp, so every CPU expert byte really is sparse.
	//
	// The GPU figures are DERIVED, not measured: the recording's Layers
	// strings put blocks 0-20 on GPU0 and 21-39 on GPU1, so the 40 layers of
	// router + shared expert (20,779,008 bytes each) split 21/19 between them.
	// Each GPU's figure is therefore
	//
	//	attn + ffn + other (+ output on GPU1)
	//	  + (experts − layers×20,779,008) × 6/384   sparse stack
	//	  + layers×20,779,008                       router + shared expert
	//
	// What is NOT derived is the sum: it lands on wsActiveBytesPerToken
	// exactly, which is the property TTP-68 is about and which no
	// mis-attribution between the two GPUs could produce by accident.
	wsCPUActiveExact  = wsCPUActive
	wsGPU0ActiveExact = 2_127_032_888
	wsGPU1ActiveExact = 2_291_689_352

	// What STREAM reports on the ws box. Not a theoretical peak: the DMI
	// tables that RAMSpeed × RAMChannels would need are root-only on Linux,
	// which is the whole of TTP-45. An operator states this one.
	wsSTREAMBytesPerSec = 115_800_000_000
)

// wsSummary is the ws prose recording reduced to what this package reads, in
// the shape a recorder writes it TODAY: each device carries its own
// ActiveBytesPerToken (TTP-68).
func wsSummary() *tape.RunSummary {
	s := wsLegacySummary()
	s.Placement.Devices[0].ActiveBytesPerToken = wsCPUActiveExact
	s.Placement.Devices[1].ActiveBytesPerToken = wsGPU0ActiveExact
	s.Placement.Devices[2].ActiveBytesPerToken = wsGPU1ActiveExact
	return s
}

// wsLegacySummary is the same recording as a tape written BEFORE TTP-68 added
// DevicePlacement.ActiveBytesPerToken: the field is 0 everywhere and a reader
// must fall back to the class-proportion estimate. Every test of that fallback
// uses this fixture, so the fallback keeps being exercised after the recorder
// stops producing it.
func wsLegacySummary() *tape.RunSummary {
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

	// 2026-09-14, TTP-68. This block used to assert a per-device sum of
	// 6,820,987,840 and a gap of 818,173,440, and the comment above it said
	// the literals pinned a KNOWN DEFECT rather than a contract: "when
	// per-device active bytes are recorded exactly, the gap becomes 0 and this
	// block must be updated with a dated comment — it failing is the fix
	// landing, not a regression." That is what happened. The recorder now
	// answers the question while it still has the tensor names
	// (placement.WithModel), so the sum is the record and the gap is 0.
	//
	// The old gap is not gone, it is EXPLAINED: 818,173,440 is exactly
	// wsExpertsDense × (1 − 6/384) = 831,160,320 × 378/384, the router and the
	// shared expert being scaled by the sparse fraction instead of counted in
	// full. TestExpertsSplitWSLegacy below still pins it on a pre-TTP-68 tape.
	tied := tiedEmbeddings(s.Placement)
	var perDevice int64
	for _, d := range s.Placement.Devices {
		perDevice += activeBytesOn(d, s, tied)
	}
	if want := int64(wsActiveBytesPerToken); perDevice != want {
		t.Fatalf("per-device active sum = %d, want the record %d (delta %d)", perDevice, want, perDevice-want)
	}
	if gap := s.Model.ActiveBytesPerToken - perDevice; gap != 0 {
		t.Fatalf("gap = %d, want 0", gap)
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

// TestExpertsSplitWSLegacy keeps the defect TTP-56 measured pinned where it
// still lives: a tape recorded before DevicePlacement.ActiveBytesPerToken
// existed. There activeBytesOn has only the class totals, scales the router
// and the shared expert with the sparse stack, and loses 818,173,440 bytes a
// token — 10.71 % of the record, past SplitTolerance, which is why that
// recording printed no "of peak" ratio at all.
//
// The fallback is not dead code: every tape already on disk is this shape.
func TestExpertsSplitWSLegacy(t *testing.T) {
	s := wsLegacySummary()

	tied := tiedEmbeddings(s.Placement)
	var perDevice int64
	for _, d := range s.Placement.Devices {
		perDevice += activeBytesOn(d, s, tied)
	}
	if want := int64(6_820_987_840); perDevice != want {
		t.Fatalf("per-device active sum = %d, want %d", perDevice, want)
	}
	gap := s.Model.ActiveBytesPerToken - perDevice
	if want := int64(818_173_440); gap != want {
		t.Fatalf("gap = %d, want %d", gap, want)
	}
	// And the gap is precisely the dense experts scaled when they should not
	// have been, which is what makes it an explanation rather than a residue.
	if want := wsExpertsDense * (384 - 6) / 384; gap != int64(want) {
		t.Errorf("gap = %d, want wsExpertsDense × (1 − 6/384) = %d", gap, int64(want))
	}
	if ratio := float64(gap) / float64(s.Model.ActiveBytesPerToken); ratio <= SplitTolerance {
		t.Fatalf("gap is %.4f of the record, expected it to exceed SplitTolerance %.2f", ratio, SplitTolerance)
	}

	// Which is why a legacy tape still cannot print a ratio, even given a host
	// bandwidth: the check that guards the ratio is the one that fails.
	s.Host.RAMBytesPerSec = wsSTREAMBytesPerSec
	s.Host.RAMSource = tape.RAMSourceMeasured
	if _, ok := Ceiling(s); ok {
		t.Error("Ceiling solved a legacy tape whose per-device sum is 10.7 % off the record")
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
	// 2026-09-14, TTP-68. This assertion used to be `if r.Exact` — "the
	// PLACEMENT cannot prove that, only the -ot string can, so Exact stays
	// false". The placement can prove it now: the recorder read the CPU's own
	// tensor names and wrote what they are read for, so the figure is not an
	// attribution that might be wrong, it is the answer. Exact claims proof,
	// and the proof is now in the tape.
	//
	// The old reasoning is not repealed, it no longer applies here — it still
	// holds on a pre-TTP-68 tape, which is what TestRAMLegacyNotExact pins.
	if !r.Exact {
		t.Error("Exact should be true: the device states its own active bytes")
	}
}

// TestRAMLegacyNotExact is the sentence that used to be TestRAMWS's: on a tape
// with no per-device figure, a CPU carrying ClassExperts bytes MIGHT be
// carrying router or shared-expert bytes that activeBytesOn scaled when it
// should not have. Nothing in the placement can rule that out, so the figure
// does not get to claim proof — "we could not check" is not "we checked and it
// is fine".
func TestRAMLegacyNotExact(t *testing.T) {
	s := wsLegacySummary()
	r, ok := RAM(s)
	if !ok {
		t.Fatal("RAM not ok on the legacy recording")
	}
	// The figure itself is right on this run, and unchanged: the -ot rule was
	// "...exps=CPU", which matches neither _shexp nor ffn_gate_inp.
	if r.ActiveBytesPerToken != wsCPUActive {
		t.Errorf("ActiveBytesPerToken = %d, want %d", r.ActiveBytesPerToken, wsCPUActive)
	}
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
		// The per-device figure is the same tensors counted, so it moves with
		// them: a CPU holding only an embedding matrix and an engram table
		// reads nothing per token.
		s.Placement.Devices[0].ActiveBytesPerToken = 0
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
	s := wsLegacySummary()
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
	s := wsLegacySummary()
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
	s := wsLegacySummary()
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

// TestSpeculativeCarriesItsOwnRatio: Verify says what share of the host bus it
// used and whether the count behind it is exact (2026-09-14).
//
// Both were the card's arithmetic before: internal/card divided
// RAMBytesPerSec by HostBytesPerSec itself and borrowed RAMSide.Exact as the
// gate — a renderer deciding the provenance of a figure this package built out
// of its own per-device active bytes. They are decided here now, where the
// inputs live, and the two sides of the package must agree: the step and the
// per-token rate are two arithmetics over one attribution.
func TestSpeculativeCarriesItsOwnRatio(t *testing.T) {
	s := wsSummary()
	// The operator's STREAM figure for this box; procmon cannot read it.
	s.Host.RAMBytesPerSec = 115_800_000_000
	s.Host.RAMSource = tape.RAMSourceMeasured

	v, ok := Speculative(s)
	if !ok {
		t.Fatal("Speculative not ok on the ws recording")
	}
	want := float64(v.RAMBytesPerSec) / float64(s.Host.RAMBytesPerSec)
	if v.OfPeak != want {
		t.Errorf("OfPeak = %v, want %v", v.OfPeak, want)
	}
	if v.OfPeak < 0.93 || v.OfPeak > 0.97 {
		t.Errorf("OfPeak = %v, want ≈ 95 %% — the run is at the RAM wall", v.OfPeak)
	}
	r, okRAM := RAM(s)
	if !okRAM {
		t.Fatal("RAM not ok")
	}
	if v.Exact != r.Exact {
		t.Errorf("Verify.Exact = %v but RAMSide.Exact = %v; one attribution, one answer", v.Exact, r.Exact)
	}

	// Without a host figure there is no ratio, and 0 is how that is said —
	// never a percentage of a ceiling nobody observed (CLAUDE.md).
	bare := wsSummary()
	if v, ok := Speculative(bare); !ok || v.OfPeak != 0 {
		t.Errorf("OfPeak = %v with no host bandwidth observed", v.OfPeak)
	}
}

// TestSpeculativeExactFollowsTheSameRuleAsRAM: the exactness of the verify
// step is the exactness of the CPU's active bytes, decided by cpuActiveExact
// for both. A pre-TTP-68 tape whose class split does not solve leaves both
// false — "we could not check" is not "we checked and it is fine".
func TestSpeculativeExactFollowsTheSameRuleAsRAM(t *testing.T) {
	exact := wsSummary() // per-device active bytes recorded
	v, ok := Speculative(exact)
	if !ok || !v.Exact {
		t.Errorf("a post-TTP-68 recording should be exact: ok=%v exact=%v", ok, v.Exact)
	}

	legacy := wsLegacySummary() // no per-device figure; the CPU holds experts
	v, ok = Speculative(legacy)
	if !ok {
		t.Fatal("Speculative not ok on the legacy shape")
	}
	r, okRAM := RAM(legacy)
	if !okRAM {
		t.Fatal("RAM not ok on the legacy shape")
	}
	if v.Exact != r.Exact {
		t.Errorf("Verify.Exact = %v but RAMSide.Exact = %v on one tape", v.Exact, r.Exact)
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

// TestSpeculativeNeverMixesASumWithAMean is the one that matters for honesty.
// At run level DraftN is the SUM over streams while Timings.PredictedN is the
// per-stream MEAN (tape.TimingsSummary, TTP-30); on the ws code recording the
// subtraction gives 250 − 719 = −469 steps.
//
// 2026-09-14, TTP-67. This used to be TestSpeculativeRefusesConcurrentRuns and
// asserted that Concurrency > 1 is refused outright. That was never the
// principle — the principle is that the two figures must be of the same kind —
// and refusing N > 1 meant the figure never appeared on the card the README
// actually shows, which is a two-stream run. So the rule is now: a concurrent
// run is answered from Aggregate.TotalPredictedN, the matching SUM, and
// refused when that is absent. It is still never answered from PredictedN.
func TestSpeculativeNeverMixesASumWithAMean(t *testing.T) {
	s := wsSummary()
	s.Concurrency = 2
	s.Timings.PredictedN = 250
	dn, da := 834, 719
	s.Timings.DraftN, s.Timings.DraftNAccepted = &dn, &da
	// No Aggregate.TotalPredictedN on this fixture: the only sum available is
	// missing, and the mean sitting right there must not be reached for.
	if s.Aggregate.TotalPredictedN != 0 {
		t.Fatal("precondition: this fixture should carry no server-wide token count")
	}
	if v, ok := Speculative(s); ok {
		t.Errorf("Speculative returned %+v for a 2-stream run with no aggregate; the only other figure is a mean", v)
	}

	// Given the matching sum, the same run is answerable — and the answer is
	// built on 834 total tokens, not on the 250 per-stream mean.
	s.Aggregate.TotalPredictedN = 834
	s.Aggregate.AggregatePredictedPerSecond = 40.0
	v, ok := Speculative(s)
	if !ok {
		t.Fatal("Speculative should answer a concurrent run given TotalPredictedN")
	}
	if v.Steps != 834-719 {
		t.Errorf("Steps = %d, want %d", v.Steps, 834-719)
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

// TestWSPrintsARatio is the end-to-end sentence TTP-68 and TTP-45 exist for,
// and neither one alone produces it.
//
// The ws recording is the machine the card is meant to settle arguments about,
// and it printed no "of peak" ratio at all: the per-device active sum was
// 10.71 % off the record (TTP-68) AND the CPU leg had no bandwidth, because
// RAMSpeed × RAMChannels needs the root-only DMI tables (TTP-45). Fix one and
// the ratio is still absent. Fix both and the card has its number.
func TestWSPrintsARatio(t *testing.T) {
	s := wsSummary()
	s.Host.RAMBytesPerSec = wsSTREAMBytesPerSec
	s.Host.RAMSource = tape.RAMSourceMeasured

	bps, source, ok := HostBandwidth(s.Host)
	if !ok {
		t.Fatal("HostBandwidth not ok with a stated figure")
	}
	if bps != wsSTREAMBytesPerSec || source != tape.RAMSourceMeasured {
		t.Fatalf("HostBandwidth = %d/%q, want %d/%q", bps, source, int64(wsSTREAMBytesPerSec), tape.RAMSourceMeasured)
	}

	ceiling, ok := Ceiling(s)
	if !ok {
		t.Fatal("Ceiling not ok: the ratio is still missing on the very machine the ticket is about")
	}
	// Σa / Σ(a/bw) over CPU 3.220 GB at 115.8 GB/s (27.81 ms), GPU0 2.127 GB at
	// 768 GB/s (2.77 ms) and GPU1 2.292 GB at 936.2 GB/s (2.45 ms): the host
	// leg is 84 % of the floor time, so the harmonic mean sits at twice the
	// host bus and nowhere near the GPUs'.
	if ceiling < 230_000_000_000 || ceiling > 233_000_000_000 {
		t.Errorf("Ceiling = %d, want ≈ 231.3 GB/s", ceiling)
	}
	if ceiling <= wsSTREAMBytesPerSec {
		t.Errorf("Ceiling %d should exceed the host bus alone (%d)", ceiling, int64(wsSTREAMBytesPerSec))
	}

	ratio, ok := OfPeak(s)
	if !ok {
		t.Fatal("OfPeak not ok")
	}
	if ratio < 0.66 || ratio > 0.68 {
		t.Errorf("OfPeak = %.4f, want ≈ 0.672 (155.4 GB/s of a 231.3 GB/s ceiling)", ratio)
	}

	// And the RAM side now has a percentage to print, which is what the
	// operator stating the figure bought.
	r, ok := RAM(s)
	if !ok {
		t.Fatal("RAM not ok")
	}
	if want := float64(r.BytesPerSec) / wsSTREAMBytesPerSec; math.Abs(r.OfPeak-want) > 1e-12 {
		t.Errorf("RAM OfPeak = %v, want %v", r.OfPeak, want)
	}
	if r.OfPeak < 0.50 || r.OfPeak > 0.62 {
		t.Errorf("RAM OfPeak = %.4f, want ≈ 0.57", r.OfPeak)
	}
}

// heroSummary is the v0.1.0 hero — a two-stream run of DeepSeek V4.1 Flash
// Q3_K_M on the ws box — reduced to what this package reads. The placement is
// the same as the prose recording's; what differs is that it ran two streams,
// which is why TTP-67 exists: the card anyone sees is a concurrent run, so a
// verify-step figure that refuses N > 1 is a figure nobody sees.
//
// It was assets/hero.tape until 2026-09-14, when that asset was re-recorded
// for v0.2.0 on a box serving a different quantisation with a different offload
// split. So this is now a fixture and nothing else, and it is worth keeping as
// one: it is the run the mean-versus-sum bug was found on, with the sign of
// PredictedN - DraftNAccepted still negative, which the new recording does not
// reproduce. TestExplainHeroTape reads the shipped asset directly and asserts
// what is true of it today.
func heroSummary() *tape.RunSummary {
	s := wsSummary()
	draftN, accepted := 459, 324
	s.Concurrency = 2
	s.Timings.PredictedN = 240 // the per-stream MEAN
	s.Timings.PredictedMs = 17730.489999999998
	s.Timings.PredictedPerSecond = 13.480323588557033
	s.Timings.DraftN, s.Timings.DraftNAccepted = &draftN, &accepted
	s.Timings.EffectiveBandwidthBytesPerSec = 102_978_365_999
	s.Aggregate = tape.AggregateTimings{
		Streams:                     2,
		WallMs:                      19072.689775,
		TotalPromptN:                60,
		TotalPredictedN:             480, // the SUM, which is what DraftN is too
		AggregatePredictedPerSecond: 26.874264340353704,
		PerStreamPredictedPerSecond: 13.480323588557033,
		SlotsBusyMax:                2,
	}
	return s
}

// TestSpeculativeHero is TTP-67: the verify-step view on the run the README
// actually shows. The subtraction is between two SUMS — Aggregate.TotalPredictedN
// and DraftNAccepted — where the old per-stream PredictedN would have mixed a
// mean into it and given 240 − 324 = −84 steps.
func TestSpeculativeHero(t *testing.T) {
	s := heroSummary()

	// Precondition, and the reason the aggregate is needed: the per-stream
	// figure does not even have the right sign here.
	if s.Timings.PredictedN-*s.Timings.DraftNAccepted > 0 {
		t.Fatal("precondition: the per-stream PredictedN should give a negative step count on this run")
	}

	v, ok := Speculative(s)
	if !ok {
		t.Fatal("Speculative not ok on the two-stream hero run")
	}
	if v.Streams != 2 {
		t.Errorf("Streams = %d, want 2", v.Streams)
	}
	if v.Steps != 156 {
		t.Errorf("Steps = %d, want 156 (total_predicted_n 480 − accepted 324)", v.Steps)
	}
	// --spec-draft-n-max was 3, so the target model saw batches of up to four.
	if math.Abs(v.Batch-3.9423) > 0.001 {
		t.Errorf("Batch = %v, want ≈ 3.9423 (459/156 + 1)", v.Batch)
	}
	if math.Abs(v.DistinctExpertsPerLayer-23.115) > 0.01 {
		t.Errorf("DistinctExpertsPerLayer = %v, want ≈ 23.115", v.DistinctExpertsPerLayer)
	}
	// The decode window is TotalPredictedN / the aggregate rate = 17.861 s.
	if math.Abs(v.StepsPerSec-8.734) > 0.01 {
		t.Errorf("StepsPerSec = %v, want ≈ 8.734", v.StepsPerSec)
	}
	if v.RAMBytesPerStep < 12_350_000_000 || v.RAMBytesPerStep > 12_460_000_000 {
		t.Errorf("RAMBytesPerStep = %d, want ≈ 12.41 GB", v.RAMBytesPerStep)
	}
	if v.RAMBytesPerSec < 107_000_000_000 || v.RAMBytesPerSec > 110_000_000_000 {
		t.Errorf("RAMBytesPerSec = %d, want ≈ 108.4 GB/s", v.RAMBytesPerSec)
	}
	if math.Abs(v.AcceptRate-324.0/459.0) > 1e-9 {
		t.Errorf("AcceptRate = %v, want %v", v.AcceptRate, 324.0/459.0)
	}

	// The headline: against the stated STREAM figure this run is at 94 % of
	// the host bus, where the per-accepted-token view reads as ≈ 75 % and the
	// card's whole-model 103 GB/s reads as headroom that is not there.
	if pct := float64(v.RAMBytesPerSec) / wsSTREAMBytesPerSec; pct < 0.92 || pct > 0.95 {
		t.Errorf("RAM at %.3f of the STREAM figure, want ≈ 0.936", pct)
	}
	perToken, ok := RAM(s)
	if !ok {
		t.Fatal("RAM not ok")
	}
	if v.RAMBytesPerSec <= perToken.BytesPerSec {
		t.Errorf("per-step %d should exceed per-accepted-token %d", v.RAMBytesPerSec, perToken.BytesPerSec)
	}
}

// TestSpeculativeConcurrentGuards: a concurrent run without the server-wide
// token count, or whose arithmetic does not describe a real batch, reports
// nothing rather than a number.
func TestSpeculativeConcurrentGuards(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*tape.RunSummary)
	}{
		{"no server-wide token count", func(s *tape.RunSummary) { s.Aggregate.TotalPredictedN = 0 }},
		{"no aggregate rate to give a window", func(s *tape.RunSummary) { s.Aggregate.AggregatePredictedPerSecond = 0 }},
		{"more accepted than predicted leaves no steps", func(s *tape.RunSummary) {
			a := s.Aggregate.TotalPredictedN
			s.Timings.DraftNAccepted = &a
		}},
		{"a batch under one token is not a verify step", func(s *tape.RunSummary) {
			// draft_n 0 is caught earlier; a negative one is the shape that
			// would produce batch < 1 if it were not rejected outright.
			n := -1
			s.Timings.DraftN = &n
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := heroSummary()
			c.mut(s)
			if v, ok := Speculative(s); ok {
				t.Errorf("Speculative returned %+v it cannot know", v)
			}
		})
	}
}

// TestSpeculativeSingleStreamUnchanged: the concurrent clause must not move
// the one-stream answer, which is still PredictedN over PredictedMs.
func TestSpeculativeSingleStreamUnchanged(t *testing.T) {
	s := wsSummary()
	v, ok := Speculative(s)
	if !ok {
		t.Fatal("Speculative not ok")
	}
	if v.Streams != 1 {
		t.Errorf("Streams = %d, want 1", v.Streams)
	}
	// An aggregate present on a single-stream run must be ignored: the window
	// that Timings.EffectiveBandwidthBytesPerSec is defined against is the
	// per-stream one.
	s.Aggregate.TotalPredictedN = 9999
	s.Aggregate.AggregatePredictedPerSecond = 1
	v2, ok := Speculative(s)
	if !ok {
		t.Fatal("Speculative not ok with an aggregate present")
	}
	if v2 != v {
		t.Errorf("single-stream answer changed with an aggregate present:\n got %+v\nwant %+v", v2, v)
	}
}

// TestDeviceExpertsSplitWS is what TTP-56 left open — "which DEVICE holds them
// is not recoverable" — closed. With each device's own active bytes recorded,
// the router and shared-expert bytes can be located, not just counted.
//
// It is also the self-consistency proof of this file's GPU fixture values.
// wsGPU0ActiveExact and wsGPU1ActiveExact are DERIVED from the recording's
// Layers strings (blocks 0-20 on GPU0, 21-39 on GPU1), and until now the only
// evidence for them was that they sum to the record — which a compensating
// pair of errors would also do. Solving each device back out independently and
// landing on 21 and 19 layers of router + shared expert is the check that a
// mis-attribution between the two GPUs could not survive.
func TestDeviceExpertsSplitWS(t *testing.T) {
	s := wsSummary()
	tied := tiedEmbeddings(s.Placement)
	const perLayerDense = 3_932_160 + 16_846_848 // router + shared expert

	cases := []struct {
		device    string
		wantDense int64
	}{
		// The -ot rule was "...exps=CPU", which matches neither ffn_gate_inp
		// nor _shexp, so nothing dense was moved here.
		{tape.DeviceCPU, 0},
		{"GPU0", 21 * perLayerDense},
		{"GPU1", 19 * perLayerDense},
	}
	var totalDense int64
	for _, c := range cases {
		d, found := device(s.Placement, c.device)
		if !found {
			t.Fatalf("device %s missing", c.device)
		}
		split, ok := DeviceExpertsSplit(d, s.Model, tied)
		if !ok {
			t.Fatalf("%s: DeviceExpertsSplit not ok", c.device)
		}
		if split.Dense != c.wantDense {
			t.Errorf("%s Dense = %d, want %d layers × %d = %d",
				c.device, split.Dense, c.wantDense/perLayerDense, int64(perLayerDense), c.wantDense)
		}
		if got := split.Sparse + split.Dense; got != d.Classes[tape.ClassExperts] {
			t.Errorf("%s Sparse+Dense = %d, want the class total %d", c.device, got, d.Classes[tape.ClassExperts])
		}
		totalDense += split.Dense
	}
	// The per-device answers must add up to the model-wide one ExpertsSplit
	// solves independently, and to 40 layers of the model's own tensors.
	if totalDense != wsExpertsDense {
		t.Errorf("dense bytes located = %d, want %d", totalDense, int64(wsExpertsDense))
	}
	if want := int64(40 * perLayerDense); totalDense != want {
		t.Errorf("dense bytes = %d, want 40 × %d = %d", totalDense, int64(perLayerDense), want)
	}
}

func TestDeviceExpertsSplitUnknown(t *testing.T) {
	s := wsSummary()
	tied := tiedEmbeddings(s.Placement)
	gpu0, _ := device(s.Placement, "GPU0")

	t.Run("a tape with no per-device figure", func(t *testing.T) {
		// The whole point: the class totals alone cannot answer this, which is
		// why the recorder had to.
		legacy, _ := device(wsLegacySummary().Placement, "GPU0")
		if _, ok := DeviceExpertsSplit(legacy, s.Model, tied); ok {
			t.Error("solved a split the tape does not carry the figures for")
		}
	})
	t.Run("a device with no experts solves trivially", func(t *testing.T) {
		d := tape.DevicePlacement{
			Device:              "GPU9",
			ActiveBytesPerToken: 1000,
			Classes:             map[tape.TensorClass]int64{tape.ClassAttention: 1000},
		}
		split, ok := DeviceExpertsSplit(d, s.Model, tied)
		if !ok || split != (ExpertsClassBytes{}) {
			t.Errorf("got %+v/%v, want an empty split and ok", split, ok)
		}
	})
	t.Run("every expert routed leaves nothing to split", func(t *testing.T) {
		m := s.Model
		m.NExpertsUsed = m.NExperts
		if _, ok := DeviceExpertsSplit(gpu0, m, tied); ok {
			t.Error("solved a split of a model that is not sparse")
		}
	})
	t.Run("no expert counts observed", func(t *testing.T) {
		m := s.Model
		m.NExperts, m.NExpertsUsed = 0, 0
		if _, ok := DeviceExpertsSplit(gpu0, m, tied); ok {
			t.Error("solved a split with no expert counts")
		}
	})
	t.Run("a figure the class rule cannot explain", func(t *testing.T) {
		// More active bytes than the device holds: no sparse/dense split of
		// its experts class reaches it, so the answer is nothing rather than a
		// clamped number that looks like one.
		d := gpu0
		d.ActiveBytesPerToken = d.Bytes * 2
		if split, ok := DeviceExpertsSplit(d, s.Model, tied); ok {
			t.Errorf("got %+v, want no answer", split)
		}
	})
	t.Run("a figure below the dense floor", func(t *testing.T) {
		d := gpu0
		d.ActiveBytesPerToken = 1
		if split, ok := DeviceExpertsSplit(d, s.Model, tied); ok {
			t.Errorf("got %+v, want no answer", split)
		}
	})
}
