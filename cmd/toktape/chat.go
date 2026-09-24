package main

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/midagedev/toktape/internal/recorder"
	"github.com/midagedev/toktape/internal/tape"
	"github.com/midagedev/toktape/internal/tui"
)

// `toktape chat` (TTP-184, 2026-09-24): talk to the server you already run,
// the way a chat app does, and get the tape of it.
//
// It is record cut at the turn: the recorder's Session attaches and probes
// once, sends one turn per message the user types, and reduces the turns into
// one tape when the user leaves. This file is the verb and the bridge; the
// screen is internal/tui (chat*.go), the session is internal/recorder
// (session.go).

const chatUsage = `toktape chat — talk to the server, every answer timed and saved

Usage:
  toktape chat [flags]

  The server is found the way toktape record finds it. Type and press enter;
  each answer streams with its figures beside it. /exit (or /quit, ctrl+d on
  an empty line, ctrl+c when no answer is streaming) saves the session as one
  tape and prints its card.

Keys:
  enter        send                 alt+enter    new line
  ctrl+c       stop the answer that is streaming; leave when none is
  ctrl+d       leave, on an empty line
  pgup pgdn    scroll the conversation
  /help        the keys             /exit /quit  save and leave

Flags (as record's):
  --url URL             server to attach to (default: discover)
  --model ID            model to request, when the server hosts more than one
  --engine-kind NAME    auto (default), llama or openai
  --engine TEXT         name the engine on an OpenAI-compatible server (a claim)
  -n, --n-predict N     cap each answer at N tokens
  --temp N              sampling temperature (unset = the server's default)
  --no-think            ask a reasoning model not to think
  --think-budget N      cap a reasoning model's thinking at N tokens
  --param key=value     extra request parameter, repeatable
  --wait DURATION       how long to wait for a loading model (default 10m)
  --out DIR             where the tape is written (default ~/.toktape/runs)
  --tag TEXT, --note TEXT  label the session for the experiment log
  --host-label TEXT     store TEXT as the hostname
  --no-card             do not render or save the card
  -o, --output FORMAT   json, jsonl, md, csv, tsv or sql instead of the card
  --quiet               no closing lines on stderr

  A chat tape holds the conversation's text. It stays on this machine;
  toktape publish refuses it unless you add --include-conversation.
`

// chatSession is what the verb needs of a recorder.Session. It is an
// interface so the tests drive the verb and the screen with a fake that
// streams on a script, without a server.
type chatSession interface {
	Send(ctx context.Context, text string) (tape.RequestRecord, error)
	History() []tape.Message
	Close() (*tape.Tape, error)
}

// openChat opens the session. A package var for the same reason pinCollectors
// is one: the tests replace it.
var openChat = func(ctx context.Context, opts recorder.Options) (chatSession, error) {
	s, err := recorder.Open(ctx, opts)
	if err != nil {
		return nil, err
	}
	return s, nil
}

// chatNeedsTerminal is whether chat refuses a stdout that is not a terminal.
// The tests turn it off to run the verb through a pipe.
var chatNeedsTerminal = true

// chatRefusedFlags are record's flags that mean nothing in a chat, each with
// the one line that says why. A chat is one conversation: one stream, the
// prompts are what the user types, and a turn ends when its answer does.
var chatRefusedFlags = []struct{ name, why string }{
	{"sessions", "a chat is one conversation, one stream at a time; streams at once are toktape record --sessions N"},
	{"max-sessions", "a chat is one stream; the ceiling belongs to toktape record --sessions"},
	{"prompt", "a chat's prompts are what you type; a fixed prompt is toktape record --prompt"},
	{"prompts", "a chat's prompts are what you type; a prompts file is toktape record --prompts"},
	{"spec-n-max", "a sweep repeats one prompt set, and a chat never repeats; use toktape record --spec-n-max"},
	{"for", "a chat turn ends when its answer does; the clock is toktape record's"},
	{"tui", "chat is always on the screen"},
	{"grid", "chat has one conversation, not a grid of tiles"},
}

