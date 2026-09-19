package png

import (
	"strings"
	"testing"

	"github.com/midagedev/toktape/internal/card"
)

// TTP-97 (FAIL-first, 2026-09-19): the PNG draws the same run as the text
// card and must not be the one that leaves the gap — two percentiles
// rendering as the same string read as a bug here too.
func TestHeroPrintsSingleTTFTWhenPercentilesRenderAlike(t *testing.T) {
	s := card.ExampleConcurrent()
	s.Aggregate.TTFTp50Ms, s.Aggregate.TTFTp95Ms = 10800, 10800
	m, ok := mustCanvas(t, s).markByID("hero.right.sub2")
	if !ok {
		t.Fatal("hero.right.sub2 was never drawn")
	}
	if strings.Contains(m.Text, "p95") {
		t.Errorf("two percentiles rendering alike still print as a pair: %q", m.Text)
	}
	if !strings.Contains(m.Text, "TTFT 10800 ms") {
		t.Errorf("the alike pair does not print as a single figure: %q", m.Text)
	}
}

// TTP-108: the PNG draws the same run and must not be the one that leaves
// the gap — a ragged aggregate says so on the decode sub-line, in the
// Streams block's own words, asked through the same predicate as the text
// card. A run whose streams shared a window keeps the line it always had.
func TestHeroNamesRaggedDecode(t *testing.T) {
	m, ok := mustCanvas(t, card.ExampleRagged()).markByID("hero.left.sub1")
	if !ok {
		t.Fatal("hero.left.sub1 was never drawn")
	}
	if !strings.Contains(m.Text, "not all decoding at once") {
		t.Errorf("a ragged aggregate prints no clause: %q", m.Text)
	}
	m, ok = mustCanvas(t, card.ExampleConcurrent()).markByID("hero.left.sub1")
	if !ok {
		t.Fatal("hero.left.sub1 was never drawn")
	}
	if strings.Contains(m.Text, "not all decoding at once") {
		t.Errorf("a shared window claims raggedness: %q", m.Text)
	}
}
