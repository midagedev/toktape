package placement

import (
	"sort"
	"strconv"
	"strings"

	"github.com/midagedev/toktape/internal/tape"
)

// Placement sources written into tape.PlacementSummary.Source.
const (
	// SourceGGUFArgs: derived from the GGUF tensor headers plus the server's
	// own -ngl/-ot/-cmoe arguments.
	SourceGGUFArgs = "gguf+args"
	// SourceUnknown: the arguments that decide placement were not observed,
	// so no device breakdown is claimed.
	SourceUnknown = "unknown"
)

// Estimate replays llama.cpp's placement rules over the tensor headers and
// the server's flags.
//
// gpus is the number of GPU devices llama.cpp was given; with gpus == 0 every
// tensor is on the CPU whatever -ngl said. lazy says whether the server loads
// the n-gram / engram tables lazily; when it does, those bytes are reported as
// NeverLoadedBytes and left out of the device totals so the card cannot count
// them twice (handover lesson 3).
//
// Warnings about dropped -ot rules are discarded; use EstimateVerbose to keep
// them for RunSummary.Warnings.
func Estimate(tensors []Tensor, flags tape.ServerFlags, gpus int, lazy bool) tape.PlacementSummary {
	s, _ := EstimateVerbose(tensors, flags, gpus, lazy)
	return s
}

// EstimateVerbose is Estimate plus the human-readable warnings it produced
// (an -ot rule Go's RE2 cannot compile, an unknown buffer type). The caller
// appends them to tape.RunSummary.Warnings, which the card prints verbatim.
func EstimateVerbose(tensors []Tensor, flags tape.ServerFlags, gpus int, lazy bool) (tape.PlacementSummary, []string) {
	if gpus < 0 {
		gpus = 0
	}

	sum := tape.PlacementSummary{Source: SourceUnknown}

	// NeverLoadedBytes is read off the tensor headers, never as
	// total-minus-RSS. It is known even when the placement is not.
	if lazy {
		for _, t := range tensors {
			if t.Class == tape.ClassNGram {
				sum.NeverLoadedBytes += t.Bytes
			}
		}
	}

	rules, warnings := ParseOverrides(flags.OverrideTens)
	if all, firstN, ok := ParseCPUMoE(flags.CPUMoE); ok {
		// llama.cpp appends the -cmoe/-ncmoe rules to the same vector the -ot
		// rules went into, in command-line order. tape.ServerFlags does not
		// preserve the interleaving, so the user's explicit -ot rules are kept
		// first: an -ot the user typed is the more deliberate instruction.
		rules = append(rules, cpuMoEOverrides(all, firstN)...)
	}

	ngl, nglOK := ParseNGL(flags.NGL)
	if !nglOK && gpus > 0 {
		// The flag that decides which layers were offloaded was not observed.
		// Guessing a default here would print a device breakdown nobody
		// measured, so the summary stays unknown. (With gpus == 0 there is
		// nothing to guess: llama.cpp keeps everything on the CPU.)
		return sum, warnings
	}

	nLayerAll := 0
	for _, t := range tensors {
		if t.Layer >= nLayerAll {
			nLayerAll = t.Layer + 1
		}
	}

	// llama-model.cpp, llama_model_base::load_tensors:
	//
	//	n_gpu_layers  = params.n_gpu_layers >= 0 ? params.n_gpu_layers : n_layer_all + 1
	//	i_gpu_start   = max(n_layer_all + 1 - n_gpu_layers, 0)
	//	act_gpu_layers= devices.empty() ? 0 : min(n_gpu_layers, n_layer_all + 1)
	//
	// There are n_layer_all + 1 offloadable slots: the blocks 0..n_layer_all-1
	// and the output at index n_layer_all. -ngl N offloads the LAST N of them.
	// (The toktape design note says "layers < N"; upstream counts from the
	// end, which is why -ngl 20 on a 40-block model puts the deep blocks on
	// the GPU, not the shallow ones.)
	nGPU := ngl
	if ngl == NGLAll {
		nGPU = nLayerAll + 1
	}
	iGPUStart := nLayerAll + 1 - nGPU
	if iGPUStart < 0 {
		iGPUStart = 0
	}
	actGPULayers := 0
	if gpus > 0 {
		actGPULayers = min(nGPU, nLayerAll+1)
	}

	type bucket struct {
		bytes   int64
		classes map[tape.TensorClass]int64
		tensors []Tensor
	}
	buckets := map[string]*bucket{}
	get := func(dev string) *bucket {
		b := buckets[dev]
		if b == nil {
			b = &bucket{classes: map[tape.TensorClass]int64{}}
			buckets[dev] = b
		}
		return b
	}
	// Always present, even at zero bytes: an empty GPU is a finding.
	get(tape.DeviceCPU)
	for i := 0; i < gpus; i++ {
		get(gpuDevice(i))
	}

	for _, t := range tensors {
		if lazy && t.Class == tape.ClassNGram {
			continue // counted in NeverLoadedBytes, not resident anywhere
		}
		dev := deviceFor(t, rules, nLayerAll, iGPUStart, actGPULayers, gpus)
		b := get(dev)
		b.bytes += t.Bytes
		b.classes[t.Class] += t.Bytes
		b.tensors = append(b.tensors, t)
	}

	for _, dev := range sortedDevices(buckets) {
		b := buckets[dev]
		sum.Devices = append(sum.Devices, tape.DevicePlacement{
			Device:  dev,
			Bytes:   b.bytes,
			Classes: b.classes,
			Layers:  LayersSummary(b.tensors),
		})
		if dev != tape.DeviceCPU {
			sum.VRAMWeightsBytes += b.bytes
		}
	}
	sum.Source = SourceGGUFArgs
	return sum, warnings
}

