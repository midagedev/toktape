package tui

import (
	"strings"
	"time"
)

// The chat screen's input line (TTP-184, 2026-09-24).
//
// Hand-rolled rather than bubbles' textarea for two reasons. The textarea
// blinks its cursor on a timer of its own, which is a second clock beside the
// clip time t every other moving part of this package reads (anim.go); and it
// measures columns with its own width function, where this package measures
// with cond — the one table that counts a Hangul syllable as two columns and a
// box-drawing rune as one on every machine. An input that measured differently
// from the frame around it would put the cursor a column off after the first
// Korean word. A slice of runes and an index is all an input line needs, and
// it makes "backspace deletes one syllable" true by construction.

// ChatInput is the text being typed and where the cursor sits in it, as a rune
// index: 0 is before the first rune, len(Runes) after the last.
//
// Every edit returns a new value and never writes into the old slice, so a
// frame that still holds the previous model keeps drawing the previous text.
type ChatInput struct {
	Runes  []rune
	Cursor int
}

// Text is what was typed.
func (in ChatInput) Text() string { return string(in.Runes) }

// Empty reports whether nothing has been typed.
func (in ChatInput) Empty() bool { return len(in.Runes) == 0 }

// clamp keeps the cursor inside the text.
func (in ChatInput) clamp() ChatInput {
	if in.Cursor < 0 {
		in.Cursor = 0
	}
	if in.Cursor > len(in.Runes) {
		in.Cursor = len(in.Runes)
	}
	return in
}

// Insert puts rs at the cursor and moves the cursor past them. A carriage
// return becomes a newline and every other control rune is dropped: a paste
// from a CRLF file is still lines, and an escape sequence pasted by accident
// must not reach a frame as a raw ESC.
func (in ChatInput) Insert(rs []rune) ChatInput {
	in = in.clamp()
	clean := make([]rune, 0, len(rs))
	for i, r := range rs {
		switch {
		case r == '\r':
			if i+1 < len(rs) && rs[i+1] == '\n' {
				continue
			}
			clean = append(clean, '\n')
		case r == '\t':
			clean = append(clean, ' ', ' ', ' ', ' ')
		case r == '\n':
			clean = append(clean, r)
		case r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0):
		default:
			clean = append(clean, r)
		}
	}
	if len(clean) == 0 {
		return in
	}
	out := make([]rune, 0, len(in.Runes)+len(clean))
	out = append(out, in.Runes[:in.Cursor]...)
	out = append(out, clean...)
	out = append(out, in.Runes[in.Cursor:]...)
	return ChatInput{Runes: out, Cursor: in.Cursor + len(clean)}
}

// Backspace deletes the rune before the cursor: one Hangul syllable, one
// letter, one newline.
func (in ChatInput) Backspace() ChatInput {
	in = in.clamp()
	if in.Cursor == 0 {
		return in
	}
	out := make([]rune, 0, len(in.Runes)-1)
	out = append(out, in.Runes[:in.Cursor-1]...)
	out = append(out, in.Runes[in.Cursor:]...)
	return ChatInput{Runes: out, Cursor: in.Cursor - 1}
}

// Delete deletes the rune under the cursor.
func (in ChatInput) Delete() ChatInput {
	in = in.clamp()
	if in.Cursor >= len(in.Runes) {
		return in
	}
	out := make([]rune, 0, len(in.Runes)-1)
	out = append(out, in.Runes[:in.Cursor]...)
	out = append(out, in.Runes[in.Cursor+1:]...)
	return ChatInput{Runes: out, Cursor: in.Cursor}
}

// DeleteToStart deletes from the start of the cursor's line to the cursor,
// which is what ctrl+u does in a shell.
func (in ChatInput) DeleteToStart() ChatInput {
	in = in.clamp()
	start := in.lineStart()
	out := make([]rune, 0, len(in.Runes)-(in.Cursor-start))
	out = append(out, in.Runes[:start]...)
	out = append(out, in.Runes[in.Cursor:]...)
	return ChatInput{Runes: out, Cursor: start}
}

// DeleteWord deletes the word before the cursor and the spaces after it, as
// ctrl+w does in a shell.
func (in ChatInput) DeleteWord() ChatInput {
	in = in.clamp()
	i := in.Cursor
	for i > 0 && in.Runes[i-1] == ' ' {
		i--
	}
	for i > 0 && in.Runes[i-1] != ' ' && in.Runes[i-1] != '\n' {
		i--
	}
	out := make([]rune, 0, len(in.Runes)-(in.Cursor-i))
	out = append(out, in.Runes[:i]...)
	out = append(out, in.Runes[in.Cursor:]...)
	return ChatInput{Runes: out, Cursor: i}
}

// Move shifts the cursor by d runes.
func (in ChatInput) Move(d int) ChatInput {
	in.Cursor += d
	return in.clamp()
}

// Home moves the cursor to the start of its line, End to the end of it.
func (in ChatInput) Home() ChatInput {
	in = in.clamp()
	in.Cursor = in.lineStart()
	return in
}

