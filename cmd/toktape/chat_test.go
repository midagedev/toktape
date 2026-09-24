package main

import (
	"context"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/midagedev/toktape/internal/recorder"
	"github.com/midagedev/toktape/internal/tape"
	"github.com/midagedev/toktape/internal/tui"
)

// fakeTurn is one scripted answer: the tokens it streams, whether it then
// hangs until its context is cancelled, or the error Send returns instead.
type fakeTurn struct {
	tokens []string
	hang   bool
	err    error
}

// fakeChat is a chatSession that streams on a script through the same
// Progress callback the recorder calls.
type fakeChat struct {
	mu        sync.Mutex
	progress  func(recorder.Event)
	script    []fakeTurn
	sent      []string
	returned  int
	cancelled int
	closes    int
}

func (f *fakeChat) Send(ctx context.Context, text string) (tape.RequestRecord, error) {
	f.mu.Lock()
	i := len(f.sent)
	f.sent = append(f.sent, text)
	sc := f.script[min(i, len(f.script)-1)]
	f.mu.Unlock()
	defer func() {
		f.mu.Lock()
		f.returned++
		f.mu.Unlock()
	}()
	if sc.err != nil {
		return tape.RequestRecord{}, sc.err
	}
	f.progress(recorder.Event{Kind: recorder.EventStreamStarted, Stream: 0, Round: i, MaxTokens: 256})
	var rec tape.RequestRecord
	for k, tok := range sc.tokens {
		ev := tape.TokenEvent{T: time.Duration(100+10*k) * time.Millisecond, Index: k, Text: tok}
		rec.Tokens = append(rec.Tokens, ev)
		f.progress(recorder.Event{Kind: recorder.EventToken, Stream: 0, Round: i, Token: ev})
	}
	if sc.hang {
		<-ctx.Done()
		f.mu.Lock()
		f.cancelled++
		f.mu.Unlock()
		return rec, ctx.Err()
	}
	rec.Timings = tape.TimingsSummary{PredictedN: len(sc.tokens), PredictedPerSecond: 40, TTFTMs: 100}
	return rec, nil
}

func (f *fakeChat) History() []tape.Message {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []tape.Message
	for _, s := range f.sent {
		out = append(out, tape.Message{Role: "user", Content: s})
	}
	return out
}

func (f *fakeChat) Close() (*tape.Tape, error) {
	f.mu.Lock()
	f.closes++
	asked := len(f.sent)
	f.mu.Unlock()
	if asked == 0 {
		// As the session does: no turn, no tape.
		return nil, recorder.ErrAllStreamsFailed
	}
	tp := tui.ExampleTapeN(1)
	tp.Summary.ID = "20260924-120000-fake-chat"
	tp.Summary.Mode = tape.ModeChat
	return tp, nil
}

func (f *fakeChat) counts() (sent []string, returned, cancelled, closes int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.sent...), f.returned, f.cancelled, f.closes
}

// chatHarness runs chatTUI against a fake session with its keys on a pipe.
type chatHarness struct {
	t      *testing.T
	fake   *fakeChat
	typist *io.PipeWriter
	stdout *syncBuffer
	stderr *syncBuffer
	done   chan int
	dir    string
}

func startChat(t *testing.T, script []fakeTurn) *chatHarness {
	t.Helper()
	h := &chatHarness{t: t, fake: &fakeChat{script: script}, stdout: &syncBuffer{}, stderr: &syncBuffer{}, done: make(chan int, 1), dir: t.TempDir()}
	prevOpen := openChat
	openChat = func(ctx context.Context, opts recorder.Options) (chatSession, error) {
		h.fake.progress = opts.Progress
		sum := tui.ExampleTapeN(1).Summary
		opts.Progress(recorder.Event{Kind: recorder.EventAttached, Stream: -1, Summary: &sum})
		return h.fake, nil
	}
	keys, typist := io.Pipe()
	prevIn := tuiInput
	tuiInput = keys
	h.typist = typist
	t.Cleanup(func() {
		openChat, tuiInput = prevOpen, prevIn
		_ = typist.Close()
	})
	cfg := recordConfig{outDir: h.dir, card: true}
	go func() {
		h.done <- chatTUI(context.Background(), &cli{stdout: h.stdout, stderr: h.stderr}, recorder.Options{}, cfg)
	}()
	h.waitFor("the session to open", func() bool { return strings.Contains(h.stdout.String(), "type a message") })
	return h
}

