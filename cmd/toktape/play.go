package main

import (
	"context"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/midagedev/toktape/internal/tape"
	"github.com/midagedev/toktape/internal/tui"
)

// runPlay replays a recorded run on the live screen.
//
// This is the "watch someone else's tape" verb, and it is what decision 10 of
// the design buys: the screen is a pure function of the run and a clip time,
// so a tape from another machine reproduces the frames its owner watched
// rather than a summary of them.
func runPlay(ctx context.Context, c *cli, args []string) int {
	fs := newFlagSet("play")
	speed := fs.Float64("speed", 1, "replay speed multiplier (4 plays four times as fast)")
	grid := fs.String("grid", tui.DefaultGrid.String(), "tile grid per page as COLSxROWS (0 = fit to the terminal)")
	files, err := parseArgs(fs, args)
	if err != nil {
		return c.badFlags("play", usageFor("play"), fs, args, err)
	}
	if len(files) != 1 {
		return c.usageTextf(usageText, "toktape play: expected one tape file")
	}
	if *speed <= 0 {
		return c.usagef("toktape play: --speed must be positive, got %v", *speed)
	}
	parsedGrid, err := tui.ParseGrid(*grid)
	if err != nil {
		return c.usagef("toktape play: --%v", err)
	}
	tp, err := tape.Read(files[0])
	if err != nil {
		return c.usagef("toktape: %v", err)
	}
	if !isTTY(c.stdout) {
		return c.fail(failure{
			code: exitUsage,
			msg:  "toktape play: needs a terminal",
			hint: "use `toktape card <tape>` to print the result instead",
		})
	}

	p := &player{
		tp:      tp,
		speed:   *speed,
		grid:    parsedGrid,
		theme:   tui.ColourTheme(),
		path:    tildePath(files[0]),
		end:     tapeDuration(tp),
		playing: true,
	}
	if _, err := tea.NewProgram(p, tea.WithContext(ctx), tea.WithAltScreen(), tea.WithOutput(c.stdout)).Run(); err != nil {
		if ctx.Err() != nil {
			return exitOK
		}
		return c.usagef("toktape: %v", err)
	}
	return exitOK
}

// playModel is the state of tp after wall seconds of a replay at speed.
//
// The whole replay is this one line: clip time is wall time times the speed,
// and tui.ModelAt cuts the tape there. Nothing accumulates, so seeking, pausing
// and a different frame rate all fall out of it for free — and a frame is
// reproducible from (tape, wall, speed) alone.
func playModel(tp *tape.Tape, wall time.Duration, speed float64) (tui.Model, time.Duration) {
	clip := clipTime(wall, speed)
	return tui.ModelAt(tp, clip), clip
}

// clipTime scales wall time by the replay speed.
func clipTime(wall time.Duration, speed float64) time.Duration {
	if speed <= 0 {
		speed = 1
	}
	if wall < 0 {
		wall = 0
	}
	return time.Duration(float64(wall) * speed)
}

// tapeDuration is the clip time of the last token in tp, which is when a
// replay has nothing left to show.
func tapeDuration(tp *tape.Tape) time.Duration {
	var end time.Duration
	if tp == nil {
		return end
	}
	for _, req := range tp.Requests {
		if n := len(req.Tokens); n > 0 {
			if t := req.StartedAt + req.Tokens[n-1].T; t > end {
				end = t
			}
		}
	}
	return end
}

// player is the bubbletea shell for a replay. It owns the wall clock and
// nothing else; every frame comes from playModel and tui.View.
type player struct {
	tp    *tape.Tape
	speed float64
	grid  tui.Grid
	theme tui.Theme
	path  string
	end   time.Duration
	wall  time.Duration
	start time.Time
	w, h  int
	// page is which page of tiles the viewer has stepped to. It lives here
	// rather than in the model because the model is rebuilt from the tape on
	// every frame; the view takes it as ordinary model state.
	page    int
	playing bool
	quit    bool
}

func (p *player) Init() tea.Cmd {
	p.start = time.Now()
	return playTick()
}

func playTick() tea.Cmd {
	return tea.Tick(tui.TickInterval, func(t time.Time) tea.Msg { return playTickMsg(t) })
}

type playTickMsg time.Time

func (p *player) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		p.w, p.h = msg.Width, msg.Height
		return p, nil
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c", "esc":
			p.quit = true
			return p, tea.Quit
		case " ":
			// Pausing freezes the clip time by stopping the clock, so the
			// frame on screen stays the frame the viewer paused on.
			p.playing = !p.playing
			p.start = time.Now().Add(-p.wallAtPause())
		case "left", "h", "[":
			p.page = p.pageAfter(-1)
		case "right", "l", "]":
			p.page = p.pageAfter(1)
		}
		return p, nil
	case playTickMsg:
		if p.playing {
			p.wall = time.Since(p.start)
		}
		return p, playTick()
	}
	return p, nil
}

// wallAtPause is the wall time the replay is currently showing, used to keep
// the clock continuous across a pause.
func (p *player) wallAtPause() time.Duration { return p.wall }

// pageAfter is the page d steps away, clamped by the model's own page count so
// the keys can never step past the last stream.
func (p *player) pageAfter(d int) int {
	m, _ := playModel(p.tp, p.wall, p.speed)
	m.Grid = p.grid
	m.Page = p.page
	w, h := p.size()
	return m.PageAfter(d, w, h)
}

// size is the screen the replay draws on, before the terminal has said how big
// it is.
func (p *player) size() (w, h int) {
	w, h = p.w, p.h
	if w <= 0 {
		w = tui.MinWidth
	}
	if h <= 0 {
		h = tui.MinHeight
	}
	return w, h
}

func (p *player) View() string {
	if p.quit {
		return ""
	}
	w, h := p.size()
	m, clip := playModel(p.tp, p.wall, p.speed)
	m.Theme = p.theme
	m.TapePath = p.path
	m.Grid = p.grid
	m.Page = p.page
	return tui.View(m, clip, w, h)
}
