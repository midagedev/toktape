package placement

import (
	"fmt"
	"slices"
	"testing"

	"github.com/midagedev/toktape/internal/tape"
)

// Sizes of the synthetic model's tensors. Distinct powers of two so that a
// misplaced tensor cannot be masked by a coincidental sum.
const (
	synLayers = 40

	bAttn  = 64 << 20  // blk.N.attn_q.weight
	bNorm  = 1 << 20   // blk.N.attn_norm.weight and blk.N.ffn_norm.weight
	bExps  = 512 << 20 // blk.N.ffn_down_exps.weight  (sparsely activated)
	bGate  = 2 << 20   // blk.N.ffn_gate_inp.weight   (router, dense)
	bShexp = 32 << 20  // blk.N.ffn_up_shexp.weight   (shared expert, dense)

	bEmbd  = 1024 << 20 // token_embd.weight
	bOut   = 768 << 20  // output.weight
	bNgram = 4096 << 20 // engram.table.0.weight

	// perLayer is every tensor of one block.
	perLayer = bAttn + bNorm + bExps + bGate + bShexp + bNorm
)

// synModel builds a 40-block MoE: attention, a dense FFN norm, stacked
// experts, a router and a shared expert per block, plus embeddings, output and
// an engram table.
func synModel() []Tensor {
	var ts []Tensor
	for i := 0; i < synLayers; i++ {
		p := fmt.Sprintf("blk.%d.", i)
		ts = append(ts,
			NewTensor(p+"attn_q.weight", bAttn),
			NewTensor(p+"attn_norm.weight", bNorm),
			NewTensor(p+"ffn_down_exps.weight", bExps),
			NewTensor(p+"ffn_gate_inp.weight", bGate),
			NewTensor(p+"ffn_up_shexp.weight", bShexp),
			NewTensor(p+"ffn_norm.weight", bNorm),
		)
	}
	ts = append(ts,
		NewTensor("token_embd.weight", bEmbd),
		NewTensor("output.weight", bOut),
		NewTensor("output_norm.weight", bNorm),
		NewTensor("engram.table.0.weight", bNgram),
	)
	return ts
}

// deviceByName finds a device in the summary; it must exist.
func deviceByName(t *testing.T, s tape.PlacementSummary, name string) tape.DevicePlacement {
	t.Helper()
	for _, d := range s.Devices {
		if d.Device == name {
			return d
		}
	}
	t.Fatalf("device %q missing from summary (have %v)", name, deviceNames(s))
	return tape.DevicePlacement{}
}

func deviceNames(s tape.PlacementSummary) []string {
	var out []string
	for _, d := range s.Devices {
		out = append(out, d.Device)
	}
	return out
}

func checkDevice(t *testing.T, s tape.PlacementSummary, name string, wantBytes int64, wantClasses map[tape.TensorClass]int64) {
	t.Helper()
	d := deviceByName(t, s, name)
	if d.Bytes != wantBytes {
		t.Errorf("%s bytes = %d, want %d (delta %d)", name, d.Bytes, wantBytes, d.Bytes-wantBytes)
	}
	for class, want := range wantClasses {
		if got := d.Classes[class]; got != want {
			t.Errorf("%s class %q = %d, want %d", name, class, got, want)
		}
	}
	for class, got := range d.Classes {
		if _, ok := wantClasses[class]; !ok && got != 0 {
			t.Errorf("%s class %q = %d, want it absent", name, class, got)
		}
	}
}

// TestEstimateNGL99 offloads everything. token_embd stays on the CPU because
// llama.cpp never offloads the input layer.
func TestEstimateNGL99(t *testing.T) {
	s := Estimate(synModel(), tape.ServerFlags{NGL: "99"}, 1, false)

	if s.Source != SourceGGUFArgs {
		t.Errorf("Source = %q, want %q", s.Source, SourceGGUFArgs)
	}
	if s.NeverLoadedBytes != 0 {
		t.Errorf("NeverLoadedBytes = %d, want 0 (lazy is off)", s.NeverLoadedBytes)
	}

	checkDevice(t, s, tape.DeviceCPU, bEmbd+bNgram, map[tape.TensorClass]int64{
		tape.ClassEmbed: bEmbd,
		tape.ClassNGram: bNgram,
	})
	checkDevice(t, s, "GPU0", synLayers*perLayer+bOut+bNorm, map[tape.TensorClass]int64{
		tape.ClassAttention: synLayers * (bAttn + bNorm),
		tape.ClassFFN:       synLayers * bNorm,
		tape.ClassExperts:   synLayers * (bExps + bGate + bShexp),
		tape.ClassOutput:    bOut + bNorm,
	})
	if s.VRAMWeightsBytes != synLayers*perLayer+bOut+bNorm {
		t.Errorf("VRAMWeightsBytes = %d", s.VRAMWeightsBytes)
	}

	if got, want := deviceByName(t, s, "GPU0").Layers, "0-39 attn, 0-39 ffn, 0-39 exps, out"; got != want {
		t.Errorf("GPU0 Layers = %q, want %q", got, want)
	}
	if got, want := deviceByName(t, s, tape.DeviceCPU).Layers, "ngram, embd"; got != want {
		t.Errorf("CPU Layers = %q, want %q", got, want)
	}
}

