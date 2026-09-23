//go:build !js

package tui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// ChatOptions configure the chat screen.
type ChatOptions struct {
	// Events is the session side's reports. RunChat keeps reading until the
	// user leaves; a closed channel only stops the reading.
	Events <-chan ChatEvent
	// Out and In default to the terminal via bubbletea; the tests set In.
	Out io.Writer
	In  io.Reader
	// Width and Height override the terminal size. Zero asks the terminal;
	// before it answers the screen assumes ChatMinWidth×ChatMinHeight.
	Width, Height int
	Colour        bool
	// Start is the origin of clip time, the instant the session side stamps
	// its events from, so a token and the key that asked for it are on one
	// clock. Zero is the program's own start.
	Start time.Time
	// Send is called on Enter with the message to send; Cancel on ctrl+c
	// while a turn is in flight. Neither may block: the session side runs the
	// turn on a goroutine of its own and reports back through Events.
	Send   func(text string)
	Cancel func()
}

// RunChat drives the chat screen until the user leaves (/exit, /quit, ctrl+c
// on an idle prompt, ctrl+d on an empty one) or ctx is cancelled.
//
// Like Run it is the only place the chat reads the wall clock, once per tick
// and once per key, to turn it into the clip time ChatView and HandleKey take.
func RunChat(ctx context.Context, opts ChatOptions) error {
	th := PlainTheme()
	if opts.Colour {
		th = ColourTheme()
	}
	p := &chatProgram{model: NewChatModel(th), opts: opts, events: opts.Events, w: opts.Width, h: opts.Height, start: opts.Start}
	teaOpts := []tea.ProgramOption{tea.WithContext(ctx), tea.WithAltScreen()}
	if opts.Out != nil {
		teaOpts = append(teaOpts, tea.WithOutput(opts.Out))
	}
	if opts.In != nil {
		teaOpts = append(teaOpts, tea.WithInput(opts.In))
	}
	if _, err := tea.NewProgram(p, teaOpts...).Run(); err != nil {
		if ctx.Err() != nil && errors.Is(err, tea.ErrProgramKilled) {
			return nil
		}
		return fmt.Errorf("tui: %w", err)
	}
	return nil
}

type chatEventMsg struct {
	ev     ChatEvent
	closed bool
}

// chatProgram is the bubbletea adapter: the clock, the keys and the channel,
// and nothing that decides.
type chatProgram struct {
	model  ChatModel
	opts   ChatOptions
	events <-chan ChatEvent
	start  time.Time
	t      time.Duration
	w, h   int
	quit   bool
}

func (p *chatProgram) Init() tea.Cmd {
	if p.start.IsZero() {
		p.start = time.Now()
	}
	return tea.Batch(tickCmd(), p.waitEvent())
}

func (p *chatProgram) waitEvent() tea.Cmd {
	if p.events == nil {
		return nil
	}
	ch := p.events
	return func() tea.Msg {
		ev, ok := <-ch
		if !ok {
			return chatEventMsg{closed: true}
		}
		return chatEventMsg{ev: ev}
	}
}

func (p *chatProgram) size() (int, int) {
	w, h := p.w, p.h
	if w <= 0 {
		w = ChatMinWidth
	}
	if h <= 0 {
		h = ChatMinHeight
	}
	return w, h
}

// chatKeyOf turns bubbletea's key into the screen's.
//
// Typed text travels as runes with no name. bubbletea names a multi-rune
// message by its text — an IME commit of "다음", or three letters typed in
// one read — and a burst that happened to spell "end" or "home" must insert
// those letters, not move the cursor.
func chatKeyOf(k tea.KeyMsg) ChatKey {
	switch k.Type {
	case tea.KeySpace:
		return ChatKey{Runes: []rune{' '}}
	case tea.KeyRunes:
		if k.Alt {
			return ChatKey{Name: k.String()}
		}
		name := ""
		if len(k.Runes) == 1 && !k.Paste {
			name = string(k.Runes)
		}
		return ChatKey{Name: name, Runes: k.Runes, Paste: k.Paste}
	}
	return ChatKey{Name: k.String(), Paste: k.Paste}
}

func (p *chatProgram) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		p.w, p.h = msg.Width, msg.Height
		return p, nil
	case tea.KeyMsg:
		t := time.Since(p.start)
		w, h := p.size()
		var act ChatAction
		p.model, act = p.model.HandleKey(chatKeyOf(msg), t, w, h)
		switch act.Kind {
		case ActSend:
			if p.opts.Send != nil {
				p.opts.Send(act.Text)
			}
		case ActCancel:
			if p.opts.Cancel != nil {
				p.opts.Cancel()
			}
		case ActExit:
			p.quit = true
			return p, tea.Quit
		}
		return p, nil
	case tickMsg:
		p.t = time.Since(p.start)
		return p, tickCmd()
	case chatEventMsg:
		if msg.closed {
			p.events = nil
			return p, nil
		}
		ev := msg.ev
		if ev.T == 0 {
			ev.T = time.Since(p.start)
		}
		p.model = p.model.Apply(ev)
		return p, p.waitEvent()
	}
	return p, nil
}

func (p *chatProgram) View() string {
	if p.quit {
		return ""
	}
	w, h := p.size()
	return ChatView(p.model, p.t, w, h)
}
