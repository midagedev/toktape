package render

import (
	"strings"
	"testing"
	"time"

	"github.com/midagedev/toktape/internal/card"
	"github.com/midagedev/toktape/internal/tui"
)

// plainRows strips the colour from a screen and splits it into rows. Every
// assertion below reads the plain text; the colours are judged by eye on a
// render, which is the only thing that can judge them.
func plainRows(s string) []string {
	return strings.Split(card.StripANSI(s), "\n")
}

func TestOpenScreenGeometry(t *testing.T) {
	// The same contract tui.View meets: exactly h rows of exactly w columns,
	// at every instant of the phase. A short row would leave the tail of a
	// longer one behind it, because the clip repaints without erasing.
	tp := tui.ExampleTapeN(4)
	for at := time.Duration(0); at <= OpenHold; at += 37 * time.Millisecond {
		rows := plainRows(OpenScreen(tp, DefaultWidth, DefaultHeight, at))
		if len(rows) != DefaultHeight {
			t.Fatalf("at %v: %d rows, want %d", at, len(rows), DefaultHeight)
		}
		for i, row := range rows {
			if w := card.Width(row); w != DefaultWidth {
				t.Fatalf("at %v: row %d is %d columns, want %d:\n%q", at, i, w, DefaultWidth, row)
			}
		}
	}
}

func TestOpenScreenBeats(t *testing.T) {
	// The script, beat by beat. Each case names what a viewer must be able to
	// see at that instant; between them the screen is allowed to be anything
	// these four states interpolate to.
	tp := tui.ExampleTapeN(4)
	tests := []struct {
		name    string
		at      time.Duration
		want    []string // substrings that must appear somewhere on the screen
		notWant []string
	}{
		{
			name:    "an empty prompt before anything is typed",
			at:      400 * time.Millisecond,
			want:    []string{"~ ❯"},
			notWant: []string{"toktape"},
		},
		{
			name:    "the command half typed",
			at:      1800 * time.Millisecond,
			want:    []string{"~ ❯ tokt"},
			notWant: []string{"toktape -n 4", "looking for"},
		},
		{
			name:    "the whole command, before the tool says anything",
			at:      2800 * time.Millisecond,
			want:    []string{"~ ❯ toktape -n 4"},
			notWant: []string{"looking for", "stream 1/4"},
		},
		{
			name:    "looking for a server",
			at:      3500 * time.Millisecond,
			want:    []string{"~ ❯ toktape -n 4", "looking for llama-server …"},
			notWant: []string{"llama-server at", "stream 1/4"},
		},
		{
			name:    "attached, before the streams start",
			at:      4 * time.Second,
			want:    []string{"→ llama-server at http://127.0.0.1:8080"},
			notWant: []string{"looking for", "stream 1/4"},
		},
		{
			name: "every stream in prefill",
			at:   5 * time.Second,
			want: []string{
				"→ llama-server at http://127.0.0.1:8080",
				"  stream 1/4 · prefill…",
				"  stream 2/4 · prefill…",
				"  stream 3/4 · prefill…",
				"  stream 4/4 · prefill…",
			},
			notWant: []string{"looking for", "stream 5/4"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			screen := card.StripANSI(OpenScreen(tp, DefaultWidth, DefaultHeight, tc.at))
			for _, want := range tc.want {
				if !strings.Contains(screen, want) {
					t.Errorf("at %v the screen does not show %q:\n%s", tc.at, want, trimScreen(screen))
				}
			}
			for _, notWant := range tc.notWant {
				if strings.Contains(screen, notWant) {
					t.Errorf("at %v the screen already shows %q:\n%s", tc.at, notWant, trimScreen(screen))
				}
			}
		})
	}
}

func TestOpenAttachLineMatchesTheCLI(t *testing.T) {
	// cmd/toktape/record.go's headerLine is in package main and cannot be
	// imported, so the wording is replicated — and pinned here against the
	// line that function produces for this fixture. If the CLI's wording
	// changes, this is the test that says the clip is now lying about it.
	const want = "→ llama-server at http://127.0.0.1:8080 (b3650) · R1 Distill Llama 70B Q4_K_M · pid 48213"
	got := strings.TrimRight(card.StripANSI(
		openAttachLine(newOpenLine(DefaultWidth), tui.ExampleTapeN(4)).String()), " ")
	if got != want {
		t.Errorf("attach line =\n  %q\nwant\n  %q", got, want)
	}
}

