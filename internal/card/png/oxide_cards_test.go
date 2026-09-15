package png

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/midagedev/toktape/internal/card"
	"github.com/midagedev/toktape/internal/tape"
)

// contendedSummary is the example run on a machine that was busy and a cache
// that was cold, with a few major faults: the one card that shows the Warn and
// Bad pills side by side.
func contendedSummary() *tape.RunSummary {
	s := card.Example()
	s.Contention.Contended = true
	s.Contention.Reasons = append(s.Contention.Reasons, "another process held the GPU")
	s.Cache.Label = tape.CacheCold
	s.Memory.MajFaultsPerToken = tape.ColdMajFaultsPerToken / 2
	return s
}

// TestOxideCardsAreWritten drops the palette's review set into testdata/out
// (gitignored) for the look-and-adjust loop (TTP-44): every example shape and
// the contended variant.
func TestOxideCardsAreWritten(t *testing.T) {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", outDir, err)
	}
	for name, s := range map[string]*tape.RunSummary{
		"example":     card.Example(),
		"speculative": card.ExampleSpeculative(),
		"rounds":      card.ExampleRounds(),
		"sweep":       card.ExampleSweep(),
		"contended":   contendedSummary(),
	} {
		path := filepath.Join(outDir, "oxide-"+name+".png")
		if err := Write(path, s); err != nil {
			t.Fatalf("Write %s: %v", name, err)
		}
	}

	t.Run("the contended variant shows a warn and a bad pill", func(t *testing.T) {
		ct := contentOf(t, contendedSummary())
		var warn, bad int
		for _, p := range ct.pills {
			switch p.col {
			case colWarn:
				warn++
			case colBad:
				bad++
			}
		}
		if warn == 0 || bad == 0 {
			t.Errorf("pills %+v: %d warn, %d bad; want at least one of each", ct.pills, warn, bad)
		}
	})
}