// runChat is the chat verb: parse, refuse what does not apply, open the
// screen.
func runChat(ctx context.Context, c *cli, args []string) int {
	fs := newFlagSet("chat")
	f := declareRecordFlags(fs)
	extra, err := parseArgs(fs, args)
	if err != nil {
		return c.badFlags("chat", chatUsage, fs, args, err)
	}
	format, refused := outputFor("chat", *f.output)
	c.json = format.isJSON()
	if refused != nil {
		return c.fail(*refused)
	}
	if len(extra) > 0 {
		return c.usagef("toktape chat: unexpected argument %q", extra[0])
	}
	for _, r := range chatRefusedFlags {
		if flagSet(fs, r.name) {
			return c.usagef("toktape chat: --%s: %s", r.name, r.why)
		}
	}

	sampling, err := samplingOptions(samplingFlags{
		endpoint:    *f.endpoint,
		tempSet:     flagSet(fs, "temp"),
		temp:        *f.temp,
		noThink:     *f.noThink,
		budgetSet:   flagSet(fs, "think-budget"),
		thinkBudget: *f.thinkBudget,
		params:      *f.params,
	})
	if err != nil {
		return c.usagef("toktape chat: %v", err)
	}
	if sampling.endpoint == tape.EndpointCompletion {
		return c.usagef("toktape chat: --endpoint completion: a chat goes through the server's chat template; the raw path is toktape record --endpoint completion")
	}
	for _, w := range sampling.warnings {
		fmt.Fprintf(c.stderr, "toktape chat: %s\n", w)
	}
	switch *f.engineKind {
	case "auto", "llama", "openai":
	default:
		return c.usagef("toktape chat: --engine-kind %s: use auto, llama or openai", *f.engineKind)
	}
	if *f.engine != "" && *f.engineKind == "llama" {
		return c.usagef("toktape chat: --engine names the engine, but a llama-server names itself; use it with --engine-kind openai (or auto)")
	}
	hostRAM, err := hostRAMOverride(fs, ramFlags{
		gbs:         *f.ramGBs,
		gbsMeasured: *f.ramGBsMeasured,
		speed:       *f.ramSpeed,
		channels:    *f.ramChannels,
	})
	if err != nil {
		return c.usagef("toktape chat: %v", err)
	}

	opts := recorder.Options{
		BaseURL:     *f.url,
		Concurrency: 1,
		// A chat answer is not cut by a benchmark clock: the turn ends when
		// the model stops, or at --n-predict when one was named.
		For:          recorder.NoClock,
		MaxTokens:    *f.nPredict,
		Params:       sampling.params,
		Endpoint:     sampling.endpoint,
		EngineKind:   *f.engineKind,
		EngineClaim:  *f.engine,
		Model:        *f.model,
		Version:      version,
		WaitForModel: waitBudget(fs, *f.wait),
		WaitForStart: flagSet(fs, "wait") && *f.wait > 0,
		HostRAM:      hostRAM,
		HostLabel:    recorder.HostLabel{Set: flagSet(fs, "host-label"), Text: *f.hostLabel},
	}
	opts = pinCollectors(opts)
	recordLabels = runLabels{tag: *f.tag, note: *f.note}
	cfg := recordConfig{outDir: *f.outDir, card: !*f.noCard, format: format, quiet: *f.quiet}

	if chatNeedsTerminal && !isTTY(c.stdout) {
		return c.fail(failure{
			code: exitUsage,
			msg:  "toktape chat: needs a terminal on stdout; a chat is typed on the screen",
			hint: "to record fixed prompts from a script, use toktape record --prompt TEXT",
		})
	}
	return chatTUI(ctx, c, opts, cfg)
}