func TestOpenScreenOfNoTape(t *testing.T) {
	// A nil tape is a failed run, and Asciicast(nil) renders one. The open
	// still draws, and prints unknowns as unknowns rather than inventing a
	// stream count (CLAUDE.md: unknown is "" / 0 and prints as ?).
	screen := card.StripANSI(OpenScreen(nil, DefaultWidth, DefaultHeight, 5*time.Second))
	if !strings.Contains(screen, "~ ❯ toktape") {
		t.Errorf("no prompt on the nil-tape open:\n%s", trimScreen(screen))
	}
	if strings.Contains(screen, "-n ") {
		t.Errorf("the command claims a stream count the tape does not have:\n%s", trimScreen(screen))
	}
	if strings.Contains(screen, "stream 1/") {
		t.Errorf("a run with no streams still drew a prefill line:\n%s", trimScreen(screen))
	}
	if !strings.Contains(screen, "unknown at ?") {
		t.Errorf("the attach line invented a server:\n%s", trimScreen(screen))
	}
}

func TestOpenCursorBlinks(t *testing.T) {
	// The cursor is the one thing alive on an empty prompt, and its phase is
	// a function of t — the whole clip has to replay to the same frames.
	tp := tui.ExampleTapeN(4)
	on := card.StripANSI(OpenScreen(tp, DefaultWidth, DefaultHeight, 200*time.Millisecond))
	off := card.StripANSI(OpenScreen(tp, DefaultWidth, DefaultHeight, 700*time.Millisecond))
	if !strings.Contains(on, openCursor) {
		t.Error("no cursor in the first half of the blink period")
	}
	if strings.Contains(off, openCursor) {
		t.Error("the cursor is still drawn in the second half of the blink period")
	}
	// A full period later the screen is back to where it was.
	again := card.StripANSI(OpenScreen(tp, DefaultWidth, DefaultHeight, 1200*time.Millisecond))
	if !strings.Contains(again, openCursor) {
		t.Error("the blink does not repeat on its period")
	}
}

func TestOpenSpinnerTurns(t *testing.T) {
	// One frame per openSpinStep, and the cycle closes.
	seen := map[rune]bool{}
	for i := 0; i < len(openSpinnerFrames); i++ {
		seen[openSpinFrame(time.Duration(i)*openSpinStep)] = true
	}
	if len(seen) != len(openSpinnerFrames) {
		t.Errorf("the spinner shows %d of its %d frames", len(seen), len(openSpinnerFrames))
	}
	if a, b := openSpinFrame(0), openSpinFrame(time.Duration(len(openSpinnerFrames))*openSpinStep); a != b {
		t.Errorf("the cycle does not close: %q then %q", a, b)
	}
	if a, b := openSpinFrame(0), openSpinFrame(openSpinStep/2); a != b {
		t.Errorf("the spinner advanced inside one step: %q then %q", a, b)
	}
}

func TestTypedCountIsMonotonicAndUneven(t *testing.T) {
	const n = 12
	prev := 0
	for at := time.Duration(0); at <= OpenHold; at += time.Millisecond {
		got := typedCount(n, at)
		if got < prev {
			t.Fatalf("at %v the command lost characters: %d after %d", at, got, prev)
		}
		if got > n {
			t.Fatalf("at %v the command is %d characters long, want at most %d", at, got, n)
		}
		prev = got
	}
	if typedCount(n, openTypeAt) != 0 {
		t.Error("a character is already typed at the instant typing starts")
	}
	if got := typedCount(n, openTypeAt+openTypeSpan); got != n {
		t.Errorf("%d characters typed when the span ends, want all %d", got, n)
	}
	if got := typedCount(n, openEnterAt-time.Millisecond); got != n {
		t.Errorf("%d characters typed at the return key, want all %d", got, n)
	}

	// Uneven on purpose: evenly spaced characters read as a macro replaying.
	w := openWeights(n)
	same := true
	for _, v := range w[1:] {
		if v != w[0] {
			same = false
		}
	}
	if same {
		t.Error("every character has the same dwell; the typing will read as a machine")
	}
	for i, v := range w {
		if v < 60 || v > 110 {
			t.Errorf("character %d dwells %d, want 60–110", i, v)
		}
	}
}

func TestOpenScreenIsReproducible(t *testing.T) {
	tp := tui.ExampleTapeN(4)
	for _, at := range []time.Duration{0, 1500 * time.Millisecond, 3500 * time.Millisecond, 5 * time.Second} {
		a := OpenScreen(tp, DefaultWidth, DefaultHeight, at)
		b := OpenScreen(tp, DefaultWidth, DefaultHeight, at)
		if a != b {
			t.Errorf("at %v two renders of the same frame differ", at)
		}
	}
}

// trimScreen drops the blank tail of a screen so a failure prints the part
// that has anything on it.
func trimScreen(s string) string {
	rows := strings.Split(s, "\n")
	last := 0
	for i, row := range rows {
		if strings.TrimSpace(row) != "" {
			last = i
		}
	}
	return strings.Join(rows[:last+1], "\n")
}
