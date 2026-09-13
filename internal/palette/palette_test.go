package palette

import (
	"math"
	"testing"
)

// all is every exported colour, by name.
var all = map[string]string{
	"Ground": Ground, "Text": Text, "Dim": Dim, "Accent": Accent, "Warn": Warn, "Bad": Bad,
	"TextMid": TextMid, "TextMuted": TextMuted, "DimMid": DimMid,
	"AccentHigh": AccentHigh, "AccentMid": AccentMid, "AccentMuted": AccentMuted, "AccentLow": AccentLow,
	"DarkFill": DarkFill,
	"CardBase": CardBase, "CardPanel": CardPanel, "CardBorder": CardBorder, "CardSurface": CardSurface,
	"CardGPU1": CardGPU1, "CardGPU2": CardGPU2, "CardGPU3": CardGPU3, "CardHost": CardHost,
}

// luminance is WCAG 2 relative luminance.
func luminance(hex string) float64 {
	c := RGBA(hex)
	lin := func(v uint8) float64 {
		s := float64(v) / 255
		if s <= 0.04045 {
			return s / 12.92
		}
		return math.Pow((s+0.055)/1.055, 2.4)
	}
	return 0.2126*lin(c.R) + 0.7152*lin(c.G) + 0.0722*lin(c.B)
}

// saturation is HSL saturation.
func saturation(hex string) float64 {
	c := RGBA(hex)
	r, g, b := float64(c.R)/255, float64(c.G)/255, float64(c.B)/255
	hi, lo := math.Max(r, math.Max(g, b)), math.Min(r, math.Min(g, b))
	if hi == lo {
		return 0
	}
	l := (hi + lo) / 2
	if l > 0.5 {
		return (hi - lo) / (2 - hi - lo)
	}
	return (hi - lo) / (hi + lo)
}

func TestEveryColourParses(t *testing.T) {
	for name, hex := range all {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("%s = %q: %v", name, hex, r)
				}
			}()
			if c := RGBA(hex); c.A != 0xff {
				t.Errorf("%s = %q parsed with alpha %d", name, hex, c.A)
			}
		}()
	}
}

func TestRGBAPanicsOnMalformed(t *testing.T) {
	for _, bad := range []string{"", "86c2b4", "#86c2b", "#86c2b4ff", "#86c2bg"} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("RGBA(%q) did not panic", bad)
				}
			}()
			RGBA(bad)
		}()
	}
	if got := RGBA("#86c2b4"); got.R != 0x86 || got.G != 0xc2 || got.B != 0xb4 {
		t.Errorf("RGBA(#86c2b4) = %v", got)
	}
}

// TestBodyTextLadder pins the TUI's emphasis contract (TTP-28): body text sits
// at about half the header's luminance, and reasoning at most 80 % of the body.
func TestBodyTextLadder(t *testing.T) {
	if r := luminance(TextMuted) / luminance(Text); r < 0.44 || r > 0.54 {
		t.Errorf("TextMuted/Text luminance = %.3f, want 0.44–0.54", r)
	}
	if r := luminance(Dim) / luminance(TextMuted); r > 0.80 {
		t.Errorf("Dim/TextMuted luminance = %.3f, want ≤ 0.80", r)
	}
	if luminance(Accent) >= luminance(Text) {
		t.Errorf("Accent luminance %.3f is not below Text's %.3f", luminance(Accent), luminance(Text))
	}
}

// TestGPUStepsDistinguishable: adjacent placement-bar segments are the same
// hue, so they must differ in luminance by at least 1.25× pairwise.
func TestGPUStepsDistinguishable(t *testing.T) {
	steps := []string{Accent, CardGPU1, CardGPU2, CardGPU3}
	names := []string{"Accent", "CardGPU1", "CardGPU2", "CardGPU3"}
	for i := range steps {
		for j := i + 1; j < len(steps); j++ {
			a, b := luminance(steps[i]), luminance(steps[j])
			if r := math.Max(a, b) / math.Min(a, b); r < 1.25 {
				t.Errorf("%s/%s luminance ratio = %.3f, want ≥ 1.25", names[i], names[j], r)
			}
		}
	}
}

// TestHostIsNotAWarning: CardHost and Warn are both warm and a few degrees
// apart in hue, so the host segment is kept from reading as a warning by
// saturation.
func TestHostIsNotAWarning(t *testing.T) {
	if h, w := saturation(CardHost), saturation(Warn); h > w/2 {
		t.Errorf("CardHost saturation %.3f is more than half of Warn's %.3f", h, w)
	}
}
