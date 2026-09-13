package tui

import (
	"strings"
	"testing"
	"time"
)

// TTP-48 (user 2026-09-14: "코드 출력시 포메팅이 깨지는"). The wrapper was
// built for prose: it split every paragraph on spaces and tabs and joined the
// words back with one space, so an answer in a fenced code block came out with
// no indentation, a tab-indented Go body flush left, and the spaces that line a
// trailing comment up collapsed. These pin the whitespace a model wrote.

func TestWrapKeepsCodeIndentation(t *testing.T) {
	src := "def get(self, key):\n    if key not in self.cache:\n        return -1\n\treturn self.cache[key]"
	want := []string{
		"def get(self, key):",
		"    if key not in self.cache:",
		"        return -1",
		"    return self.cache[key]", // one tab is four columns
	}
	assertLines(t, wrap(src, 40), want)
}

func TestWrapKeepsRunsOfSpaces(t *testing.T) {
	assertLines(t, wrap("x := 1   // one", 40), []string{"x := 1   // one"})
}

// A wrapped line of an indented block continues at its own indent, so the
// block's shape survives a narrow tile.
func TestWrapHangsAnIndentedContinuation(t *testing.T) {
	got := wrap("    return compute(alpha, beta, gamma)", 26)
	assertLines(t, got, []string{"    return compute(alpha,", "    beta, gamma)"})
	for i, l := range got {
		if width(l) > 26 {
			t.Errorf("line %d is %d columns: %q", i, width(l), l)
		}
	}
}

// Prose wraps exactly as it did: a single space between words is still the
// only thing a break may drop.
func TestWrapLeavesProseAlone(t *testing.T) {
	assertLines(t, wrap("the quick brown fox jumps", 11), []string{"the quick", "brown fox", "jumps"})
}

func assertLines(t *testing.T, got, want []string) {
	t.Helper()
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("wrap:\n got %q\nwant %q", got, want)
	}
}

// TestBandsFollowTheSourceThroughCode guards the other half of TTP-48: a
// rune's age band is read off the offset the wrapper reports, so a tab drawn
// as four spaces and the indent a wrapped line hangs from cannot move the glow
// off the token that produced it. The fresh and mid bands must spell exactly
// the newest two tokens, and every rune the wrapper wrote itself is settled.
func TestBandsFollowTheSourceThroughCode(t *testing.T) {
	const at = 10 * time.Second
	texts := []string{
		"func main() {\n",
		"\tfor i := 0; i < n; i++ {\n",
		"\t\tout <- compute(alpha, beta, gamma, delta)\n",
		"\t}\n",
		"\treturn done",
	}
	ages := []time.Duration{3 * time.Second, 3 * time.Second, 3 * time.Second, 300 * time.Millisecond, 50 * time.Millisecond}
	var s Stream
	for i, tx := range texts {
		s.Tokens = append(s.Tokens, Token{T: at - ages[i], Text: tx})
		s.Text += tx
	}
	// 32 columns: the for line (28) fits whole, and the call under it (49)
	// has to wrap, so the hanging indent is exercised.
	lines := streamTextLines(s, 32)
	var drawn []string
	for _, l := range lines {
		drawn = append(drawn, l.text)
	}
	if !strings.HasPrefix(strings.Join(drawn, "\n"), "func main() {\n    for i := 0; i < n; i++ {\n        out <- ") {
		t.Fatalf("the code lost its indentation:\n%s", strings.Join(drawn, "\n"))
	}
	bands := bodyBands(s, lines, at)
	var fresh, mid strings.Builder
	for i, l := range lines {
		k := 0
		for _, sg := range bands[i] {
			for _, r := range sg.text {
				if l.src[k] < 0 && sg.band != bandSettled {
					t.Errorf("line %d col %d: a rune the wrapper wrote is in band %d", i, k, sg.band)
				}
				k++
				if r == ' ' {
					continue
				}
				switch sg.band {
				case bandFresh:
					fresh.WriteRune(r)
				case bandMid:
					mid.WriteRune(r)
				}
			}
		}
	}
	if got := fresh.String(); got != "returndone" {
		t.Errorf("the fresh band spells %q, want the newest token %q\n%s", got, "returndone", strings.Join(drawn, "\n"))
	}
	if got := mid.String(); got != "}" {
		t.Errorf("the mid band spells %q, want the token before it %q", got, "}")
	}
}

// TestTheIndentDoesNotGlow: the write head's fill stops at a line's indent
// (TTP-47 × TTP-48, 2026-09-14). Seen on the real deepseek code tape: a fresh
// token "\t\tgo func" carried its tab-expanded spaces at source offsets
// >= freshStart, so the fill painted an eight-column blank slab before the
// code. Leading indent is layout, not a token the reader is watching arrive.
func TestTheIndentDoesNotGlow(t *testing.T) {
	const at = 10 * time.Second
	var s Stream
	for _, tk := range []struct {
		age  time.Duration
		text string
	}{
		{3 * time.Second, "func main() {\n"},
		{50 * time.Millisecond, "\t\tgo func"},
	} {
		s.Tokens = append(s.Tokens, Token{T: at - tk.age, Text: tk.text})
		s.Text += tk.text
	}
	lines := streamTextLines(s, 40)
	bands := bodyBands(s, lines, at)
	last := bands[len(bands)-1]
	if len(last) < 2 || last[0].band != bandSettled || last[0].text != "        " {
		t.Fatalf("the indent should be a settled run of 8 spaces before the fresh code, got %+v", last)
	}
	if last[1].band != bandFresh || last[1].text != "go func" {
		t.Fatalf("the fresh run should be the code alone, got %+v", last)
	}
}
