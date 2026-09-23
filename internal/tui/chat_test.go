package tui

import (
	"errors"
	"os"
	"path/filepath"
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
