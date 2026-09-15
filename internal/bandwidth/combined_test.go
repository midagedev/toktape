package bandwidth

import (
	"testing"

	"github.com/midagedev/toktape/internal/placement"
	"github.com/midagedev/toktape/internal/tape"
)

// TestCombinedRefusesAnEnginePlacement (2026-09-15, lead review): the one
// all-bus figure is refused for an engine placement and kept for GGUF.
func TestCombinedRefusesAnEnginePlacement(t *testing.T) {
	s := &tape.RunSummary{}
	s.Timings.EffectiveBandwidthBytesPerSec = 138600000000

	s.Placement.Source = placement.SourceEngine
	if v, ok := Combined(s); ok {
		t.Errorf("engine placement: Combined = %d, true; want no figure", v)
	}
	s.Placement.Source = placement.SourceGGUFArgs
	if v, ok := Combined(s); !ok || v != 138600000000 {
		t.Errorf("gguf placement: Combined = %d, %v; want the recorded figure", v, ok)
	}
	if _, ok := Combined(nil); ok {
		t.Error("nil summary: want no figure")
	}
}
