package render

import (
	"fmt"
	"image/color"
	"strings"
	"time"

	"github.com/midagedev/toktape/internal/tape"
)

// The cold open is the part of the clip before the TUI exists: an empty shell
// prompt, the command typed into it, and the tool finding the server and
// attaching. A clip that opens on the finished screen reads as a mock-up; one
// that opens on a prompt reads as a session, and a reader who has never run
// the tool learns the command from the clip itself (user, 2026-09-13).
//
// It draws no tui.Model — there is no model yet — so it is the one screen in
// this package written cell by cell. Everything below is a pure function of
// the open's own clock, the same rule the rest of the pipeline follows: the
// characters typed, the spinner frame, the blink phase and which progress
// lines exist are all derived from t and nothing else.

// The beats, on the open's nominal OpenHold-long clock.
const (
	// openTypeAt is when the first character appears; before it the screen is
	// an empty prompt with a blinking cursor, which is the beat that says
	// "this is a terminal" before anything happens in it.
	openTypeAt = 800 * time.Millisecond
	// openTypeSpan is how long the whole command takes to type, whatever its
	// length. The per-character cadence is uneven inside it (openWeights).
	openTypeSpan = 1400 * time.Millisecond
	// openPause is the beat between the last character and the return key —
	// the moment a person reads back what they typed.
	openPause = 400 * time.Millisecond
	// openEnterAt is the return key: the cursor leaves the command line.
	openEnterAt = openTypeAt + openTypeSpan + openPause
	// openSpinnerAt is when the tool starts looking for a server, and
	// openAttachAt is when it has found one and the line is replaced by what
	// it found.
	openSpinnerAt = 3 * time.Second
	openAttachAt  = 3700 * time.Millisecond
	// openStreamsAt is the first prefill line; the rest follow one
	// openStreamGap apart, which is what makes N streams read as N.
	openStreamsAt = 4300 * time.Millisecond
	openStreamGap = 150 * time.Millisecond
	// openBlink is the cursor's full on-off period and openSpinStep is one
	// spinner frame. Both are phases of t, never of a wall clock.
	//
	// The spinner is slower than the TUI's 80 ms: this one is a program
	// looking for a server, and a wheel that races reads as a program in
	// trouble rather than one working.
	openBlink    = time.Second
	openSpinStep = 200 * time.Millisecond
)

// The rows the open writes on. The screen is a terminal's: output starts at
// the top-left and grows downward, and everything below it is background.
const (
	openPromptRow = 0
	openStatusRow = 1
	openStreamRow = 2
)

// openPrompt is the shell prompt. Two segments: the working directory, dim,
// and the chevron, which is the one accent on the screen until the tool
// starts printing.
const (
	openPromptDir   = "~ "
	openPromptGlyph = "❯"
	// openCursor is the block a terminal parks where the next character will
	// go. It is drawn as geometry (glyph.go), so it fills the cell the way a
	// real cursor does rather than floating inside it.
	openCursor = "█"
)

// openSpinnerFrames is the quarter-circle cycle the TUI spins during prefill
// (tui/anim.go), not the Braille wheel cmd/toktape's stderr lines use.
//
// The two disagree and the screen wins: the open hands over to a TUI that
// spins this set two seconds later, and a clip whose spinner changes shape at
// the seam reads as two clips. Braille is also the wrong glyph for a raster —
// neither embedded face has U+2800–U+28FF, so it is drawn as a dot grid
// (glyph.go) that at a 13 px cell is a smudge. Measured 2026-09-13 on
// scratch/clip-3.5s: the Braille frame was the least legible mark on the
// screen and the quarter circle reads at every cell size the clip uses.
var openSpinnerFrames = []rune{'◐', '◓', '◑', '◒'}

// The open's palette mirrors internal/tui/theme.go, whose colour constants are
// unexported. Same three roles the TUI uses and no fourth: the accent carries
// the prompt glyph, the spinner and the cursor; text carries what the operator
// typed and the model that was found; everything else is chrome and is dim.
var (
	openAccent = color.RGBA{R: 0x7d, G: 0xd3, B: 0xfc, A: 0xff}
	openText   = color.RGBA{R: 0xe5, G: 0xe7, B: 0xeb, A: 0xff}
	openDim    = color.RGBA{R: 0x6b, G: 0x72, B: 0x80, A: 0xff}
)

