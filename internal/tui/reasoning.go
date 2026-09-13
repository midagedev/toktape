package tui

import (
	"strings"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"
)

// This file is how the answer pane shows a thinking model thinking.
//
// A thinking model (DeepSeek, Qwen3, GLM) emits its reasoning as
// reasoning_content deltas. Those are decode tokens like any other — the
// server counts them in predicted_n, so they carry TTFT and every rate — and
// the difference shows only in how the pane draws them:
//
//   - the reasoning text is dim and the answer is the normal text colour;
//   - a "▸ answer" rule marks the instant the first answer token arrived, so
//     the transition is legible in a replay and not only in a live terminal
//     where the reader watched it happen;
//   - the stream header carries a "thinking" badge while the monologue is
//     still running, and "thinking · cut" when the stream ended without ever
//     reaching an answer, which is what n_predict running out looks like.
//
// All of it is a function of the model and the clip time, like every other
// moving part of the view, so a replayed frame reproduces the live one.

// answerMarker is the rule drawn where thinking ends and the answer begins.
// A glyph and a word, in the dim chrome colour: it is a label, not something
// the model said, and it must not compete with the answer underneath it.
const answerMarker = "▸ answer"

// bodyLine is one drawn line of a stream's text.
type bodyLine struct {
	text string
	// reasoning draws the line one step below the answer text.
	reasoning bool
	// marker marks the answerMarker rule, which is chrome rather than text
	// the model produced.
	marker bool
	// src is, rune for rune, where each drawn rune sits in the text the
	// stream's tokens spell out, or -1 for a rune the wrapper wrote itself.
	// Nil on the marker, which the model did not write.
	src []int
}

// tokenBand is how long ago the text under it arrived. It is a pure function
// of clip time, so a replayed frame glows exactly where the live one did.
type tokenBand int

const (
	bandSettled tokenBand = iota // the text has been on screen a while
	bandMid                      // it landed between glowFresh and glowSettled ago
	bandFresh                    // it landed within glowFresh
)

// The two ages that split the ramp (TTP-28, lead contract 2026-09-13).
//
// At the example run's 12.5 tok/s the inter-token latency is about 80 ms, so
// glowFresh holds one or two tokens and glowSettled four to six: a short
// bright tail at the write head with everything behind it already settled.
// Widening either one turns the effect from a write head into a lit paragraph,
// which is the thing the emphasis contract just finished removing.
const (
	glowFresh   = 150 * time.Millisecond
	glowSettled = 500 * time.Millisecond
)

// bodySeg is a run of one line's text that shares an age band.
type bodySeg struct {
	text string
	band tokenBand
}

// streamTextLines wraps a stream's generated text into display lines, keeping
// thinking and answering apart.
//
// The text is split into contiguous runs of one kind and each run is wrapped
// on its own, so the boundary always falls on a line break and the marker has
// somewhere to go. A stream with no reasoning token is one run and wraps
// exactly as it did before any of this existed, which is why no frame of a
// non-thinking model moves.
func streamTextLines(s Stream, w int) []bodyLine {
	runs := textRuns(s)
	if len(runs) == 0 {
		return nil
	}
	var (
		out         []bodyLine
		sawAnswer   bool
		sawThinking bool
		// base is where the run being wrapped starts in the stream's text,
		// so a line's source offsets are the stream's and not the run's.
		base int
	)
	for _, r := range runs {
		// The marker earns its line only at the first answer run that follows
		// thinking. A stream that never thought gets no marker, and an
		// interleaved stream is not re-announced every time.
		if !r.reasoning && !sawAnswer && sawThinking {
			out = append(out, bodyLine{text: answerMarker, marker: true})
		}
		if r.reasoning {
			sawThinking = true
		} else {
			sawAnswer = true
		}
		for _, wl := range wrapSource(r.text, w) {
			for k, o := range wl.src {
				if o >= 0 {
					wl.src[k] = o + base
				}
			}
			out = append(out, bodyLine{text: wl.text, reasoning: r.reasoning, src: wl.src})
		}
		base += utf8.RuneCountInString(r.text)
	}
	return out
}

// textRun is a stretch of a stream's text that is all thinking or all answer.
type textRun struct {
	text      string
	reasoning bool
}

// textRuns groups a stream's tokens into contiguous runs of one kind.
//
// It reads the tokens rather than Stream.Text because only the tokens carry
// the flag; Text is their concatenation, by construction in both the live path
// (Apply, EventToken) and the replay path (ModelAt). A stream that has text
// but no tokens — nothing builds one today, but a frozen frame rebuilt from a
// tape without a timeline would — still renders, as one answer run.
func textRuns(s Stream) []textRun {
	if len(s.Tokens) == 0 {
		if s.Text == "" {
			return nil
		}
		return []textRun{{text: s.Text}}
	}
	var out []textRun
	for _, tk := range s.Tokens {
		if n := len(out); n > 0 && out[n-1].reasoning == tk.Reasoning {
			out[n-1].text += tk.Text
			continue
		}
		out = append(out, textRun{text: tk.Text, reasoning: tk.Reasoning})
	}
	return out
}

