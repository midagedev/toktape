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

// tabWidth is the columns one tab in generated text takes (TTP-48,
// 2026-09-14). A fixed width rather than tab stops: a token does not know the
// column it lands in, and the tabs a model writes are indentation, where the
// two agree. Four rather than eight, because a tile is forty to seventy
// columns wide and gofmt's own indentation reads at four.
const tabWidth = 4

// wrappedLine is one wrapped line and, rune for rune, where each drawn rune
// came from: its rune offset in the string that was wrapped, or -1 for a rune
// the wrapper wrote itself (the indent a continuation line hangs from).
type wrappedLine struct {
	text string
	src  []int
}

// wrap word-wraps s to lines of at most w display columns.
//
// Wrapping is done in columns, not bytes or runes, so a Hangul syllable
// (two columns) is never split across the pane border: when the next rune
// would overflow, the line ends one column short and the rune starts the next
// line. Explicit newlines in s start a new line. A word longer than w is
// broken at the column limit rather than overflowing.
//
// The whitespace is the model's (TTP-48, user 2026-09-14: "코드 출력시 포메팅이
// 깨지는"). The wrapper was written for prose and joined every word back with
// one space, so a fenced code block lost its indentation, a tab-indented Go
// body came out flush left, and the spaces lining up a trailing comment
// collapsed. A line's leading indent is kept (a tab is tabWidth columns), runs
// of spaces between words are kept, and a line that has to wrap continues at
// its own indent so the block keeps its shape. Only the gap a break falls on
// is dropped, which is all prose ever lost, so prose wraps as it did.
func wrap(s string, w int) []string {
	lines := wrapSource(s, w)
	if lines == nil {
		return nil
	}
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = l.text
	}
	return out
}

// wrapCell is one display rune of a paragraph and its offset in the source.
type wrapCell struct {
	r   rune
	src int
}

// wrapSource is wrap that also says where every drawn rune came from.
//
// The answer pane needs that to shade a rune by the age of the token that
// produced it. It used to recover it by walking the source forward to the next
// occurrence of each drawn rune, which held only while the wrapper drew
// nothing but the source's own runes in their order; a tab drawn as spaces and
// a hanging indent both break that. The wrapper is the one place that knows,
// so it reports it.
func wrapSource(s string, w int) []wrappedLine {
	if w <= 0 {
		return nil
	}
	var (
		out  []wrappedLine
		para []wrapCell
	)
	off := 0
	for _, r := range s {
		switch r {
		case '\n':
			out = append(out, wrapParagraph(para, w)...)
			para = para[:0]
		case '\r':
			// A CRLF answer is still one line break.
		case '\t':
			for k := 0; k < tabWidth; k++ {
				para = append(para, wrapCell{' ', off})
			}
		default:
			para = append(para, wrapCell{r, off})
		}
		off++
	}
	return append(out, wrapParagraph(para, w)...)
}

// wrapParagraph wraps one newline-free run of cells greedily, word by word.
func wrapParagraph(cells []wrapCell, w int) []wrappedLine {
	ind := 0
	for ind < len(cells) && cells[ind].r == ' ' {
		ind++
	}
	if ind == len(cells) {
		return []wrappedLine{{}}
	}
	// One leading space is a tokenizer's word boundary, not an indent: a
	// stream that opens with " The" would otherwise hang every line of its
	// paragraph a column in. And an indent is capped at half the line, so a
	// deeply nested block in a narrow tile still has room for its words.
	if ind == 1 {
		cells, ind = cells[1:], 0
	}
	if ind > w/2 {
		cells, ind = cells[ind-w/2:], w/2
	}

	var (
		lines   []wrappedLine
		cur     = append([]wrapCell(nil), cells[:ind]...)
		used    = ind
		hasWord bool
		gap     []wrapCell
	)
	// flush ends the line and starts the next one at the paragraph's indent,
	// written by the wrapper.
	flush := func() {
		lines = append(lines, cellLine(cur))
		cur = make([]wrapCell, ind, ind+w)
		for k := range cur {
			cur[k] = wrapCell{' ', -1}
		}
		used, hasWord = ind, false
	}
	for i := ind; i < len(cells); {
		j := i
		if cells[i].r == ' ' {
			for j < len(cells) && cells[j].r == ' ' {
				j++
			}
			gap, i = cells[i:j], j
			continue
		}
		for j < len(cells) && cells[j].r != ' ' {
			j++
		}
		word, ww := cells[i:j], cellsWidth(cells[i:j])
		i = j
		if hasWord {
			if used+len(gap)+ww <= w {
				cur = append(append(cur, gap...), word...)
				used += len(gap) + ww
				gap = nil
				continue
			}
			flush()
		}
		gap = nil
		// A word wider than what is left of the line is broken at the column
		// limit.
		for used+ww > w {
			n, nw := fitCells(word, w-used)
			if n == 0 {
				if used > 0 {
					// Not one rune fits beside the indent: this line gives
					// the indent up.
					cur, used = cur[:0], 0
					continue
				}
				// The line is narrower than the rune itself. Draw it anyway:
				// a wrapper that cannot place a rune must still make progress.
				n, nw = 1, runeWidth(word[0].r)
			}
			cur = append(cur, word[:n]...)
			used += nw
			word, ww = word[n:], ww-nw
			if len(word) == 0 {
				break
			}
			flush()
		}
		cur = append(cur, word...)
		used += ww
		hasWord = true
	}
	return append(lines, cellLine(cur))
}

// cellLine turns a line's cells into its text and its source offsets.
func cellLine(cells []wrapCell) wrappedLine {
	var b strings.Builder
	src := make([]int, len(cells))
	for i, c := range cells {
		b.WriteRune(c.r)
		src[i] = c.src
	}
	return wrappedLine{text: b.String(), src: src}
}

// cellsWidth is the display width of a run of cells.
func cellsWidth(cells []wrapCell) int {
	n := 0
	for _, c := range cells {
		n += runeWidth(c.r)
	}
	return n
}

// fitCells is how many leading cells fit in room columns, and their width.
func fitCells(cells []wrapCell, room int) (n, w int) {
	for n < len(cells) {
		rw := runeWidth(cells[n].r)
		if w+rw > room {
			break
		}
		w += rw
		n++
	}
	return n, w
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
