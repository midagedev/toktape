package tui

import (
	"testing"

	"github.com/midagedev/toktape/internal/card"
	"github.com/midagedev/toktape/internal/tape"
)

// TestModelNameDropsThePartMarker: the title bar names the model, and a split
// set's part one is not a different model from part nine (TTP-32, 2026-09-13).
func TestModelNameDropsThePartMarker(t *testing.T) {
	cases := []struct {
		name string
		mi   tape.ModelInfo
		want string
	}{
		{
			"the GGUF's own name still wins",
			card.ExampleSharded().Model,
			"DeepSeek V4.1 Flash",
		}, {
			"a header-less split set falls back to the stem",
			tape.ModelInfo{FileName: "DeepSeek-V4.1-Flash-00001-of-00009.gguf", Dir: "DeepSeek-V4.1-Flash-engramQ8-tokembdBF16", Shards: 9},
			"DeepSeek-V4.1-Flash",
		}, {
			// Unchanged: every non-sharded title is what it always was.
			"a single file keeps its whole name",
			tape.ModelInfo{FileName: "DeepSeek-R1-Distill-Llama-70B-Q4_K_M.gguf"},
			"DeepSeek-R1-Distill-Llama-70B-Q4_K_M",
		}, {
			"nothing observed",
			tape.ModelInfo{},
			"",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := modelName(c.mi); got != c.want {
				t.Errorf("modelName = %q, want %q", got, c.want)
			}
		})
	}
}
