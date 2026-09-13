package tui

import (
	"strings"

	"github.com/mattn/go-runewidth"
	"github.com/midagedev/toktape/internal/card"
)

// cond measures per-rune display width for the live view.
//
// It is deliberately the same condition internal/card pins: runewidth's
// package-level functions widen East Asian *Ambiguous* runes to two columns
// under a CJK locale, and this view is built out of ambiguous runes — the box
// drawing set, "█", "░", "▏", "▁", "·", "×", "°". A locale-sensitive
// measurement would lay the same tape out differently on the recorder's
// machine and on the reader's. Hangul syllables are category Wide, not
// Ambiguous, so they still measure two columns, which is what the CJK
// contract asks for (handover lesson 5).
var cond = &runewidth.Condition{EastAsianWidth: false, StrictEmojiNeutral: true}

// width returns the display width of s in terminal columns, ignoring ANSI
// escapes. It is card.Width, re-exported under the package's own name so the
// two renderers can never drift apart.
func width(s string) int { return card.Width(s) }

// runeWidth returns the column count of a single rune under cond.
func runeWidth(r rune) int { return cond.RuneWidth(r) }

// truncate shortens the ANSI-free string s to at most w columns, appending "…"
// when anything was cut. A cut that would land inside a wide rune leaves the
// result one column short rather than splitting the rune in half.
func truncate(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if cond.StringWidth(s) <= w {
		return s
	}
	return cond.Truncate(s, w, "…")
}

// clip shortens the ANSI-free string s to at most w columns with no ellipsis.
// Used where an ellipsis would read as data (bars, sparklines, strips).
func clip(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if cond.StringWidth(s) <= w {
		return s
	}
	return cond.Truncate(s, w, "")
}

// pad right-pads s with spaces to exactly w columns, truncating first when it
// is too wide. s may contain ANSI escapes; they do not count toward the width.
func pad(s string, w int) string {
	n := w - width(s)
	if n <= 0 {
		return s
	}
	return s + strings.Repeat(" ", n)
}

// padLeft left-pads s with spaces to exactly w columns. Used for the tabular
// alignment of every number in the right pane.
func padLeft(s string, w int) string {
	n := w - width(s)
	if n <= 0 {
		return s
	}
	return strings.Repeat(" ", n) + s
}

// center pads s with spaces on both sides to exactly w columns.
func center(s string, w int) string {
	n := w - width(s)
	if n <= 0 {
		return s
	}
	left := n / 2
	return strings.Repeat(" ", left) + s + strings.Repeat(" ", n-left)
}

// repeat returns n copies of the single-column rune r.
func repeat(r rune, n int) string {
	if n <= 0 {
		return ""
	}
	return strings.Repeat(string(r), n)
}

// wrap word-wraps s to lines of at most w display columns.
//
// Wrapping is done in columns, not bytes or runes, so a Hangul syllable
// (two columns) is never split across the pane border: when the next rune
// would overflow, the line ends one column short and the rune starts the next
// line. Explicit newlines in s start a new line. A word longer than w is
// broken at the column limit rather than overflowing.
func wrap(s string, w int) []string {
	if w <= 0 {
		return nil
	}
	var out []string
	for i, para := range strings.Split(s, "\n") {
		if i > 0 && para == "" {
			out = append(out, "")
			continue
		}
		out = append(out, wrapParagraph(para, w)...)
	}
	return out
}

// wrapParagraph wraps one newline-free string greedily, word by word.
func wrapParagraph(s string, w int) []string {
	words := splitWords(s)
	if len(words) == 0 {
		return []string{""}
	}
	var (
		out  []string
		line strings.Builder
		used int
	)
	flushLine := func() {
		out = append(out, line.String())
		line.Reset()
		used = 0
	}
	for _, word := range words {
		ww := cond.StringWidth(word)
		// A word wider than the whole line is broken at the column limit.
		for ww > w {
			if used > 0 {
				flushLine()
			}
			head := cond.Truncate(word, w, "")
			out = append(out, head)
			word = word[len(head):]
			ww = cond.StringWidth(word)
		}
		if used == 0 {
			line.WriteString(word)
			used = ww
			continue
		}
		if used+1+ww > w {
			flushLine()
			line.WriteString(word)
			used = ww
			continue
		}
		line.WriteString(" ")
		line.WriteString(word)
		used += 1 + ww
	}
	if used > 0 || len(out) == 0 {
		flushLine()
	}
	return out
}

// splitWords splits s on spaces, dropping empty runs. CJK text often carries
// no spaces at all, in which case the whole run is one "word" and the
// column-limited break above handles it — which is exactly the case the
// Hangul border test pins.
func splitWords(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool { return r == ' ' || r == '\t' })
}

// tail returns the last n elements of lines, or all of them when there are
// fewer. Streaming panes are anchored to the bottom, like a terminal.
//
// It is generic over the element type because the answer pane draws styled
// lines (bodyLine) while every other caller draws plain strings, and both want
// the same anchoring rule.
func tail[T any](lines []T, n int) []T {
	if n <= 0 {
		return nil
	}
	if len(lines) <= n {
		return lines
	}
	return lines[len(lines)-n:]
}
