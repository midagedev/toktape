package tui

import (
	"strings"
	"testing"
)

// TestBigFigureIsMonospaced: every glyph of the face is the same width on
// every row, and the decimal point is narrower than a digit, so a figure lines
// up as a block whatever its runes. "?" is in the face because an unmeasured
// rate prints "?" (fmtRate), and the modal must be able to say so at size.
func TestBigFigureIsMonospaced(t *testing.T) {
	for r, g := range bigGlyphs {
		want := 3
		if r == '.' {
			want = 1
		}
		for k, row := range g {
			if got := width(row); got != want {
				t.Errorf("glyph %q row %d is %d wide, want %d", r, k, got, want)
			}
		}
	}
	for _, s := range []string{"34.5", "2787", "?", "7.0"} {
		rows := bigFigure(s)
		for k := 1; k < bigRows; k++ {
			if width(rows[k]) != width(rows[0]) {
				t.Errorf("figure %q: row %d is %d wide, row 0 is %d", s, k, width(rows[k]), width(rows[0]))
			}
		}
	}
	if got := width(bigFigure("2787")[0]); got != 15 {
		t.Errorf("a four-digit figure is %d wide, want 15 (four glyphs and three gaps)", got)
	}
}

// TestResultModalFitsTheSmallestScreen: at MinWidth×MinHeight the modal still
// holds a four-digit decode rate and the widest latency figure with their
// units side by side, and the frame is exactly the screen.
//
// 2026-09-15: the contract changed with the modal's hierarchy. The right-hand
// figure is the time to first token, not a second rate, so the row under test
// carries " tok/s" once — decode — beside the TTFT's own unit (" s" or
// " ms"), where it used to carry " tok/s" twice. FAIL-first on the old modal:
// no glyph row there carries any TTFT unit, so the count below was 0.
func TestResultModalFitsTheSmallestScreen(t *testing.T) {
	m := goldenModel(t, doneAt)
	m.Mode = ModeCard
	// The widest pair the face can draw: a four-digit decode rate and the
	// TTFT at "100.0 s" (99999 ms), with every source the p50 path could read
	// set to it, whichever the stream count makes it use.
	m.Summary.Aggregate.AggregatePredictedPerSecond = 2787
	m.Summary.Aggregate.TTFTp50Ms = 99999
	m.Summary.Aggregate.TTFTp95Ms = 99999
	m.Summary.Timings.TTFTMs = 99999
	frame := View(m, doneAt, MinWidth, MinHeight)
	checkFrame(t, frame, MinWidth, MinHeight)
	rows := parseFrame(frame, MinWidth, MinHeight)
	units := 0
	for _, row := range rows {
		p := plainRow(row)
		if countSubstring(p, " tok/s") > 0 &&
			(strings.Contains(p, " s") || strings.Contains(p, " ms")) &&
			countSubstring(p, "▀") > 0 {
			units++
		}
	}
	if units != 1 {
		t.Errorf("want exactly one row carrying the decode unit and the TTFT unit beside big-figure glyphs, got %d\n%s", units, frame)
	}
}

func countSubstring(s, sub string) int {
	n := 0
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			n++
		}
	}
	return n
}