// deviceFor decides one tensor's device, in llama.cpp's own order.
func deviceFor(t Tensor, rules []Override, nLayerAll, iGPUStart, actGPULayers, gpus int) string {
	// 1. -ot / -cmoe / -ncmoe. Checked before the layer assignment and first
	//    match wins (llama-model-loader.cpp, "check overrides").
	for _, r := range rules {
		if r.Matches(t.Name) {
			return r.Device
		}
	}

	// 2. The input layer is never offloaded: "there is very little benefit to
	//    offloading the input layer, so always keep it on the CPU"
	//    (llama-model.cpp, dev_input).
	//
	//    The n-gram / engram tables ride with it. INFERENCE, NOT A
	//    TRANSCRIPTION: llama-arch.cpp's LLM_TENSOR_INFOS has no ngram or
	//    engram tensor at all, so there is no upstream rule to copy. Every
	//    GET_ROWS lookup table it does define — TOKEN_EMBD, POS_EMBD,
	//    TOKEN_TYPES, PER_LAYER_TOKEN_EMBD, MASKED_EMBD_CENTROIDS,
	//    MASKED_EMBD_ORDERING — is LLM_TENSOR_LAYER_INPUT, which is pinned to
	//    the CPU unconditionally, and -ngl only moves numbered slots. Putting
	//    these tables in the output slot instead would have reported the demo
	//    machine's 84.6 GB of never-read engram tables as VRAM weights on a
	//    24 GB card. A fork that really does offload them needs an -ot rule,
	//    which is checked above and still wins.
	if t.Class == tape.ClassEmbed || t.Class == tape.ClassNGram {
		return tape.DeviceCPU
	}

	// 3. The slot this tensor belongs to. Block tensors use their block index;
	//    everything else (output, output_norm, stray rope tables) rides with
	//    the output slot at index n_layer_all.
	slot := t.Layer
	if slot < 0 {
		slot = nLayerAll
	}
	if slot < iGPUStart || (slot-iGPUStart) >= actGPULayers {
		return tape.DeviceCPU
	}

	// 4. Which GPU. llama.cpp walks the cumulative tensor-split points:
	//    upper_bound(splits, (il - i_gpu_start) / act_gpu_layers). With an even
	//    split those points are (i+1)/gpus and the whole expression collapses
	//    to the integer division below.
	//
	//    ESTIMATE: with no --tensor-split llama.cpp weights the split by each
	//    device's FREE memory, which toktape cannot see after the fact. An
	//    even split is the closest honest stand-in, and it is exact whenever
	//    the devices are identical and idle.
	if gpus <= 1 {
		return gpuDevice(0)
	}
	idx := (slot - iGPUStart) * gpus / actGPULayers
	if idx >= gpus {
		idx = gpus - 1
	}
	return gpuDevice(idx)
}