// TestEstimateNGL99Lazy: the engram table is reported as never loaded and is
// left out of the device totals, so the card cannot count it twice.
func TestEstimateNGL99Lazy(t *testing.T) {
	s := Estimate(synModel(), tape.ServerFlags{NGL: "99"}, 1, true)

	if s.NeverLoadedBytes != bNgram {
		t.Errorf("NeverLoadedBytes = %d, want %d", s.NeverLoadedBytes, int64(bNgram))
	}
	checkDevice(t, s, "GPU0", synLayers*perLayer+bOut+bNorm, map[tape.TensorClass]int64{
		tape.ClassAttention: synLayers * (bAttn + bNorm),
		tape.ClassFFN:       synLayers * bNorm,
		tape.ClassExperts:   synLayers * (bExps + bGate + bShexp),
		tape.ClassOutput:    bOut + bNorm,
	})
}

// TestEstimateNCMoE12: -ncmoe 12 moves the stacked experts of blocks 0..11 to
// the CPU. The router and the shared expert of those blocks stay on the GPU —
// upstream's pattern does not match them.
func TestEstimateNCMoE12(t *testing.T) {
	s := Estimate(synModel(), tape.ServerFlags{NGL: "99", CPUMoE: "-ncmoe 12"}, 1, false)

	checkDevice(t, s, tape.DeviceCPU, bEmbd+bNgram+12*bExps, map[tape.TensorClass]int64{
		tape.ClassEmbed:   bEmbd,
		tape.ClassNGram:   bNgram,
		tape.ClassExperts: 12 * bExps,
	})
	checkDevice(t, s, "GPU0", synLayers*perLayer+bOut+bNorm-12*bExps, map[tape.TensorClass]int64{
		tape.ClassAttention: synLayers * (bAttn + bNorm),
		tape.ClassFFN:       synLayers * bNorm,
		tape.ClassExperts:   synLayers*(bExps+bGate+bShexp) - 12*bExps,
		tape.ClassOutput:    bOut + bNorm,
	})
	if got, want := deviceByName(t, s, tape.DeviceCPU).Layers, "0-11 exps, ngram, embd"; got != want {
		t.Errorf("CPU Layers = %q, want %q", got, want)
	}
}

// TestEstimateCMoE: -cmoe moves every block's stacked experts to the CPU.
func TestEstimateCMoE(t *testing.T) {
	s := Estimate(synModel(), tape.ServerFlags{NGL: "99", CPUMoE: "-cmoe"}, 1, false)

	checkDevice(t, s, tape.DeviceCPU, bEmbd+bNgram+synLayers*bExps, map[tape.TensorClass]int64{
		tape.ClassEmbed:   bEmbd,
		tape.ClassNGram:   bNgram,
		tape.ClassExperts: synLayers * bExps,
	})
	// The routers and shared experts are still on the GPU.
	gpu := deviceByName(t, s, "GPU0")
	if got, want := gpu.Classes[tape.ClassExperts], int64(synLayers*(bGate+bShexp)); got != want {
		t.Errorf("GPU0 experts = %d, want %d", got, want)
	}
}

// TestEstimateOverrideTensor: an explicit -ot regex wins over -ngl, and the
// pattern in the spec must not catch the shared expert or the router.
func TestEstimateOverrideTensor(t *testing.T) {
	s := Estimate(synModel(), tape.ServerFlags{
		NGL:          "99",
		OverrideTens: []string{`blk\.(1[0-9]|2[0-9])\.ffn_.*_exps=CPU`},
	}, 1, false)

	checkDevice(t, s, tape.DeviceCPU, bEmbd+bNgram+20*bExps, map[tape.TensorClass]int64{
		tape.ClassEmbed:   bEmbd,
		tape.ClassNGram:   bNgram,
		tape.ClassExperts: 20 * bExps,
	})
	if got, want := deviceByName(t, s, tape.DeviceCPU).Layers, "10-29 exps, ngram, embd"; got != want {
		t.Errorf("CPU Layers = %q, want %q", got, want)
	}
}

