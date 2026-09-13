package png

import (
	"testing"

	"github.com/midagedev/toktape/internal/tape"
)

// TestFormatParamsB pins the parameter-count format the lead asked for: model
// cards say "35B" and "671B", never "35.00 B", and the decimal survives only
// where it changes which model you are looking at.
func TestFormatParamsB(t *testing.T) {
	cases := []struct {
		in   int64
		want string
	}{
		{0, unknown},
		{-1, unknown},
		{500_000_000, "0.5B"},
		{3_000_000_000, "3.0B"},
		{3_800_000_000, "3.8B"},
		{9_900_000_000, "9.9B"}, // last value in the one-decimal band
		{10_000_000_000, "10B"}, // the band boundary: no decimal from here up
		{35_000_000_000, "35B"},
		{670_600_000_000, "671B"},
	}
	for _, c := range cases {
		if got := formatParamsB(c.in); got != c.want {
			t.Errorf("formatParamsB(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestFormatRate pins the tok/s format both cards share.
func TestFormatRate(t *testing.T) {
	cases := []struct {
		in   float64
		want string
	}{
		{0, unknown},
		{12.1, "12.1"},
		{68.4, "68.4"},
		{96.8, "96.8"},
		{99.94, "99.9"},
		{100, "100"},
		{2450, "2450"},
	}
	for _, c := range cases {
		if got := formatRate(c.in); got != c.want {
			t.Errorf("formatRate(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestRamString: an unread RAM size is "?", not "? GB" — the unit would dress
// an unobserved field up as a measurement with a digit missing.
func TestRamString(t *testing.T) {
	cases := []struct {
		name string
		host tape.HostInfo
		want string
	}{
		{"unread", tape.HostInfo{}, unknown},
		{"speed only", tape.HostInfo{RAMSpeed: "DDR5-6000"}, "? · DDR5-6000"},
		{"size only", tape.HostInfo{RAMBytes: 68719476736}, "64 GB"},
		{"both", tape.HostInfo{RAMBytes: 68719476736, RAMSpeed: "DDR5-6000"}, "64 GB DDR5-6000"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ramString(c.host); got != c.want {
				t.Errorf("ramString = %q, want %q", got, c.want)
			}
		})
	}
}

// TestUnknownRules covers the shared "never print a default you did not
// observe" rule across the formatters the card leans on.
func TestUnknownRules(t *testing.T) {
	if got := formatGiB(0); got != unknown {
		t.Errorf("formatGiB(0) = %q, want %q", got, unknown)
	}
	if got := formatMs(0); got != unknown {
		t.Errorf("formatMs(0) = %q, want %q", got, unknown)
	}
	if got := formatInt(0); got != unknown {
		t.Errorf("formatInt(0) = %q, want %q", got, unknown)
	}
	// formatFloat1 is the exception: its caller has already decided the
	// counter was observed, so an observed zero prints as 0.0.
	if got := formatFloat1(0); got != "0.0" {
		t.Errorf("formatFloat1(0) = %q, want %q", got, "0.0")
	}
	if got := omitUnknown(unknown); got != "" {
		t.Errorf("omitUnknown(%q) = %q, want %q", unknown, got, "")
	}
	if got := joinParts(" · ", "", unknown, "x"); got != "? · x" {
		t.Errorf("joinParts kept the wrong parts: %q", got)
	}
	if got := joinParts(" · ", "", ""); got != unknown {
		t.Errorf("joinParts with nothing observed = %q, want %q", got, unknown)
	}
}

// TestShadeSteps: the three VRAM lightness steps must be distinguishable and
// must descend, or the subdivided bar reads as one flat block.
func TestShadeSteps(t *testing.T) {
	base := colCyan
	steps := []float64{shadeWeights, shadeKV, shadeCompute}
	lum := func(t float64) float64 {
		c := shade(base, t)
		return 0.2126*float64(c.R) + 0.7152*float64(c.G) + 0.0722*float64(c.B)
	}
	for i := 1; i < len(steps); i++ {
		prev, cur := lum(steps[i-1]), lum(steps[i])
		if cur >= prev {
			t.Errorf("step %d is not darker than step %d (%.1f vs %.1f)", i, i-1, cur, prev)
		}
		if prev-cur < 20 {
			t.Errorf("steps %d and %d are only %.1f apart in luminance; they will read as one block", i-1, i, prev-cur)
		}
	}
	if got := shade(base, shadeWeights); got != base {
		t.Errorf("the weights step should keep the device hue exactly: %v != %v", got, base)
	}
}

// TestThinkingString pins the decode column's thinking clause. It is empty
// until a run actually reports reasoning tokens, so joinParts drops it and no
// existing PNG golden moves (TTP-20, 2026-09-13).
func TestThinkingString(t *testing.T) {
	cases := []struct {
		n    int
		want string
	}{
		{0, ""},
		{-1, ""},
		{96, "96 thinking"},
		{12800, "12800 thinking"},
	}
	for _, tc := range cases {
		if got := thinkingString(tc.n); got != tc.want {
			t.Errorf("thinkingString(%d) = %q, want %q", tc.n, got, tc.want)
		}
	}
}
