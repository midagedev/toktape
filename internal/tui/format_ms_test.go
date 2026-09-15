package tui

import "testing"

// TestFmtMsParts: the two halves of fmtMs, exactly. The result modal draws the
// TTFT's number three rows tall and its unit dim beside the bottom row, so the
// halves are the contract — and the join has to be the string fmtMs has always
// printed, which the second half of each case checks by construction rather
// than by hope.
func TestFmtMsParts(t *testing.T) {
	for _, c := range []struct {
		v         float64
		num, unit string
	}{
		{10753.7, "10.8", "s"}, // the hero's p50: seconds at one decimal past 10 s
		{1050, "1.05", "s"},    // seconds at two decimals under 10 s
		{630, "630", "ms"},     // milliseconds as an integer
		{0, "?", ""},           // never measured: no figure and no unit
		{-1, "?", ""},
		{99999, "100.0", "s"}, // the widest the modal's face has to hold
	} {
		num, unit := fmtMsParts(c.v)
		if num != c.num || unit != c.unit {
			t.Errorf("fmtMsParts(%v) = (%q, %q), want (%q, %q)", c.v, num, unit, c.num, c.unit)
		}
		want := c.num
		if c.unit != "" {
			want += " " + c.unit
		}
		if got := fmtMs(c.v); got != want {
			t.Errorf("fmtMs(%v) = %q, want %q (the halves joined)", c.v, got, want)
		}
	}
}
