package tui

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/midagedev/toktape/internal/tape"
)

// The state behind `toktape chat` (TTP-184, 2026-09-24).
//
// A chat is the record screen cut the other way: one stream at a time, turn
// after turn, each one a person typed after reading the last answer. The
// state reuses the record screen's pieces rather than growing a parallel set:
// every answer is a Stream, so the wrap, the age bands, the thinking/answer
// split and the code classes of reasoning.go and highlight.go draw it
// unchanged; and the machine readings live in an ordinary Model, so the
// RESOURCES rows of resources.go draw them unchanged. Nothing in this file
// reads the clock: every instant is the clip time the event or the key was
// stamped with, and ChatView is a pure function of (ChatModel, t), like View.

// ChatPhase is what the session is doing, which decides what the input takes.
type ChatPhase int

const (
	// ChatMeasuring: the session is still opening — attaching and running the
	// measurement probe. The input takes no keys yet.
	ChatMeasuring ChatPhase = iota
	// ChatIdle: waiting for the user.
	ChatIdle
	// ChatPrefill: a turn was sent and no token has come back.
	ChatPrefill
	// ChatStreaming: the answer is arriving.
	ChatStreaming
	// ChatFull: the conversation no longer fits the server's context. The
	// input takes /exit and nothing else is sent.
	ChatFull
	// ChatFailed: the session could not be opened. Any exit key leaves.
	ChatFailed
)

// ChatTurn is one exchange: what the user typed and what came back.
type ChatTurn struct {
	User string
	// SentAt is the clip time Enter was pressed.
	SentAt time.Duration
	// Answer is the reply as the screen saw it arrive, on the clip clock.
	// Answer.Done marks a finished turn, which is what stops its write-head
	// glow and its cursor.
	Answer Stream
	// Record is the recorder's record of the turn, set when Send returned.
	// Its figures are the record; the live ones are the check (CLAUDE.md).
	Record *tape.RequestRecord
	// Stopping is set when ctrl+c asked this turn to end and the recorder has
	// not returned yet; Cancelled when it returned having been cut short.
	Stopping  bool
	Cancelled bool
}

// ChatNoteKind is what a notice in the transcript is about.
type ChatNoteKind int

const (
	// NoteHint answers an unknown command or a key that did nothing.
	NoteHint ChatNoteKind = iota
	// NoteHelp is the key list /help prints.
	NoteHelp
	// NoteFull is the context-full notice.
	NoteFull
	// NoteError is a turn the recorder could not send.
	NoteError
)

// ChatItem is one entry of the transcript: a turn, or a notice from toktape
// itself. Notices are the screen's, never the session's: nothing in one is
// sent to the model or written to the tape.
type ChatItem struct {
	Turn   *ChatTurn
	Note   string
	NoteOf ChatNoteKind
}

// ChatModel is everything ChatView draws.
type ChatModel struct {
	// Machine carries the server's identity and the host samples, and is
	// what the title and the RESOURCES rows are drawn from. Its Streams stay
	// empty: the answers live on the turns.
	Machine Model
	Items   []ChatItem
	Phase   ChatPhase
	Input   ChatInput
	// Scroll is how many transcript rows the view is lifted off the tail. 0
	// follows the conversation as it grows.
	Scroll int
	// Err is why the session could not be opened, set with ChatFailed.
	Err string
	// Attached is set once the recorder said which server and model it found.
	Attached bool
	// MeasureNote is the recorder's latest word while the session opens.
	MeasureNote string
}

// NewChatModel is the state the screen opens in: measuring, nothing typed.
func NewChatModel(th Theme) ChatModel {
	return ChatModel{Machine: Model{Theme: th, Mode: ModeLive}, Phase: ChatMeasuring}
}

// ChatEventKind is what the session side reported.
type ChatEventKind int

const (
	// ChatObserved carries one recorder observation in Event: the attach
	// (EventProps), a host sample, a turn's stream start and its tokens.
	ChatObserved ChatEventKind = iota
	// ChatOpened: Open returned and the first turn may be sent.
	ChatOpened
	// ChatOpenFailed: Open returned Err. The screen shows it and waits for
	// the user to leave.
	ChatOpenFailed
	// ChatTurnDone: Send returned. Record is its record (nil when nothing was
	// recorded), Err its error, Cancelled whether ctrl+c ended it.
	ChatTurnDone
	// ChatContextFull: Send refused the turn because the conversation no
	// longer fits. Nothing was sent.
	ChatContextFull
	// ChatStatus: a line of the recorder's own while the session opens, in
	// Note — "measuring this server's prefill…". Shown under the spinner
	// until the session is open, and dropped after.
	ChatStatus
)

