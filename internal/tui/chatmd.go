package tui

import (
	"sync"
	"unicode/utf8"
)

// Markdown in the chat's answers (look round 2, 2026-09-24: a live Qwen3-1.7B
// answer showed "### Explanation:" and "- " verbatim, which read as a raw dump
// rather than a reply).
//
// Only an answer's own lines are touched — not the user's message, not a
// thought, not a line inside a fenced block — and only what a small model
// actually writes:
//
//   - an ATX heading ("#" to "######", then a space) draws its text bold, the
//     hashes gone;
//   - a "- ", "* " or "+ " item draws "• " at the same indent, and a wrapped
//     item's continuation hangs under its text, not under the bullet;
//   - "**bold**" draws bold, the markers gone;
//   - a numbered item and an inline `code` span stay as they are (the span is
//     already styled by highlight.go).
//
// It must hold while the answer streams. A marker is recognised only once it
// is complete — the space after "-" or "###", the character after a closing
// "**" — and every one of those decisions reads only the text before and at
// the marker, so a later token can never undo it: a line may flip from raw to
// styled once, and never back. The record screen's tiles keep their raw text;
// this is the chat's reading of an answer, through streamTextLinesWith.

// chatMD is the markdown pass over one answer: it wraps the answer runs and
// records which runes of the stream's text are strong.
type chatMD struct {
	// strong is, per rune of the stream's text, whether it draws bold.
	strong []bool
	// total is the rune count of the stream's text, so a run knows whether
	// its last line is the one still being written.
	total int
	// done is a stream that will get no more text: its last line is whole.
	done bool
}

func newChatMD(s Stream) *chatMD {
	md := &chatMD{done: s.Done || s.Err != ""}
	for _, r := range textRuns(s) {
		md.total += utf8.RuneCountInString(r.text)
	}
	return md
}

// isStrong reports whether the rune at stream offset o draws bold.
func (md *chatMD) isStrong(o int) bool {
	return o >= 0 && o < len(md.strong) && md.strong[o]
}

func (md *chatMD) mark(o int) {
	if o < 0 {
		return
	}
	for len(md.strong) <= o {
		md.strong = append(md.strong, false)
	}
	md.strong[o] = true
}

// wrap is an answerWrapper: wrapSource, line by line, with each line read as
// markdown first. A line with no markdown in it wraps exactly as wrapSource
// wraps it.
func (md *chatMD) wrap(text string, base, w int) []wrappedLine {
	if w <= 0 {
		return nil
	}
	lastRun := base+utf8.RuneCountInString(text) >= md.total
	var (
		out     []wrappedLine
		cells   []wrapCell
		raw     []rune
		inFence bool
	)
	off := 0
	for _, r := range text {
		switch r {
		case '\n':
			out = append(out, md.line(cells, string(raw), base, w, true, &inFence)...)
			cells, raw = cells[:0], raw[:0]
		case '\r':
		case '\t':
			for k := 0; k < tabWidth; k++ {
				cells = append(cells, wrapCell{' ', off})
			}
			raw = append(raw, r)
		default:
			cells = append(cells, wrapCell{r, off})
			raw = append(raw, r)
		}
		off++
	}
	return append(out, md.line(cells, string(raw), base, w, !lastRun || md.done, &inFence)...)
}

// line reads one newline-free line of an answer run and wraps it. complete is
// a line whose end has arrived. The fence rule is classifyFences': a fence
// line toggles the block, and every line inside one is code, drawn as written.
func (md *chatMD) line(cells []wrapCell, raw string, base, w int, complete bool, inFence *bool) []wrappedLine {
	if _, ok := fenceLine(raw); ok {
		*inFence = !*inFence
		return wrapParagraph(cells, w)
	}
	if *inFence {
		return wrapParagraph(cells, w)
	}
	ind := 0
	for ind < len(cells) && cells[ind].r == ' ' {
		ind++
	}
	rest := cells[ind:]

	// A heading: up to three spaces of indent, one to six hashes, a space.
	if ind <= 3 {
		if n := atxLevel(rest); n > 0 {
			content := trimCells(rest[n:])
			content = md.inline(content, base, complete)
			for _, c := range content {
				md.mark(base + c.src)
			}
			return wrapParagraph(content, w)
		}
	}

	// A bullet item, at any indent: its marker becomes "•" and the text
	// hangs after it.
	if len(rest) >= 2 && isBulletRune(rest[0].r) && rest[1].r == ' ' {
		ind = min(ind, w/2)
		prefix := make([]wrapCell, 0, ind+2)
		for k := 0; k < ind; k++ {
			prefix = append(prefix, wrapCell{' ', cells[k].src})
		}
		prefix = append(prefix, wrapCell{bulletGlyph, rest[0].src}, wrapCell{' ', rest[1].src})
		pw := cellsWidth(prefix)
		content := md.inline(trimCells(rest[2:]), base, complete)
		if w-pw < bulletMinText {
			// Too narrow to hang: the bullet stays, the text wraps under it.
			return wrapParagraph(append(prefix, content...), w)
		}
		lines := wrapParagraph(content, w-pw)
		for i, wl := range lines {
			lead := prefix
			if i > 0 {
				lead = make([]wrapCell, pw)
				for k := range lead {
					lead[k] = wrapCell{' ', -1}
				}
			}
			pl := cellLine(lead)
			lines[i] = wrappedLine{text: pl.text + wl.text, src: append(pl.src, wl.src...)}
		}
		return lines
	}

	return wrapParagraph(md.inline(cells, base, complete), w)
}

