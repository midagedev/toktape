package gguf

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"github.com/midagedev/toktape/internal/placement"
	"github.com/midagedev/toktape/internal/tape"
)

// --- minimal GGUF v3 writer -------------------------------------------------
//
// Written here rather than checked in as a binary so the fixture's every byte
// is readable. The layout is the one in
// https://github.com/ggml-org/ggml/blob/master/docs/gguf.md#file-structure:
//
//	magic u32 | version u32 | tensor_count u64 | kv_count u64
//	kv*      : key(u64 len + bytes) | value_type u32 | value
//	tensor*  : name(u64 len + bytes) | n_dims u32 | dims u64* | type u32 | offset u64
//	padding to alignment, then the tensor data

const (
	ggufMagicLE     = 0x46554747 // "GGUF"
	ggufVersion3    = 3
	ggufAlignment   = 32
	ggufTypeUint32  = 4
	ggufTypeString  = 8
	ggmlTypeF32     = 0
	ggmlF32TypeSize = 4
)

type ggufKV struct {
	key   string
	typ   uint32
	value any // string or uint32
}

type ggufTensor struct {
	name   string
	dims   []uint64
	offset uint64
}

func (t ggufTensor) elements() uint64 {
	n := uint64(1)
	for _, d := range t.dims {
		n *= d
	}
	return n
}

func (t ggufTensor) bytes() uint64 { return t.elements() * ggmlF32TypeSize }

func putString(b *bytes.Buffer, s string) {
	_ = binary.Write(b, binary.LittleEndian, uint64(len(s)))
	b.WriteString(s)
}