// TestEstimateNGL20TwoGPUs pins the rule the toktape design note got backwards.
//
// llama.cpp offloads the LAST -ngl slots, not the first: with 40 blocks there
// are 41 slots (blocks 0..39 plus the output at 40), i_gpu_start = 41-20 = 21,
// so blocks 0..20 stay on the CPU and blocks 21..39 plus the output go to the
// GPUs, split evenly in order.
func TestEstimateNGL20TwoGPUs(t *testing.T) {
	s := Estimate(synModel(), tape.ServerFlags{NGL: "20"}, 2, false)

	if got, want := deviceNames(s), 3; len(got) != want {
		t.Fatalf("devices = %v, want %d of them", got, want)
	}

	checkDevice(t, s, tape.DeviceCPU, bEmbd+bNgram+21*perLayer, map[tape.TensorClass]int64{
		tape.ClassEmbed:     bEmbd,
		tape.ClassNGram:     bNgram,
		tape.ClassAttention: 21 * (bAttn + bNorm),
		tape.ClassFFN:       21 * bNorm,
		tape.ClassExperts:   21 * (bExps + bGate + bShexp),
	})
	checkDevice(t, s, "GPU0", 10*perLayer, map[tape.TensorClass]int64{
		tape.ClassAttention: 10 * (bAttn + bNorm),
		tape.ClassFFN:       10 * bNorm,
		tape.ClassExperts:   10 * (bExps + bGate + bShexp),
	})
	checkDevice(t, s, "GPU1", 9*perLayer+bOut+bNorm, map[tape.TensorClass]int64{
		tape.ClassAttention: 9 * (bAttn + bNorm),
		tape.ClassFFN:       9 * bNorm,
		tape.ClassExperts:   9 * (bExps + bGate + bShexp),
		tape.ClassOutput:    bOut + bNorm,
	})

	if got, want := deviceByName(t, s, tape.DeviceCPU).Layers, "0-20 attn, 0-20 ffn, 0-20 exps, ngram, embd"; got != want {
		t.Errorf("CPU Layers = %q, want %q", got, want)
	}
	if got, want := deviceByName(t, s, "GPU0").Layers, "21-30 attn, 21-30 ffn, 21-30 exps"; got != want {
		t.Errorf("GPU0 Layers = %q, want %q", got, want)
	}
	if got, want := deviceByName(t, s, "GPU1").Layers, "31-39 attn, 31-39 ffn, 31-39 exps, out"; got != want {
		t.Errorf("GPU1 Layers = %q, want %q", got, want)
	}

	// Every byte is accounted for exactly once.
	var total int64
	for _, d := range s.Devices {
		total += d.Bytes
	}
	if want := int64(synLayers*perLayer + bEmbd + bOut + bNorm + bNgram); total != want {
		t.Errorf("device bytes sum to %d, want %d", total, want)
	}
}

