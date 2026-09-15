package tui

import (
	"testing"
)

// TestGleamSettlesAtBothEnds is the derivation gleamLead exists for (2026-09-15),
// pinned: at phase 0 and phase 1 — the first and last frames the sweep's
// formula is allowed to speak on — every cell of every figure width the modal
// can draw is back on accentBold, so the pass neither starts on a half-lit
// figure nor snaps off one. The lean is the hazard: the top row leads by
// gleamSlant*(bigRows-1)/2 columns, and a travel extended by exactly that
// still leaves the top row's first cell inside the band at phase 0.
//
// FAIL-first was run by shrinking gleamLead to gleamSlant*(bigRows-1)/2: the
// width-1 figure then reports a cell still on accentMid at phase 0.
func TestGleamSettlesAtBothEnds(t *testing.T) {
	th := ColourTheme()
	settled := styleHex(th.accentBold)
	for _, w := range []int{1, 19, 23, 29, 35} {
		for _, p := range []float64{0, 1} {
			for r := 0; r < bigRows; r++ {
				for x := 0; x < w; x++ {
					if got := styleHex(gleamStyle(th, p, w, r, x)); got != settled {
						t.Errorf("w=%d p=%v r=%d x=%d: the sweep's edge is not settled (%s), want accentBold on every cell", w, p, r, x, got)
					}
				}
			}
		}
	}
}

// TestGleamCrossesMidSweep is the other half: between the ends the band is
// actually on the figures, so some cell at some row wears accentHigh, the
// band's centre, and no cell ever wears anything outside the gleam's four
// accent lightnesses (one hue — TTP-28 reserved the accent for exactly these
// figures, and the sweep spends only more of its own lightnesses).
func TestGleamCrossesMidSweep(t *testing.T) {
	th := ColourTheme()
	allowed := map[string]bool{
		styleHex(th.accentHigh): true,
		styleHex(th.accent):     true,
		styleHex(th.accentMid):  true,
		styleHex(th.accentBold): true,
	}
	for _, w := range []int{1, 19, 23, 29, 35} {
		high := false
		for r := 0; r < bigRows; r++ {
			for x := 0; x < w; x++ {
				hex := styleHex(gleamStyle(th, 0.5, w, r, x))
				if !allowed[hex] {
					t.Errorf("w=%d r=%d x=%d: the gleam wears %s, outside its four accent lightnesses", w, r, x, hex)
				}
				if hex == styleHex(th.accentHigh) {
					high = true
				}
			}
		}
		if !high {
			t.Errorf("w=%d: no cell wears accentHigh at the sweep's midpoint; the band never crosses the figure", w)
		}
	}
}

// TestGleamLeansForward pins the slash the lean exists for (2026-09-15): at
// the same phase, the top row's band centre sits to the right of the bottom
// row's — the light enters at the top first, the way a highlight travelling
// across type reads, and not as a vertical wipe. The lead row is 0 and the
// trailing row is bigRows-1, so the difference is the full lean,
// gleamSlant*(bigRows-1).
func TestGleamLeansForward(t *testing.T) {
	centre := func(p float64, w, r int) float64 {
		return p*float64(w+2*gleamBand+2*gleamLead) - float64(gleamBand+gleamLead) +
			float64(gleamSlant)*float64((bigRows-1)/2-r)
	}
	for _, w := range []int{1, 19, 35} {
		for _, p := range []float64{0.25, 0.5, 0.75} {
			if top, bottom := centre(p, w, 0), centre(p, w, bigRows-1); top <= bottom {
				t.Errorf("w=%d p=%v: the top row's band (%.2f) does not lead the bottom row's (%.2f); the sweep reads as a wipe, not a slash", w, p, top, bottom)
			}
		}
	}
}
