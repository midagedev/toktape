package render

import (
	"strings"
	"testing"
	"time"

	"github.com/midagedev/toktape/internal/card"
	"github.com/midagedev/toktape/internal/tape"
	"github.com/midagedev/toktape/internal/tui"
)

// clipFrame is one live frame of tp at run instant at, without colour.
func clipFrame(tp *tape.Tape, at time.Duration) string {
	return card.StripANSI(FrameText(tp, Options{}.withDefaults(), Frame{At: at, Anim: at, Mode: tui.ModeLive}))
}

// TestClipDrawsOneRoundAtATime (TTP-38): a clip of a `record --prompts` tape
// shows the round active at each instant — N tiles, never N×K — and its
// command and prefill lines count N streams.
func TestClipDrawsOneRoundAtATime(t *testing.T) {
	tp := tui.ExampleRoundsTape(4, 3)
	start1 := tui.ExampleRoundStart(tp, 1)

	mid := clipFrame(tp, start1+5*time.Second)
	for _, want := range []string{"round 2/3 prose-1", "stream 1/4", "stream 4/4"} {
		if !strings.Contains(mid, want) {
			t.Errorf("the frame inside round 2 has no %q:\n%s", want, mid)
		}
	}
	// "stream 1/12" rather than "/12", which a tile's "40/128" cap also carries.
	if strings.Contains(mid, "stream 1/12") || strings.Contains(mid, "stream 5/") {
		t.Errorf("the frame inside round 2 counts every round's streams:\n%s", mid)
	}

	// At the boundary: the gap still shows round 1 with every tile finished
	// and the run not over; the first instant of round 2 has switched.
	gap := clipFrame(tp, start1-500*time.Millisecond)
	if !strings.Contains(gap, "round 1/3 sql-1") {
		t.Errorf("the gap before round 2 does not show round 1:\n%s", gap)
	}
	if strings.Contains(gap, "c card") {
		t.Errorf("the gap between rounds offers the card, as if the run had ended:\n%s", gap)
	}
	if at := clipFrame(tp, start1); !strings.Contains(at, "round 2/3 prose-1") {
		t.Errorf("the first instant of round 2 still shows round 1:\n%s", at)
	}

	if n := openStreams(tp); n != 4 {
		t.Errorf("openStreams = %d, want 4", n)
	}
	if got := openCommand(tp); got != "toktape -n 4" {
		t.Errorf("openCommand = %q, want toktape -n 4", got)
	}
	// Without a summary figure the count is still one round's streams.
	bare := *tp
	bare.Summary.Concurrency = 0
	if n := openStreams(&bare); n != 4 {
		t.Errorf("openStreams without Concurrency = %d, want 4", n)
	}
}

// TestOpenAttachLineUnknownEngine (TTP-37): the clip's attach line prints an
// unidentified engine as "?", the way the CLI's headerLine does.
func TestOpenAttachLineUnknownEngine(t *testing.T) {
	for _, kind := range []tape.ServerKind{tape.ServerUnknown, ""} {
		tp := tui.ExampleTapeN(4)
		tp.Summary.Server.Kind = kind
		got := strings.TrimRight(card.StripANSI(openAttachLine(newOpenLine(DefaultWidth), tp).String()), " ")
		if !strings.HasPrefix(got, "→ ? at http://127.0.0.1:8080 (b3650)") {
			t.Errorf("kind %q: attach line = %q", kind, got)
		}
		if strings.Contains(got, "unknown") {
			t.Errorf("kind %q: attach line prints the word unknown: %q", kind, got)
		}
	}
}
