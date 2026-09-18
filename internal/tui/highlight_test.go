package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/midagedev/toktape/internal/card"
)

// codeStream is a stream whose answer is prose, a fenced Go block and prose
// again, built token by token so every rune has an arrival time. The block
// is left open when open is true, which is what a block looks like while it
// is still being written.
func codeStream(at time.Duration, open bool, lang string) Stream {
	texts := []string{
		"Here is the worker:\n",
		"```" + lang + "\n",
		"// fan out\n",
		"func run(n int) {\n",
		"\tfor i := 0; i < n; i++ {\n",
		"\t\tgo work(i)\n",
		"\t}\n",
		"}\n",
	}
	if !open {
		texts = append(texts, "```\n", "That is all.")
	}
	var s Stream
	for i, tx := range texts {
		s.Tokens = append(s.Tokens, Token{T: at - 3*time.Second + time.Duration(i)*10*time.Millisecond, Text: tx})
		s.Text += tx
	}
	return s
}

// classAt is the class of the first rune of word in the stream's text.
func classAt(t *testing.T, s Stream, classes []codeClass, word string) codeClass {
	t.Helper()
	i := strings.Index(s.Text, word)
	if i < 0 {
		t.Fatalf("%q is not in the stream", word)
	}
	off := len([]rune(s.Text[:i]))
	if classes == nil || off >= len(classes) {
		return classPlain
	}
	return classes[off]
}

// TestCodeClassesFollowTheFence (TTP-52, 2026-09-14). New feature, so
// FAIL-first is by construction: before highlight.go every rune was plain.
func TestCodeClassesFollowTheFence(t *testing.T) {
	const at = 10 * time.Second
	s := codeStream(at, false, "go")
	classes := codeClasses(s)
	if classes == nil {
		t.Fatal("a fenced Go block produced no classes")
	}
	for word, want := range map[string]codeClass{
		"Here":      classPlain, // prose before the fence
		"```go":     classFence,
		"// fan":    classComment,
		"func":      classKeyword,
		"for":       classKeyword,
		"go work":   classKeyword,
		"run":       classFunc, // a call (2026-09-16)
		"{":         classPunct,
		"++":        classPunct,
		"That":      classPlain, // prose after the fence
		"```\nThat": classFence,
	} {
		if got := classAt(t, s, classes, word); got != want {
			t.Errorf("%q is class %d, want %d", word, got, want)
		}
	}

	// An open block is lexed as far as it goes.
	open := codeStream(at, true, "go")
	if got := classAt(t, open, codeClasses(open), "go work"); got != classKeyword {
		t.Errorf("an unclosed block is not highlighted: %q is class %d", "go", got)
	}
	// No tag, or a tag chroma does not know, is drawn plain rather than
	// lexed as a guess.
	for _, lang := range []string{"", "nosuchlanguage"} {
		s := codeStream(at, false, lang)
		if got := classAt(t, s, codeClasses(s), "func"); got != classPlain {
			t.Errorf("lang %q: %q is class %d, want plain", lang, "func", got)
		}
	}
	// Reasoning is never lexed, whatever it contains.
	r := codeStream(at, false, "go")
	for i := range r.Tokens {
		r.Tokens[i].Reasoning = true
	}
	if got := codeClasses(r); got != nil {
		t.Errorf("a reasoning stream produced classes")
	}
	// Prose alone costs nothing — stream 1 is the Korean KV-cache answer and
	// has no fence in it.
	m := ModelAt(ExampleTape(), doneAt)
	if got := codeClasses(m.Streams[1]); got != nil {
		t.Errorf("a prose answer produced classes")
	}
	// The example's own first stream opens on a fenced Go block (2026-09-14):
	// the hero clip is rendered from this fixture, so the highlighting has to
	// be exercised by it and not only by a stream built inside a test.
	exClasses := codeClasses(m.Streams[0])
	if exClasses == nil {
		t.Fatal("the example's code answer produced no classes")
	}
	for word, want := range map[string]codeClass{
		"```go":        classFence,
		"// one token": classComment,
		"func (m":      classKeyword,
		"range":        classKeyword,
		"argmax":       classFunc,
		"Nothing in":   classPlain,
	} {
		if got := classAt(t, m.Streams[0], exClasses, word); got != want {
			t.Errorf("the example answer: %q is class %d, want %d", word, got, want)
		}
	}
}

