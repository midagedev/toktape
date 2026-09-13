package placement

import (
	"fmt"
	"testing"
)

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

// TestActiveBytesPerTokenWSShape is the ws DeepSeek V4.1 Flash Q3_K_M
// recording, at its real sizes, asserting the figure the card printed
// "≈ 155 GB/s" from: 7,639,161,280 bytes a token.
//
// TTP-56 (2026-09-14) asked whether that figure was over-counted, because
// 155 GB/s is above the machine's 115.8 GB/s STREAM wall for host RAM. It is
// not. Every clause below is load-bearing and the total is wrong if any one of
// them flips:
//
//   - the 384-way expert stacks count at 6/384, and the CPU's share of them is
//     3,220,439,040 a token, which is rig-log's own RAM figure for this model;
//   - the router and the shared expert are ClassExperts but are read by EVERY
//     token, so they count in full — 40 × (3,932,160 + 16,846,848) =
//     831,160,320, which is 10.7 % of the token and the single largest thing a
//     class-only accounting gets wrong (internal/bandwidth/split.go);
//   - the 209 GB of engram tables count for nothing;
//   - the embedding matrix counts for nothing, because output.weight exists.
//
// The 2.2× against rig-log's 3.44 GB is not an error in either: 7.639 GB is
// what a token reads across three buses, 3.22 GB is the host-RAM share alone.
func TestActiveBytesPerTokenWSShape(t *testing.T) {
	// Per-layer tensor sizes, from rig-log's measurement of one expert of this
	// model: up and gate are Q3_K at 5,070,848, down is Q4_K/Q5_K at 6,705,152.
	const (
		nLayers = 40
		nExp    = 384
		nUsed   = 6

		router = 3_932_160                     // blk.N.ffn_gate_inp.weight
		shUp   = 5_070_848                     // blk.N.ffn_up_shexp.weight
		shGate = 5_070_848                     // blk.N.ffn_gate_shexp.weight
		shDown = 6_705_152                     // blk.N.ffn_down_shexp.weight
		shexp  = shUp + shGate + shDown        // 16,846,848
		sparse = 258_767_585_280               // the stacked ffn_*_exps, all layers
		attn   = 1_150_986_496 + 1_034_227_456 // every layer's attention, summed
		ffn    = 430_080 + 389_120             // the stray dense ffn tensors
		other  = 18_346_936 + 17_380_872       // norms and rope tables
		output = 542_996_480                   // output.weight + output_norm.weight
		embed  = 1_323_827_200                 // token_embd.weight
		ngram  = 209_236_612_640               // engram tables, never read
	)

	var ts []Tensor
	for i := 0; i < nLayers; i++ {
		p := fmt.Sprintf("blk.%d.", i)
		ts = append(ts,
			NewTensor(p+"ffn_gate_inp.weight", router),
			NewTensor(p+"ffn_up_shexp.weight", shUp),
			NewTensor(p+"ffn_gate_shexp.weight", shGate),
			NewTensor(p+"ffn_down_shexp.weight", shDown),
			// The stacked experts are not uniform across this model's layers
			// (blocks 0-7 are quantised heavier than 8-39), and the split does
			// not matter here: one tensor carrying the measured total exercises
			// the same rule.
			NewTensor(p+"ffn_down_exps.weight", 0),
		)
	}
	ts[4].Bytes = sparse // the first layer's stack carries the whole measured total
	ts = append(ts,
		NewTensor("blk.0.attn_q.weight", attn),
		NewTensor("blk.0.ffn_down.weight", ffn),
		NewTensor("blk.0.attn_norm.weight", other),
		NewTensor("output.weight", output),
		NewTensor("token_embd.weight", embed),
		NewTensor("engram.table.0.weight", ngram),
	)

	got := ActiveBytesPerToken(ts, nUsed, nExp)
	want := int64(sparse*nUsed/nExp + nLayers*(router+shexp) + attn + ffn + other + output)
	if got != want {
		t.Fatalf("ActiveBytesPerToken = %d, want %d (delta %d)", got, want, got-want)
	}
	// The number the ws tape recorded and the card printed 155 GB/s from.
	if want := int64(7_639_161_280); got != want {
		t.Errorf("ActiveBytesPerToken = %d, want the recorded %d", got, want)
	}
	// The router and the shared expert are 10.7 % of the token. Counting them
	// sparsely — the mistake internal/bandwidth's class-only mirror makes — is
	// what TTP-56 measured.
	if sparsely := ActiveBytesPerToken(ts, nUsed, nExp) - nLayers*(router+shexp)*(nExp-nUsed)/nExp; sparsely != 6_820_987_840 {
		t.Errorf("counting router and shared expert sparsely = %d, want 6,820,987,840", sparsely)
	}
}
