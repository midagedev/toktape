package tui

import (
	"strings"
	"testing"
	"time"
)

// speedPane is the SPEED section of a frame, as plain text.
func speedPane(t *testing.T, m Model, at time.Duration) string {
	t.Helper()
	return strings.Join(speedRows(m, PlainTheme(), at, rightW-2), "\n")
}

// 2026-09-13: fixing the leak below re-baselined six rows of the golden frames
// in testdata (view-{100x30,140x40}-{t0,mid}.txt) via `go test -update`. At t0
// the four SPEED figures became "?"; mid-run prefill moved 1980 → 1972 and ttft
// 210 → 330 ms because both are now measured from the live state instead of
// read from the summary. The "done" frames did not change: after the run the
// server's timings are still the record. Nothing was loosened — the t0 rows
// went from printing an unobserved number to printing nothing.
func TestSpeedPanePrintsNothingItHasNotObserved(t *testing.T) {
	// The first frame of a replay holds the whole tape, so every final figure
	// is already sitting in m.Summary. Reading one of them before the run has
	// produced it makes the screen announce a decode rate while all eight
	// streams still say "waiting for the first token" — the exact thing
	// CLAUDE.md's "never print a default you did not observe" forbids.
	m := ModelAt(ExampleTape(), 0)
	pane := speedPane(t, m, 0)

	for _, row := range []string{"prefill", "decode", "ttft", "streams"} {
		line := rowStarting(pane, row)
		if line == "" {
			t.Fatalf("the pane has no %q row:\n%s", row, pane)
		}
		if !strings.Contains(line, unknown) {
			t.Errorf("at t=0 the %q row reads %q, want %q", row, strings.TrimSpace(line), unknown)
		}
	}

	// The same, from the outside: none of the summary's headline figures may
	// appear anywhere on the screen before the run has measured them.
	frame := View(m, 0, MinWidth+20, MinHeight+6)
	for _, leak := range []struct {
		figure string
		what   string
	}{
		{"1980", "the summary's prefill rate"},
		{"11.3", "the summary's per-stream decode rate"},
		{"90.6", "the summary's aggregate decode rate"},
		{"210 ms", "the summary's TTFT"},
	} {
		if strings.Contains(frame, leak.figure) {
			t.Errorf("the t=0 frame prints %s (%q) before a single token has arrived", leak.what, leak.figure)
		}
	}
}

func TestSpeedPaneFillsInAsFiguresBecomeObservable(t *testing.T) {
	tp := ExampleTape()

	// 250 ms: the first stream has reported prompt progress and has its first
	// token, so prefill and TTFT are measurements. A decode rate still is not
	// — one arrival is not a rate, it takes two.
	early := ModelAt(tp, 250*time.Millisecond)
	pane := speedPane(t, early, 250*time.Millisecond)
	if line := rowStarting(pane, "prefill"); strings.Contains(line, unknown) {
		t.Errorf("prefill is still %q at 250 ms, with four progress rows in: %q", unknown, strings.TrimSpace(line))
	}
	if line := rowStarting(pane, "ttft"); !strings.Contains(line, "210 ms") {
		t.Errorf("ttft reads %q at 250 ms, want the first token's observed 210 ms", strings.TrimSpace(line))
	}
	if line := rowStarting(pane, "decode"); !strings.Contains(line, unknown) {
		t.Errorf("decode reads %q after one token, want %q — one arrival is not a rate",
			strings.TrimSpace(line), unknown)
	}

	// Mid-run: every figure is a live measurement, and none of them is the
	// summary's.
	mid := ModelAt(tp, 3*time.Second)
	pane = speedPane(t, mid, 3*time.Second)
	for _, row := range []string{"prefill", "decode", "ttft", "streams"} {
		if line := rowStarting(pane, row); strings.Contains(line, unknown) {
			t.Errorf("at 3 s the %q row is still %q", row, unknown)
		}
	}

	// Done: the server's own timings are the record (CLAUDE.md), so the
	// summary comes back.
	done := ModelAt(tp, 9*time.Second)
	if !done.Done {
		t.Fatal("the example run is not finished at 9 s")
	}
	pane = speedPane(t, done, 9*time.Second)
	for _, want := range []string{"1980", "11.3", "90.6", "210 ms"} {
		if !strings.Contains(pane, want) {
			t.Errorf("the finished run does not print the summary's %q:\n%s", want, pane)
		}
	}
}

// rowStarting returns the pane line carrying label as one of its words. The
// aggregate row leads with the stream count ("8 streams"), so a first-word
// match would not find it.
func rowStarting(pane, label string) string {
	for _, line := range strings.Split(pane, "\n") {
		for _, f := range strings.Fields(line) {
			if f == label {
				return line
			}
		}
	}
	return ""
}
