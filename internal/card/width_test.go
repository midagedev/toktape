package card

import "testing"

func TestWidth(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want int
	}{
		{"empty", "", 0},
		{"latin", "Decode 68.4 tok/s", 17},
		{"hangul is two columns", "워크스테이션", 12},
		{"mixed hangul and latin", "RIG 워크스테이션", 16},
		{"box drawing stays one column", "┌──┐│├┤└┘", 9},
		{"bar glyphs stay one column", "[████░░]", 8},
		{"middot times degree approx ellipsis", "· × ° ≈ …", 9},
		{"ansi sgr is stripped", "\x1b[1;31mDecode\x1b[0m", 6},
		{"ansi around hangul", "\x1b[38;5;42m한글\x1b[0m", 4},
		{"osc hyperlink is stripped", "\x1b]8;;https://example.com\x07link\x1b]8;;\x07", 4},
		{"two char escape is stripped", "\x1b(Babc", 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Width(tt.in); got != tt.want {
				t.Errorf("Width(%q) = %d, want %d", tt.in, got, tt.want)
			}
		})
	}
}

func TestStripANSI(t *testing.T) {
	in := "\x1b[1mDecode\x1b[0m 68.4 \x1b[32mtok/s\x1b[0m"
	want := "Decode 68.4 tok/s"
	if got := StripANSI(in); got != want {
		t.Errorf("StripANSI() = %q, want %q", got, want)
	}
}

func TestTruncateAndPad(t *testing.T) {
	tests := []struct {
		name string
		in   string
		w    int
		want string
	}{
		{"fits", "abc", 5, "abc"},
		{"exact", "abcde", 5, "abcde"},
		{"cut latin", "abcdefgh", 5, "abcd…"},
		{"cut before splitting a wide rune", "한글모델", 5, "한글…"},
		{"zero width", "abc", 0, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncate(tt.in, tt.w)
			if got != tt.want {
				t.Errorf("truncate(%q, %d) = %q, want %q", tt.in, tt.w, got, tt.want)
			}
			if w := Width(got); w > tt.w {
				t.Errorf("truncate(%q, %d) width = %d, want <= %d", tt.in, tt.w, w, tt.w)
			}
			if w := Width(pad(tt.in, tt.w)); w != tt.w {
				t.Errorf("pad(%q, %d) width = %d, want %d", tt.in, tt.w, w, tt.w)
			}
		})
	}
}

// The card must measure the same on a CJK locale, where runewidth's package
// level functions widen every East Asian *Ambiguous* rune to two columns. Our
// Condition pins that off; this test asserts the glyphs the card is built from
// are unaffected.
func TestWidthIgnoresAmbiguousWidening(t *testing.T) {
	for _, s := range []string{"┌─┐│├┤└┘", "█░", "·×°≈…"} {
		if got, want := Width(s), len([]rune(s)); got != want {
			t.Errorf("Width(%q) = %d, want %d (ambiguous runes must stay one column)", s, got, want)
		}
	}
}
