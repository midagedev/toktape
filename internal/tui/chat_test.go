package tui

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/midagedev/toktape/internal/card"
	"github.com/midagedev/toktape/internal/tape"
)

// TestChatFramesAreExactAndPinned: every example chat state draws exactly h
// lines of exactly w columns, the coloured frame stripped of its escapes is
// the plain frame, and the plain frame is the golden in testdata. The
// goldens are the chat's own; the record screen's are untouched by it.
func TestChatFramesAreExactAndPinned(t *testing.T) {
	for _, st := range ExampleChatStates() {
		t.Run(st.Name, func(t *testing.T) {
			plain := ChatView(st.Model(PlainTheme()), st.At, st.W, st.H)
			colour := ChatView(st.Model(ColourTheme()), st.At, st.W, st.H)
			lines := strings.Split(plain, "\n")
			if len(lines) != st.H {
				t.Fatalf("%d rows, want %d", len(lines), st.H)
			}
			for i, l := range lines {
				if w := card.Width(l); w != st.W {
					t.Errorf("row %d is %d columns, want %d: %q", i, w, st.W, l)
				}
			}
			if card.StripANSI(colour) != plain {
				t.Error("stripping the palette does not reproduce the plain frame")
			}
			golden := filepath.Join("testdata", "chat-"+st.Name+".txt")
			if *update {
				if err := os.WriteFile(golden, []byte(plain+"\n"), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatalf("read golden (run with -update to create it): %v", err)
			}
			if string(want) != plain+"\n" {
				t.Errorf("frame differs from %s:\n%s", golden, plain)
			}
		})
	}
}

// TestChatViewIsAFunctionOfStateAndTime: the same (model, t) draws the same
// frame twice, and a different t moves only what is meant to move — the
// spinner in prefill.
func TestChatViewIsAFunctionOfStateAndTime(t *testing.T) {
	for _, st := range ExampleChatStates() {
		m := st.Model(ColourTheme())
		if a, b := ChatView(m, st.At, st.W, st.H), ChatView(m, st.At, st.W, st.H); a != b {
			t.Errorf("%s: two frames of one (state, t) differ", st.Name)
		}
	}
	st := ExampleChatStates()[1] // prefill
	m := st.Model(PlainTheme())
	a := ChatView(m, st.At, st.W, st.H)
	b := ChatView(m, st.At+spinFrame, st.W, st.H)
	if a == b {
		t.Error("the prefill spinner does not move with t")
	}
}

// TestChatInputEditsKorean: typing Hangul, backspacing and moving the cursor
// edit whole syllables, and the drawn input keeps every row w columns with
// the cursor on the syllable it names.
func TestChatInputEditsKorean(t *testing.T) {
	m := ExampleChatAt(chatExampleOpened, PlainTheme(), false)
	press := func(k ChatKey) {
		m, _ = m.HandleKey(k, chatExampleOpened, 120, 36)
	}
	press(ChatKey{Runes: []rune("안녕하세요")})
	press(ChatKey{Name: "backspace"})
	if got := m.Input.Text(); got != "안녕하세" {
		t.Fatalf("after backspace: %q, want 안녕하세", got)
	}
	press(ChatKey{Name: "left"})
	press(ChatKey{Name: "left"})
	press(ChatKey{Name: "backspace"})
	if got, cur := m.Input.Text(), m.Input.Cursor; got != "안하세" || cur != 1 {
		t.Fatalf("after left left backspace: %q cursor %d, want 안하세 cursor 1", got, cur)
	}
	press(ChatKey{Runes: []rune("녕")})
	if got := m.Input.Text(); got != "안녕하세" {
		t.Fatalf("after insert: %q", got)
	}
	press(ChatKey{Name: "end"})
	press(ChatKey{Runes: []rune("요 hi")})
	if got := m.Input.Text(); got != "안녕하세요 hi" {
		t.Fatalf("after end + type: %q", got)
	}
	// A burst of letters bubbletea delivers in one read is text, even when it
	// spells a key name.
	press(ChatKey{Runes: []rune("end")})
	if got := m.Input.Text(); got != "안녕하세요 hiend" {
		t.Fatalf("a typed burst was read as a key: %q", got)
	}

	// The cursor lands on the right cell: two columns per syllable.
	in := ChatInput{Runes: []rune("안녕하세요"), Cursor: 2}
	rows := inputView(PlainTheme(), 0, in, 30, 1, true, "")
	if w := width(rows[0]); w != 30 {
		t.Fatalf("input row is %d columns, want 30", w)
	}
	colour := inputView(ColourTheme(), 0, in, 30, 1, true, "")
	// The cursor cell is the syllable 하, painted on the write head's fill.
	th := ColourTheme()
	if !strings.Contains(colour[0], th.paint(th.textFresh, "하")) {
		t.Errorf("the cursor is not on 하: %q", colour[0])
	}
	// A wrapped Korean line never splits a syllable and never overflows.
	long := ChatInput{Runes: []rune(strings.Repeat("가나다라", 10))}
	long.Cursor = len(long.Runes)
	for _, r := range inputView(PlainTheme(), 0, long, 21, 4, true, "") {
		if w := width(r); w != 21 {
			t.Errorf("wrapped row is %d columns, want 21: %q", w, r)
		}
	}
}

// TestChatKeys pins the key contract: enter sends, ctrl+c stops a streaming
// turn and leaves an idle prompt, ctrl+d leaves on an empty line, the
// commands, and the measuring input taking nothing.
func TestChatKeys(t *testing.T) {
	type step struct {
		k    ChatKey
		want ChatActionKind
	}
	m := NewChatModel(PlainTheme())
	if _, a := m.HandleKey(ChatKey{Runes: []rune("hi")}, 0, 120, 36); a.Kind != ActNone {
		t.Fatal("measuring: typing did something")
	}
	if m2, _ := m.HandleKey(ChatKey{Runes: []rune("hi")}, 0, 120, 36); !m2.Input.Empty() {
		t.Fatal("measuring: the input took text")
	}
	m = m.Apply(ChatEvent{Kind: ChatOpened})

	m, a := m.HandleKey(ChatKey{Runes: []rune("hello")}, time.Second, 120, 36)
	m, a = m.HandleKey(ChatKey{Name: "alt+enter"}, time.Second, 120, 36)
	m, a = m.HandleKey(ChatKey{Runes: []rune("world")}, time.Second, 120, 36)
	m, a = m.HandleKey(ChatKey{Name: "enter"}, time.Second, 120, 36)
	if a.Kind != ActSend || a.Text != "hello\nworld" {
		t.Fatalf("enter: %+v, want send of hello\\nworld", a)
	}
	if m.Phase != ChatPrefill || !m.Input.Empty() {
		t.Fatalf("after send: phase %d, input %q", m.Phase, m.Input.Text())
	}
	// Enter while an answer is on its way sends nothing: one turn at a time.
	m, _ = m.HandleKey(ChatKey{Runes: []rune("next")}, time.Second, 120, 36)
	if m2, a := m.HandleKey(ChatKey{Name: "enter"}, time.Second, 120, 36); a.Kind != ActNone || m2.Input.Text() != "next" {
		t.Fatalf("enter mid-turn: %+v, input %q", a, m2.Input.Text())
	}
	if _, a := m.HandleKey(ChatKey{Name: "ctrl+c"}, time.Second, 120, 36); a.Kind != ActCancel {
		t.Fatalf("ctrl+c mid-turn: %+v, want cancel", a)
	}
	m = m.Apply(ChatEvent{Kind: ChatTurnDone, Cancelled: true, Record: &tape.RequestRecord{}})
	if m.Phase != ChatIdle || !m.Turns()[0].Cancelled {
		t.Fatalf("after a cancelled turn: phase %d", m.Phase)
	}
	if _, a := m.HandleKey(ChatKey{Name: "ctrl+c"}, time.Second, 120, 36); a.Kind != ActExit {
		t.Fatalf("ctrl+c idle: %+v, want exit", a)
	}
	if _, a := m.HandleKey(ChatKey{Name: "ctrl+d"}, time.Second, 120, 36); a.Kind != ActNone {
		t.Fatal("ctrl+d on a non-empty line left")
	}
	m.Input = ChatInput{}
	if _, a := m.HandleKey(ChatKey{Name: "ctrl+d"}, time.Second, 120, 36); a.Kind != ActExit {
		t.Fatal("ctrl+d on an empty line did not leave")
	}
	for _, cmd := range []string{"/exit", "/quit", "/EXIT"} {
		m.Input = ChatInput{Runes: []rune(cmd), Cursor: len(cmd)}
		if _, a := m.HandleKey(ChatKey{Name: "enter"}, time.Second, 120, 36); a.Kind != ActExit {
			t.Errorf("%s: %+v, want exit", cmd, a)
		}
	}
	m.Input = ChatInput{Runes: []rune("/foo"), Cursor: 4}
	m2, a := m.HandleKey(ChatKey{Name: "enter"}, time.Second, 120, 36)
	last := m2.Items[len(m2.Items)-1]
	if a.Kind != ActNone || last.NoteOf != NoteHint || !strings.Contains(last.Note, "/exit") || !strings.Contains(last.Note, "/help") {
		t.Errorf("/foo: %+v, note %q", a, last.Note)
	}
	m.Input = ChatInput{Runes: []rune("/help"), Cursor: 5}
	m2, _ = m.HandleKey(ChatKey{Name: "enter"}, time.Second, 120, 36)
	if m2.Items[len(m2.Items)-1].NoteOf != NoteHelp {
		t.Error("/help printed no key list")
	}
	frame := ChatView(m2, time.Second, 120, 36)
	for _, want := range []string{"alt+enter", "ctrl+c", "/exit /quit"} {
		if !strings.Contains(frame, want) {
			t.Errorf("/help frame lacks %q", want)
		}
	}
}

// TestChatContextFull: a refused turn leaves the calm notice, its text back in
// the input, and an input that sends nothing but takes /exit.
func TestChatContextFull(t *testing.T) {
	m := NewChatModel(PlainTheme()).Apply(ChatEvent{Kind: ChatOpened})
	m.Input = ChatInput{Runes: []rune("one more"), Cursor: 8}
	m, _ = m.HandleKey(ChatKey{Name: "enter"}, time.Second, 120, 36)
	m = m.Apply(ChatEvent{Kind: ChatContextFull})
	if m.Phase != ChatFull || len(m.Turns()) != 0 || !m.Input.Empty() {
		t.Fatalf("phase %d, %d turns, input %q", m.Phase, len(m.Turns()), m.Input.Text())
	}
	frame := ChatView(m, time.Second, 120, 36)
	if !strings.Contains(frame, "the conversation is full — /exit to save it") {
		t.Error("no context-full notice on screen")
	}
	if !strings.Contains(frame, "not sent: “one more”") {
		t.Error("the refused message is not quoted in the notice")
	}
	m.Input = ChatInput{Runes: []rune("still"), Cursor: 5}
	if _, a := m.HandleKey(ChatKey{Name: "enter"}, time.Second, 120, 36); a.Kind != ActNone {
		t.Errorf("a full conversation sent a turn: %+v", a)
	}
	m.Input = ChatInput{Runes: []rune("/exit"), Cursor: 5}
	if _, a := m.HandleKey(ChatKey{Name: "enter"}, time.Second, 120, 36); a.Kind != ActExit {
		t.Errorf("/exit on a full conversation: %+v", a)
	}
}

// TestChatTurnDoneRebuildsDroppedTokens: the record is the answer. A token the
// screen's queue dropped is back once the turn is done.
func TestChatTurnDoneRebuildsDroppedTokens(t *testing.T) {
	m := NewChatModel(PlainTheme()).Apply(ChatEvent{Kind: ChatOpened})
	m.Input = ChatInput{Runes: []rune("hi"), Cursor: 2}
	m, _ = m.HandleKey(ChatKey{Name: "enter"}, time.Second, 120, 36)
	m = m.Apply(ChatEvent{Kind: ChatObserved, Event: Event{Kind: EventToken, T: 1100 * time.Millisecond, Token: tape.TokenEvent{Text: "Hello"}}})
	rec := &tape.RequestRecord{Tokens: []tape.TokenEvent{
		{T: 100 * time.Millisecond, Text: "Hello"}, {T: 120 * time.Millisecond, Text: ","}, {T: 140 * time.Millisecond, Text: " world"},
	}, Timings: tape.TimingsSummary{PredictedN: 3, PredictedPerSecond: 50, TTFTMs: 100}}
	m = m.Apply(ChatEvent{Kind: ChatTurnDone, Record: rec})
	tr := m.Turns()[0]
	if tr.Answer.Text != "Hello, world" || !tr.Answer.Done {
		t.Fatalf("answer %q done %v", tr.Answer.Text, tr.Answer.Done)
	}
	if !strings.Contains(ChatView(m, 2*time.Second, 120, 36), "50.0 tok/s") {
		t.Error("the finished turn does not show the server's rate")
	}
	// A turn that failed says so where its answer would be.
	m.Input = ChatInput{Runes: []rune("again"), Cursor: 5}
	m, _ = m.HandleKey(ChatKey{Name: "enter"}, 3*time.Second, 120, 36)
	m = m.Apply(ChatEvent{Kind: ChatTurnDone, Err: errors.New("server said 500")})
	if !strings.Contains(ChatView(m, 4*time.Second, 120, 36), "✗ server said 500") {
		t.Error("a failed turn does not show its error")
	}
}

// TestChatTooSmall: below the chat's own minimum it draws the one line, at
// exactly the size it was given.
func TestChatTooSmall(t *testing.T) {
	f := ChatView(NewChatModel(PlainTheme()), 0, 50, 12)
	if lines := strings.Split(f, "\n"); len(lines) != 12 || width(lines[0]) != 50 || !strings.Contains(f, "60×20") {
		t.Errorf("too-small frame: %q", f)
	}
	// The record screen's own message is not the chat's.
	if strings.Contains(f, "100×30") {
		t.Error("the chat quoted the record screen's minimum")
	}
}

// TestTurnBars: the by-turn chart is one bar per turn with a gap between
// bars, newest at the right; an answered turn is never below the lowest step,
// a turn with no rate is an empty column, a stopped turn is drawn dim, and
// with more turns than fit the oldest go (look round 1, 2026-09-24).
func TestTurnBars(t *testing.T) {
	th := ColourTheme()
	draw := func(bars []turnBar, w int) string {
		l := newLine(PlainTheme(), w)
		turnBars(l, PlainTheme(), bars, w)
		return l.String()
	}
	if got := draw([]turnBar{{rate: 40}, {rate: 1}, {rate: 0}, {rate: 20}}, 19); got != "████ ▁▁▁▁      ▄▄▄▄" {
		t.Errorf("four turns in 19 cells: %q", got)
	}
	many := make([]turnBar, 12)
	for i := range many {
		many[i] = turnBar{rate: float64(i + 1)}
	}
	if got := draw(many, 9); got != "▅ ▆ ▇ ▇ █" {
		t.Errorf("twelve turns in 9 cells, want the last five: %q", got)
	}
	l := newLine(th, 9)
	turnBars(l, th, []turnBar{{rate: 30}, {rate: 30, stopped: true}}, 9)
	if s := l.String(); !strings.Contains(s, th.paint(th.dim, "████")) || !strings.Contains(s, th.paint(th.accentMuted, "████")) {
		t.Errorf("a stopped turn is not the dim one: %q", s)
	}
}

// mdStream is a stream of the given token texts, an answer throughout.
func mdStream(pieces []string, done bool) Stream {
	var s Stream
	for i, p := range pieces {
		s.Tokens = append(s.Tokens, Token{T: time.Duration(i) * time.Millisecond, Text: p})
		s.Text += p
	}
	s.Done = done
	return s
}

// mdRow is one drawn row of an answer as the markdown pass leaves it: the
// text, and which of its runes draw strong.
type mdRow struct {
	text   string
	strong string // one byte per drawn rune: 'B' strong, '.' not
}

// mdSourceLines draws s through the chat's markdown pass at width w and
// groups the rows by the source line (the count of newlines before the row's
// first source rune) they came from. Rows the wrapper wrote whole — a blank
// line — belong to no source line and are left out.
func mdSourceLines(s Stream, w int) (map[int][]mdRow, *chatMD) {
	md := newChatMD(s)
	text := []rune(s.Text)
	out := map[int][]mdRow{}
	for _, bl := range streamTextLinesWith(s, w, md.wrap) {
		first := -1
		var mask strings.Builder
		for _, o := range bl.src {
			if first < 0 && o >= 0 {
				first = o
			}
			if md.isStrong(o) {
				mask.WriteByte('B')
			} else {
				mask.WriteByte('.')
			}
		}
		if first < 0 {
			continue
		}
		line := strings.Count(string(text[:first]), "\n")
		out[line] = append(out[line], mdRow{text: bl.text, strong: mask.String()})
	}
	return out, md
}

// TestChatMarkdownNeverFlickersBack (look round 2, 2026-09-24): the answer is
// fed a token at a time — the example's tokens, and then one rune at a time,
// which splits every marker — and at every prefix
//
//   - a source line whose newline has arrived draws exactly as it will at the
//     end: it may have flipped to styled while it was the line being written,
//     never after;
//   - the line being written only ever gains style: once it has a bullet or a
//     strong rune, every later prefix still has one;
//   - a rune that drew strong and is still drawn stays strong.
func TestChatMarkdownNeverFlickersBack(t *testing.T) {
	answer := chatExampleMarkdownTurn.answer
	runesOf := func(text string) []string {
		var out []string
		for _, r := range text {
			out = append(out, string(r))
		}
		return out
	}
	// The cases a marker is not yet decided in: a "**" that becomes "***", a
	// "**" inside a code span that has not closed yet, a hash run that is not
	// a heading, a dash that is not a bullet.
	const tricky = "**b*** and **c** then `x **y** z` end\n##tag\n-- not\n#### Deep **one**\n  + nested **two** end\n**Bottom line:** done"
	for name, pieces := range map[string][]string{
		"tokens": chatExampleTokens(answer),
		"runes":  runesOf(answer),
		"tricky": runesOf(tricky),
	} {
		t.Run(name, func(t *testing.T) {
			const w = 60
			final, _ := mdSourceLines(mdStream(pieces, true), w)
			styled := map[int]bool{}
			var wasStrong []bool
			for k := 1; k <= len(pieces); k++ {
				s := mdStream(pieces[:k], k == len(pieces))
				got, md := mdSourceLines(s, w)
				complete := strings.Count(s.Text, "\n")
				if s.Done {
					complete++
				}
				for line, rows := range got {
					if line < complete {
						if !reflect.DeepEqual(rows, final[line]) {
							t.Fatalf("prefix %d: whole line %d draws %q, at the end %q", k, line, rows, final[line])
						}
						continue
					}
					now := false
					for _, r := range rows {
						if strings.ContainsRune(r.text, bulletGlyph) || strings.Contains(r.strong, "B") {
							now = true
						}
					}
					if styled[line] && !now {
						t.Fatalf("prefix %d (%q): line %d lost its style: %q", k, pieces[k-1], line, rows)
					}
					styled[line] = styled[line] || now
				}
				// A marker drawn while its pair was open is gone once it
				// closes; that is the one flip. A rune still drawn keeps its
				// weight.
				drawn := map[int]bool{}
				for _, bl := range streamTextLinesWith(s, w, newChatMD(s).wrap) {
					for _, o := range bl.src {
						drawn[o] = true
					}
				}
				for o, was := range wasStrong {
					if was && drawn[o] && !md.isStrong(o) {
						t.Fatalf("prefix %d: rune %d was strong and is not", k, o)
					}
				}
				wasStrong = append(wasStrong[:0], md.strong...)
			}
			// And the pass did something: the heading, a bullet and a bold
			// word all drew styled at the end.
			var all strings.Builder
			all.WriteString("\n")
			for _, rows := range final {
				for _, r := range rows {
					all.WriteString(r.text + "\n")
				}
			}
			if name == "tricky" {
				want := []string{"**b*** and c then `x **y** z` end", "##tag", "-- not", "Deep one", "  • nested two end", "Bottom line: done"}
				var got []string
				for line := 0; line < len(final); line++ {
					for _, r := range final[line] {
						got = append(got, r.text)
					}
				}
				if !reflect.DeepEqual(got, want) {
					t.Errorf("the finished answer draws %q, want %q", got, want)
				}
				return
			}
			for _, want := range []string{"Counting lines", "• `wc -l`", "\n  • `-F`", "at disk speed."} {
				if !strings.Contains(all.String(), want) {
					t.Errorf("the finished answer has no %q:\n%s", want, all.String())
				}
			}
			if strings.Contains(all.String(), "###") || strings.Contains(all.String(), "- `wc") {
				t.Errorf("markers left in the finished answer:\n%s", all.String())
			}
		})
	}
}

// TestChatMarkdownBulletHangsUnderItsText: a wrapped item's continuation rows
// start at the column its text starts at — under the text, not the bullet —
// with Korean's two-column runes counted as two, and no row is wider than
// the column.
func TestChatMarkdownBulletHangsUnderItsText(t *testing.T) {
	cases := []struct {
		line string
		pw   int // the columns before the item's text
	}{
		{"- 한국어 항목: 큰 파일은 한 번에 읽지 말고 1 MiB 버퍼로 나눠 읽으면 메모리를 거의 쓰지 않고", 2},
		{"  * 한국어 항목: 큰 파일은 한 번에 읽지 말고 1 MiB 버퍼로 나눠 읽으면 메모리를 거의 쓰지 않고", 4},
		{"+ plain words that wrap more than once when the column is only this narrow", 2},
	}
	for _, c := range cases {
		for _, w := range []int{21, 30, 47} {
			md := newChatMD(mdStream([]string{c.line}, true))
			lines := md.wrap(c.line, 0, w)
			if len(lines) < 2 {
				t.Fatalf("%q at %d: %d rows, the case must wrap", c.line, w, len(lines))
			}
			first := []rune(lines[0].text)
			if string(first[c.pw-2:c.pw]) != "• " || strings.TrimLeft(string(first[:c.pw-2]), " ") != "" {
				t.Errorf("%q at %d: first row %q does not open with its bullet at column %d", c.line, w, lines[0].text, c.pw-2)
			}
			for i, wl := range lines {
				if cw := width(wl.text); cw > w {
					t.Errorf("%q at %d: row %d is %d columns: %q", c.line, w, i, cw, wl.text)
				}
				if i == 0 {
					continue
				}
				rs := []rune(wl.text)
				for k := 0; k < c.pw; k++ {
					if rs[k] != ' ' || wl.src[k] != -1 {
						t.Fatalf("%q at %d: row %d %q does not hang %d wrapper columns", c.line, w, i, wl.text, c.pw)
					}
				}
				if rs[c.pw] == ' ' {
					t.Errorf("%q at %d: row %d %q hangs deeper than the text", c.line, w, i, wl.text)
				}
			}
		}
	}
}

// TestChatMarkdownLeavesCodeAlone: inside a fenced block and inside an inline
// code span a "#", a "- " and a "**" are code, drawn as written and never
// strong; the same markers outside them are markdown.
func TestChatMarkdownLeavesCodeAlone(t *testing.T) {
	text := "```sh\n# **not bold**\n- not a bullet\n```\nsee `a**b**c` and **yes**"
	s := mdStream([]string{text}, true)
	md := newChatMD(s)
	lines := streamTextLinesWith(s, 60, md.wrap)
	var got []string
	for _, bl := range lines {
		got = append(got, bl.text)
	}
	want := []string{"```sh", "# **not bold**", "- not a bullet", "```", "see `a**b**c` and yes"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("drawn %q, want %q", got, want)
	}
	end := strings.Index(text, "\nsee")
	for o := 0; o < len([]rune(text)); o++ {
		inCode := o < len([]rune(text[:end]))
		span := strings.Index(text, "`a**b**c`")
		inSpan := o >= len([]rune(text[:span])) && o < len([]rune(text[:span+len("`a**b**c`")]))
		if (inCode || inSpan) && md.isStrong(o) {
			t.Errorf("rune %d (%q) is code and drew strong", o, string([]rune(text)[o]))
		}
	}
	yes := len([]rune(text)) - len("yes**")
	if !md.isStrong(yes) {
		t.Error("the **yes** outside the code did not draw strong")
	}
}

// TestChatMachineCollapsesOnlyWhenNothingObserved (look round 2,
// 2026-09-24): the MACHINE block is its header and one dim line only when not
// one host figure has been observed; a single figure keeps the rows, with "?"
// where the others are missing.
func TestChatMachineCollapsesOnlyWhenNothingObserved(t *testing.T) {
	empty := tape.RunSample{T: time.Second}
	cases := []struct {
		name    string
		samples []tape.RunSample
		gpus    bool
		want    string
	}{
		{"no sample yet", nil, false, "read while a turn runs"},
		{"samples carry nothing", []tape.RunSample{empty, {T: 2 * time.Second}}, false, "no /proc view of the server"},
		{"load only", []tape.RunSample{empty, {T: 2 * time.Second, LoadAvg1: 0.4}}, false, ""},
		{"cpu time only", []tape.RunSample{{T: time.Second, Mem: tape.MemSample{CPUSeconds: 3}}}, false, ""},
		{"rss only", []tape.RunSample{{T: time.Second, Mem: tape.MemSample{RSSBytes: 1 << 30}}}, false, ""},
		{"a gpu sample", []tape.RunSample{{T: time.Second, GPUs: []tape.GPUSample{{Index: 0}}}}, false, ""},
		{"a listed gpu, no sample", nil, true, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := ExampleChatAt(chatExampleOpened, PlainTheme(), false)
			m.Machine.Samples = c.samples
			m.Machine.Summary.Host.GPUs = nil
			m.Machine.Summary.GPUsAtEnd = nil
			if c.gpus {
				m.Machine.Summary.Host.GPUs = ExampleTapeN(1).Summary.Host.GPUs
			}
			if got := hostUnobserved(m.Machine); got != c.want {
				t.Fatalf("hostUnobserved = %q, want %q", got, c.want)
			}
			frame := ChatView(m, chatExampleOpened, 120, 36)
			hasCPU := strings.Contains(frame, "CPU ")
			if c.want == "" && !hasCPU {
				t.Errorf("a figure was observed and the rows are gone:\n%s", frame)
			}
			if c.want != "" && (hasCPU || !strings.Contains(frame, c.want)) {
				t.Errorf("nothing was observed and the block is not collapsed to %q:\n%s", c.want, frame)
			}
		})
	}
}