// TestCodeWearsItsOwnHues: a fenced block is coloured by role, the write head
// still wins over every role, and no role borrows a hue that means something
// else on this screen.
//
// It replaces TestCodeShapesTheBodyWithoutLiftingIt, which pinned the opposite
// rule — a class could only darken a rune, never lift it — and is gone by
// instruction (user, 2026-09-16: "좀 더 밝게 가도 될 것 같아, 좀더 vscode스럽게
// 그리고 기왕 의존성 추가한거 하일라이팅 할 수 있는거 최대한 활용하자"). The
// reason the old rule existed is kept: prose still walks the grey ladder, and
// the code hues appear only where the lexer found code.
func TestCodeWearsItsOwnHues(t *testing.T) {
	th := ColourTheme()
	answer := bodyLine{}
	for _, tc := range []struct {
		class codeClass
		want  style
		name  string
	}{
		{classKeyword, th.codeKeyword, "keyword"},
		{classFunc, th.codeFunc, "call"},
		{classType, th.codeType, "type"},
		{classString, th.codeString, "string"},
		{classNumber, th.codeNumber, "number"},
		{classComment, th.codeComment, "comment"},
		{classVar, th.codeVar, "identifier"},
	} {
		if got, want := styleHex(bodyStyle(th, answer, bandSettled, tc.class)), styleHex(tc.want); got != want {
			t.Errorf("a settled %s is %s, want %s", tc.name, got, want)
		}
	}
	if !bodyStyle(th, answer, bandSettled, classKeyword).st.GetBold() {
		t.Error("a keyword is not bold")
	}
	// Punctuation and the fence line stay chrome: they are the parts of a
	// block a reader's eye should pass over.
	if got, want := styleHex(bodyStyle(th, answer, bandSettled, classPunct)), styleHex(th.dimMid); got != want {
		t.Errorf("settled punctuation is %s, want dimMid %s", got, want)
	}
	if got, want := styleHex(bodyStyle(th, answer, bandSettled, classFence)), styleHex(th.dim); got != want {
		t.Errorf("a settled fence is %s, want dim %s", got, want)
	}
	// Plain prose is untouched by any of it.
	if got, want := styleHex(bodyStyle(th, answer, bandSettled, classPlain)), styleHex(th.textMuted); got != want {
		t.Errorf("settled prose is %s, want the body's own %s", got, want)
	}
	// The write head keeps its fill over every class: "this token just landed"
	// outranks what the token is.
	for _, c := range []codeClass{classPlain, classKeyword, classPunct, classComment,
		classString, classNumber, classFunc, classType, classVar} {
		if got, want := styleBG(bodyStyle(th, answer, bandFresh, c)), styleBG(th.textFresh); got != want {
			t.Errorf("class %d at the write head lost the fill", c)
		}
	}
	// No code hue may be one of the accent's shades: the accent is the rates
	// and the active stream, and the emphasis contract (emphasis_test.go)
	// reads the screen for exactly those (2026-09-16).
	accents := accentShades()
	for _, st := range []style{th.codeKeyword, th.codeFunc, th.codeType,
		th.codeString, th.codeNumber, th.codeComment, th.codeVar} {
		if accents[styleHex(st)] {
			t.Errorf("a code hue is an accent shade: %s", styleHex(st))
		}
	}
}

func TestACodeAnswerKeepsTheContracts(t *testing.T) {
	const w, h = 120, 36
	m := ModelAt(ExampleTapeN(2), midRun)
	s := codeStream(midRun, true, "go")
	m.Streams[0] = s

	plain := View(m, midRun, w, h)
	m.Theme = ColourTheme()
	coloured := View(m, midRun, w, h)
	if got := card.StripANSI(coloured); got != plain {
		t.Error("stripping the palette does not reproduce the plain frame of a code answer")
	}
	if !strings.Contains(plain, "go work(i)") {
		t.Fatalf("the code is not on the frame:\n%s", plain)
	}

	rows := parseFrame(coloured, w, h)
	rightStart := rightPaneStart(rows, w)
	text, accent := styleHex(ColourTheme().text), styleHex(ColourTheme().accent)
	for y, row := range rows {
		if y == 0 {
			continue
		}
		spans := rateSpans(rows, y)
		// The full accent only, as TestAccentIsReserved reads it: the stat
		// line's sparkline wears the muted shade by contract.
		for _, rn := range accentRuns(row, func(r rune) bool { return r != ' ' }) {
			if row[rn.from].fg != accent || rn.from >= rightStart || within(spans, rn) || reservedChrome(rows, y, rn) || sparkWriteHead(rows, y, rn) {
				continue
			}
			t.Errorf("%q at row %d col %d wears the accent inside a code tile", rn.text, y, rn.from)
		}
		if strings.HasPrefix(segmentAt(row, 2), "stream ") {
			continue
		}
		for _, rn := range accentRunsOf(row, text) {
			if rn.from < rightStart && row[rn.from].bg == "" {
				t.Errorf("%q at row %d col %d wears the header tone with no fill: only the write head may", rn.text, y, rn.from)
			}
		}
	}
}
