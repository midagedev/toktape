package placement

import (
	"testing"

	"github.com/midagedev/toktape/internal/tape"
)

// The synthetic model's expert configuration, taken from the DeepSeek V4.1
// Flash recording TTP-68 was opened about: 384 experts, 6 of them routed per
// token.
const (
	synNExperts     = 384
	synNExpertsUsed = 6
)

// synModelInfo is synModel()'s card line, reduced to what WithModel reads.
func synModelInfo() tape.ModelInfo {
	return tape.ModelInfo{
		NLayers:      synLayers,
		NExperts:     synNExperts,
		NExpertsUsed: synNExpertsUsed,
	}
}

// activeOnLayers is what one device reads per token for the n whole blocks it
// holds: attention and the dense FFN norm in full, the stacked experts at
// used/count, and the router and the shared expert in FULL — they are
// ClassExperts but every token runs both.
func activeOnLayers(n int64) int64 {
	return n * (bAttn + bNorm + bNorm + bExps*synNExpertsUsed/synNExperts + bGate + bShexp)
}

// TestEstimateActiveBytesPerDevice is TTP-68's contract: each device answers
// what it is read for on one token from its OWN tensors, so the per-device
// figures sum to ModelInfo.ActiveBytesPerToken exactly rather than to a
// class-proportion estimate that is 10.7 % low.
//
// The layout is the two-GPU -ngl 20 one of TestEstimateNGL20TwoGPUs: 21 blocks
// plus token_embd and the engram table on the CPU, 10 blocks on GPU0, 9 blocks
// plus output.weight and output_norm.weight on GPU1.
func TestEstimateActiveBytesPerDevice(t *testing.T) {
	ts := synModel()
	s := Estimate(ts, tape.ServerFlags{NGL: "20"}, 2, false, WithModel(synModelInfo()))

	// The CPU reads its 21 blocks and NOTHING else: token_embd is a row
	// lookup on an untied model and the engram table is never read at all.
	if got, want := deviceByName(t, s, tape.DeviceCPU).ActiveBytesPerToken, activeOnLayers(21); got != want {
		t.Errorf("CPU ActiveBytesPerToken = %d, want %d (delta %d)", got, want, got-want)
	}
	if got, want := deviceByName(t, s, "GPU0").ActiveBytesPerToken, activeOnLayers(10); got != want {
		t.Errorf("GPU0 ActiveBytesPerToken = %d, want %d (delta %d)", got, want, got-want)
	}
	// GPU1 also holds the output projection and its norm, both read in full.
	if got, want := deviceByName(t, s, "GPU1").ActiveBytesPerToken, activeOnLayers(9)+bOut+bNorm; got != want {
		t.Errorf("GPU1 ActiveBytesPerToken = %d, want %d (delta %d)", got, want, got-want)
	}

	// The contract that makes the figures usable: they are a partition of the
	// model's own answer, so they sum to it with no residue. Not "within a
	// tolerance" — the same tensors are being added in a different order.
	var sum int64
	for _, d := range s.Devices {
		sum += d.ActiveBytesPerToken
	}
	if want := ActiveBytesPerToken(ts, synNExpertsUsed, synNExperts); sum != want {
		t.Errorf("per-device sum = %d, want ModelInfo.ActiveBytesPerToken %d (delta %d)", sum, want, sum-want)
	}
}

// TestEstimateActiveBytesAsksTiedOfTheWholeModel is the trap TTP-68 names
// explicitly. TiedEmbeddings must be asked of the WHOLE tensor list, once,
// before the devices are walked. Asked of one device's share it finds no
// output.weight on the CPU — on this layout, and on the ws rig, that tensor is
// on GPU1 — calls the model tied, and counts token_embd in full on the very
// device the card is trying to report honestly. That is 1 GiB of phantom
// traffic here and 1.32 GB on the ws recording.
func TestEstimateActiveBytesAsksTiedOfTheWholeModel(t *testing.T) {
	ts := synModel()
	s := Estimate(ts, tape.ServerFlags{NGL: "20"}, 2, false, WithModel(synModelInfo()))

	cpu := deviceByName(t, s, tape.DeviceCPU)
	if cpu.Classes[tape.ClassEmbed] != bEmbd {
		t.Fatalf("precondition: the CPU should hold token_embd; classes = %v", cpu.Classes)
	}
	if deviceByName(t, s, "GPU1").Classes[tape.ClassOutput] == 0 {
		t.Fatal("precondition: output.weight should be on GPU1, which is what makes this the trap case")
	}
	if got, want := cpu.ActiveBytesPerToken, activeOnLayers(21); got != want {
		t.Errorf("CPU ActiveBytesPerToken = %d, want %d; the difference is bEmbd = %d",
			got, want, int64(bEmbd))
	}
}