// thinkingBadge is the word beside the stream index while a thinking model has
// not answered yet. Empty for every stream that is not in that state, so the
// header of an ordinary run is unchanged.
//
//	thinking        reasoning tokens are arriving and no answer token has
//	thinking · cut  the stream ended having produced nothing but reasoning —
//	                the answer was cut off, usually by n_predict, and this is
//	                why the pane below it holds no reply
func thinkingBadge(s Stream) string {
	thought := false
	for _, tk := range s.Tokens {
		if !tk.Reasoning {
			// It answered. Whatever it thought first is history the pane
			// already shows; the header goes back to being about the rate.
			return ""
		}
		thought = true
	}
	if !thought {
		return ""
	}
	if s.Done || s.Err != "" {
		return "thinking · cut"
	}
	return "thinking"
}

// bodyStyle is the style one segment of a body line is drawn in.
//
// Two ladders, two stops apart, so the answer/thinking distinction survives at
// every age: an answer that has settled wears the shade a thought wears when
// it has just arrived, and a thought never reaches the shade a fresh answer
// has. The marker is chrome and stays at the bottom whatever its age.
//
// The answer's freshest band is the one stop that also carries a fill (TTP-47,
// 2026-09-14). Both ladders end at the top of the palette's lightness, so the
// answer's write head had nowhere brighter to go and the user could not find
// it. The reasoning ladder keeps its foreground-only step: its glow was the
// one that was visible when the answer's was not, and a monologue is not what
// the eye is meant to be led to.
func bodyStyle(th Theme, bl bodyLine, band tokenBand) lipgloss.Style {
	if bl.marker {
		return th.dim
	}
	if bl.reasoning {
		switch band {
		case bandFresh:
			return th.textMuted
		case bandMid:
			return th.dimMid
		}
		return th.dim
	}
	switch band {
	case bandFresh:
		return th.textFresh
	case bandMid:
		return th.textMid
	}
	return th.textMuted
}

// bandStarts is where the two glow bands begin, as rune offsets into the text
// the stream's tokens spell out.
//
// Tokens arrive in order, so each band is a suffix and two offsets describe
// the whole ramp. A finished stream has no bands at all: the glow says "this
// is being written now", and a card frame must not shimmer.
func bandStarts(s Stream, t time.Duration) (midStart, freshStart int) {
	off := 0
	midStart, freshStart = -1, -1
	for _, tk := range s.Tokens {
		age := t - tk.T
		if midStart < 0 && age < glowSettled {
			midStart = off
		}
		if freshStart < 0 && age < glowFresh {
			freshStart = off
		}
		off += utf8.RuneCountInString(tk.Text)
	}
	if s.Done || s.Err != "" {
		return off, off
	}
	if midStart < 0 {
		midStart = off
	}
	if freshStart < 0 {
		freshStart = off
	}
	return midStart, freshStart
}

// bodyBands splits each drawn line into age-banded segments.
//
// Every drawn rune carries its offset in the stream's text (bodyLine.src), and
// the bands are suffixes of that text, so a rune's band is a comparison. It
// used to be a search: the wrapper collapsed whitespace, and each drawn rune
// was matched to the next occurrence of itself in the source, which broke the
// moment the wrapper drew a rune the source did not have in that place — a
// tab as spaces, the indent a wrapped code line hangs from (TTP-48). A rune
// the wrapper wrote itself belongs to no token and is settled: it is layout,
// and the write head's fill has no business lighting it.
func bodyBands(s Stream, lines []bodyLine, t time.Duration) [][]bodySeg {
	midStart, freshStart := bandStarts(s, t)
	out := make([][]bodySeg, len(lines))
	for i, bl := range lines {
		if bl.marker {
			out[i] = []bodySeg{{text: bl.text, band: bandSettled}}
			continue
		}
		var (
			segs []bodySeg
			b    strings.Builder
			cur  = bandSettled
		)
		flush := func() {
			if b.Len() > 0 {
				segs = append(segs, bodySeg{text: b.String(), band: cur})
				b.Reset()
			}
		}
		// A line's leading indent never glows (2026-09-14, seen on a real
		// code tape): a tab-expanded indent carries the newest token's source
		// offset, and the fill drew a blank slab before the code. Only the
		// indent is exempt; the gaps between words stay in their token's run.
		j, indent := 0, true
		for _, r := range bl.text {
			band := bandSettled
			if indent && r != ' ' {
				indent = false
			}
			if j < len(bl.src) && !indent {
				switch o := bl.src[j]; {
				case o < 0:
				case o >= freshStart:
					band = bandFresh
				case o >= midStart:
					band = bandMid
				}
			}
			j++
			if band != cur {
				flush()
				cur = band
			}
			b.WriteRune(r)
		}
		flush()
		out[i] = segs
	}
	return out
}
