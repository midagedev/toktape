package tui

import (
	"strings"
	"testing"
	"time"
)

// reasoningStream builds one stream out of (text, reasoning) pairs, exactly as
// the live path does: Text is the concatenation of the token texts.
func reasoningStream(pairs ...any) Stream {
	var s Stream
	at := 100 * time.Millisecond
	for i := 0; i < len(pairs); i += 2 {
		text := pairs[i].(string)
		s.Tokens = append(s.Tokens, Token{
			T:         at,
			Text:      text,
			Reasoning: pairs[i+1].(bool),
		})
		s.Text += text
		at += 25 * time.Millisecond
	}
	s.EndedAt = at
	return s
}

// TestTextRuns: tokens group into contiguous runs of one kind, and a stream
// with no thinking is a single run — which is what keeps every frame of a
// non-thinking model exactly as it was.
func TestTextRuns(t *testing.T) {
	got := textRuns(reasoningStream(
		"The user", true, " wants", true, " Four", false, " bytes", false))
	want := []textRun{
		{text: "The user wants", reasoning: true},
		{text: " Four bytes"},
	}
	if len(got) != len(want) {
		t.Fatalf("runs = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("run %d = %+v, want %+v", i, got[i], want[i])
		}
	}

	plain := reasoningStream("Four", false, " bytes", false)
	if got := textRuns(plain); len(got) != 1 || got[0].reasoning {
		t.Errorf("runs of a stream with no thinking = %+v, want one answer run", got)
	}
}

// TestStreamTextLinesMarker: the answer marker appears exactly once, where
// thinking ends, and never on a stream that only ever did one of the two.
func TestStreamTextLinesMarker(t *testing.T) {
	markers := func(lines []bodyLine) int {
		n := 0
		for _, bl := range lines {
			if bl.marker {
				n++
			}
		}
		return n
	}

	mixed := streamTextLines(reasoningStream(
		"Thinking about it.", true, " Four bytes.", false), 40)
	if got := markers(mixed); got != 1 {
		t.Errorf("markers = %d, want 1 in %+v", got, mixed)
	}
	var seenThinking, seenAnswer bool
	for _, bl := range mixed {
		switch {
		case bl.marker:
			if !seenThinking || seenAnswer {
				t.Errorf("the marker is in the wrong place: %+v", mixed)
			}
		case bl.reasoning:
			seenThinking = true
		default:
			seenAnswer = true
		}
	}
	if !seenThinking || !seenAnswer {
		t.Errorf("lines = %+v, want both kinds", mixed)
	}

	onlyThinking := streamTextLines(reasoningStream("Still thinking.", true), 40)
	if got := markers(onlyThinking); got != 0 {
		t.Errorf("markers = %d, want 0: the answer never started", got)
	}
	onlyAnswer := streamTextLines(reasoningStream("Four bytes.", false), 40)
	if got := markers(onlyAnswer); got != 0 {
		t.Errorf("markers = %d, want 0: the stream never thought", got)
	}
}

// TestThinkingBadge is the header's state word: present while a thinking model
// has not answered, "cut" when it finished without ever answering, gone the
// moment an answer token lands.
func TestThinkingBadge(t *testing.T) {
	thinking := reasoningStream("Still", true, " thinking", true)
	if got, want := thinkingBadge(thinking), "thinking"; got != want {
		t.Errorf("badge = %q, want %q", got, want)
	}

	cut := thinking
	cut.Done = true
	if got, want := thinkingBadge(cut), "thinking · cut"; got != want {
		t.Errorf("badge = %q, want %q (the answer never started)", got, want)
	}

	answered := reasoningStream("Thought", true, " Four", false)
	if got := thinkingBadge(answered); got != "" {
		t.Errorf("badge = %q, want none once an answer token arrived", got)
	}
	answered.Done = true
	if got := thinkingBadge(answered); got != "" {
		t.Errorf("badge = %q, want none on a finished stream that answered", got)
	}

	if got := thinkingBadge(reasoningStream("Four", false)); got != "" {
		t.Errorf("badge = %q, want none for a model that does not think", got)
	}
	if got := thinkingBadge(Stream{}); got != "" {
		t.Errorf("badge = %q, want none before any token arrived", got)
	}
}

// TestStreamBodyDimsReasoning is the visible contract: thinking is dim, the
// answer sits one tone above it, and the marker sits between them.
//
// 2026-09-13 TTP-28: the answer's settled shade is textMuted rather than text
// (user: "토큰 내용 자체는 한 톤 내리는 게 맞겠어"). The stream here is Done, so
// no token is in a glow band and every answer rune is settled. The assertion
// is the same one, against the shade the contract now names.
func TestStreamBodyDimsReasoning(t *testing.T) {
	th := ColourTheme()
	s := reasoningStream(
		"The user wants a", true,
		" short answer about", true,
		" memory.", true,
		"Four bytes", false,
		" per word.", false,
	)
	s.Done = true
	const cw = 24
	got := streamBody(Model{Streams: []Stream{s}}, th, 0, s, cw, 6, false)

	dim := th.paint(th.dim, "x")
	textStyle := th.paint(th.textMuted, "x")
	if dim == textStyle {
		t.Fatal("the dim and body styles paint identically; this test cannot tell them apart")
	}
	dimPrefix, textPrefix := escPrefix(dim), escPrefix(textStyle)

	joined := strings.Join(got, "\n")
	for _, word := range []string{"user", "wants", "short", "memory"} {
		if !styledWith(joined, word, dimPrefix) {
			t.Errorf("reasoning word %q is not drawn dim in:\n%s", word, joined)
		}
	}
	for _, word := range []string{"Four", "bytes", "word"} {
		if !styledWith(joined, word, textPrefix) {
			t.Errorf("answer word %q is not drawn in the text style in:\n%s", word, joined)
		}
	}
	if !strings.Contains(joined, answerMarker) {
		t.Errorf("no %q marker line:\n%s", answerMarker, joined)
	}
	if !styledWith(joined, answerMarker, dimPrefix) {
		t.Errorf("the marker is not drawn dim:\n%s", joined)
	}
	// The marker separates the two: no answer word may appear above it and no
	// thinking word below it.
	var at int
	for i, line := range got {
		if strings.Contains(line, answerMarker) {
			at = i
		}
	}
	above, below := strings.Join(got[:at], "\n"), strings.Join(got[at+1:], "\n")
	if strings.Contains(above, "Four") {
		t.Errorf("answer text appears above the marker:\n%s", above)
	}
	if strings.Contains(below, "memory") {
		t.Errorf("thinking text appears below the marker:\n%s", below)
	}
}