// TestEstimateActiveBytesTiedModel: with no output.weight anywhere the
// embedding matrix IS the output projection, and the device holding it reads
// it in full.
func TestEstimateActiveBytesTiedModel(t *testing.T) {
	var ts []Tensor
	for _, x := range synModel() {
		if x.Name == "output.weight" {
			continue
		}
		ts = append(ts, x)
	}
	s := Estimate(ts, tape.ServerFlags{NGL: "20"}, 2, false, WithModel(synModelInfo()))

	if got, want := deviceByName(t, s, tape.DeviceCPU).ActiveBytesPerToken, activeOnLayers(21)+bEmbd; got != want {
		t.Errorf("CPU ActiveBytesPerToken = %d, want %d (tied: token_embd counts in full)", got, want)
	}
	var sum int64
	for _, d := range s.Devices {
		sum += d.ActiveBytesPerToken
	}
	if want := ActiveBytesPerToken(ts, synNExpertsUsed, synNExperts); sum != want {
		t.Errorf("per-device sum = %d, want %d", sum, want)
	}
}

// TestEstimateActiveBytesUnknownWithoutModel: without the expert counts there
// is no honest per-device figure — counting the expert stack in full would
// claim a dense model nobody observed — so the field stays 0 and a reader
// falls back. Unknown is 0 (CLAUDE.md).
func TestEstimateActiveBytesUnknownWithoutModel(t *testing.T) {
	s := Estimate(synModel(), tape.ServerFlags{NGL: "20"}, 2, false)
	for _, d := range s.Devices {
		if d.ActiveBytesPerToken != 0 {
			t.Errorf("%s ActiveBytesPerToken = %d with no model info, want 0",
				d.Device, d.ActiveBytesPerToken)
		}
	}
}

// TestEstimateActiveBytesDenseModel: a model with no experts at all reads
// every weight it holds, and the sum still reconstructs the record.
func TestEstimateActiveBytesDenseModel(t *testing.T) {
	ts := synModel()
	m := tape.ModelInfo{NLayers: synLayers} // NExperts == 0: dense
	s := Estimate(ts, tape.ServerFlags{NGL: "20"}, 2, false, WithModel(m))

	if got, want := deviceByName(t, s, "GPU0").ActiveBytesPerToken, int64(10*perLayer); got != want {
		t.Errorf("GPU0 ActiveBytesPerToken = %d, want %d (dense: every byte it holds)", got, want)
	}
	var sum int64
	for _, d := range s.Devices {
		sum += d.ActiveBytesPerToken
	}
	if want := ActiveBytesPerToken(ts, 0, 0); sum != want {
		t.Errorf("per-device sum = %d, want %d", sum, want)
	}
}

// TestEstimateActiveBytesLazyNGram: under lazy loading the engram tables are
// reported as NeverLoadedBytes and are on no device. They were never in the
// per-token figure either, so the sum is unchanged.
func TestEstimateActiveBytesLazyNGram(t *testing.T) {
	ts := synModel()
	s := Estimate(ts, tape.ServerFlags{NGL: "20"}, 2, true, WithModel(synModelInfo()))

	if s.NeverLoadedBytes != bNgram {
		t.Fatalf("NeverLoadedBytes = %d, want %d", s.NeverLoadedBytes, int64(bNgram))
	}
	var sum int64
	for _, d := range s.Devices {
		sum += d.ActiveBytesPerToken
	}
	if want := ActiveBytesPerToken(ts, synNExpertsUsed, synNExperts); sum != want {
		t.Errorf("per-device sum = %d, want %d", sum, want)
	}
}

// TestEstimateActiveBytesUnknownPlacement: when the flag that decides
// placement was not observed the summary claims no devices, and there is
// nothing to attribute.
func TestEstimateActiveBytesUnknownPlacement(t *testing.T) {
	s := Estimate(synModel(), tape.ServerFlags{}, 2, false, WithModel(synModelInfo()))
	if s.Source != SourceUnknown {
		t.Fatalf("Source = %q, want %q", s.Source, SourceUnknown)
	}
	if len(s.Devices) != 0 {
		t.Errorf("Devices = %v, want none", deviceNames(s))
	}
}

// TestEstimateVerboseAcceptsWithModel pins the exact call shape
// internal/recorder/record.go makes, so the one-argument change that adopts
// this (adding placement.WithModel(r.model) to its EstimateVerbose call) is
// known to compile and to fill the field. The recorder already has r.model by
// then: collectModel runs before collectPlacement.
func TestEstimateVerboseAcceptsWithModel(t *testing.T) {
	ts := synModel()
	s, warns := EstimateVerbose(ts, tape.ServerFlags{NGL: "20"}, 2, false, WithModel(synModelInfo()))
	if len(warns) != 0 {
		t.Errorf("warnings = %v, want none", warns)
	}
	var sum int64
	for _, d := range s.Devices {
		sum += d.ActiveBytesPerToken
	}
	if want := ActiveBytesPerToken(ts, synNExpertsUsed, synNExperts); sum != want {
		t.Errorf("per-device sum = %d, want %d", sum, want)
	}
}
