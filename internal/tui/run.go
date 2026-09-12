package tui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// Options configure the live screen.
type Options struct {
	// Events is the recorder's stream of observations. Run returns when it is
	// closed, when the context is cancelled, or when the user quits.
	Events <-chan Event
	// Out is where frames are written. Defaults to os.Stdout via bubbletea.
	Out io.Writer
	// In is the input source. Defaults to os.Stdin via bubbletea.
	In io.Reader
	// Width and Height override the terminal size. Zero means "ask the
	// terminal"; the tests and the headless dump set them.
	Width, Height int
	// Colour turns the palette on. Off renders the same layout with no escape
	// sequences, which is what the golden tests compare.
	Colour bool
}

// Run drives the live screen until the context is cancelled, the event channel
// closes or the user presses q.
//
// It is the only function in this package that reads the wall clock, and it
// reads it for one purpose: to turn elapsed real time into the clip time t
// that View animates from. Everything else downstream is a pure function of
// that number and of the model the events built.
func Run(ctx context.Context, opts Options) error {
	th := PlainTheme()
	if opts.Colour {
		th = ColourTheme()
	}
	p := &program{
		model:  Model{Theme: th, Mode: ModeLive},
		events: opts.Events,
		w:      opts.Width,
		h:      opts.Height,
	}
	teaOpts := []tea.ProgramOption{tea.WithContext(ctx), tea.WithAltScreen()}
	if opts.Out != nil {
		teaOpts = append(teaOpts, tea.WithOutput(opts.Out))
	}
	if opts.In != nil {
		teaOpts = append(teaOpts, tea.WithInput(opts.In))
	}
	if _, err := tea.NewProgram(p, teaOpts...).Run(); err != nil {
		// Cancelling the context is how the recorder asks the screen to close,
		// so bubbletea's "program was killed" is the success path, not a
		// failure the caller should print.
		if ctx.Err() != nil && errors.Is(err, tea.ErrProgramKilled) {
			return nil
		}
		return fmt.Errorf("tui: %w", err)
	}
	return nil
}

// tickMsg advances the clip time and carries nothing else. The redraw tick
// changes no state: if it did, a replay at a different frame rate would draw a
// different screen.
type tickMsg time.Time

// eventMsg wraps one recorder observation. A closed channel arrives as
// eventMsg{closed: true}.
type eventMsg struct {
	ev     Event
	closed bool
}

// program is the bubbletea adapter. It owns the wall clock and nothing else;
// all rendering goes through the package's pure View.
type program struct {
	model  Model
	events <-chan Event
	start  time.Time
	t      time.Duration
	w, h   int
	quit   bool
}

func (p *program) Init() tea.Cmd {
	p.start = time.Now()
	return tea.Batch(tickCmd(), p.waitEvent())
}

func tickCmd() tea.Cmd {
	return tea.Tick(TickInterval, func(t time.Time) tea.Msg { return tickMsg(t) })
}

// waitEvent blocks on the recorder channel in bubbletea's own goroutine pool,
// so a slow recorder never stalls a redraw.
func (p *program) waitEvent() tea.Cmd {
	if p.events == nil {
		return nil
	}
	ch := p.events
	return func() tea.Msg {
		ev, ok := <-ch
		if !ok {
			return eventMsg{closed: true}
		}
		return eventMsg{ev: ev}
	}
}

func (p *program) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		p.w, p.h = msg.Width, msg.Height
		return p, nil
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c", "esc":
			p.quit = true
			return p, tea.Quit
		case "p":
			if p.model.Mode == ModePrompt {
				p.model.Mode = ModeLive
			} else {
				p.model.Mode = ModePrompt
			}
		case "c":
			switch {
			case p.model.Mode == ModeCard:
				p.model.Mode = ModeLive
			case p.model.Done:
				p.model.Mode = ModeCard
			}
		}
		return p, nil
	case tickMsg:
		p.t = time.Since(p.start)
		return p, tickCmd()
	case eventMsg:
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

func (p *program) View() string {
	if p.quit {
		return ""
	}
	w, h := p.w, p.h
	if w <= 0 {
		w = MinWidth
	}
	if h <= 0 {
		h = MinHeight
	}
	return View(p.model, p.t, w, h)
}
