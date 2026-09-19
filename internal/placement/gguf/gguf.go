// Package gguf is the one corner of the placement world that touches the
// file system: it reads a model file's GGUF header so the recorder can
// classify and size it. It lives outside internal/placement on purpose —
// a renderer reads a tape and nothing else, and this package is the only
// thing between it and a GGUF parser linked into every render path.
package gguf

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	ggufparser "github.com/gpustack/gguf-parser-go"

	"github.com/midagedev/toktape/internal/placement"
	"github.com/midagedev/toktape/internal/tape"
)

// ModelInfo reads a GGUF header — never the tensor data — and returns
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
func ModelInfo(path string) (tape.ModelInfo, []placement.Tensor, error) {
	// SkipLargeMetadata drops the tokenizer arrays, which are megabytes of
	// data placement has no use for.
	f, err := ggufparser.ParseGGUFFile(path, ggufparser.SkipLargeMetadata())
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

	// The upstream naming convention (ggml docs/gguf.md), which is the one
	// vocabulary two quantizers of a model agree on. The header is the record;
	// the file name is only consulted for the files that predate these keys.
	info.BaseName = kvString(kv, "general.basename")
	info.SizeLabel = kvString(kv, "general.size_label")
	info.FineTune = kvString(kv, "general.finetune")
	info.QuantizedBy = kvString(kv, "general.quantized_by")
	info.RepoURL = kvString(kv, "general.repo_url")
	if info.BaseName != "" || info.SizeLabel != "" {
		info.NameSource = "gguf"
	} else if b, s := NameFromFileName(info.FileName); b != "" {
		info.BaseName, info.SizeLabel, info.NameSource = b, s, "filename"
	}

	// The file name carries tags the file_type enum cannot express
	// (UD-Q4_K_M vs Q4_K_M are both file_type 15), so it wins when present.
	info.Quant = placement.QuantFromFileName(info.FileName)
	if info.Quant == "" {
		if ft, ok := kvUint(kv, "general.file_type"); ok {
			info.Quant = placement.QuantFromFileType(uint32(ft))
		}
	}

	if info.Arch != "" {
		info.NLayers = kvInt(kv, info.Arch+".block_count")
		info.NExperts = kvInt(kv, info.Arch+".expert_count")
		info.NExpertsUsed = kvInt(kv, info.Arch+".expert_used_count")
		info.CtxTrain = kvInt(kv, info.Arch+".context_length")
	}

	tensors := make([]placement.Tensor, 0, len(f.TensorInfos))
	for _, ti := range f.TensorInfos {
		tensors = append(tensors, placement.NewTensor(ti.Name, int64(ti.Bytes())))
	}

	info.ActiveBytesPerToken = placement.ActiveBytesPerToken(tensors, info.NExpertsUsed, info.NExperts)
	return info, tensors, nil
}

// NameFromFileName reads the GGUF naming convention out of a file name, for
// the files whose header does not carry general.basename — and for a run
// recorded against a server whose model file is not on this machine at all,
// where the name from /props is the only thing there is to read. The upstream
// parser returns nil for a name that does not follow the convention, which is
// the property this needs: it refuses instead of guessing.
//
// It deliberately returns no fine-tune. Measured 2026-09-18 against real
// names: the convention's Encoding group does not match a quantizer's own
// tag, so `Qwen3-0.6B-UD-Q6_K_XL.gguf` parses with FineTune "UD-Q6_K_XL" and
// `Qwen3-30B-A3B-Instruct-2507-UD-Q4_K_XL.gguf` with
// "Instruct-2507-UD-Q4_K_XL" — the quantisation wearing the fine-tune's name.
// A fine-tune is only ever general.finetune.
func NameFromFileName(name string) (baseName, sizeLabel string) {
	f := ggufparser.ParseGGUFFilename(name)
	if f == nil {
		return "", ""
	}
	return f.BaseName, withActiveSize(name, f.SizeLabel)
}

// activeSize matches the MoE half of a size label — the "-A3B" in
// "35B-A3B" — where it follows the size label the parser already found.
var activeSize = regexp.MustCompile(`(?i)-(A\d+(?:\.\d+)?[BMK])(?:[-_.]|$)`)

// withActiveSize puts back the active-parameter half of a MoE size label
// when the upstream parser dropped it (lead, 2026-09-19).
//
// The parser is inconsistent about it, and what decides is the quantizer's
// tag rather than anything about the model: "Qwen3-30B-A3B-UD-Q4_K_XL.gguf"
// comes back "30B-A3B" while "Qwen3.6-35B-A3B-UD-Q6_K.gguf" comes back "35B".
// A header for either writes general.size_label "30B-A3B", so without this
// one model holds two ids — measured on the live site, where seven runs sat
// under qwen3.6-35b and one under qwen3.6-35b-a3b (TTP-131). The suffix is
// read out of the name immediately after the size label the parser found,
// so this observes rather than guesses: a name that does not carry one is
// returned unchanged, and the active half is never computed from the
// weights (a byte ratio says 3.5B where the publisher wrote A3B).
func withActiveSize(name, size string) string {
	if size == "" || strings.Contains(strings.ToUpper(size), "-A") {
		return size
	}
	i := strings.Index(strings.ToUpper(name), strings.ToUpper(size))
	if i < 0 {
		return size
	}
	m := activeSize.FindStringSubmatch(name[i+len(size):])
	if m == nil || !strings.HasPrefix(strings.ToUpper(name[i+len(size):]), "-A") {
		return size
	}
	return size + "-" + m[1]
}

// kvString reads a string metadata value, or "" when the key is absent or
// holds another type. The parser's typed accessors panic on a mismatch, so
// the type is checked first.
func kvString(kv ggufparser.GGUFMetadataKVs, key string) string {
	v, ok := kv.Get(key)
	if !ok || v.ValueType != ggufparser.GGUFMetadataValueTypeString {
		return ""
	}
	return v.ValueString()
}

// kvUint reads any numeric metadata value as a uint64.
func kvUint(kv ggufparser.GGUFMetadataKVs, key string) (uint64, bool) {
	v, ok := kv.Get(key)
	if !ok || !v.ValueType.IsNumeric() {
		return 0, false
	}
	return ggufparser.ValueNumeric[uint64](v), true
}

// kvInt reads a numeric metadata value as an int, or 0 when absent.
func kvInt(kv ggufparser.GGUFMetadataKVs, key string) int {
	n, ok := kvUint(kv, key)
	if !ok {
		return 0
	}
	return int(n)
}
