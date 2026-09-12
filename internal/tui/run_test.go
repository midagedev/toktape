package tui

import (
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/midagedev/toktape/internal/tape"
)

// newProgram builds the bubbletea adapter directly. The key handling and the
// tick contract are tested without starting a terminal program: Run itself is
// thirty lines of wiring around this type, and a test that needed a TTY would
// not run on the gate.
func newProgram(m Model) *program {
	return &program{model: m, w: 120, h: 36, start: time.Now()}
}

// TestTickOnlyAdvancesTime is the determinism contract in one assertion: the
// redraw tick must not change a single byte of state. If it did, a replay at a
// different frame rate would draw a different screen (decision 10).
func TestTickOnlyAdvancesTime(t *testing.T) {
	p := newProgram(ModelAt(ExampleTape(), midRun))
	before := p.model
	p.Update(tickMsg(time.Now()))
	after := p.model

	if len(after.Streams) != len(before.Streams) || after.At != before.At ||
		after.Done != before.Done || after.Mode != before.Mode {
		t.Error("the tick changed the model")
	}
	for i := range before.Streams {
		if after.Streams[i].Text != before.Streams[i].Text {
			t.Fatalf("the tick changed stream %d", i)
		}
	}
	if p.t <= 0 {
		t.Error("the tick did not advance the clip time")
	}
}

func TestKeysSwitchModes(t *testing.T) {
	p := newProgram(ModelAt(ExampleTape(), midRun))

	press := func(key string) {
		p.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
	}

	press("p")
	if p.model.Mode != ModePrompt {
		t.Error("p did not open the rendered prompt")
	}
	press("p")
	if p.model.Mode != ModeLive {
		t.Error("p did not close the rendered prompt")
	}

	// The card only exists once the run has finished.
	press("c")
	if p.model.Mode != ModeLive {
		t.Error("c opened a card before the run was done")
	}
	p.model.Done = true
	press("c")
	if p.model.Mode != ModeCard {
		t.Error("c did not open the card after the run finished")
	}
	press("c")
	if p.model.Mode != ModeLive {
		t.Error("c did not return to the live screen")
	}

	press("q")
	if !p.quit {
		t.Error("q did not quit")
	}
	if p.View() != "" {
		t.Error("the program kept drawing after quitting")
	}
}

func TestWindowSizeIsHonoured(t *testing.T) {
	p := newProgram(ModelAt(ExampleTape(), midRun))
	p.Update(tea.WindowSizeMsg{Width: 140, Height: 40})
	checkFrame(t, p.View(), 140, 40)
}

// TestProgramFallsBackToTheMinimumSize: before the first WindowSizeMsg arrives
// the program has no dimensions, and it must still produce a frame rather than
// an empty string.
func TestProgramFallsBackToTheMinimumSize(t *testing.T) {
	p := &program{model: Model{}, start: time.Now()}
	checkFrame(t, p.View(), MinWidth, MinHeight)
}

// TestEventStampsArrivalTime: a recorder that does not fill in T gets the
// program's own elapsed clip time, so the timeline is never left at zero.
func TestEventStampsArrivalTime(t *testing.T) {
	p := newProgram(Model{})
	time.Sleep(2 * time.Millisecond)
	p.Update(eventMsg{ev: Event{Kind: EventToken, Stream: 0,
		Token: tape.TokenEvent{Text: "x"}}})
	if len(p.model.Streams) != 1 {
		t.Fatalf("got %d streams, want 1", len(p.model.Streams))
	}
	if p.model.Streams[0].Tokens[0].T <= 0 {
		t.Error("the token was stamped at zero")
	}
}

// TestClosedChannelStopsPolling: a finished recorder must not leave the
// program waiting on a closed channel forever.
func TestClosedChannelStopsPolling(t *testing.T) {
	ch := make(chan Event)
	p := &program{model: Model{}, events: ch, start: time.Now(), w: 120, h: 36}
	if _, cmd := p.Update(eventMsg{closed: true}); cmd != nil {
		t.Error("the program issued another wait after the channel closed")
	}
	if p.events != nil {
		t.Error("the program kept a reference to the closed channel")
	}
}
