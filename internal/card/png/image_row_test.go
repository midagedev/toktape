package png

import (
	"testing"

	"github.com/midagedev/toktape/internal/card"
)

// TTP-208 gate 7, the PNG half. The lead asked for one row after Prefill and,
// if it does not fit, the measured overflow with nothing else shrunk
// (2026-10-12). It does not fit, so the PNG draws no Image row yet.
//
// Measured with the row drawn as a third hero sub-line at baseline
// bandHeroTop+188 = 306, in stBody, "1 × 640×480 PNG (builtin) · prefill
// includes it": the advance box is y 290..311 and 423 px wide (inside the 504
// px column, so width is not the problem). The hero band ends at the 1 px rule
// at y=298 (bandMemTop), which runs through the box 8 px below its top; the
// memory pills begin at y=310, so the box also reaches 1 px into them (their
// first at x=590..702, the others to x=1140). The sub2 line above ends at
// y=289, leaving 9 px, and the row needs 21. Room for it means moving the
// rule or a band, which is the lead's decision.
//
// This test pins the absence and carries the numbers; when the PNG gets the
// row, it flips to asserting the row's text and rect.
func TestImageRunDrawsNoImageRowOnThePNGYet(t *testing.T) {
	c := mustCanvas(t, card.ExampleImage())
	if _, ok := c.markByID("hero.right.sub3"); ok {
		t.Fatal("an image row is drawn; update this test and the measured-overflow note")
	}
	sub2, ok := c.markByID("hero.right.sub2")
	if !ok {
		t.Fatal("hero.right.sub2 was never drawn")
	}
	if got := bandMemTop - sub2.Rect.Max.Y; got != 9 {
		t.Errorf("room between sub2 and the hero rule = %d px, the note above says 9", got)
	}
	// And the text-only card is the same canvas: the image run adds nothing.
	plain := mustCanvas(t, card.Example())
	if len(plain.marks) != len(c.marks) {
		t.Errorf("an image run draws %d marks, a text-only run %d", len(c.marks), len(plain.marks))
	}
}
