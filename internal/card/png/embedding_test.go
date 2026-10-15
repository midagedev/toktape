package png

import (
	"testing"

	"github.com/midagedev/toktape/internal/tape"
)

func TestEmbeddingCard(t *testing.T) {
	tp, err := tape.Read("../../../assets/embedding-demo.tape")
	if err != nil {
		t.Fatal(err)
	}
	c, err := renderCanvas(&tp.Summary)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"hero.left.number", "hero.right.number", "stat.dim.label", "stat.max.label", "strip.credit"} {
		if _, ok := c.markByID(id); !ok {
			t.Errorf("the embedding card has no %s", id)
		}
	}
}