// OpenScreen renders one frame of the cold open as a block of ANSI-coloured
// text, exactly h lines of w display columns — the same contract tui.View
// meets, so everything downstream is unaware there are two kinds of frame.
//
// t is the open's own clock, from 0 to OpenHold. tp supplies the command's
// stream count and the attach line's figures; a nil tape draws the same screen
// with unknowns printed as unknowns.
func OpenScreen(tp *tape.Tape, w, h int, t time.Duration) string {
	if t < 0 {
		t = 0
	}
	cmd := openCommand(tp)
	streams := openStreams(tp)

	rows := make([]string, h)
	blank := strings.Repeat(" ", w)
	for i := range rows {
		rows[i] = blank
	}

	// How much of the tool's output exists yet, and therefore which row the
	// cursor has been pushed down to.
	shown := 0
	if t >= openStreamsAt && streams > 0 {
		shown = int((t-openStreamsAt)/openStreamGap) + 1
		if shown > streams {
			shown = streams
		}
	}
	last := openPromptRow
	switch {
	case shown > 0:
		last = openStreamRow + shown - 1
	case t >= openSpinnerAt:
		last = openStatusRow
	}

	// The cursor sits where the next character would go: inline while the
	// command is being typed, and at the left margin under the last line of
	// output once the tool is running. While the spinner turns it is hidden —
	// two things blinking at once is noise, not life.
	cursorRow := -1
	switch {
	case t < openEnterAt:
		cursorRow = openPromptRow
	case t >= openSpinnerAt && t < openAttachAt:
		cursorRow = -1
	default:
		cursorRow = last + 1
	}
	// A terminal cursor stops blinking while keys are going in — it is solid
	// under the hand that is typing — and resumes when the line is idle.
	// Without this the command appears one character at a time under a cursor
	// that is absent for half of them, which reads as a dropped frame.
	if typing := t >= openTypeAt && t < openTypeAt+openTypeSpan; !typing && t%openBlink >= openBlink/2 {
		cursorRow = -1
	}

	// The prompt line, with the command as far as it has been typed.
	l := newOpenLine(w)
	l.add(openDim, openPromptDir)
	l.addBold(openAccent, openPromptGlyph)
	l.add(openText, " "+typedPrefix(cmd, t))
	if cursorRow == openPromptRow {
		l.add(openAccent, openCursor)
	}
	rows[openPromptRow] = l.String()

	// The status line: the search, then what was found, in place. The real CLI
	// redraws that line rather than scrolling it, and replacing it here is
	// both what it looks like and one fewer row of chrome.
	if openStatusRow < h {
		switch {
		case t >= openAttachAt:
			rows[openStatusRow] = openAttachLine(newOpenLine(w), tp).String()
		case t >= openSpinnerAt:
			l := newOpenLine(w)
			l.add(openAccent, string(openSpinFrame(t-openSpinnerAt)))
			l.add(openDim, " looking for llama-server …")
			rows[openStatusRow] = l.String()
		}
	}

	// One prefill line per stream, at the indent cmd/toktape/progress.go's
	// line() uses.
	for i := 0; i < shown; i++ {
		row := openStreamRow + i
		if row >= h {
			break
		}
		rows[row] = newOpenLine(w).
			add(openDim, fmt.Sprintf("  stream %d/%d · prefill…", i+1, streams)).
			String()
	}

	if cursorRow > openPromptRow && cursorRow < h {
		rows[cursorRow] = newOpenLine(w).add(openAccent, openCursor).String()
	}
	return strings.Join(rows, "\n")
}

// openSpinFrame is the spinner glyph after d of spinning.
func openSpinFrame(d time.Duration) rune {
	if d < 0 {
		d = 0
	}
	return openSpinnerFrames[int(d/openSpinStep)%len(openSpinnerFrames)]
}

// openCommand is the command line the clip types. The stream count comes from
// the run itself, so the command a viewer copies is the one that produced the
// screen they are about to watch — a hard-coded "-n 4" would go on claiming
// four streams after the fixture changed.
func openCommand(tp *tape.Tape) string {
	if n := openStreams(tp); n > 1 {
		return fmt.Sprintf("toktape -n %d", n)
	}
	return "toktape"
}

// openStreams is how many streams the run had: the summary's figure, or the
// requests actually recorded when the summary does not say. Zero when there is
// no tape, which prints no prefill lines rather than an invented one.
func openStreams(tp *tape.Tape) int {
	if tp == nil {
		return 0
	}
	if n := tp.Summary.Concurrency; n > 0 {
		return n
	}
	return len(tp.Requests)
}

// typedPrefix is the part of cmd that has been typed at open time t.
func typedPrefix(cmd string, t time.Duration) string {
	r := []rune(cmd)
	return string(r[:typedCount(len(r), t)])
}

