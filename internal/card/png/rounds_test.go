package png

import (
	"testing"

	"github.com/midagedev/toktape/internal/card"
	"github.com/midagedev/toktape/internal/tape"
)

// TestRoundsClauseReplacesThePerStreamLine pins TTP-31 on the share image. On
// a multi-prompt run the decode column's first sub-line is the median over the
// rounds with its range, whole and inside the column, and the median leads so
// the line cannot be read as the label of the mean in the hero. Nothing else in
// the hero moves, and a run of one round keeps the line it had.
func TestRoundsClauseReplacesThePerStreamLine(t *testing.T) {
	const want = "14.8 median of 6 prompts · 9.1–19.3 tok/s"
	single := card.ExampleRounds()
	single.Concurrency = 1
	for name, s := range map[string]*tape.RunSummary{
		"concurrent": card.ExampleRounds(),
		"single":     single,
	} {
		t.Run(name, func(t *testing.T) {
			c, err := renderCanvas(s)
			if err != nil {
				t.Fatalf("renderCanvas: %v", err)
			}
			m, ok := c.markByID("hero.left.sub1")
			if !ok {
				t.Fatal("hero.left.sub1 was never drawn")
			}
			if m.Text != want {
				t.Errorf("hero.left.sub1 = %q, want %q", m.Text, want)
			}
			if m.Rect.Max.X > heroSplitX-heroGutter {
				t.Errorf("the rounds clause ends at x=%d, past the decode column (%d)", m.Rect.Max.X, heroSplitX-heroGutter)
			}
			// The draft clause on the second line is untouched — except that
			// on the concurrent run it now carries the verify-step bandwidth
			// and has dropped the model name to fit it (2026-09-14, TTP-67
			// and TTP-69; see TestDraftClauseReplacesTheSecondDecodeLine).
			// The single-stream copy of this fixture derives no verify-step
			// figure, so its clause is the string it always was.
			want2 := "draft n_max 3 · 38% accepted · ≈ 226 GB/s RAM/step"
			if s.Concurrency <= 1 {
				want2 = "draft DSpark-0.6B-Q8_0.gguf · n_max 3 · 38% accepted"
			}
			if m2, _ := c.markByID("hero.left.sub2"); m2.Text != want2 {
				t.Errorf("hero.left.sub2 = %q, want %q", m2.Text, want2)
			}
		})
	}

	t.Run("every other hero line matches the speculative run's shape", func(t *testing.T) {
		rounds, base := build(card.ExampleRounds()), build(card.ExampleSpeculative())
		if rounds.left.eyebrow != base.left.eyebrow || rounds.right.eyebrow != base.right.eyebrow ||
			rounds.right.sub1 != base.right.sub1 {
			t.Errorf("rounds hero %+v / %+v moved beyond sub1 against %+v / %+v", rounds.left, rounds.right, base.left, base.right)
		}
	})

	t.Run("one round keeps its per-stream line", func(t *testing.T) {
		s := card.ExampleSpeculative()
		s.Rounds = 1
		if got, want := build(s).left.sub1, build(card.ExampleSpeculative()).left.sub1; got != want {
			t.Errorf("hero.left.sub1 = %q for one round, want the plain run's %q", got, want)
		}
	})

	t.Run("no observed rate keeps the per-stream line", func(t *testing.T) {
		s := card.ExampleRounds()
		s.Spread.PerStreamPredictedPerSecond = tape.Spread{}
		if got := build(s).left.sub1; got != "4 × 14.4 tok/s per stream" {
			t.Errorf("hero.left.sub1 = %q, want the per-stream line rather than \"? median\"", got)
		}
	})
}
