package render

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/midagedev/toktape/internal/card"
	"github.com/midagedev/toktape/internal/tui"
)

// castEventOf splits one asciicast event line into its time and its payload.
func castEventOf(t *testing.T, line string) (float64, string) {
	t.Helper()
	var ev []any
	if err := json.Unmarshal([]byte(line), &ev); err != nil {
		t.Fatalf("event is not JSON: %v\n%s", err, line)
	}
	if len(ev) != 3 {
		t.Fatalf("event has %d fields, want [time, \"o\", data]", len(ev))
	}
	at, ok := ev[0].(float64)
	if !ok {
		t.Fatalf("event time is %T, want a number", ev[0])
	}
	if ev[1] != "o" {
		t.Fatalf("event kind is %v, want \"o\"", ev[1])
	}
	data, ok := ev[2].(string)
	if !ok {
		t.Fatalf("event data is %T, want a string", ev[2])
	}
	return at, data
}

func TestAsciicastOfAConcurrentRun(t *testing.T) {
	tp := tui.ExampleTape() // eight streams, ends at 7.0s
	opts := Options{}

	data, err := Asciicast(tp, opts)
	if err != nil {
		t.Fatalf("Asciicast: %v", err)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) < 2 {
		t.Fatalf("recording has %d lines, want a header and events", len(lines))
	}

	var hdr castHeader
	if err := json.Unmarshal([]byte(lines[0]), &hdr); err != nil {
		t.Fatalf("header is not JSON: %v\n%s", err, lines[0])
	}
	if hdr.Version != 2 {
		t.Errorf("header version = %d, want 2", hdr.Version)
	}
	if hdr.Width != DefaultWidth || hdr.Height != DefaultHeight {
		t.Errorf("header size = %d×%d, want %d×%d", hdr.Width, hdr.Height, DefaultWidth, DefaultHeight)
	}
	if hdr.Env["TERM"] != "xterm-256color" {
		t.Errorf("header TERM = %q, want xterm-256color", hdr.Env["TERM"])
	}
	if want := tp.Summary.StartedAt.Unix(); hdr.Timestamp != want {
		t.Errorf("header timestamp = %d, want the run's start %d", hdr.Timestamp, want)
	}

	events := lines[1:]
	sched := NewSchedule(RunEnd(tp), DefaultFPS, 0)
	if len(events) != sched.Count {
		t.Errorf("recording has %d events, want the schedule's %d frames", len(events), sched.Count)
	}
	// The spec's own arithmetic, checked independently of the schedule.
	if want := int(sched.Duration.Seconds()*DefaultFPS + 0.5); len(events)-want < 0 || len(events)-want > 1 {
		t.Errorf("%d events, want fps × duration = %d ± 1", len(events), want)
	}

	prev := -1.0
	var last float64
	for i, line := range events {
		at, payload := castEventOf(t, line)
		if at < prev {
			t.Fatalf("event %d goes back in time: %.3f after %.3f", i, at, prev)
		}
		prev, last = at, at

		plain := card.StripANSI(payload)
		rows := strings.Split(plain, "\r\n")
		if len(rows) != DefaultHeight {
			t.Fatalf("event %d has %d rows, want %d", i, len(rows), DefaultHeight)
		}
		for r, row := range rows {
			if w := card.Width(row); w != DefaultWidth {
				t.Fatalf("event %d row %d is %d columns, want %d:\n%q", i, r, w, DefaultWidth, row)
			}
		}
	}
	if lo, hi := MinDuration.Seconds(), MaxDuration.Seconds(); last < lo || last > hi {
		t.Errorf("recording ends at %.3fs, want it inside [%.0fs, %.0fs]", last, lo, hi)
	}
}

func TestAsciicastFramesAreFullRedraws(t *testing.T) {
	data, err := Asciicast(tui.ExampleTape(), Options{FPS: 2, Duration: time.Second})
	if err != nil {
		t.Fatalf("Asciicast: %v", err)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	events := lines[1:]
	if len(events) != 3 {
		t.Fatalf("%d events, want 3", len(events))
	}

	_, first := castEventOf(t, events[0])
	if !strings.HasPrefix(first, hideCursor+clear+home) {
		t.Errorf("the first frame does not clear the screen and hide the cursor: %q", first[:min(40, len(first))])
	}
	for i, line := range events[1:] {
		_, payload := castEventOf(t, line)
		if !strings.HasPrefix(payload, home) {
			t.Errorf("event %d does not start by homing the cursor: %q", i+1, payload[:min(20, len(payload))])
		}
	}
	_, lastEv := castEventOf(t, events[len(events)-1])
	if !strings.HasSuffix(lastEv, showCursor) {
		t.Errorf("the recording does not give the cursor back")
	}
}

func TestAsciicastIsReproducible(t *testing.T) {
	// Nothing in the pipeline reads a clock, so the same tape gives the same
	// bytes — which is what lets two runs of the same tape be diffed.
	opts := Options{FPS: 4, Duration: 2 * time.Second}
	a, err := Asciicast(tui.ExampleTape(), opts)
	if err != nil {
		t.Fatalf("Asciicast: %v", err)
	}
	b, err := Asciicast(tui.ExampleTape(), opts)
	if err != nil {
		t.Fatalf("Asciicast: %v", err)
	}
	if string(a) != string(b) {
		t.Error("two renders of the same tape produced different recordings")
	}
}

func TestAsciicastRejectsATinyTerminal(t *testing.T) {
	if _, err := Asciicast(tui.ExampleTape(), Options{Width: 80, Height: 24}); err == nil {
		t.Fatal("an 80×24 clip was accepted; the TUI cannot lay out that frame")
	}
}

func TestAsciicastOfAnEmptyTape(t *testing.T) {
	// A tape with no tokens is a failed run, not a crash: it still renders a
	// clip, and the clip is the minimum length.
	data, err := Asciicast(nil, Options{})
	if err != nil {
		t.Fatalf("Asciicast(nil): %v", err)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if want := DefaultFPS*int(MinDuration/time.Second) + 1; len(lines)-1 != want {
		t.Errorf("%d events, want %d", len(lines)-1, want)
	}
}
