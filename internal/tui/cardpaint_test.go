package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/midagedev/toktape/internal/card"
)

// TestPaintCardLineByRole is the rune-class contract (TTP-42): one case per
// class, checked as the segment the painter actually emits rather than as a
// colour somewhere on the line.
func TestPaintCardLineByRole(t *testing.T) {
	th := ColourTheme()
	for _, c := range []struct {
		what string
		in   string
		st   lipgloss.Style
	}{
		{"a gauge fill", "████", th.accentMuted},
		{"a gauge track", "░░░░", th.darkFill},
		{"a rule", "├────┤", th.dim},
		{"a border", "│", th.dim},
		{"a figure", "23.8/24.0 GiB", th.text},
		{"a label", "MEMORY", th.text},
	} {
		t.Run(c.what, func(t *testing.T) {
			if got, want := paintCardLine(th, c.in), th.paint(c.st, c.in); got != want {
				t.Errorf("%q\n got %q\nwant %q", c.in, got, want)
			}
		})
	}
}

// TestPaintCardLineSegmentsRuns: a run of one class is one styled segment, so
// a seventy-column frame costs one escape sequence and not seventy. The whole
// MEMORY row is checked as a sequence of segments, in order, which is also the
// readable statement of what the row is made of.
func TestPaintCardLineSegmentsRuns(t *testing.T) {
	th := ColourTheme()
	const line = "│ MEMORY   GPU0 [███░░] 23.8/24.0 GiB │"
	want := th.paint(th.dim, "│") +
		th.paint(th.text, " MEMORY   GPU0 [") +
		th.paint(th.accentMuted, "███") +
		th.paint(th.darkFill, "░░") +
		th.paint(th.text, "] 23.8/24.0 GiB ") +
		th.paint(th.dim, "│")
	got := paintCardLine(th, line)
	if got != want {
		t.Errorf("\n got %q\nwant %q", got, want)
	}
	// Six segments, six escape sequences, and not one per column.
	if n := strings.Count(got, "\x1b["); n != 12 { // an opener and a reset each
		t.Errorf("the line carries %d escape sequences, want 12 (six segments)", n)
	}
}

// TestPaintCardLineKeepsTheText: the painter adds escapes and changes nothing
// else — not the runes, not their order, not the line's width. This is the
// per-line half of TestColourMatchesPlain, which pins the same thing for the
// whole ModeCard frame.
func TestPaintCardLineKeepsTheText(t *testing.T) {
	th := ColourTheme()
	summary := ExampleTapeN(4).Summary
	for _, line := range strings.Split(strings.TrimRight(card.Text(&summary), "\n"), "\n") {
		painted := paintCardLine(th, line)
		if got := card.StripANSI(painted); got != line {
			t.Errorf("stripping the palette does not reproduce the line\n got %q\nwant %q", got, line)
		}
		if got := width(painted); got != width(line) {
			t.Errorf("the painted line is %d columns, want %d: %q", got, width(line), line)
		}
	}
}

// TestPaintCardLinePlain: the plain theme returns the line untouched, which is
// what the goldens render and what the -update baseline rests on.
func TestPaintCardLinePlain(t *testing.T) {
	th := PlainTheme()
	for _, line := range []string{"", "│ MEMORY   GPU0 [███░░] │", "┌──┐"} {
		if got := paintCardLine(th, line); got != line {
			t.Errorf("plain theme painted %q as %q", line, got)
		}
	}
}
