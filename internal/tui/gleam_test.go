package tui

import (
	"testing"
)

// TestGleamStartsUnlitAndSettlesLit is the derivation gleamLead exists for
// (2026-09-15), re-pinned to the sweep's new shape (2026-09-16, user: "메인
// 숫자에 애니메이션 준게 너무 티가 안나더라고").
//
// The pass used to start and end on the same settled tone, which is what made
// it invisible: a band of the accent's own lightnesses crossing a figure
// already wearing one of them. It now runs from unlit to lit — every cell is
// accentMuted at phase 0 and accentBold at phase 1 — so the figure reads as
// lighting up rather than as being smudged.
//
// The phase-1 half is the half that must never move: the modal's settled tail
// and the clip's poster frame are rendered from it, and they are byte-identical
// to a modal with no gleam at all. The lean is the hazard the lead covers: the
// top row leads by gleamSlant*(bigRows-1)/2 columns, and a travel extended by
// exactly that still leaves the top row's first cell inside the band at phase 0.
//
// FAIL-first was run by shrinking gleamLead to gleamSlant*(bigRows-1)/2: the
// width-1 figure then reports a cell already lit at phase 0.
func TestGleamStartsUnlitAndSettlesLit(t *testing.T) {
	th := ColourTheme()
	want := map[float64]string{
		0: styleHex(th.accentMuted), // nothing lit yet
		1: styleHex(th.accentBold),  // the settled tail, and the poster frame
	}
	for _, w := range []int{1, 19, 23, 29, 35} {
		for _, p := range []float64{0, 1} {
			for r := 0; r < bigRows; r++ {
				for x := 0; x < w; x++ {
					if got := styleHex(gleamStyle(th, p, w, r, x)); got != want[p] {
						t.Errorf("w=%d p=%v r=%d x=%d: the sweep's edge is %s, want %s on every cell", w, p, r, x, got, want[p])
					}
				}
			}
		}
	}
}

// TestGleamCrossesMidSweep is the other half: between the ends the band is
// actually on the figures, so some cell at some row wears the core, and no
// cell ever wears anything outside the sweep's own ramp — the accent's four
// lightnesses plus the peak the core was given in 2026-09-16. One hue still:
// TTP-28 reserved the accent for exactly these figures, and the peak is that
// hue washed to white rather than a colour of its own.
func TestGleamCrossesMidSweep(t *testing.T) {
	th := ColourTheme()
	allowed := map[string]bool{
		styleHex(th.gleamPeak):   true,
		styleHex(th.accentHigh):  true,
		styleHex(th.accent):      true,
		styleHex(th.accentMid):   true,
		styleHex(th.accentMuted): true,
		styleHex(th.accentBold):  true,
	}
	for _, w := range []int{1, 19, 23, 29, 35} {
		high := false
		for r := 0; r < bigRows; r++ {
			for x := 0; x < w; x++ {
				hex := styleHex(gleamStyle(th, 0.5, w, r, x))
				if !allowed[hex] {
					t.Errorf("w=%d r=%d x=%d: the gleam wears %s, outside its own ramp", w, r, x, hex)
				}
				if hex == styleHex(th.gleamPeak) {
					high = true
				}
			}
		}
		if !high {
			t.Errorf("w=%d: no cell wears the core at the sweep's midpoint; the band never crosses the figure", w)
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
