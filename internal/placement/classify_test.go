package placement

import (
	"testing"

	"github.com/midagedev/toktape/internal/tape"
)

func TestClassify(t *testing.T) {
	tests := []struct {
		name      string
		wantLayer int
		wantClass tape.TensorClass
	}{
		// Embeddings and output never carry a block index.
		{"token_embd.weight", NoLayer, tape.ClassEmbed},
		{"token_embd_norm.weight", NoLayer, tape.ClassEmbed},
		{"output.weight", NoLayer, tape.ClassOutput},
		{"output_norm.weight", NoLayer, tape.ClassOutput},

		// Attention.
		{"blk.0.attn_q.weight", 0, tape.ClassAttention},
		{"blk.7.attn_k_norm.weight", 7, tape.ClassAttention},
		{"blk.31.attn_output.weight", 31, tape.ClassAttention},
		{"blk.31.attn_norm.weight", 31, tape.ClassAttention},

		// MoE: stacked experts, router and shared expert all bucket as
		// experts, even though only the stacked ones are sparsely activated.
		{"blk.12.ffn_down_exps.weight", 12, tape.ClassExperts},
		{"blk.12.ffn_gate_up_exps.weight", 12, tape.ClassExperts},
		{"blk.12.ffn_down_chexps.weight", 12, tape.ClassExperts},
		{"blk.3.ffn_gate_inp.weight", 3, tape.ClassExperts},
		{"blk.5.ffn_up_shexp.weight", 5, tape.ClassExperts},
		{"blk.5.ffn_gate_shexp.weight", 5, tape.ClassExperts},

		// Dense feed-forward.
		{"blk.1.ffn_down.weight", 1, tape.ClassFFN},
		{"blk.1.ffn_up.weight", 1, tape.ClassFFN},
		{"blk.1.ffn_norm.weight", 1, tape.ClassFFN},

		// n-gram / engram tables, with and without a block index.
		{"engram.table.0.weight", NoLayer, tape.ClassNGram},
		{"ngram.embd.weight", NoLayer, tape.ClassNGram},
		{"blk.2.n_gram_proj.weight", 2, tape.ClassNGram},

		// Anything else.
		{"rope_freqs.weight", NoLayer, tape.ClassOther},
		{"blk.9.something_new.weight", 9, tape.ClassOther},

		// Anchoring: these must NOT be read as output / embeddings / a block.
		{"blk.4.attn_output_norm.weight", 4, tape.ClassAttention},
		{"cls.output.weight", NoLayer, tape.ClassOther},
		{"enc.blk.0.attn_q.weight", NoLayer, tape.ClassOther},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotLayer, gotClass := Classify(tc.name)
			if gotLayer != tc.wantLayer || gotClass != tc.wantClass {
				t.Errorf("Classify(%q) = (%d, %q), want (%d, %q)",
					tc.name, gotLayer, gotClass, tc.wantLayer, tc.wantClass)
			}
		})
	}
}

func TestIsSparseExpert(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{"blk.12.ffn_down_exps.weight", true},
		{"blk.12.ffn_up_exps.weight", true},
		{"blk.12.ffn_gate_exps.weight", true},
		{"blk.12.ffn_gate_up_exps.weight", true},
		{"blk.12.ffn_down_chexps.weight", true},
		// The router and the shared expert run for every token.
		{"blk.3.ffn_gate_inp.weight", false},
		{"blk.5.ffn_up_shexp.weight", false},
		{"blk.0.attn_q.weight", false},
		{"token_embd.weight", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := (Tensor{Name: tc.name}).IsSparseExpert(); got != tc.want {
				t.Errorf("IsSparseExpert(%q) = %v, want %v", tc.name, got, tc.want)
			}
		})
	}
}