// chatTUI runs one session under the chat screen and, once the screen is
// gone, saves the tape and prints the card into the scrollback.
//
// Three goroutines meet here: the screen's, Open's, and one per turn. The
// screen never waits on the session — Enter hands the text to a turn goroutine
// and returns — and the session never waits on the screen: an observation
// that does not fit the queue is dropped, as on the record screen, because a
// token held up on its way to the screen would be a token timed late. The
// reports the screen cannot do without (the session opened, a turn ended, the
// context is full) are sent so that they cannot be dropped, and give up only
// when the screen has already gone.
func chatTUI(ctx context.Context, c *cli, opts recorder.Options, cfg recordConfig) int {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	events := make(chan tui.ChatEvent, tuiQueue)
	screenGone := make(chan struct{})
	start := time.Now()
	observe := func(e tui.ChatEvent) {
		select {
		case events <- e:
		default:
		}
	}
	report := func(e tui.ChatEvent) {
		e.T = time.Since(start)
		select {
		case events <- e:
		case <-screenGone:
		}
	}
	opts.Progress = func(ev recorder.Event) {
		if e, ok := bridgeEvent(ev, time.Since(start)); ok {
			observe(tui.ChatEvent{Kind: tui.ChatObserved, T: e.T, Event: e})
			return
		}
		// What the recorder says while it opens — the prefill probe's note,
		// a loading server's wait — is the only news the measuring screen
		// has. The screen keeps it while measuring and drops it after, so a
		// later warning cannot land under a spinner that is gone.
		if ev.Message != "" {
			observe(tui.ChatEvent{Kind: tui.ChatStatus, T: time.Since(start), Note: ev.Message})
		}
	}

	var (
		mu       sync.Mutex
		sess     chatSession
		openErr  error
		turnStop context.CancelFunc
		stopped  bool
		turnDone chan struct{}
		// attempted counts the turns handed to Send, which is what tells
		// "left without asking anything" from "every turn failed" when Close
		// says no turn was answered.
		attempted int
	)
	opened := make(chan struct{})
	go func() {
		defer close(opened)
		s, err := openChat(ctx, opts)
		mu.Lock()
		sess, openErr = s, err
		mu.Unlock()
		if err != nil {
			report(tui.ChatEvent{Kind: tui.ChatOpenFailed, Err: err})
			return
		}
		report(tui.ChatEvent{Kind: tui.ChatOpened})
	}()

	sendTurn := func(text string) {
		mu.Lock()
		s := sess
		if s == nil || turnDone != nil {
			mu.Unlock()
			return
		}
		tctx, stop := context.WithCancel(ctx)
		done := make(chan struct{})
		turnStop, stopped, turnDone = stop, false, done
		attempted++
		mu.Unlock()
		go func() {
			defer close(done)
			rec, err := s.Send(tctx, text)
			stop()
			mu.Lock()
			// Stopped by ctrl+c, not merely asked to be: a turn that finished
			// on its own a moment before the key is an answer, not a stop.
			cancelled := stopped && errors.Is(err, context.Canceled)
			turnStop, turnDone = nil, nil
			mu.Unlock()
			if errors.Is(err, recorder.ErrContextFull) {
				report(tui.ChatEvent{Kind: tui.ChatContextFull})
				return
			}
			ev := tui.ChatEvent{Kind: tui.ChatTurnDone, Err: err, Cancelled: cancelled}
			if err == nil || cancelled || len(rec.Tokens) > 0 {
				r := rec
				ev.Record = &r
			}
			if cancelled {
				// The partial answer is kept, as the recorder keeps it; the
				// cancellation is the user's, not a failure.
				ev.Err = nil
			}
			report(ev)
		}()
	}
	cancelTurn := func() {
		mu.Lock()
		defer mu.Unlock()
		if turnStop != nil {
			stopped = true
			turnStop()
		}
	}

	screenErr := tui.RunChat(ctx, tui.ChatOptions{
		Events: events, Out: c.stdout, In: tuiInput, Colour: true, Start: start,
		Send: sendTurn, Cancel: cancelTurn,
	})
	close(screenGone)

	// The screen is gone. End a turn still in flight — the user left in the
	// middle of it — and wait for the recorder to hand it back, so Close
	// reduces a session with no request outstanding.
	cancelTurn()
	mu.Lock()
	inflight := turnDone
	mu.Unlock()
	if inflight != nil {
		<-inflight
	}
	select {
	case <-opened:
	default:
		// Left while the session was still opening: stop the probe.
		cancel()
		<-opened
	}
	if screenErr != nil {
		fmt.Fprintf(c.stderr, "toktape: %v\n", screenErr)
	}

	mu.Lock()
	s, oerr := sess, openErr
	mu.Unlock()
	if s == nil {
		if oerr != nil && !errors.Is(oerr, context.Canceled) {
			return c.reportRecordError(oerr, opts.BaseURL != "")
		}
		fmt.Fprintln(c.stderr, "toktape chat: left before the session opened; nothing was recorded")
		return exitOK
	}

	tp, err := s.Close()
	if err != nil {
		mu.Lock()
		asked := attempted
		mu.Unlock()
		if errors.Is(err, recorder.ErrAllStreamsFailed) && asked == 0 {
			fmt.Fprintln(c.stderr, "toktape chat: nothing was asked, so nothing was recorded")
			return exitOK
		}
		return c.reportRecordError(err, opts.BaseURL != "")
	}
	arts, saveErr := saveRun(cfg.outDir, tp, cfg.card)
	if saveErr != nil {
		if arts.tape == "" {
			return c.usagef("toktape: %v", saveErr)
		}
		fmt.Fprintf(c.stderr, "toktape: %v\n", saveErr)
	}
	// The screen is gone by now, so the card lands in the scrollback where
	// the conversation was.
	if code := printCard(c, tp, cfg); code != exitOK {
		return code
	}
	if !cfg.quiet {
		fmt.Fprint(c.stderr, chatSavedNote(arts))
	}
	return exitOK
}

// chatSavedNote is what a chat ends with, in place of record's share block.
//
// There is no Markdown line and no publish line: a chat tape carries the
// user's own words, and the one thing its closing lines owe them is where it
// went and that it went nowhere else.
func chatSavedNote(a artifacts) string {
	var out string
	if a.tape != "" {
		out += fmt.Sprintf("✓ Tape   %s\n", tildePath(a.tape))
	}
	if a.cardText != "" {
		out += fmt.Sprintf("✓ Card   %s\n", tildePath(a.cardText))
	}
	if a.tape != "" {
		out += "· the tape holds the conversation's text and stays on this machine; nothing is published\n"
	}
	return out
}