// TestStreamBodyPlainUnchanged: a stream with no reasoning token renders
// exactly as it did before reasoning styling existed — no marker, no dim.
func TestStreamBodyPlainUnchanged(t *testing.T) {
	th := ColourTheme()
	s := reasoningStream("Four bytes", false, " per word.", false)
	s.Done = true
	got := streamBody(Model{Streams: []Stream{s}}, th, 0, s, 40, 3, false)
	joined := strings.Join(got, "\n")
	// 2026-09-13 TTP-28: settled answer text is textMuted, one tone under the
	// header, and this stream is Done so nothing glows.
	if !styledWith(joined, "Four", escPrefix(th.paint(th.textMuted, "x"))) {
		t.Errorf("answer is not drawn in the body style:\n%s", joined)
	}
	if strings.Contains(joined, answerMarker) {
		t.Errorf("a stream that never thought got a marker:\n%s", joined)
	}
}

// TestStreamHeaderBadge: the badge is in the header line, next to the stream
// name and not next to the rate, and it is gone once the stream answers.
func TestStreamHeaderBadge(t *testing.T) {
	th := PlainTheme()
	m := Model{Streams: make([]Stream, 8)}

	thinking := reasoningStream("Still", true, " thinking", true)
	thinking.Index = 2
	got := streamHeader(m, th, thinking, 60, true, false)
	if !strings.Contains(got, "stream 3/8 · thinking") {
		t.Errorf("header = %q, want the badge after the stream name", got)
	}

	cut := thinking
	cut.Done = true
	if got := streamHeader(m, th, cut, 60, true, false); !strings.Contains(got, "· thinking · cut") {
		t.Errorf("header = %q, want the cut badge", got)
	}

	answered := reasoningStream("Thought", true, " Four", false)
	answered.Index = 2
	if got := streamHeader(m, th, answered, 60, true, false); strings.Contains(got, "thinking") {
		t.Errorf("header = %q, want no badge once the answer started", got)
	}
}

// TestExampleTapeShowsEveryThinkingState is the three-state check the hero clip
// depends on: at a mid-run instant one stream is thinking, another has crossed
// the answer marker, and at the end the reasoning-only stream is "cut".
func TestExampleTapeShowsEveryThinkingState(t *testing.T) {
	const (
		w = 140
		h = 40
	)

	mid := ModelAt(ExampleTape(), midRun)
	var thinkingNow, crossed int
	for _, s := range mid.Streams {
		if thinkingBadge(s) == "thinking" {
			thinkingNow++
		}
		for _, bl := range streamTextLines(s, 60) {
			if bl.marker {
				crossed++
				break
			}
		}
	}
	if thinkingNow == 0 {
		t.Errorf("no stream is thinking at %v", midRun)
	}
	if crossed == 0 {
		t.Errorf("no stream has crossed the answer marker at %v", midRun)
	}

	// And it reaches the screen, not just the model.
	frame := View(mid, midRun, w, h)
	if !strings.Contains(frame, "· thinking") {
		t.Errorf("no thinking badge in the frame at %v:\n%s", midRun, frame)
	}
	if !strings.Contains(frame, answerMarker) {
		t.Errorf("no %q marker in the frame at %v:\n%s", answerMarker, midRun, frame)
	}

	// At the end the stream that never answered says so.
	done := ModelAt(ExampleTape(), doneAt)
	var cut int
	for _, s := range done.Streams {
		if thinkingBadge(s) == "thinking · cut" {
			cut++
		}
	}
	if cut != 1 {
		t.Errorf("%d streams read \"thinking · cut\" at %v, want 1", cut, doneAt)
	}
	if got := View(done, doneAt, w, h); !strings.Contains(got, "thinking · cut") {
		t.Errorf("no cut badge in the final frame:\n%s", got)
	}
}

// escPrefix is the escape sequence a style emits before its text.
func escPrefix(painted string) string {
	i := strings.Index(painted, "x")
	if i <= 0 {
		return ""
	}
	return painted[:i]
}

// styledWith reports whether word appears in s preceded, since the last escape
// sequence, by prefix.
func styledWith(s, word, prefix string) bool {
	for i := 0; ; {
		j := strings.Index(s[i:], word)
		if j < 0 {
			return false
		}
		j += i
		if k := strings.LastIndex(s[:j], "\x1b["); k >= 0 && strings.HasPrefix(s[k:], prefix) {
			return true
		}
		i = j + len(word)
	}
}
