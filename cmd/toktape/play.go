package main

import (
	"context"
	"fmt"
	"io"
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
func runPlay(ctx context.Context, stdout, stderr io.Writer, args []string) int {
	fs := newFlagSet("play", stderr)
	speed := fs.Float64("speed", 1, "replay speed multiplier (4 plays four times as fast)")
	files, err := parseArgs(fs, args)
	if err != nil {
		return exitUsage
	}
	if len(files) != 1 {
		fmt.Fprintf(stderr, "toktape play: expected one tape file\n\n%s", usageText)
		return exitUsage
	}
	if *speed <= 0 {
		fmt.Fprintf(stderr, "toktape play: --speed must be positive, got %v\n", *speed)
		return exitUsage
	}
	tp, err := tape.Read(files[0])
	if err != nil {
		fmt.Fprintf(stderr, "toktape: %v\n", err)
		return exitUsage
	}
	if !isTTY(stdout) {
		fmt.Fprintln(stderr, "toktape play: needs a terminal; use `toktape card <tape>` to print the result instead")
		return exitUsage
	}

	p := &player{
		tp:      tp,
		speed:   *speed,
		theme:   tui.ColourTheme(),
		path:    tildePath(files[0]),
		end:     tapeDuration(tp),
		playing: true,
	}
	if _, err := tea.NewProgram(p, tea.WithContext(ctx), tea.WithAltScreen(), tea.WithOutput(stdout)).Run(); err != nil {
		if ctx.Err() != nil {
			return exitOK
		}
		fmt.Fprintf(stderr, "toktape: %v\n", err)
		return exitUsage
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
	tp      *tape.Tape
	speed   float64
	theme   tui.Theme
	path    string
	end     time.Duration
	wall    time.Duration
	start   time.Time
	w, h    int
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

func (p *player) View() string {
	if p.quit {
		return ""
	}
	w, h := p.w, p.h
	if w <= 0 {
		w = tui.MinWidth
	}
	if h <= 0 {
		h = tui.MinHeight
	}
	m, clip := playModel(p.tp, p.wall, p.speed)
	m.Theme = p.theme
	m.TapePath = p.path
	return tui.View(m, clip, w, h)
}
