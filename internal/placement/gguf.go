package placement

import (
	"fmt"
	"path/filepath"

	gguf "github.com/gpustack/gguf-parser-go"

	"github.com/midagedev/toktape/internal/tape"
)

// ModelInfoFromFile reads a GGUF header — never the tensor data — and returns
// the model's card line plus every tensor, classified and sized.
//
// A sharded model ("...-00001-of-00009.gguf") is handled by the parser: given
// any shard's path it opens all of them, merges the tensor lists and sums the
// file sizes. Every shard must be present next to the one named, which is how
// llama.cpp loads them too.
//
// Fields the header does not carry are left at ""/0. Nothing is defaulted:
// the parser's own Metadata() falls back to architecture "llama" when the key
// is absent, so the keys are read directly instead.
func ModelInfoFromFile(path string) (tape.ModelInfo, []Tensor, error) {
	// SkipLargeMetadata drops the tokenizer arrays, which are megabytes of
	// data placement has no use for.
	f, err := gguf.ParseGGUFFile(path, gguf.SkipLargeMetadata())
	if err != nil {
		return tape.ModelInfo{}, nil, fmt.Errorf("read gguf header %s: %w", path, err)
	}

	kv := f.Header.MetadataKV
	info := tape.ModelInfo{
		Path:      path,
		FileName:  filepath.Base(path),
		Name:      kvString(kv, "general.name"),
		Arch:      kvString(kv, "general.architecture"),
		FileBytes: int64(f.Size),
		Params:    int64(f.ModelParameters),
	}

	// The file name carries tags the file_type enum cannot express
	// (UD-Q4_K_M vs Q4_K_M are both file_type 15), so it wins when present.
	info.Quant = QuantFromFileName(info.FileName)
	if info.Quant == "" {
		if ft, ok := kvUint(kv, "general.file_type"); ok {
			info.Quant = QuantFromFileType(uint32(ft))
		}
	}

	if info.Arch != "" {
		info.NLayers = kvInt(kv, info.Arch+".block_count")
		info.NExperts = kvInt(kv, info.Arch+".expert_count")
		info.NExpertsUsed = kvInt(kv, info.Arch+".expert_used_count")
		info.CtxTrain = kvInt(kv, info.Arch+".context_length")
	}

	tensors := make([]Tensor, 0, len(f.TensorInfos))
	for _, ti := range f.TensorInfos {
		tensors = append(tensors, NewTensor(ti.Name, int64(ti.Bytes())))
	}

	info.ActiveBytesPerToken = ActiveBytesPerToken(tensors, info.NExpertsUsed, info.NExperts)
	return info, tensors, nil
}

// kvString reads a string metadata value, or "" when the key is absent or
// holds another type. The parser's typed accessors panic on a mismatch, so
// the type is checked first.
func kvString(kv gguf.GGUFMetadataKVs, key string) string {
	v, ok := kv.Get(key)
	if !ok || v.ValueType != gguf.GGUFMetadataValueTypeString {
		return ""
	}
	return v.ValueString()
}

// kvUint reads any numeric metadata value as a uint64.
func kvUint(kv gguf.GGUFMetadataKVs, key string) (uint64, bool) {
	v, ok := kv.Get(key)
	if !ok || !v.ValueType.IsNumeric() {
		return 0, false
	}
	return gguf.ValueNumeric[uint64](v), true
}

// kvInt reads a numeric metadata value as an int, or 0 when absent.
func kvInt(kv gguf.GGUFMetadataKVs, key string) int {
	n, ok := kvUint(kv, key)
	if !ok {
		return 0
	}
	return int(n)
}
