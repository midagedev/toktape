package png

import (
	"strings"
	"testing"

	"github.com/midagedev/toktape/internal/card"
	"github.com/midagedev/toktape/internal/gpu"
)

// TestEnvironmentLineAgreesWithTheHostRow (2026-09-15): the strip renders the
// same two figures the text card's HOST row does — the draw against the
// board's enforced limit and the narrow throttle verdict — because a PNG and
// a text card of one run that disagree is two cards of two different runs. It
// renders both figures (the GPU state and the "throttled" verdict are both on
// this line), and both changed with the power-limit sweep.
func TestEnvironmentLineAgreesWithTheHostRow(t *testing.T) {
	s := card.Example()
	s.GPUsAtEnd[0].PowerW, s.GPUsAtEnd[0].PowerLimitW = 281, 300
	s.GPUsAtEnd[0].ThrottleMask = gpu.ThrottleSWPowerCap
	s.GPUsAtEnd[0].Throttled = true // the wide verdict the recorder stored

	c, err := renderCanvas(s)
	if err != nil {
		t.Fatalf("renderCanvas: %v", err)
	}
	m, ok := c.markByID("strip.env")
	if !ok {
		t.Fatal("strip.env was never drawn")
	}
	if !strings.Contains(m.Text, "GPU0 68°C 281 of 300W") {
		t.Errorf("the environment line does not carry the draw against the limit: %q", m.Text)
	}
	if !strings.Contains(m.Text, "throttled no") {
		t.Errorf("the environment line's verdict is not the narrow one: %q", m.Text)
	}
	// The text card's HOST row says the same two things, in its own spacing.
	text := card.Text(s)
	if !strings.Contains(text, "GPU0 68°C 281 of 300 W") || !strings.Contains(text, "throttled: no") {
		t.Errorf("the text card and the image disagree:\n%s", text)
	}
}
