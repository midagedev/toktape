package tui

import (
	"strings"
	"testing"

	"github.com/midagedev/toktape/internal/tape"
)

// TestWrapNeverSplitsAWideRune is the core of the CJK contract: wrapping is
// done in display columns, so a Hangul syllable either fits whole on a line or
// starts the next one. A line may come back one column short; it may never
// come back one column long, because that is the column the border lives in
// (handover lesson 5).
func TestWrapNeverSplitsAWideRune(t *testing.T) {
	texts := []string{
		"레이어 배치를 바꾸면 디코드 속도가 달라지는 이유는 간단하다.",
		"페이지캐시에서밀려난텐서는NVMe까지내려간다",                          // no spaces at all
		"mixed 한글 and latin 텍스트 wrapped 여러 번 across lines", // mixed scripts
		"短い日本語のテキストも同じ規則で折り返す",
	}
	for _, text := range texts {
		for w := 3; w <= 40; w++ {
			lines := wrap(text, w)
			var rebuilt strings.Builder
			for i, line := range lines {
				if got := width(line); got > w {
					t.Fatalf("width %d: line %d is %d columns: %q", w, i, got, line)
				}
				rebuilt.WriteString(line)
			}
			// Every rune survives the wrap; only the spaces at the breaks move.
			if got, want := stripSpaces(rebuilt.String()), stripSpaces(text); got != want {
				t.Fatalf("width %d: wrapping changed the text\n got %q\nwant %q", w, got, want)
			}
		}
	}
}

func stripSpaces(s string) string {
	return strings.Join(strings.Fields(s), "")
}

func TestWrapKeepsWordsWhole(t *testing.T) {
	got := wrap("the quick brown fox jumps", 11)
	want := []string{"the quick", "brown fox", "jumps"}
	if len(got) != len(want) {
		t.Fatalf("got %d lines %q, want %d", len(got), got, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestWrapBreaksAnOverlongWord(t *testing.T) {
	got := wrap("supercalifragilistic", 6)
	for i, line := range got {
		if width(line) > 6 {
			t.Fatalf("line %d is too wide: %q", i, line)
		}
	}
	if strings.Join(got, "") != "supercalifragilistic" {
		t.Errorf("breaking the word lost characters: %q", got)
	}
}

func TestWrapEdges(t *testing.T) {
	if got := wrap("anything", 0); got != nil {
		t.Errorf("width 0 returned %q", got)
	}
	if got := wrap("", 10); len(got) != 1 || got[0] != "" {
		t.Errorf("the empty string wrapped to %q, want one empty line", got)
	}
	if got := wrap("a\n\nb", 10); len(got) != 3 {
		t.Errorf("newlines were not preserved: %q", got)
	}
}

func TestPadAndTruncate(t *testing.T) {
	tests := []struct {
		in   string
		w    int
		want string
	}{
		{"abc", 5, "abc  "},
		{"abc", 3, "abc"},
		{"한글", 5, "한글 "},
		{"한글", 4, "한글"},
	}
	for _, tc := range tests {
		if got := pad(tc.in, tc.w); got != tc.want {
			t.Errorf("pad(%q, %d) = %q, want %q", tc.in, tc.w, got, tc.want)
		}
		if got := width(pad(tc.in, tc.w)); got != tc.w {
			t.Errorf("pad(%q, %d) is %d columns", tc.in, tc.w, got)
		}
	}
	// Cutting a wide rune in half is never allowed: the line comes back a
	// column short instead.
	if got := clip("한글", 3); width(got) > 3 {
		t.Errorf("clip(%q, 3) = %q, %d columns", "한글", got, width(got))
	}
	if got := truncate("abcdefgh", 4); width(got) != 4 {
		t.Errorf("truncate = %q, %d columns, want 4", got, width(got))
	}
}

func TestPadLeftAndCenter(t *testing.T) {
	if got := padLeft("42", 6); got != "    42" {
		t.Errorf("padLeft = %q", got)
	}
	if got := center("ab", 6); got != "  ab  " {
		t.Errorf("center = %q", got)
	}
	if got := center("한", 5); width(got) != 5 {
		t.Errorf("center of a wide rune is %d columns", width(got))
	}
}

func TestTail(t *testing.T) {
	lines := []string{"a", "b", "c", "d"}
	if got := tail(lines, 2); len(got) != 2 || got[0] != "c" {
		t.Errorf("tail = %q, want the last two", got)
	}
	if got := tail(lines, 9); len(got) != 4 {
		t.Errorf("tail beyond the length returned %d lines", len(got))
	}
	if got := tail(lines, 0); got != nil {
		t.Errorf("tail(0) = %q", got)
	}
}

// TestLineBufIsExact: every screen line goes through lineBuf, so its width
// guarantee is the guarantee the whole frame rests on — including when the
// caller asks it to write more than fits.
func TestLineBufIsExact(t *testing.T) {
	th := PlainTheme()
	for _, w := range []int{1, 5, 20} {
		l := newLine(th, w)
		l.add(th.text, "한글 text that is far too long for this line")
		l.space(10)
		l.add(th.dim, "more")
		if got := width(l.String()); got != w {
			t.Errorf("overfilled lineBuf of width %d produced %d columns", w, got)
		}
	}
	l := newLine(th, 20)
	l.add(th.text, "ab")
	l.gapTo(4)
	l.add(th.dim, "cdef")
	if got := l.String(); got != "ab              cdef" {
		t.Errorf("gapTo did not right-align: %q", got)
	}
}

func TestFormatters(t *testing.T) {
	tests := []struct{ got, want string }{
		{fmtG(0), "?"},
		{fmtG(-1), "?"},
		{fmtG(gib), "1.0G"},
		{fmtG(200 * gib), "200G"},
		{fmtRate(0), "?"},
		{fmtRate(12.14), "12.1"},
		{fmtRate(2450), "2450"},
		{fmtMs(0), "?"},
		{fmtMs(143.4), "143 ms"},
		{fmtPct(0), "0%"},
		{fmtPct(0.78125), "78%"},
		{orUnknown(""), "?"},
		{orUnknown("chatml"), "chatml"},
	}
	for _, tc := range tests {
		if tc.got != tc.want {
			t.Errorf("got %q, want %q", tc.got, tc.want)
		}
	}
}

func TestRigSummary(t *testing.T) {
	m := ModelAt(ExampleTape(), 0)
	if got := rigSummary(m.Summary.Host); got != "2× RTX 3090" {
		t.Errorf("rigSummary = %q, want %q", got, "2× RTX 3090")
	}
	if got := rigSummary(tape.HostInfo{}); got != "" {
		t.Errorf("a rig with no GPUs summarised as %q", got)
	}
}