// metaCount is how many finished-turn figure lines the screen has drawn. A
// fake turn can start and end between two renders, so the footer is no
// witness; a finished turn's figure line is a new line and is always drawn.
func (h *chatHarness) metaCount() int { return strings.Count(h.stdout.String(), " · ttft 100 ms") }

func (h *chatHarness) waitFor(what string, cond func() bool) {
	h.t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			out := h.stdout.String()
			sent, returned, cancelled, closes := h.fake.counts()
			h.t.Fatalf("timed out waiting for %s (sent %q, returned %d, cancelled %d, closes %d); stdout tail:\n%q",
				what, sent, returned, cancelled, closes, out[max(0, len(out)-1500):])
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// waitTurns waits for the screen to have drawn more than n finished turns.
func (h *chatHarness) waitTurns(n int) int {
	h.t.Helper()
	h.waitFor("a finished turn on screen", func() bool { return h.metaCount() > n })
	return h.metaCount()
}

func (h *chatHarness) typeKeys(s string) {
	h.t.Helper()
	if _, err := h.typist.Write([]byte(s)); err != nil {
		h.t.Fatalf("typing %q: %v", s, err)
	}
}

func (h *chatHarness) exit() int {
	h.t.Helper()
	select {
	case code := <-h.done:
		return code
	case <-time.After(20 * time.Second):
		h.t.Fatal("the chat screen did not close")
	}
	return -1
}

// TestChatTwoTurnsThenExit: two turns stream, /exit closes the session once,
// and the card lands in the scrollback with the chat's closing lines and no
// share hint. The first message is Korean, typed and backspaced.
func TestChatTwoTurnsThenExit(t *testing.T) {
	h := startChat(t, []fakeTurn{{tokens: []string{"Hello", ",", " there"}}, {tokens: []string{"Second", " answer"}}})
	h.typeKeys("안녕하세요")
	h.typeKeys("\x7f")
	h.typeKeys("\r")
	seen := h.waitTurns(0)
	// alt+enter as a terminal sends it, ESC CR: a new line, not a send.
	h.typeKeys("again\x1b\rand")
	h.typeKeys("\r")
	h.waitTurns(seen)
	h.typeKeys("/exit\r")

	if code := h.exit(); code != exitOK {
		t.Fatalf("exit %d\n%s", code, h.stderr.String())
	}
	sent, _, _, closes := h.fake.counts()
	if len(sent) != 2 || sent[0] != "안녕하세" || sent[1] != "again\nand" {
		t.Errorf("sent %q, want [안녕하세 again\\nand]", sent)
	}
	if closes != 1 {
		t.Errorf("Close called %d times, want 1", closes)
	}
	// The card is printed after the screen is gone: it follows the last
	// frame on stdout.
	out := h.stdout.String()
	if i := strings.LastIndex(out, "Tokens"); i < 0 && !strings.Contains(out, "tok/s") {
		t.Errorf("no card on stdout after the screen closed")
	}
	stderr := h.stderr.String()
	if !strings.Contains(stderr, "✓ Tape   ") || !strings.Contains(stderr, "stays on this machine") {
		t.Errorf("closing lines missing:\n%s", stderr)
	}
	for _, banned := range []string{"Markdown", "Replay", "publish ", "Compare"} {
		if strings.Contains(stderr, banned) {
			t.Errorf("a chat tape was offered a share line (%q):\n%s", banned, stderr)
		}
	}
	if tapes, _ := filepath.Glob(filepath.Join(h.dir, "*"+tape.Ext)); len(tapes) != 1 {
		t.Errorf("tapes written: %v, want one", tapes)
	}
}

// TestChatCtrlCStopsOnlyTheTurn: ctrl+c while an answer streams cancels that
// Send's context and the session goes on; ctrl+c on the idle prompt leaves.
func TestChatCtrlCStopsOnlyTheTurn(t *testing.T) {
	h := startChat(t, []fakeTurn{{tokens: []string{"a", " long", " answer"}, hang: true}, {tokens: []string{"ok"}}})
	h.typeKeys("tell me everything\r")
	h.waitFor("the turn to stream", func() bool { return strings.Contains(h.stdout.String(), "answering") })
	h.typeKeys("\x03")
	h.waitFor("the turn to be cancelled", func() bool { _, _, c, _ := h.fake.counts(); return c == 1 })
	h.waitFor("the stopped mark", func() bool { return strings.Contains(h.stdout.String(), "stopped") })

	h.typeKeys("short then\r")
	h.waitTurns(0)
	h.typeKeys("\x03")
	if code := h.exit(); code != exitOK {
		t.Fatalf("exit %d\n%s", code, h.stderr.String())
	}
	sent, _, cancelled, closes := h.fake.counts()
	if len(sent) != 2 || cancelled != 1 || closes != 1 {
		t.Errorf("sent %q, cancelled %d, closes %d; want 2 sends, 1 cancel, 1 close", sent, cancelled, closes)
	}
}

// TestChatContextFullLeavesTheNotice: ErrContextFull is a calm notice in the
// transcript, and /exit still saves.
func TestChatContextFullLeavesTheNotice(t *testing.T) {
	h := startChat(t, []fakeTurn{{tokens: []string{"fine"}}, {err: recorder.ErrContextFull}})
	h.typeKeys("first\r")
	h.waitTurns(0)
	h.typeKeys("second\r")
	h.waitFor("the context-full notice", func() bool {
		return strings.Contains(h.stdout.String(), "the conversation is full")
	})
	h.typeKeys("/exit\r")
	if code := h.exit(); code != exitOK {
		t.Fatalf("exit %d\n%s", code, h.stderr.String())
	}
	if _, _, _, closes := h.fake.counts(); closes != 1 {
		t.Errorf("Close called %d times, want 1", closes)
	}
}

// TestChatUnknownCommandHints: an unknown command answers with the two that
// exist, sends nothing, and ctrl+d on the empty line leaves.
func TestChatUnknownCommandHints(t *testing.T) {
	h := startChat(t, []fakeTurn{{tokens: []string{"x"}}})
	h.typeKeys("/model\r")
	h.waitFor("the hint", func() bool { return strings.Contains(h.stdout.String(), "no command /model") })
	h.typeKeys("\x04")
	if code := h.exit(); code != exitOK {
		t.Fatalf("exit %d\n%s", code, h.stderr.String())
	}
	sent, _, _, closes := h.fake.counts()
	if len(sent) != 0 {
		t.Errorf("a command was sent as a turn: %q", sent)
	}
	// Nothing was asked, so there is nothing to save — and it is not an error.
	if closes != 1 || !strings.Contains(h.stderr.String(), "nothing was recorded") || strings.Contains(h.stderr.String(), "✓ Tape") {
		t.Errorf("closes %d, stderr:\n%s", closes, h.stderr.String())
	}
	if tapes, _ := filepath.Glob(filepath.Join(h.dir, "*"+tape.Ext)); len(tapes) != 0 {
		t.Errorf("a session with no turn wrote %v", tapes)
	}
}

// TestChatRefusesRecordOnlyFlags: --sessions and the other record-only flags
// are refused with one line saying why, before anything is contacted.
func TestChatRefusesRecordOnlyFlags(t *testing.T) {
	opened := false
	prev, prevTTY := openChat, chatNeedsTerminal
	openChat = func(context.Context, recorder.Options) (chatSession, error) { opened = true; return nil, nil }
	// Past the terminal check, so it is the refusal and not the pipe that
	// keeps the session closed.
	chatNeedsTerminal = false
	t.Cleanup(func() { openChat, chatNeedsTerminal = prev, prevTTY })
	for _, args := range [][]string{
		{"chat", "--sessions", "2"},
		{"chat", "--prompt", "hi"},
		{"chat", "--for", "10s"},
		{"chat", "--endpoint", "completion"},
	} {
		code, _, stderr := exec(t, args...)
		if code != exitUsage {
			t.Errorf("%v: exit %d, want %d", args, code, exitUsage)
		}
		if n := strings.Count(strings.TrimSpace(stderr), "\n"); n != 0 || !strings.HasPrefix(stderr, "toktape chat: ") {
			t.Errorf("%v: want one line, got:\n%s", args, stderr)
		}
	}
	if _, _, stderr := exec(t, "chat", "--sessions", "2"); !strings.Contains(stderr, "one stream") {
		t.Errorf("--sessions refusal does not say why: %s", stderr)
	}
	if opened {
		t.Error("a refused invocation opened a session")
	}
}

// TestChatNeedsATerminal: a chat with stdout on a pipe says so instead of
// drawing a screen into it.
func TestChatNeedsATerminal(t *testing.T) {
	code, _, stderr := exec(t, "chat")
	if code != exitUsage || !strings.Contains(stderr, "needs a terminal") {
		t.Errorf("exit %d: %s", code, stderr)
	}
}

// TestChatUsagePublishRule: the help says what publish does with a chat tape
// — it refuses one unless --include-conversation is given — not only that
// nothing leaves the machine by itself.
func TestChatUsagePublishRule(t *testing.T) {
	if !strings.Contains(chatUsage, "--include-conversation") {
		t.Errorf("chat usage does not name --include-conversation:\n%s", chatUsage)
	}
}