// sortedDevices orders the device names deterministically: CPU first, then
// GPU0, GPU1, ... and anything unrecognised last, alphabetically.
func sortedDevices[T any](m map[string]T) []string {
	names := make([]string, 0, len(m))
	for k := range m {
		names = append(names, k)
	}
	rank := func(s string) (int, int) {
		if s == tape.DeviceCPU {
			return 0, 0
		}
		if n, err := strconv.Atoi(strings.TrimPrefix(s, "GPU")); err == nil && strings.HasPrefix(s, "GPU") {
			return 1, n
		}
		return 2, 0
	}
	sort.Slice(names, func(i, j int) bool {
		ri, ni := rank(names[i])
		rj, nj := rank(names[j])
		if ri != rj {
			return ri < rj
		}
		if ni != nj {
			return ni < nj
		}
		return names[i] < names[j]
	})
	return names
}

// classAbbrev is the short name each class gets in a Layers string, in the
// order the string prints them.
var classAbbrev = []struct {
	class tape.TensorClass
	short string
}{
	{tape.ClassAttention, "attn"},
	{tape.ClassFFN, "ffn"},
	{tape.ClassExperts, "exps"},
	{tape.ClassNGram, "ngram"},
	{tape.ClassEmbed, "embd"},
	{tape.ClassOutput, "out"},
	{tape.ClassOther, "other"},
}

// LayersSummary renders the tensors placed on ONE device as the human string
// that goes into tape.DevicePlacement.Layers, e.g. "0-39 attn, 0-11 exps".
//
// Each class present gets its block indices compressed into ranges. Classes
// that belong to no block (embeddings, output) print their name alone.
// Returns "" for an empty device.
func LayersSummary(tensors []Tensor) string {
	byClass := map[tape.TensorClass]map[int]bool{}
	for _, t := range tensors {
		if byClass[t.Class] == nil {
			byClass[t.Class] = map[int]bool{}
		}
		byClass[t.Class][t.Layer] = true
	}

	var parts []string
	for _, ca := range classAbbrev {
		layers, ok := byClass[ca.class]
		if !ok {
			continue
		}
		if r := compressLayers(layers); r != "" {
			parts = append(parts, r+" "+ca.short)
		} else {
			parts = append(parts, ca.short)
		}
	}
	return strings.Join(parts, ", ")
}

// compressLayers turns a set of block indices into "0-11,20-39". NoLayer
// entries are dropped; an all-NoLayer set yields "".
func compressLayers(set map[int]bool) string {
	nums := make([]int, 0, len(set))
	for n := range set {
		if n >= 0 {
			nums = append(nums, n)
		}
	}
	if len(nums) == 0 {
		return ""
	}
	sort.Ints(nums)

	var b strings.Builder
	for i := 0; i < len(nums); {
		j := i
		for j+1 < len(nums) && nums[j+1] == nums[j]+1 {
			j++
		}
		if b.Len() > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.Itoa(nums[i]))
		if j > i {
			b.WriteByte('-')
			b.WriteString(strconv.Itoa(nums[j]))
		}
		i = j + 1
	}
	return b.String()
}
