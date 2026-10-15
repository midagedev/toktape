package publish

import (
	"testing"

	"github.com/midagedev/toktape/internal/tape"
)

func TestEmbeddingIndex(t *testing.T) {
	tp, err := tape.Read("../../assets/embedding-demo.tape")
	if err != nil {
		t.Fatal(err)
	}
	v := PublicView(tp, WithText)
	idx := IndexOf(v)
	if idx.Mode != tape.ModeEmbedding || idx.Sessions != 0 || idx.DecisionP50Ms != tp.Summary.Embedding.LatencyP50Ms {
		t.Errorf("index row: mode %q sessions %d p50 %v", idx.Mode, idx.Sessions, idx.DecisionP50Ms)
	}
	if w := PublicView(tp, WithoutText); w.Embeddings[0].Input != "" {
		t.Error("without text, an embedding input still travels")
	}
	if tp.Embeddings[0].Input == "" {
		t.Error("PublicView edited the input tape")
	}
}
