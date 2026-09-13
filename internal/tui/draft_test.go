package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/midagedev/toktape/internal/tape"
)

// draftTape is the four-stream example with a draft model reporting on every
// stream: 290 drafted and 174 accepted across the four (60 %), the figures
// card.ExampleSpeculative pins.
func draftTape() *tape.Tape {
	tp := ExampleTapeN(4)
	drafted := []int{80, 70, 70, 70}
	accepted := []int{48, 42, 42, 42}
	for i := range tp.Requests {
		d, a := drafted[i], accepted[i]
		tp.Requests[i].Timings.DraftN = &d
		tp.Requests[i].Timings.DraftNAccepted = &a
	}
	d, a := 290, 174
	tp.Summary.Timings.DraftN, tp.Summary.Timings.DraftNAccepted = &d, &a
	return tp
}

// draftRowText is the right pane's draft line on a frame, or "" when there is
// none.
func draftRowText(t *testing.T, m Model, at time.Duration) string {
	t.Helper()
	rows := parseFrame(View(m, at, 120, 36), 120, 36)
	for _, row := range rows {
		for _, seg := range segments(row) {
			if f := strings.Fields(seg.text); len(f) > 0 && f[0] == "draft" {
				return strings.Join(f, " ")
			}
		}
	}
	return ""
}

// TestDraftLineAppearsWhenAStreamFinishes (TTP-30, 2026-09-13).
func TestDraftLineAppearsWhenAStreamFinishes(t *testing.T) {
	t.Run("done: the pooled rate and its counts", func(t *testing.T) {
		m := ModelAt(draftTape(), doneAt)
		m.Theme = ColourTheme()
		if got, want := draftRowText(t, m, doneAt), "draft 60% · 174/290"; got != want {
			t.Errorf("draft line = %q, want %q", got, want)
		}
	})

	t.Run("before any stream finishes there is no line", func(t *testing.T) {
		m := ModelAt(draftTape(), 0)
		m.Theme = ColourTheme()
		if got := draftRowText(t, m, 0); got != "" {
			t.Errorf("draft line at t=0 = %q; no stream has reported its final timings yet", got)
		}
	})

	t.Run("mid-run: only the finished streams count", func(t *testing.T) {
		m := ModelAt(draftTape(), midRun)
		drafted, accepted, ok := liveDraft(m)
		wantD, wantA, wantOK := 0, 0, false
		for _, s := range m.Streams {
			if s.Done && s.Err == "" {
				wantOK = true
				wantD += *s.Timings.DraftN
				wantA += *s.Timings.DraftNAccepted
			}
		}
		if drafted != wantD || accepted != wantA || ok != wantOK {
			t.Errorf("liveDraft = %d/%d ok=%v, want %d/%d ok=%v", accepted, drafted, ok, wantA, wantD, wantOK)
		}
	})

	t.Run("a tape without a draft has no line", func(t *testing.T) {
		m := ModelAt(ExampleTapeN(4), doneAt)
		m.Theme = ColourTheme()
		if got := draftRowText(t, m, doneAt); got != "" {
			t.Errorf("draft line = %q on a tape that reported no draft", got)
		}
	})

	// The emphasis contract on a draft frame: the draft figures must not add
	// a lit digit (TestOnlyTheRateIsAccent's rule, run on this frame).
	t.Run("the draft figures are not accent", func(t *testing.T) {
		m := ModelAt(draftTape(), doneAt)
		m.Theme = ColourTheme()
		rows := parseFrame(View(m, doneAt, 120, 36), 120, 36)
		for y, row := range rows {
			spans := rateSpans(rows, y)
			for _, rn := range accentRuns(row, isFigureRune) {
				if !within(spans, rn) {
					t.Errorf("the figure %q at row %d col %d is accent\n%s", rn.text, y, rn.from, rowContext(rows, y))
				}
			}
		}
	})
}