// writeGGUF builds a valid single-shard GGUF v3 file at path.
func writeGGUF(t *testing.T, path string, kvs []ggufKV, tensors []ggufTensor) {
	t.Helper()

	var b bytes.Buffer
	_ = binary.Write(&b, binary.LittleEndian, uint32(ggufMagicLE))
	_ = binary.Write(&b, binary.LittleEndian, uint32(ggufVersion3))
	_ = binary.Write(&b, binary.LittleEndian, uint64(len(tensors)))
	_ = binary.Write(&b, binary.LittleEndian, uint64(len(kvs)))

	for _, kv := range kvs {
		putString(&b, kv.key)
		_ = binary.Write(&b, binary.LittleEndian, kv.typ)
		switch v := kv.value.(type) {
		case string:
			putString(&b, v)
		case uint32:
			_ = binary.Write(&b, binary.LittleEndian, v)
		default:
			t.Fatalf("fixture: unsupported kv type %T for key %q", kv.value, kv.key)
		}
	}

	for _, ti := range tensors {
		putString(&b, ti.name)
		_ = binary.Write(&b, binary.LittleEndian, uint32(len(ti.dims)))
		for _, d := range ti.dims {
			_ = binary.Write(&b, binary.LittleEndian, d)
		}
		_ = binary.Write(&b, binary.LittleEndian, uint32(ggmlTypeF32))
		_ = binary.Write(&b, binary.LittleEndian, ti.offset)
	}

	// Pad to the alignment, then write the (zeroed) tensor data so the file is
	// as long as the offsets claim.
	for b.Len()%ggufAlignment != 0 {
		b.WriteByte(0)
	}
	var dataLen uint64
	for _, ti := range tensors {
		if end := ti.offset + ti.bytes(); end > dataLen {
			dataLen = end
		}
	}
	b.Write(make([]byte, dataLen))

	if err := os.WriteFile(path, b.Bytes(), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
}

// synFixtureTensors is the tensor set of the synthetic header fixture: an
// embedding matrix, one MoE block, one attention block and an output.
func synFixtureTensors() []ggufTensor {
	return []ggufTensor{
		{name: "token_embd.weight", dims: []uint64{8, 16}, offset: 0},              // 512 B
		{name: "blk.0.ffn_down_exps.weight", dims: []uint64{4, 4, 8}, offset: 512}, // 512 B
		{name: "blk.1.attn_q.weight", dims: []uint64{8, 8}, offset: 1024},          // 256 B
		{name: "output.weight", dims: []uint64{8, 16}, offset: 1280},               // 512 B
	}
}

// --- tests ------------------------------------------------------------------

func TestModelInfo(t *testing.T) {
	// A non-llama architecture on purpose: gguf-parser-go's own Metadata()
	// defaults the architecture to "llama", and a "llama" fixture would hide
	// the difference between reading the key and inheriting that default.
	const arch = "qwen3moe"
	dir := t.TempDir()
	path := filepath.Join(dir, "Synthetic-MoE-4B-A1B-UD-Q4_K_M.gguf")

	writeGGUF(t, path, []ggufKV{
		{"general.architecture", ggufTypeString, arch},
		{"general.name", ggufTypeString, "Synthetic MoE"},
		{"general.file_type", ggufTypeUint32, uint32(15)},
		{arch + ".block_count", ggufTypeUint32, uint32(2)},
		{arch + ".expert_count", ggufTypeUint32, uint32(8)},
		{arch + ".expert_used_count", ggufTypeUint32, uint32(2)},
		{arch + ".context_length", ggufTypeUint32, uint32(32768)},
	}, synFixtureTensors())

	info, tensors, err := ModelInfo(path)
	if err != nil {
		t.Fatalf("ModelInfo: %v", err)
	}

	if info.Arch != arch {
		t.Errorf("Arch = %q, want %q", info.Arch, arch)
	}
	if info.Name != "Synthetic MoE" {
		t.Errorf("Name = %q", info.Name)
	}
	// The file name's UD- tag beats the file_type enum, which only knows Q4_K_M.
	if info.Quant != "UD-Q4_K_M" {
		t.Errorf("Quant = %q, want %q", info.Quant, "UD-Q4_K_M")
	}
	if info.NLayers != 2 || info.NExperts != 8 || info.NExpertsUsed != 2 || info.CtxTrain != 32768 {
		t.Errorf("hparams = layers %d, experts %d/%d, ctx %d",
			info.NLayers, info.NExpertsUsed, info.NExperts, info.CtxTrain)
	}
	if info.Params != 128+128+64+128 {
		t.Errorf("Params = %d, want %d", info.Params, 128+128+64+128)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.FileBytes != st.Size() {
		t.Errorf("FileBytes = %d, want %d", info.FileBytes, st.Size())
	}
	if info.FileName != filepath.Base(path) || info.Path != path {
		t.Errorf("Path/FileName = %q / %q", info.Path, info.FileName)
	}

	if len(tensors) != 4 {
		t.Fatalf("got %d tensors, want 4", len(tensors))
	}
	want := map[string]struct {
		bytes int64
		layer int
		class tape.TensorClass
	}{
		"token_embd.weight":          {512, placement.NoLayer, tape.ClassEmbed},
		"blk.0.ffn_down_exps.weight": {512, 0, tape.ClassExperts},
		"blk.1.attn_q.weight":        {256, 1, tape.ClassAttention},
		"output.weight":              {512, placement.NoLayer, tape.ClassOutput},
	}
	for _, got := range tensors {
		w, ok := want[got.Name]
		if !ok {
			t.Errorf("unexpected tensor %q", got.Name)
			continue
		}
		if got.Bytes != w.bytes || got.Layer != w.layer || got.Class != w.class {
			t.Errorf("%s = (%d B, layer %d, %q), want (%d B, layer %d, %q)",
				got.Name, got.Bytes, got.Layer, got.Class, w.bytes, w.layer, w.class)
		}
	}

	// 2 of 8 experts: 512 + 256 + 512/4 = 896. token_embd is excluded because
	// output.weight exists.
	if info.ActiveBytesPerToken != 256+512+128 {
		t.Errorf("ActiveBytesPerToken = %d, want %d", info.ActiveBytesPerToken, 256+512+128)
	}
}

// TestModelInfoMissingKeys pins rule 2: a header without the keys
// yields "" and 0, never the parser's "llama" default.
func TestModelInfoMissingKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nameless.gguf")
	writeGGUF(t, path, []ggufKV{
		{"general.quantization_version", ggufTypeUint32, uint32(2)},
	}, synFixtureTensors())

	info, tensors, err := ModelInfo(path)
	if err != nil {
		t.Fatalf("ModelInfo: %v", err)
	}
	if info.Arch != "" {
		t.Errorf("Arch = %q, want \"\" (the parser defaults this to \"llama\")", info.Arch)
	}
	if info.Name != "" {
		t.Errorf("Name = %q, want \"\"", info.Name)
	}
	if info.Quant != "" {
		t.Errorf("Quant = %q, want \"\"", info.Quant)
	}
	if info.NLayers != 0 || info.NExperts != 0 || info.NExpertsUsed != 0 || info.CtxTrain != 0 {
		t.Errorf("hparams should all be 0, got layers %d, experts %d/%d, ctx %d",
			info.NLayers, info.NExpertsUsed, info.NExperts, info.CtxTrain)
	}
	if len(tensors) != 4 {
		t.Errorf("got %d tensors, want 4", len(tensors))
	}
}

func TestModelInfoNotGGUF(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notamodel.gguf")
	if err := os.WriteFile(path, []byte("this is not a gguf file at all"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ModelInfo(path); err == nil {
		t.Fatal("want an error for a non-GGUF file")
	}
}

// TestModelInfoSharded checks the multi-shard path: given any shard,
// the parser opens all of them and merges the tensor lists.
func TestModelInfoSharded(t *testing.T) {
	const arch = "qwen3moe"
	dir := t.TempDir()
	kvs := []ggufKV{
		{"general.architecture", ggufTypeString, arch},
		{arch + ".block_count", ggufTypeUint32, uint32(2)},
	}

	shard1 := filepath.Join(dir, "Split-Model-Q3_K_M-00001-of-00002.gguf")
	shard2 := filepath.Join(dir, "Split-Model-Q3_K_M-00002-of-00002.gguf")
	writeGGUF(t, shard1, kvs, []ggufTensor{
		{name: "token_embd.weight", dims: []uint64{8, 16}, offset: 0},
		{name: "blk.0.attn_q.weight", dims: []uint64{8, 8}, offset: 512},
	})
	writeGGUF(t, shard2, kvs, []ggufTensor{
		{name: "blk.1.attn_q.weight", dims: []uint64{8, 8}, offset: 0},
		{name: "output.weight", dims: []uint64{8, 16}, offset: 256},
	})

	info, tensors, err := ModelInfo(shard1)
	if err != nil {
		t.Fatalf("ModelInfo: %v", err)
	}
	if len(tensors) != 4 {
		t.Fatalf("got %d tensors, want 4 across both shards: %v", len(tensors), tensorNames(tensors))
	}
	// The shard suffix must not swallow the quant tag.
	if info.Quant != "Q3_K_M" {
		t.Errorf("Quant = %q, want %q", info.Quant, "Q3_K_M")
	}
	s1, _ := os.Stat(shard1)
	s2, _ := os.Stat(shard2)
	if want := s1.Size() + s2.Size(); info.FileBytes != want {
		t.Errorf("FileBytes = %d, want the sum of both shards %d", info.FileBytes, want)
	}
}

func tensorNames(ts []placement.Tensor) []string {
	out := make([]string, 0, len(ts))
	for _, t := range ts {
		out = append(out, t.Name)
	}
	return out
}

// TestModelInfoRealGGUF runs against a model on this machine when
// TOKTAPE_GGUF points at one. The synthetic fixtures above cover the parsing
// contract; this is the escape hatch for checking a real header by hand:
//
//	TOKTAPE_GGUF=/models/Qwen3-30B-A3B-UD-Q4_K_M.gguf go test ./internal/placement/gguf/ -run RealGGUF -v
func TestModelInfoRealGGUF(t *testing.T) {
	path := os.Getenv("TOKTAPE_GGUF")
	if path == "" {
		t.Skip("set TOKTAPE_GGUF=<path to a .gguf> to run this")
	}

	info, tensors, err := ModelInfo(path)
	if err != nil {
		t.Fatalf("ModelInfo(%s): %v", path, err)
	}
	if len(tensors) == 0 {
		t.Fatal("no tensors read from the header")
	}
	if info.FileBytes <= 0 {
		t.Errorf("FileBytes = %d", info.FileBytes)
	}

	// Nothing may fall through to ClassOther in bulk: that would mean the
	// classifier does not know this architecture's naming.
	var other, total int64
	for _, ts := range tensors {
		total += ts.Bytes
		if ts.Class == tape.ClassOther {
			other += ts.Bytes
		}
	}
	if other*100/total > 5 {
		t.Errorf("%d%% of bytes classified as %q; the classifier likely does not know this architecture",
			other*100/total, tape.ClassOther)
	}

	s := placement.Estimate(tensors, tape.ServerFlags{NGL: "99"}, 1, false)
	t.Logf("model: %s %s %s, %d layers, %d/%d experts, %d tensors",
		info.Name, info.Arch, info.Quant, info.NLayers, info.NExpertsUsed, info.NExperts, len(tensors))
	t.Logf("active bytes/token: %d of %d total", info.ActiveBytesPerToken, total)
	for _, d := range s.Devices {
		t.Logf("  %-5s %12d B  %s", d.Device, d.Bytes, d.Layers)
	}
}
