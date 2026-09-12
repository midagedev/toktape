package placement

import (
	"regexp"
	"strconv"

	"github.com/midagedev/toktape/internal/tape"
)

// Tensor is one GGUF tensor reduced to what placement needs: how big it is,
// which block it belongs to, and which class it falls in.
type Tensor struct {
	// Name is the GGUF tensor name, verbatim (e.g. "blk.12.ffn_down_exps.weight").
	Name string
	// Bytes is the tensor's size on disk, from the header's dimensions and type.
	Bytes int64
	// Layer is the block index parsed out of "blk.<N>.", or -1 for tensors
	// that belong to no block (token_embd, output, output_norm, ...).
	Layer int
	// Class buckets the tensor for the placement view.
	Class tape.TensorClass
}

// NoLayer is Tensor.Layer for a tensor that belongs to no transformer block.
const NoLayer = -1

var (
	// blkPrefixRe pulls the block index out of a repeating-layer tensor name.
	// Anchored: "cls.blk.0.weight" is not a block tensor.
	blkPrefixRe = regexp.MustCompile(`^blk\.(\d+)\.`)

	// ngramRe matches the n-gram / engram lookup tables. Checked before every
	// other rule because such a table can live inside a block too, and because
	// it is the class that drives NeverLoadedBytes.
	ngramRe = regexp.MustCompile(`(?i)(n_?gram|engram)`)

	// embedRe matches the input embedding matrix.
	embedRe = regexp.MustCompile(`^token_embd`)

	// outputRe matches the output projection and its norm. Anchored so that
	// "blk.0.attn_output.weight" is not classified as output.
	outputRe = regexp.MustCompile(`^output(\.|_norm|$)`)

	// expertRe matches the MoE tensors inside a block: the per-expert weight
	// stacks ("_exps" / "_chexps"), the router ("ffn_gate_inp") and the shared
	// expert ("_shexp"). Checked before the dense FFN rule, since
	// "ffn_up_shexp" and "ffn_down_exps" both start with "ffn_".
	expertRe = regexp.MustCompile(`(_exps|_chexps|ffn_gate_inp|_shexp)`)

	// attnRe and ffnRe classify the remainder of a block tensor.
	attnRe = regexp.MustCompile(`^attn_`)
	ffnRe  = regexp.MustCompile(`^ffn_`)

	// sparseExpertRe matches only the tensors that are sparsely activated: the
	// stacked per-expert weights. The router and the shared expert run for
	// every token and are therefore not sparse. Mirrors llama.cpp's
	// LLM_FFN_EXPS_REGEX (common/common.h).
	sparseExpertRe = regexp.MustCompile(`\.ffn_(up|down|gate|gate_up)_(ch|)exps`)
)

// Classify buckets a GGUF tensor name into its block index and class.
//
// Layer is the "blk.<N>." index, or NoLayer when the name carries none. The
// rules are applied in a fixed order; the first that matches wins:
//
//	*ngram* / *engram*      -> ClassNGram
//	token_embd*             -> ClassEmbed
//	output* / output_norm   -> ClassOutput
//	blk.N.*_exps / *_chexps
//	       / ffn_gate_inp
//	       / *_shexp        -> ClassExperts
//	blk.N.attn_*            -> ClassAttention
//	blk.N.ffn_*             -> ClassFFN
//	anything else           -> ClassOther
func Classify(name string) (layer int, class tape.TensorClass) {
	layer = NoLayer
	rest := name
	if m := blkPrefixRe.FindStringSubmatch(name); m != nil {
		// The regex guarantees digits, so a parse failure can only be an
		// out-of-range index; leave it as NoLayer in that case.
		if n, err := strconv.Atoi(m[1]); err == nil {
			layer = n
			// Everything after "blk.<N>." is the tensor's own role, so the
			// anchors below cannot match a substring of the block prefix.
			rest = name[len(m[0]):]
		}
	}

	switch {
	case ngramRe.MatchString(name):
		return layer, tape.ClassNGram
	case embedRe.MatchString(name):
		return NoLayer, tape.ClassEmbed
	case outputRe.MatchString(name):
		return NoLayer, tape.ClassOutput
	}

	if layer == NoLayer {
		return NoLayer, tape.ClassOther
	}

	switch {
	case expertRe.MatchString(rest):
		return layer, tape.ClassExperts
	case attnRe.MatchString(rest):
		return layer, tape.ClassAttention
	case ffnRe.MatchString(rest):
		return layer, tape.ClassFFN
	}
	return layer, tape.ClassOther
}

// NewTensor builds a Tensor from a name and size, classifying it.
func NewTensor(name string, bytes int64) Tensor {
	layer, class := Classify(name)
	return Tensor{Name: name, Bytes: bytes, Layer: layer, Class: class}
}

// IsSparseExpert reports whether the tensor is a stacked per-expert weight,
// i.e. one of the tensors of which only ExpertUsedCount of ExpertCount slices
// are read per token. The MoE router and the shared expert are class
// ClassExperts but are read in full every token, so they are not sparse.
func (t Tensor) IsSparseExpert() bool {
	return sparseExpertRe.MatchString(t.Name)
}
