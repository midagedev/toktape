package compare_test

import (
	"strings"
	"testing"

	"github.com/midagedev/toktape/internal/card"
	"github.com/midagedev/toktape/internal/compare"
	"github.com/midagedev/toktape/internal/tape"
)

func rowsByLabel(r compare.Report) map[string]compare.Row {
	out := map[string]compare.Row{}
	for _, m := range r.Metrics {
		out[m.Label] = m
	}
	return out
}

func metaByLabel(r compare.Report) map[string]compare.Change {
	out := map[string]compare.Change{}
	for _, c := range r.Meta {
		out[c.Label] = c
	}
	return out
}

// TestPromptRounds (TTP-31, 2026-09-13): a run over several prompts is a
// different measurement from a run over one, so compare names the prompt count
// when it differs and sets the median rate beside the other run's.
func TestPromptRounds(t *testing.T) {
	t.Run("one prompt against six", func(t *testing.T) {
		r := compare.Diff(card.ExampleSpeculative(), card.ExampleRounds())
		row, ok := rowsByLabel(r)["tok/s median"]
		if !ok {
			t.Fatal("no tok/s median row on a pair where B ran six rounds")
		}
		if row.A != "?" || row.B != "14.8" || row.HasDelta {
			t.Errorf("row = %+v, want ? → 14.8 and no delta: one prompt has no median", row)
		}
		c, ok := metaByLabel(r)["prompts"]
		if !ok || c.A != "1" || c.B != "6" {
			t.Errorf("prompts change = %+v (present %v), want 1 → 6", c, ok)
		}
		// The metric rows stay the all-stream figures.
		if d := rowsByLabel(r)["decode tok/s"]; d.B != "14.4" {
			t.Errorf("decode tok/s B = %q, want the mean over every stream, 14.4", d.B)
		}
	})

	t.Run("two multi-prompt runs carry a delta", func(t *testing.T) {
		b := card.ExampleRounds()
		spread := *b.Spread
		spread.PerStreamPredictedPerSecond.Median = 16.28
		b.Spread = &spread
		row := rowsByLabel(compare.Diff(card.ExampleRounds(), b))["tok/s median"]
		if row.A != "14.8" || row.B != "16.3" || !row.HasDelta || row.DeltaPct < 9.9 || row.DeltaPct > 10.1 {
			t.Errorf("row = %+v, want 14.8 → 16.3 at +10%%", row)
		}
		if _, ok := metaByLabel(compare.Diff(card.ExampleRounds(), b))["prompts"]; ok {
			t.Error("a prompts change was reported between two six-round runs")
		}
	})

	t.Run("single-round pairs are unchanged", func(t *testing.T) {
		zero, one := card.ExampleSpeculative(), card.ExampleSpeculative()
		one.Rounds = 1
		for name, r := range map[string]compare.Report{
			"example vs modified":  compare.Diff(card.Example(), modified()),
			"rounds 0 vs rounds 1": compare.Diff(zero, one),
		} {
			if _, ok := rowsByLabel(r)["tok/s median"]; ok {
				t.Errorf("%s: a tok/s median row appeared", name)
			}
			if _, ok := metaByLabel(r)["prompts"]; ok {
				t.Errorf("%s: a prompts change appeared", name)
			}
		}
	})

	t.Run("the block stays within the card width", func(t *testing.T) {
		out := compare.Text(compare.Diff(card.ExampleSpeculative(), card.ExampleRounds()))
		for i, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
			if w := card.Width(line); w > compare.Width {
				t.Errorf("line %d is %d columns: %q", i+1, w, line)
			}
		}
		if !strings.Contains(out, "prompts       1 → 6") {
			t.Errorf("the run section does not name the prompt count:\n%s", out)
		}
	})

	t.Run("a nil spread on a multi-round side prints ?", func(t *testing.T) {
		b := card.ExampleRounds()
		b.Spread = (*tape.RoundSpread)(nil)
		row := rowsByLabel(compare.Diff(card.ExampleRounds(), b))["tok/s median"]
		if row.B != "?" || row.HasDelta {
			t.Errorf("row = %+v, want B ? with no delta", row)
		}
	})
}
