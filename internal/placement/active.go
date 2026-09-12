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
//     in full.
//   - The input embedding matrix is excluded: decoding looks up one row, not
//     the matrix. The exception is a tied-embedding model, which has no
//     output.weight and runs the embedding matrix as the output projection —
//     there it counts in full. Presence of output.weight is the test, not
//     presence of the output class: output_norm.weight is class output and a
//     tied model has one too.
//   - The n-gram / engram tables are excluded: they are lookup tables, not
//     weights in the per-token matmul chain, and on the demo machine they were
//     84.6 GB that was never read at all (handover lesson 3).
func ActiveBytesPerToken(tensors []Tensor, expertsUsed, expertCount int) int64 {
	// Tied embeddings are detected by the absence of the output projection
	// itself, not of the output class: output_norm.weight is class output and
	// every transformer has one, tied or not.
	hasOutputProj := false
	for _, t := range tensors {
		if t.Name == "output.weight" {
			hasOutputProj = true
			break
		}
	}

	var total int64
	for _, t := range tensors {
		switch {
		case t.Class == tape.ClassNGram:
			continue
		case t.Class == tape.ClassEmbed:
			// Tied embeddings: the matrix is the output projection too.
			if hasOutputProj {
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
