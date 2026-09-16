package placement

import "github.com/midagedev/toktape/internal/tape"

// ActiveBytesPerToken estimates the weight bytes touched to decode one token.
// Multiplied by the decode rate it gives the effective memory bandwidth the
// card prints, so it must count what the GEMMs actually read:
//
//   - Dense weights (attention, FFN, output, router, shared experts) count in
//     full: every token reads all of them.
//   - Stacked per-expert weights count as expertsUsed/expertCount of their
//     size: a token routes through that fraction of the experts. With
//     expertCount == 0 or expertsUsed == 0 the model is dense and they count
//     in full. Sparseness is decided by the tensor NAME (IsSparseExpert), not
//     by the class: the MoE router and the shared expert are ClassExperts too
//     and run for every token, so they fall through to the full count above.
//     TTP-56 (2026-09-14) re-derived this against the ws DeepSeek V4.1 Flash
//     recording and confirmed it: those two are 0.831 GB of that model's
//     7.639 GB token, and counting them sparsely would be wrong by 10.7 %.
//   - The input embedding matrix is excluded: decoding looks up one row, not
//     the matrix. The exception is a tied-embedding model, which has no
//     output.weight and runs the embedding matrix as the output projection —
//     there it counts in full. Presence of output.weight is the test, not
//     presence of the output class: output_norm.weight is class output and a
//     tied model has one too. And only the MAIN matrix (token_embd itself)
//     ever counts in full: a per-layer lookup table is tied to no output
//     projection and is read by row however large it is (2026-09-16 — the
//     qwen38 recording's 26.8 GiB per_layer_token_embd counted here was the
//     whole of a host-bus figure that printed 10x over the machine's own).
//   - The n-gram / engram tables are excluded: they are lookup tables, not
//     weights in the per-token matmul chain, and on the demo machine they were
//     84.6 GB that was never read at all (handover lesson 3).
func ActiveBytesPerToken(tensors []Tensor, expertsUsed, expertCount int) int64 {
	return ActiveBytesPerTokenTied(tensors, expertsUsed, expertCount, TiedEmbeddings(tensors))
}

// TiedEmbeddings reports whether a model ties its embedding matrix to its
// output projection, in which case a token reads the whole matrix rather than
// one row of it.
//
// The test is the absence of the output projection ITSELF, not of the output
// class: output_norm.weight is class output and every transformer has one,
// tied or not.
//
// It must be asked of the WHOLE tensor list. Asked of one device's share it
// gives the wrong answer on any sharded placement — a model whose
// output.weight sits on GPU1 looks tied to GPU0 and to the CPU, and the CPU is
// where token_embd lives, so the embedding matrix would be counted in full for
// every token. On the ws recording that is 1.32 GB of phantom traffic on the
// one device the card is trying to report honestly (TTP-56, 2026-09-14). That
// is why this is exported and why ActiveBytesPerTokenTied takes the answer
// rather than deriving it.
func TiedEmbeddings(tensors []Tensor) bool {
	for _, t := range tensors {
		if t.Name == "output.weight" {
			return false
		}
	}
	return true
}

// ActiveBytesPerTokenTied is ActiveBytesPerToken with the tied-embedding
// question already answered, so that a caller holding one device's tensors can
// pass the answer derived from the whole model. See TiedEmbeddings.
func ActiveBytesPerTokenTied(tensors []Tensor, expertsUsed, expertCount int, tied bool) int64 {
	var total int64
	for _, t := range tensors {
		switch {
		case t.Class == tape.ClassNGram:
			continue
		case t.Class == tape.ClassEmbed:
			// Tied embeddings: the MAIN matrix is the output projection too,
			// so a tied model reads it in full. Nothing else in the class
			// ever counts: a per-layer table (per_layer_token_embd.weight) is
			// a row lookup — a few KB a token — because no output projection
			// is tied to it, whatever the model does with its logits.
			if !tied || !t.IsMainEmbedding() {
				continue
			}
			total += t.Bytes
		case t.IsSparseExpert() && expertCount > 0 && expertsUsed > 0:
			if expertsUsed >= expertCount {
				total += t.Bytes
				continue
			}
			total += t.Bytes * int64(expertsUsed) / int64(expertCount)
		default:
			total += t.Bytes
		}
	}
	return total
}