// TestEstimateWithGPUIndices: the option narrows the device set the estimate
// is spread over and names the devices by their host indices (lead, 2026-09-16).
//
// A server launched with CUDA_VISIBLE_DEVICES=0 on a two-GPU box is, to
// llama.cpp, a one-GPU server — and to a recorder that knows only how many
// devices the host has, the even split over both was the only story
// available. The caller that measured which card actually holds the weights
// hands the surviving host indices here, and the split is replayed over
// exactly those devices. FAIL-first: without the option every call below
// spread the model over both GPUs (the package did not compile).
func TestEstimateWithGPUIndices(t *testing.T) {
	flags := tape.ServerFlags{NGL: "20"}
	two := Estimate(synModel(), flags, 2, false) // today's even split, the baseline

	// The one-GPU split carries every byte the two-GPU one put on cards.
	oneGPUBytes := int64(19*perLayer + bOut + bNorm)

	t.Run("only host index 0 in play", func(t *testing.T) {
		s := Estimate(synModel(), flags, 2, false, WithGPUIndices([]int{0}))
		checkDevice(t, s, "GPU0", oneGPUBytes, map[tape.TensorClass]int64{
			tape.ClassAttention: 19 * (bAttn + bNorm),
			tape.ClassFFN:       19 * bNorm,
			tape.ClassExperts:   19 * (bExps + bGate + bShexp),
			tape.ClassOutput:    bOut + bNorm,
		})
		for _, name := range deviceNames(s) {
			if name == "GPU1" {
				t.Errorf("GPU1 is a device of the summary (%v); the estimate was told only GPU0 is in play", deviceNames(s))
			}
		}
		// The CPU side of the split is the narrowing's to keep constant: the
		// input layer never offloads, whatever devices are in play.
		checkDevice(t, s, tape.DeviceCPU, bEmbd+bNgram+21*perLayer, map[tape.TensorClass]int64{
			tape.ClassEmbed:     bEmbd,
			tape.ClassNGram:     bNgram,
			tape.ClassAttention: 21 * (bAttn + bNorm),
			tape.ClassFFN:       21 * bNorm,
			tape.ClassExperts:   21 * (bExps + bGate + bShexp),
		})
	})

	t.Run("only host index 1 in play", func(t *testing.T) {
		s := Estimate(synModel(), flags, 2, false, WithGPUIndices([]int{1}))
		if got, want := deviceNames(s), []string{tape.DeviceCPU, "GPU1"}; !slices.Equal(got, want) {
			t.Fatalf("devices = %v, want %v: the surviving card keeps its host name", got, want)
		}
		checkDevice(t, s, "GPU1", oneGPUBytes, map[tape.TensorClass]int64{
			tape.ClassAttention: 19 * (bAttn + bNorm),
			tape.ClassFFN:       19 * bNorm,
			tape.ClassExperts:   19 * (bExps + bGate + bShexp),
			tape.ClassOutput:    bOut + bNorm,
		})
		if got, want := deviceByName(t, s, "GPU1").Layers, "21-39 attn, 21-39 ffn, 21-39 exps, out"; got != want {
			t.Errorf("GPU1 Layers = %q, want %q", got, want)
		}
	})

	t.Run("two indices split as gpus does", func(t *testing.T) {
		s := Estimate(synModel(), flags, 2, false, WithGPUIndices([]int{0, 1}))
		assertSameSplit(t, s, two)
	})

	t.Run("nil and empty mean not known", func(t *testing.T) {
		assertSameSplit(t, Estimate(synModel(), flags, 2, false, WithGPUIndices(nil)), two)
		assertSameSplit(t, Estimate(synModel(), flags, 2, false, WithGPUIndices([]int{})), two)
	})

	t.Run("every byte is accounted for exactly once", func(t *testing.T) {
		s := Estimate(synModel(), flags, 2, false, WithGPUIndices([]int{0}))
		var total int64
		for _, d := range s.Devices {
			total += d.Bytes
		}
		if want := int64(synLayers*perLayer + bEmbd + bOut + bNorm + bNgram); total != want {
			t.Errorf("device bytes sum to %d, want %d", total, want)
		}
	})
}

// assertSameSplit holds that two summaries spread the model identically:
// same device names, same bytes, same layers on each.
func assertSameSplit(t *testing.T, got, want tape.PlacementSummary) {
	t.Helper()
	if len(got.Devices) != len(want.Devices) {
		t.Fatalf("devices = %v, want %v", deviceNames(got), deviceNames(want))
	}
	for i := range want.Devices {
		g, w := got.Devices[i], want.Devices[i]
		if g.Device != w.Device || g.Bytes != w.Bytes || g.Layers != w.Layers {
			t.Errorf("device %d = %q %d %q, want %q %d %q", i, g.Device, g.Bytes, g.Layers, w.Device, w.Bytes, w.Layers)
		}
	}
}

// TestEstimateNGramStaysOnCPU pins the inference in deviceFor: llama.cpp has
// no n-gram / engram tensor at all, every lookup table it does define is
// LLM_TENSOR_LAYER_INPUT (CPU), and -ngl only moves numbered slots. Reporting
// the demo machine's 84.6 GB of never-read engram tables as VRAM weights on a
// 24 GB card is the failure this rule prevents. An -ot rule still overrides it.
func TestEstimateNGramStaysOnCPU(t *testing.T) {
	s := Estimate(synModel(), tape.ServerFlags{NGL: "99"}, 1, false)
	if got := deviceByName(t, s, tape.DeviceCPU).Classes[tape.ClassNGram]; got != bNgram {
		t.Errorf("CPU ngram bytes = %d, want %d", got, int64(bNgram))
	}

	forced := Estimate(synModel(), tape.ServerFlags{
		NGL:          "99",
		OverrideTens: []string{`engram=CUDA0`},
	}, 1, false)
	if got := deviceByName(t, forced, "GPU0").Classes[tape.ClassNGram]; got != bNgram {
		t.Errorf("-ot should move the engram table to GPU0; got %d bytes there", got)
	}
}

