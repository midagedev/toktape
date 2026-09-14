package png

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/midagedev/toktape/internal/card"
	"github.com/midagedev/toktape/internal/tape"
)

// TestSweepClauseNamesTheBestNMax pins TTP-35 on the share image. On a
// speculative n_max sweep the decode column's first sub-line names the fastest
// value with its median over that value's prompts, whole and inside the
// column. Nothing else in the hero moves, and a run that is not a sweep keeps
// the line it had.
func TestSweepClauseNamesTheBestNMax(t *testing.T) {
	const want = "best n_max 5 · 16.1 tok/s median of 6 prompts"
	c, err := renderCanvas(card.ExampleSweep())
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
		t.Errorf("the sweep clause ends at x=%d, past the decode column (%d)", m.Rect.Max.X, heroSplitX-heroGutter)
	}

	t.Run("every other hero line matches the rounds run's shape", func(t *testing.T) {
		sweep, rounds := build(card.ExampleSweep()), build(card.ExampleRounds())
		if sweep.left.eyebrow != rounds.left.eyebrow || sweep.right.eyebrow != rounds.right.eyebrow ||
			sweep.right.sub1 != rounds.right.sub1 {
			t.Errorf("sweep hero %+v / %+v moved beyond sub1 against %+v / %+v", sweep.left, sweep.right, rounds.left, rounds.right)
		}
	})

	t.Run("without a best value the rounds clause stays", func(t *testing.T) {
		one := card.ExampleSweep()
		one.BySpecNMax = one.BySpecNMax[:1]
		tie := card.ExampleSweep()
		tie.BySpecNMax[0].Spread.PerStreamPredictedPerSecond.Median = 16.1
		for name, s := range map[string]*tape.RunSummary{"one value": one, "a tie": tie} {
			if got := build(s).left.sub1; got != "15.5 median of 12 prompts · 8.0–20.9 tok/s" {
				t.Errorf("%s: hero.left.sub1 = %q, want the rounds clause", name, got)
			}
		}
	})

	t.Run("a rounds run keeps its clause", func(t *testing.T) {
		if got := build(card.ExampleRounds()).left.sub1; got != "14.8 median of 6 prompts · 9.1–19.3 tok/s" {
			t.Errorf("hero.left.sub1 = %q", got)
		}
	})

	t.Run("the card is written", func(t *testing.T) {
		if err := os.MkdirAll(outDir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", outDir, err)
		}
		path := filepath.Join(outDir, "card-sweep.png")
		if err := Write(path, card.ExampleSweep()); err != nil {
			t.Fatalf("Write: %v", err)
		}
		abs, _ := filepath.Abs(path)
		t.Logf("wrote %s", abs)
	})
}

// TestDraftClauseNamesTheSweptNMax (TTP-35, 2026-09-13): the share image's
// draft clause names the n_max values a sweep's requests carried, as the text
// card's Draft row does, never the server flag they overrode.
func TestDraftClauseNamesTheSweptNMax(t *testing.T) {
	// 2026-09-14: the clause gained the verify-step bandwidth (TTP-67). What
	// this test is about is the n_max, so it reads the clause's n_max element
	// rather than pinning the whole string twice over — the whole string,
	// including which element is given up when the column is full, is pinned
	// by TestDraftClauseReplacesTheSecondDecodeLine.
	if got, _ := draftString(card.ExampleSweep()); !strings.Contains(got, "n_max 3,5 · 30% accepted") {
		t.Errorf("sweep draft clause = %q, want the swept values", got)
	}
	s := card.ExampleSweep()
	s.SpecNMax = nil
	if got, _ := draftString(s); !strings.Contains(got, "n_max 3 · 30% accepted") {
		t.Errorf("draft clause without a sweep = %q, want the server flag", got)
	}
}