// bulletGlyph is what a list item's marker draws as.
const bulletGlyph = '•'

// bulletMinText is the fewest columns an item's text keeps beside its bullet
// before the hang is given up.
const bulletMinText = 8

func isBulletRune(r rune) bool { return r == '-' || r == '*' || r == '+' }

// atxLevel is the length of the hash run that opens a heading, or 0 when
// cells do not open one. The space after the hashes is what makes it one, so
// "#" alone and "#tag" are text.
func atxLevel(cells []wrapCell) int {
	n := 0
	for n < len(cells) && n < 7 && cells[n].r == '#' {
		n++
	}
	if n == 0 || n > 6 || n >= len(cells) || cells[n].r != ' ' {
		return 0
	}
	return n
}

// trimCells drops the spaces at both ends.
func trimCells(cells []wrapCell) []wrapCell {
	for len(cells) > 0 && cells[0].r == ' ' {
		cells = cells[1:]
	}
	for len(cells) > 0 && cells[len(cells)-1].r == ' ' {
		cells = cells[:len(cells)-1]
	}
	return cells
}

// inline takes the markers of every closed "**" pair out of cells and marks
// the text between them strong. The cells it returns are a new slice when
// anything was taken out, and cells itself when nothing was.
//
// A run of stars is a marker only when it is exactly two long, which is known
// once a character after it has arrived (or the line is complete). An opener
// has text right after it, a closer has text right before it. Inside an inline
// code span — from a backtick on, closed or not — nothing is a marker: a span
// still being written may close, and a "**" that turned bold before it closed
// would turn back.
func (md *chatMD) inline(cells []wrapCell, base int, complete bool) []wrapCell {
	n := len(cells)
	var pairs [][2]int
	open, inCode := -1, false
	for i := 0; i < n; i++ {
		r := cells[i].r
		if r == '`' {
			// Three are a fence's, which classifyFences owns; they end a span.
			if i+2 < n && cells[i+1].r == '`' && cells[i+2].r == '`' {
				inCode = false
				i += 2
				continue
			}
			inCode = !inCode
			continue
		}
		if inCode || r != '*' {
			continue
		}
		j := i
		for j < n && cells[j].r == '*' {
			j++
		}
		if j-i == 2 && (j < n || complete) {
			switch {
			case open >= 0 && i > open+2 && cells[i-1].r != ' ':
				pairs = append(pairs, [2]int{open, i})
				open = -1
			case j < n && cells[j].r != ' ':
				open = i
			}
		}
		i = j - 1
	}
	if len(pairs) == 0 {
		return cells
	}
	out := make([]wrapCell, 0, n-4*len(pairs))
	p := 0
	for i := 0; i < n; i++ {
		if p < len(pairs) {
			a, b := pairs[p][0], pairs[p][1]
			switch {
			case i == a || i == a+1:
				continue
			case i == b || i == b+1:
				if i == b+1 {
					p++
				}
				continue
			case i > a+1 && i < b:
				md.mark(base + cells[i].src)
			}
		}
		out = append(out, cells[i])
	}
	return out
}

// paintBody adds one body line's segments to l, each in its bodyStyle and in
// bold where the markdown made its runes strong.
func (md *chatMD) paintBody(l *lineBuf, th Theme, bl bodyLine, segs []bodySeg) {
	j := 0 // the drawn rune's index into bl.src
	for _, sg := range segs {
		st := bodyStyle(th, bl, sg.band, sg.class)
		start, pos := 0, 0
		cur := false
		for _, r := range sg.text {
			s := cur
			if j < len(bl.src) && bl.src[j] >= 0 && r != ' ' {
				s = md.isStrong(bl.src[j])
			}
			if s != cur && pos > start {
				l.add(strongOf(st, cur), sg.text[start:pos])
				start = pos
			}
			cur = s
			pos += utf8.RuneLen(r)
			j++
		}
		if pos > start {
			l.add(strongOf(st, cur), sg.text[start:pos])
		}
	}
}

// strongStyles caches the bold twin of each style the body paints with, so
// a frame does not re-render escape sequences per segment (TTP-123).
var strongStyles sync.Map // open sequence → style

// strongOf is st in bold when strong is set. The plain theme has nothing to
// set: its zero styles paint text as it is.
func strongOf(st style, strong bool) style {
	if !strong || (st.open == "" && !st.raw) {
		return st
	}
	if st.raw {
		return style{st: st.st.Bold(true), raw: true}
	}
	if v, ok := strongStyles.Load(st.open); ok {
		return v.(style)
	}
	b := seqOf(st.st.Bold(true))
	strongStyles.Store(st.open, b)
	return b
}