// ChatEvent is one report from the session side of `toktape chat`.
type ChatEvent struct {
	Kind      ChatEventKind
	T         time.Duration
	Event     Event
	Record    *tape.RequestRecord
	Err       error
	Cancelled bool
	Note      string
}

// Apply folds one session report into the model.
func (m ChatModel) Apply(e ChatEvent) ChatModel {
	switch e.Kind {
	case ChatObserved:
		return m.observe(e.Event)
	case ChatOpened:
		m.Attached = true
		if m.Phase == ChatMeasuring {
			m.Phase = ChatIdle
		}
	case ChatOpenFailed:
		m.Phase = ChatFailed
		m.Err = "the session could not be opened"
		if e.Err != nil {
			m.Err = e.Err.Error()
		}
	case ChatTurnDone:
		m = m.finishTurn(e)
	case ChatContextFull:
		m = m.contextFull()
	case ChatStatus:
		if m.Phase == ChatMeasuring && strings.TrimSpace(e.Note) != "" {
			m.MeasureNote = strings.TrimSpace(e.Note)
		}
	}
	return m
}

// observe applies one recorder observation. The machine's own kinds go to the
// Model that owns them; a stream start and its tokens go to the live turn.
func (m ChatModel) observe(e Event) ChatModel {
	switch e.Kind {
	case EventDiscovered, EventProps, EventPID, EventSample:
		m.Machine = m.Machine.Apply(e)
		if e.Kind == EventProps {
			m.Attached = true
		}
	case EventStreamStart:
		if tr := m.liveTurn(); tr != nil {
			tr.Answer.StartedAt = e.T
			if e.MaxTokens > 0 {
				tr.Answer.MaxTokens = e.MaxTokens
			}
		}
	case EventToken:
		tr := m.liveTurn()
		if tr == nil {
			return m
		}
		s := &tr.Answer
		itl := time.Duration(0)
		if n := len(s.Tokens); n > 0 {
			itl = e.T - s.Tokens[n-1].T
		}
		s.Tokens = append(s.Tokens, Token{
			T: e.T, Text: e.Token.Text, ITL: itl,
			MajFaultsDelta: e.Token.MajFaultsDelta, Reasoning: e.Token.Reasoning,
		})
		s.Text += e.Token.Text
		s.EndedAt = e.T
		if m.Phase == ChatPrefill {
			m.Phase = ChatStreaming
		}
	}
	return m
}

// liveTurn is the turn waiting on the recorder, nil when none is.
func (m ChatModel) liveTurn() *ChatTurn {
	if m.Phase != ChatPrefill && m.Phase != ChatStreaming {
		return nil
	}
	for i := len(m.Items) - 1; i >= 0; i-- {
		if tr := m.Items[i].Turn; tr != nil {
			if tr.Answer.Done {
				return nil
			}
			return tr
		}
	}
	return nil
}

// finishTurn closes the live turn with what Send returned.
//
// The tokens are rebuilt from the record when it holds more than the screen
// saw. The screen's queue drops an event rather than stall the recorder
// (cmd/toktape/tui.go, tuiQueue), which on the record screen costs a frame;
// here the screen is the only place the user reads the answer, so a dropped
// token would be a word missing from the reply. The record is the answer.
func (m ChatModel) finishTurn(e ChatEvent) ChatModel {
	tr := m.liveTurn()
	if tr == nil {
		return m
	}
	if m.Phase != ChatFull {
		m.Phase = ChatIdle
	}
	s := &tr.Answer
	if rec := e.Record; rec != nil {
		cp := *rec
		tr.Record = &cp
		s.Timings, s.Cache = rec.Timings, rec.Cache
		if len(rec.Tokens) > len(s.Tokens) {
			s.Tokens, s.Text = replayTokens(rec.Tokens, tr.SentAt), ""
			for _, tk := range s.Tokens {
				s.Text += tk.Text
			}
			if n := len(s.Tokens); n > 0 {
				s.EndedAt = s.Tokens[n-1].T
			}
		}
		if rec.Error != "" && s.Err == "" && !e.Cancelled {
			s.Err = rec.Error
		}
	}
	tr.Cancelled = e.Cancelled || errors.Is(e.Err, context.Canceled)
	tr.Stopping = false
	if e.Err != nil && !tr.Cancelled && s.Err == "" {
		s.Err = e.Err.Error()
	}
	s.Done = true
	return m
}

