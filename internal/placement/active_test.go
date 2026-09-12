package placement

import "testing"

func TestActiveBytesPerTokenMoE(t *testing.T) {
	ts := synModel()

	// 2 of 8 experts per token: only the stacked expert weights scale.
	got := ActiveBytesPerToken(ts, 2, 8)
	want := int64(synLayers*(bAttn+bNorm+bGate+bShexp+bNorm) + // dense, every token
		synLayers*bExps*2/8 + // stacked experts, 2 of 8
		bOut + bNorm) // output projection and its norm
	// token_embd is excluded (a lookup reads one row) and the engram table is
	// excluded (not a per-token matmul).
	if got != want {
		t.Errorf("ActiveBytesPerToken = %d, want %d (delta %d)", got, want, got-want)
	}
	if got >= totalBytes(ts) {
		t.Errorf("active bytes %d should be well below the full model %d", got, totalBytes(ts))
	}
}

func TestActiveBytesPerTokenDense(t *testing.T) {
	// A dense model reports 0 experts; every weight is read per token.
	ts := []Tensor{
		NewTensor("blk.0.attn_q.weight", 100),
		NewTensor("blk.0.ffn_down.weight", 200),
		NewTensor("token_embd.weight", 1000),
		NewTensor("output.weight", 300),
	}
	if got, want := ActiveBytesPerToken(ts, 0, 0), int64(600); got != want {
		t.Errorf("ActiveBytesPerToken = %d, want %d", got, want)
	}
}

func TestActiveBytesPerTokenTiedEmbeddings(t *testing.T) {
	// A tied-embedding model (Gemma, the smaller Qwen3s) has no output.weight:
	// the embedding matrix IS the output projection and is read in full every
	// token. It still has output_norm.weight, so the presence of the output
	// CLASS must not be what decides this.
	tied := []Tensor{
		NewTensor("blk.0.attn_q.weight", 100),
		NewTensor("token_embd.weight", 1000),
		NewTensor("output_norm.weight", 5),
	}
	if got, want := ActiveBytesPerToken(tied, 0, 0), int64(1105); got != want {
		t.Errorf("tied: ActiveBytesPerToken = %d, want %d", got, want)
	}

	// The same model with a real output projection: the embedding matrix is
	// only a row lookup and drops out.
	untied := append(append([]Tensor{}, tied...), NewTensor("output.weight", 300))
	if got, want := ActiveBytesPerToken(untied, 0, 0), int64(405); got != want {
		t.Errorf("untied: ActiveBytesPerToken = %d, want %d", got, want)
	}
}

func TestActiveBytesPerTokenAllExpertsUsed(t *testing.T) {
	ts := []Tensor{NewTensor("blk.0.ffn_down_exps.weight", 800)}
	if got, want := ActiveBytesPerToken(ts, 8, 8), int64(800); got != want {
		t.Errorf("ActiveBytesPerToken = %d, want %d", got, want)
	}
}

func totalBytes(ts []Tensor) int64 {
	var n int64
	for _, t := range ts {
		n += t.Bytes
	}
	return n
}