func (in ChatInput) End() ChatInput {
	in = in.clamp()
	for in.Cursor < len(in.Runes) && in.Runes[in.Cursor] != '\n' {
		in.Cursor++
	}
	return in
}

func (in ChatInput) lineStart() int {
	i := in.Cursor
	for i > 0 && in.Runes[i-1] != '\n' {
		i--
	}
	return i
}

// inputRow is one drawn row of the input: its runes, and the column the
// cursor sits at when it is on this row (-1 otherwise).
type inputRow struct {
	runes  []rune
	cursor int // rune index into runes, len(runes) = after the last
}

// inputRows wraps the input to rows of at most w columns, breaking at the
// column limit and at every newline, and says which row holds the cursor.
//
// It breaks by column, not by word. A word wrap that moved a half-typed word
// down a row as it grew would make the cursor jump while the user types, and
// the input is read one keystroke at a time rather than as prose. A wide rune
// that would straddle the edge starts the next row, so a syllable is never
// split.
//
// One column is kept free at the end of every row for the cursor, so a cursor
// after the last rune of a full row never forces a re-wrap that the text
// itself does not have.
func inputRows(in ChatInput, w int) (rows []inputRow, cursorRow int) {
	in = in.clamp()
	if w < 2 {
		w = 2
	}
	room := w - 1
	cur := inputRow{cursor: -1}
	used := 0
	cursorRow = -1
	flush := func() {
		rows = append(rows, cur)
		cur = inputRow{cursor: -1}
		used = 0
	}
	for i, r := range in.Runes {
		if i == in.Cursor {
			cur.cursor = len(cur.runes)
			cursorRow = len(rows)
		}
		if r == '\n' {
			flush()
			continue
		}
		rw := runeWidth(r)
		if used+rw > room && len(cur.runes) > 0 {
			if cur.cursor == len(cur.runes) && i == in.Cursor {
				// The cursor sits before this rune, which is moving down:
				// the cursor moves with it.
				cur.cursor = -1
				flush()
				cur.cursor = 0
				cursorRow = len(rows)
			} else {
				flush()
			}
		}
		cur.runes = append(cur.runes, r)
		used += rw
	}
	if in.Cursor == len(in.Runes) {
		cur.cursor = len(cur.runes)
		cursorRow = len(rows)
	}
	rows = append(rows, cur)
	return rows, cursorRow
}

// inputView draws the input as rows lines of exactly w columns, scrolled so
// the cursor's row is visible.
//
// The prompt glyph opens the first row and a blank hangs every row after it,
// so a multi-line message reads as one block. The cursor is a cell painted on
// the fill the answer's write head wears when it sits on a rune, and the
// breathing bar the streams use when it sits after the last one — the same two
// marks the rest of the screen already uses for "here".
func inputView(th Theme, t time.Duration, in ChatInput, w, rows int, enabled bool, placeholder string) []string {
	const promptW = 2
	textW := w - promptW
	drawn, curRow := inputRows(in, textW)
	first := 0
	if curRow >= rows {
		first = curRow - rows + 1
	}
	promptSt := th.accent
	if !enabled {
		promptSt = th.dim
	}
	out := make([]string, 0, rows)
	for i := 0; i < rows; i++ {
		l := newLine(th, w)
		idx := first + i
		if idx == 0 {
			l.add(promptSt, "›")
			l.space(1)
		} else {
			l.space(promptW)
		}
		if idx < len(drawn) {
			r := drawn[idx]
			switch {
			case r.cursor < 0 || !enabled && in.Empty():
				l.add(th.text, string(r.runes))
			case r.cursor < len(r.runes):
				l.add(th.text, string(r.runes[:r.cursor]))
				l.add(th.textFresh, string(r.runes[r.cursor]))
				l.add(th.text, string(r.runes[r.cursor+1:]))
			default:
				l.add(th.text, string(r.runes))
				l.add(inputCursorStyle(th, t, enabled), "▍")
			}
			if idx == 0 && in.Empty() && placeholder != "" {
				l.space(1)
				l.addTrunc(th.dim, placeholder)
			}
		}
		out = append(out, l.String())
	}
	return out
}

// inputCursorStyle breathes the end-of-text cursor while the input takes keys
// and holds it at the darkest shade while it does not.
func inputCursorStyle(th Theme, t time.Duration, enabled bool) style {
	if !enabled {
		return th.accentLow
	}
	switch breathPhase(t) {
	case 1:
		return th.accentMid
	case 2:
		return th.accentHigh
	}
	return th.accentLow
}

// inputHeight is how many rows the input takes for the text it holds: one to
// max, growing as the message does.
func inputHeight(in ChatInput, w, maxRows int) int {
	rows, _ := inputRows(in, w-2)
	return min(max(1, len(rows)), maxRows)
}

// isCommand reports whether a submitted line is a slash command, and which.
// A command is one word: "/exit" is one, "/usr/bin is a path" is a message.
func isCommand(text string) (string, bool) {
	s := strings.TrimSpace(text)
	if !strings.HasPrefix(s, "/") || strings.ContainsAny(s, " \n\t") || len(s) < 2 {
		return "", false
	}
	return strings.ToLower(s), true
}