// TestEstimateNGL1 pins the other half of the -ngl rule the design note got
// wrong. The note says the output goes to the GPU only when N > block_count;
// upstream makes the output the LAST of the n_layer_all+1 slots, so -ngl 1
// offloads the output and nothing else.
func TestEstimateNGL1(t *testing.T) {
	s := Estimate(synModel(), tape.ServerFlags{NGL: "1"}, 1, false)

	checkDevice(t, s, "GPU0", bOut+bNorm, map[tape.TensorClass]int64{
		tape.ClassOutput: bOut + bNorm,
	})
	if got, want := deviceByName(t, s, "GPU0").Layers, "out"; got != want {
		t.Errorf("GPU0 Layers = %q, want %q", got, want)
	}
	// Every block and the embeddings stay in RAM.
	checkDevice(t, s, tape.DeviceCPU, bEmbd+synLayers*perLayer+bNgram, map[tape.TensorClass]int64{
		tape.ClassEmbed:     bEmbd,
		tape.ClassAttention: synLayers * (bAttn + bNorm),
		tape.ClassFFN:       synLayers * bNorm,
		tape.ClassExperts:   synLayers * (bExps + bGate + bShexp),
		tape.ClassNGram:     bNgram,
	})
}

// TestEstimateNoGPUs: with no GPU device nothing is offloaded whatever -ngl
// said, and that needs no guess, so the source stays "gguf+args".
func TestEstimateNoGPUs(t *testing.T) {
	s := Estimate(synModel(), tape.ServerFlags{NGL: "99"}, 0, false)

	if s.Source != SourceGGUFArgs {
		t.Errorf("Source = %q, want %q", s.Source, SourceGGUFArgs)
	}
	if len(s.Devices) != 1 || s.Devices[0].Device != tape.DeviceCPU {
		t.Fatalf("devices = %v, want CPU only", deviceNames(s))
	}
	if want := int64(synLayers*perLayer + bEmbd + bOut + bNorm + bNgram); s.Devices[0].Bytes != want {
		t.Errorf("CPU bytes = %d, want %d", s.Devices[0].Bytes, want)
	}
	if s.VRAMWeightsBytes != 0 {
		t.Errorf("VRAMWeightsBytes = %d, want 0", s.VRAMWeightsBytes)
	}
}

// TestEstimateNGLUnknown: with a GPU present but -ngl unobserved the placement
// is not derivable. Unknown prints as "?"; it is never a guessed default.
func TestEstimateNGLUnknown(t *testing.T) {
	s := Estimate(synModel(), tape.ServerFlags{}, 2, true)

	if s.Source != SourceUnknown {
		t.Errorf("Source = %q, want %q", s.Source, SourceUnknown)
	}
	if len(s.Devices) != 0 {
		t.Errorf("Devices = %v, want none", deviceNames(s))
	}
	// The header still tells us what is never loaded.
	if s.NeverLoadedBytes != bNgram {
		t.Errorf("NeverLoadedBytes = %d, want %d", s.NeverLoadedBytes, int64(bNgram))
	}
}

// TestEstimateVerboseWarns surfaces a dropped -ot rule instead of silently
// placing those tensors somewhere else.
func TestEstimateVerboseWarns(t *testing.T) {
	_, warnings := EstimateVerbose(synModel(), tape.ServerFlags{
		NGL:          "99",
		OverrideTens: []string{`blk\.0\.=NVME`},
	}, 1, false)
	if len(warnings) != 1 {
		t.Fatalf("warnings = %v, want 1", warnings)
	}
}

func TestLayersSummary(t *testing.T) {
	tests := []struct {
		name    string
		tensors []Tensor
		want    string
	}{
		{"empty", nil, ""},
		{
			"contiguous range",
			[]Tensor{
				NewTensor("blk.0.attn_q.weight", 1),
				NewTensor("blk.1.attn_q.weight", 1),
				NewTensor("blk.2.attn_q.weight", 1),
			},
			"0-2 attn",
		},
		{
			"split range",
			[]Tensor{
				NewTensor("blk.0.ffn_down_exps.weight", 1),
				NewTensor("blk.1.ffn_down_exps.weight", 1),
				NewTensor("blk.7.ffn_down_exps.weight", 1),
			},
			"0-1,7 exps",
		},
		{
			"class order is fixed",
			[]Tensor{
				NewTensor("output.weight", 1),
				NewTensor("blk.3.ffn_down_exps.weight", 1),
				NewTensor("token_embd.weight", 1),
				NewTensor("blk.3.attn_q.weight", 1),
			},
			"3 attn, 3 exps, embd, out",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := LayersSummary(tc.tensors); got != tc.want {
				t.Errorf("LayersSummary() = %q, want %q", got, tc.want)
			}
		})
	}
}