// replayTokens places a record's tokens on the clip clock: each token's time is
// relative to its own request, and the request went out when Enter was pressed.
func replayTokens(tks []tape.TokenEvent, sentAt time.Duration) []Token {
	out := make([]Token, 0, len(tks))
	var prev time.Duration
	for i, tk := range tks {
		at := sentAt + tk.T
		itl := time.Duration(0)
		if i > 0 {
			itl = at - prev
		}
		prev = at
		out = append(out, Token{T: at, Text: tk.Text, ITL: itl, MajFaultsDelta: tk.MajFaultsDelta, Reasoning: tk.Reasoning})
	}
	return out
}

// contextFullNote is the notice the context-full turn leaves behind. Calm,
// because nothing was lost: the conversation so far is on its way to the
// tape, and the message that did not fit was never sent.
const contextFullNote = "the conversation is full — /exit to save it"

// contextFull turns the refused turn into the notice. The message is taken
// off the transcript, because a message drawn as sent that the model never saw
// is the transcript lying. It is quoted in the notice rather than put back in
// the input: the input now takes /exit and nothing else, and a message left
// in it would turn the /exit the notice asks for into "…text/exit".
func (m ChatModel) contextFull() ChatModel {
	text := contextFullNote
	if tr := m.liveTurn(); tr != nil {
		for i := len(m.Items) - 1; i >= 0; i-- {
			if m.Items[i].Turn == tr {
				m.Items = append(append([]ChatItem(nil), m.Items[:i]...), m.Items[i+1:]...)
				break
			}
		}
		text += "\nnot sent: “" + truncate(strings.Join(strings.Fields(tr.User), " "), 120) + "”"
	}
	m.Phase = ChatFull
	return m.note(NoteFull, text)
}

// note appends a notice, unless the last item already says the same thing: a
// key pressed twice is one hint, not two.
func (m ChatModel) note(kind ChatNoteKind, text string) ChatModel {
	if n := len(m.Items); n > 0 && m.Items[n-1].Turn == nil && m.Items[n-1].Note == text {
		return m
	}
	m.Items = append(append([]ChatItem(nil), m.Items...), ChatItem{Note: text, NoteOf: kind})
	m.Scroll = 0
	return m
}

// Turns is the turns of the transcript, oldest first.
func (m ChatModel) Turns() []*ChatTurn {
	var out []*ChatTurn
	for _, it := range m.Items {
		if it.Turn != nil {
			out = append(out, it.Turn)
		}
	}
	return out
}

// ChatActionKind is what a key asked the session side to do.
type ChatActionKind int

const (
	// ActNone: the key changed the screen only.
	ActNone ChatActionKind = iota
	// ActSend: send Text as the next turn.
	ActSend
	// ActCancel: end the turn in flight, and only it.
	ActCancel
	// ActExit: close the session, save the tape and leave.
	ActExit
)

// ChatAction is the one thing a key may ask of the session.
type ChatAction struct {
	Kind ChatActionKind
	Text string
}

// ChatKey is one key press, as the adapter read it: bubbletea's name for it
// ("enter", "alt+enter", "ctrl+c", "backspace", "pgup") and, for typed text,
// the runes. Paste marks a bracketed paste, whose newlines are text.
type ChatKey struct {
	Name  string
	Runes []rune
	Paste bool
}

// The chat keys. Enter sends and alt+enter breaks the line: bubbletea v1
// reports alt+enter on every terminal, and shift+enter on none — it arrives as
// a plain enter. ctrl+j is the same newline on a terminal that eats alt.
const (
	keyNewline    = "alt+enter"
	keyNewlineAlt = "ctrl+j"
)

// chatHelpLines are the rows /help prints: key, then what it does.
var chatHelpLines = [][2]string{
	{"enter", "send"},
	{"alt+enter", "new line"},
	{"ctrl+c", "stop the answer · leave when idle"},
	{"ctrl+d", "leave, on an empty line"},
	{"pgup pgdn", "scroll the conversation"},
	{"/exit /quit", "save the conversation and leave"},
}

