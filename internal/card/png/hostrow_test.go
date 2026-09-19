package png

import (
	"strings"
	"testing"

	"github.com/midagedev/toktape/internal/card"
	"github.com/midagedev/toktape/internal/gpu"
)

// TestThrottlePillAgreesWithTheHostRow (2026-09-15; pill since 2026-09-19):
// the PNG renders the same narrow throttle verdict the text card's HOST row
// does, because a PNG and a text card of one run that disagree is two cards
// of two different runs. The case is the power-limit sweep: the GPU drew 281
// of an enforced 300 W, the driver set the SW power cap bit, and the
// recorder's wide Throttled flag is true — but a GPU held AT its own setting
// is not a GPU held BELOW it, so the verdict both renderings print is "no".
//
// The draw-against-the-limit clause itself left the image with the
// environment line; it stays on the text card, which is where the two
// renderings are compared.
func TestThrottlePillAgreesWithTheHostRow(t *testing.T) {
	s := card.Example()
	s.GPUsAtEnd[0].PowerW, s.GPUsAtEnd[0].PowerLimitW = 281, 300
	s.GPUsAtEnd[0].ThrottleMask = gpu.ThrottleSWPowerCap
	s.GPUsAtEnd[0].Throttled = true // the wide verdict the recorder stored

	c, err := renderCanvas(s)
	if err != nil {
		t.Fatalf("renderCanvas: %v", err)
	}
	m, ok := c.markByID("memory.pill3")
	if !ok {
		t.Fatal("memory.pill3 was never drawn")
	}
	if m.Text != "throttled no" {
		t.Errorf("the throttle pill = %q, want the narrow verdict %q", m.Text, "throttled no")
	}
	// The text card's HOST row says the same thing about the same run, in its
	// own spacing, and carries the draw-against-the-limit figure the image no
	// longer has room for.
	text := card.Text(s)
	if !strings.Contains(text, "GPU0 68°C 281 of 300 W") || !strings.Contains(text, "throttled: no") {
		t.Errorf("the text card and the image disagree:\n%s", text)
	}
}