// typedCount is how many of n characters have been typed at open time t.
//
// The cadence is uneven — openWeights spreads the dwell over 60–110 ms — and
// then normalised so that the command always finishes in openTypeSpan whatever
// its length. Evenly spaced characters read as a macro being replayed; this
// reads as hands, and it costs no randomness: the same t gives the same frame.
func typedCount(n int, t time.Duration) int {
	if n <= 0 || t < openTypeAt {
		return 0
	}
	if elapsed := t - openTypeAt; elapsed < openTypeSpan {
		w := openWeights(n)
		total := 0
		for _, v := range w {
			total += v
		}
		acc := 0
		for i, v := range w {
			acc += v
			if openTypeSpan*time.Duration(acc)/time.Duration(total) > elapsed {
				return i
			}
		}
	}
	return n
}

// openWeights is the relative dwell of each of n characters, 60–110 before
// normalisation. The hash is an integer one of the index: deterministic, the
// same on every machine, and no package-level state.
func openWeights(n int) []int {
	w := make([]int, n)
	for i := range w {
		h := uint32(i+1) * 2654435761
		h ^= h >> 13
		w[i] = 60 + int(h%51)
	}
	return w
}

// openAttachLine writes the line the CLI prints when it has attached.
//
// It replicates cmd/toktape/record.go's headerLine, which is in package main
// and cannot be imported: same order, same separators, same fallbacks. The
// colours are this package's — the model is the one thing on the line a reader
// is looking for, so it is the one thing not dim.
func openAttachLine(l *openLine, tp *tape.Tape) *openLine {
	var s tape.RunSummary
	if tp != nil {
		s = tp.Summary
	}
	kind := string(s.Server.Kind)
	if kind == "" {
		kind = string(tape.ServerUnknown)
	}
	url := s.Server.URL
	if url == "" {
		url = "?"
	}
	l.add(openAccent, "→ ")
	l.add(openDim, kind+" at "+url)
	if s.Server.Build != "" {
		l.add(openDim, " ("+s.Server.Build+")")
	}
	l.add(openDim, " · ")
	l.add(openText, openModelName(s))
	l.add(openDim, " · ")
	if s.Server.PID > 0 {
		l.add(openDim, fmt.Sprintf("pid %d", s.Server.PID))
	} else {
		l.add(openDim, "no /proc view")
	}
	return l
}

// openModelName is headerLine's model segment: the label, or the file name
// without its extension, with the quant appended only when the label does not
// already carry it.
func openModelName(s tape.RunSummary) string {
	name := s.Model.Name
	if name == "" {
		name = strings.TrimSuffix(s.Model.FileName, ".gguf")
	}
	if name == "" {
		return "?"
	}
	if q := s.Model.Quant; q != "" && !strings.Contains(name, q) {
		name += " " + q
	}
	return name
}

// openLine assembles one line of the open to an exact column count.
//
// It is tui's lineBuf with the parts the open needs: every segment is clipped
// to what is left before it is painted, so an escape sequence can never be cut
// in half, and String pads to the full width — the clip repaints a fixed-size
// screen without erasing it, so a short line would leave the tail of a longer
// one behind.
type openLine struct {
	w, used int
	b       strings.Builder
}

func newOpenLine(w int) *openLine { return &openLine{w: w} }

// add appends s in col.
func (l *openLine) add(col color.RGBA, s string) *openLine { return l.paint(col, false, s) }

// addBold appends s in col, bold.
func (l *openLine) addBold(col color.RGBA, s string) *openLine { return l.paint(col, true, s) }

func (l *openLine) paint(col color.RGBA, bold bool, s string) *openLine {
	s = openClip(s, l.w-l.used)
	if s == "" {
		return l
	}
	l.used += cellWidth.StringWidth(s)
	if bold {
		l.b.WriteString("\x1b[1m")
	}
	fmt.Fprintf(&l.b, "\x1b[38;2;%d;%d;%dm%s\x1b[0m", col.R, col.G, col.B, s)
	return l
}

// String returns the line padded with spaces to exactly w columns.
func (l *openLine) String() string {
	if l.used >= l.w {
		return l.b.String()
	}
	return l.b.String() + strings.Repeat(" ", l.w-l.used)
}

// openClip truncates s to n display columns, measured the way parseScreen
// measures it so the two cannot disagree about where a line ends.
func openClip(s string, n int) string {
	if s == "" || n <= 0 {
		return ""
	}
	if cellWidth.StringWidth(s) <= n {
		return s
	}
	var b strings.Builder
	used := 0
	for _, r := range s {
		rw := cellWidth.RuneWidth(r)
		if used+rw > n {
			break
		}
		b.WriteRune(r)
		used += rw
	}
	return b.String()
}