// unknownCommandHint answers a slash command the screen does not have.
func unknownCommandHint(cmd string) string {
	return "no command " + cmd + " — /exit saves and leaves, /help lists the keys"
}

// HandleKey applies one key at clip time t to a screen of w×h, and returns
// what the session side must do about it.
//
// It is pure — the adapter reads the key and the clock, and this decides — so
// every key rule is testable without a terminal.
func (m ChatModel) HandleKey(k ChatKey, t time.Duration, w, h int) (ChatModel, ChatAction) {
	switch k.Name {
	case "ctrl+c":
		switch m.Phase {
		case ChatPrefill, ChatStreaming:
			if tr := m.liveTurn(); tr != nil {
				tr.Stopping = true
			}
			return m, ChatAction{Kind: ActCancel}
		}
		return m, ChatAction{Kind: ActExit}
	case "ctrl+d":
		if m.Input.Empty() || m.Phase == ChatFailed {
			return m, ChatAction{Kind: ActExit}
		}
		m.Input = m.Input.Delete()
		return m, ChatAction{}
	case "pgup":
		m.Scroll = min(m.Scroll+chatPage(m, w, h), chatMaxScroll(m, t, w, h))
		return m, ChatAction{}
	case "pgdown":
		m.Scroll = max(0, m.Scroll-chatPage(m, w, h))
		return m, ChatAction{}
	}
	if m.Phase == ChatFailed {
		switch k.Name {
		case "enter", "esc", "q":
			return m, ChatAction{Kind: ActExit}
		}
		return m, ChatAction{}
	}
	if m.Phase == ChatMeasuring {
		// The input is disabled until the session is open: a message typed
		// now could not be sent, and one queued behind the probe would be
		// sent on a key press the user made long before.
		return m, ChatAction{}
	}
	switch k.Name {
	case "enter":
		if k.Paste {
			m.Input = m.Input.Insert([]rune{'\n'})
			return m, ChatAction{}
		}
		return m.submit(t)
	case keyNewline, keyNewlineAlt:
		m.Input = m.Input.Insert([]rune{'\n'})
	case "backspace", "ctrl+h":
		m.Input = m.Input.Backspace()
	case "delete":
		m.Input = m.Input.Delete()
	case "left", "ctrl+b":
		m.Input = m.Input.Move(-1)
	case "right", "ctrl+f":
		m.Input = m.Input.Move(1)
	case "home", "ctrl+a":
		m.Input = m.Input.Home()
	case "end", "ctrl+e":
		m.Input = m.Input.End()
	case "ctrl+u":
		m.Input = m.Input.DeleteToStart()
	case "ctrl+w", "alt+backspace":
		m.Input = m.Input.DeleteWord()
	default:
		if len(k.Runes) > 0 {
			m.Input = m.Input.Insert(k.Runes)
		}
	}
	return m, ChatAction{}
}

// submit is Enter.
func (m ChatModel) submit(t time.Duration) (ChatModel, ChatAction) {
	text := m.Input.Text()
	if strings.TrimSpace(text) == "" {
		return m, ChatAction{}
	}
	if cmd, ok := isCommand(text); ok {
		m.Input = ChatInput{}
		switch cmd {
		case "/exit", "/quit":
			return m, ChatAction{Kind: ActExit}
		case "/help":
			return m.note(NoteHelp, "keys"), ChatAction{}
		}
		return m.note(NoteHint, unknownCommandHint(cmd)), ChatAction{}
	}
	switch m.Phase {
	case ChatFull:
		return m.note(NoteFull, contextFullNote), ChatAction{}
	case ChatPrefill, ChatStreaming:
		// One turn at a time: the text stays in the input, to be sent when
		// this answer is done.
		return m, ChatAction{}
	}
	tr := &ChatTurn{User: text, SentAt: t, Answer: Stream{Slot: -1, StartedAt: t}}
	m.Items = append(append([]ChatItem(nil), m.Items...), ChatItem{Turn: tr})
	m.Input = ChatInput{}
	m.Phase = ChatPrefill
	m.Scroll = 0
	return m, ChatAction{Kind: ActSend, Text: text}
}
