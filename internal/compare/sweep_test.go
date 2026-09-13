package compare_test

import (
	"strings"
	"testing"

	"github.com/midagedev/toktape/internal/card"
	"github.com/midagedev/toktape/internal/compare"
)

// TestSpecNMaxMeta (TTP-35, 2026-09-13): the per-value lines of two sweeps are
// only comparable over the same block sizes, so compare names the values when
// they differ. A run that swept nothing is observed to have swept nothing, and
// says "none" rather than "?".
func TestSpecNMaxMeta(t *testing.T) {
	t.Run("a rounds run against a sweep", func(t *testing.T) {
		r := compare.Diff(card.ExampleRounds(), card.ExampleSweep())
		c, ok := metaByLabel(r)["spec n_max"]
		if !ok || c.A != "none" || c.B != "3,5" {
			t.Errorf("spec n_max change = %+v (present %v), want none → 3,5", c, ok)
		}
		if out := compare.Text(r); !strings.Contains(out, "none → 3,5") {
			t.Errorf("the run section does not name the values:\n%s", out)
		}
	})

	t.Run("two sweeps over different values", func(t *testing.T) {
		b := card.ExampleSweep()
		b.SpecNMax = []int{3, 5, 8}
		c, ok := metaByLabel(compare.Diff(card.ExampleSweep(), b))["spec n_max"]
		if !ok || c.A != "3,5" || c.B != "3,5,8" {
			t.Errorf("spec n_max change = %+v (present %v), want 3,5 → 3,5,8", c, ok)
		}
	})

	t.Run("the same values, or no sweep on either side, report nothing", func(t *testing.T) {
		for name, r := range map[string]compare.Report{
			"sweep vs itself":       compare.Diff(card.ExampleSweep(), card.ExampleSweep()),
			"rounds vs speculative": compare.Diff(card.ExampleSpeculative(), card.ExampleRounds()),
			"example vs modified":   compare.Diff(card.Example(), modified()),
		} {
			if c, ok := metaByLabel(r)["spec n_max"]; ok {
				t.Errorf("%s: a spec n_max change appeared: %+v", name, c)
			}
		}
	})
}
