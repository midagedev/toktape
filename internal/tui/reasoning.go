package tui

import "github.com/charmbracelet/lipgloss"

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
	// reasoning draws the line dim.
	reasoning bool
	// marker marks the answerMarker rule, which is chrome rather than text
	// the model produced.
	marker bool
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
		for _, line := range wrap(r.text, w) {
			out = append(out, bodyLine{text: line, reasoning: r.reasoning})
		}
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

// bodyStyle is the style one body line is drawn in: dim for thinking text and
// for the answer marker (which is chrome), the normal text colour for the
// answer itself.
func bodyStyle(th Theme, bl bodyLine) lipgloss.Style {
	if bl.reasoning || bl.marker {
		return th.dim
	}
	return th.text
}
