package main

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/midagedev/toktape/internal/render"
	"github.com/midagedev/toktape/internal/tape"
)

// The clip-length teaching in `toktape help render` (TTP-71 addendum,
// 2026-09-14).
//
// The note tells a reader how to aim a clip's length, and it does it with a
// worked figure taken from this repo's own hero recording. A worked figure is
// the part that rots: re-record the hero and the numbers quoted in the help
// become a lie a reader cannot check. So they are checked here, against the
// tape itself and against the schedule the renderer really builds.

// heroTape is the recording the README's clip plays and the help quotes.
const heroTape = "../../assets/hero.tape"

// heroPrefillLead mirrors internal/render/cmd/hero's HeroPrefillLead: how much
// of the wait for the first token the published clip keeps. A test binary
// cannot import package main, which is why it is written twice; the two
// lengths the note quotes are what would catch them drifting apart.
const heroPrefillLead = 3 * time.Second

func TestClipLengthNoteMatchesTheHero(t *testing.T) {
	tp, err := tape.Read(filepath.Join(heroTape))
	if err != nil {
		// Another track owns the asset; a missing hero is their problem to
		// report, not a reason to fail this one.
		t.Skipf("the hero recording is not readable here: %v", err)
	}
	s := tp.Summary

	// The figures the note quotes about the recording itself.
	for _, q := range []struct {
		what string
		text string
	}{
		{"streams", fmt.Sprintf("%d streams", s.Concurrency)},
		{"tokens per stream", fmt.Sprintf("%d tokens", s.Timings.PredictedN)},
		{"per-stream rate", fmt.Sprintf("%.1f tok/s", s.Timings.PredictedPerSecond)},
		{"TTFT", fmt.Sprintf("%.1fs TTFT", s.Timings.TTFTMs/1000)},
	} {
		if !strings.Contains(renderUsage, q.text) {
			t.Errorf("the clip-length note no longer quotes the hero's %s (%q); re-quote it from `toktape card %s`",
				q.what, q.text, heroTape)
		}
	}

	// The durations, from the schedules the renderer really builds. The last
	// two are the windowed clip the hero is actually published as: the note
	// teaches --prefill-lead with the hero's own figures, so those rot the
	// same way the rest do.
	runEnd := render.RunEnd(tp)
	runFrom := render.RunFrom(tp, heroPrefillLead)
	plain := render.NewSchedule(0, runEnd, render.DefaultFPS, 0, false)
	open := render.NewSchedule(0, runEnd, render.DefaultFPS, 0, true)
	windowed := render.NewSchedule(runFrom, runEnd, render.DefaultFPS, 0, true)
	for _, q := range []struct {
		what string
		d    time.Duration
	}{
		{"run", runEnd},
		{"clip without --open", plain.Duration},
		{"clip with --open", open.Duration},
		{"wait cut by --prefill-lead", runFrom},
		{"windowed clip with --open", windowed.Duration},
	} {
		text := fmt.Sprintf("%.1fs", q.d.Seconds())
		if !strings.Contains(renderUsage, text) {
			t.Errorf("the clip-length note no longer quotes the hero's %s (%s); the renderer now makes a different clip", q.what, text)
		}
	}

	// The clock the windowed clip opens on, which is the note's whole argument
	// for why the cut needs no label. It is the tile's own figure — integer
	// seconds since the run's start, over the budget — so quoting it means
	// reading it the way tui does rather than rounding runFrom to taste.
	// FAIL-first: the note said 8/30s, because 7.8 rounds up and the screen
	// truncates. The frame at the cut says 7/30s.
	if clock := fmt.Sprintf("%d/%ds", int(runFrom.Seconds()), int(s.Limit.For.Seconds())); !strings.Contains(renderUsage, clock) {
		t.Errorf("the note does not quote the clock a windowed hero opens on (%s); that figure is why the cut needs no caption", clock)
	}

	// The formula's own arithmetic, against the run it claims to predict.
	//
	// The input is --n-predict, the figure the note tells a reader to aim, and
	// not Timings.PredictedN, the figure the streams turned out to average.
	// Aimed that way the formula answers the longest run those settings can
	// produce, and the run's shape decides what that answer is worth:
	//
	//   cap-bound — every stream reached the cap, so the longest run is the
	//   run and the answer is held to a token's worth of time.
	//
	//   ragged — streams stopped at EOS first, so the answer is an upper
	//   bound, and an upper bound is the only correct thing for it to be: a
	//   formula that predicted this hero's 11.6s from --n-predict 512 would be
	//   telling a reader to size a budget that cuts the next run, whose
	//   streams may well use the cap. The bound is checked, and under it the
	//   lesson the note draws from the gap — that the tokens a run averaged
	//   predict *less* than the run — is checked as its own inequality, so the
	//   ragged branch asserts the note's claim and not merely that some number
	//   is large.
	//
	// 2026-09-17, lead. This replaced a "twentieth of the run" tolerance
	// written for the previous ragged hero (one stream stopped at 137 of 512,
	// a 12% shortfall). The current one is ragged all the way through — 152 to
	// 279 tokens against a 512 cap — and predicts 17.9s for an 11.6s run, 54%
	// over. A tolerance wide enough for that would check nothing, which is the
	// signal that the tolerance was never the right instrument here: what
	// changes with the shape is the claim, not its precision.
	//
	// FAIL-first, both branches, on this recording: with the inequality
	// reversed (predicted < runEnd) the bound fails by 6.24s, and with the
	// average's inequality reversed it fails by 799ms.
	if s.Limit.MaxTokens == 0 {
		t.Fatal("the hero was recorded without --n-predict, so the note's formula has no input to check against")
	}
	estimate := func(tokens int) time.Duration {
		return time.Duration(s.Timings.TTFTMs*float64(time.Millisecond)) +
			time.Duration(float64(tokens)/s.Timings.PredictedPerSecond*float64(time.Second))
	}
	predicted := estimate(s.Limit.MaxTokens)
	if s.Aggregate.MinPredictedN >= s.Limit.MaxTokens {
		const tol = 200 * time.Millisecond
		if predicted-runEnd > tol || runEnd-predicted > tol {
			t.Errorf("TTFT + n-predict ÷ tok/s predicts %v, the cap-bound run is %v (off by %v, over the %v allowed); the formula in the note is wrong",
				predicted, runEnd, predicted-runEnd, tol)
		}
	} else {
		if predicted < runEnd {
			t.Errorf("TTFT + n-predict ÷ tok/s predicts %v for a ragged run of %v; the note calls it the length to size a budget by, and it came in under the run",
				predicted, runEnd)
		}
		if avg := estimate(s.Timings.PredictedN); avg >= runEnd {
			t.Errorf("the tokens the run averaged predict %v for a %v run; the note says the average comes in short, and here it does not",
				avg, runEnd)
		}
	}

	// The rule the note exists for.
	if !strings.Contains(renderUsage, "--n-predict") {
		t.Error("the note does not say to aim --n-predict")
	}
	if !strings.Contains(renderUsage, "--duration") {
		t.Error("the note does not say what --duration does instead")
	}
}

// TestClipLengthLineAgreesWithTheRenderer: the line `render` prints before it
// spends a minute encoding is the schedule the encoder will use, not a second
// arithmetic that can drift from it.
func TestClipLengthLineAgreesWithTheRenderer(t *testing.T) {
	tp, err := tape.Read(filepath.Join(heroTape))
	if err != nil {
		t.Skipf("the hero recording is not readable here: %v", err)
	}
	runEnd := render.RunEnd(tp)

	for _, open := range []bool{false, true} {
		opts := render.Options{FPS: render.DefaultFPS, ColdOpen: open}
		want := render.NewSchedule(0, runEnd, render.DefaultFPS, 0, open).WithPoster()
		line := clipLengthLine(tp, opts)
		if !strings.Contains(line, fmt.Sprintf("%.1fs", want.Duration.Seconds())) {
			t.Errorf("--open=%v: %q does not name the schedule's %v", open, line, want.Duration)
		}
		if open != strings.Contains(line, "open") {
			t.Errorf("--open=%v: the breakdown is wrong: %q", open, line)
		}
	}

	// A window prints the shorter clip it really renders, and says what is not
	// in it. A reader who is told "27.1s" and nothing else would read the run
	// as eight seconds shorter than it was.
	windowed := clipLengthLine(tp, render.Options{FPS: render.DefaultFPS, PrefillLead: heroPrefillLead})
	want := render.NewSchedule(render.RunFrom(tp, heroPrefillLead), runEnd, render.DefaultFPS, 0, false).WithPoster()
	if !strings.Contains(windowed, fmt.Sprintf("%.1fs", want.Duration.Seconds())) {
		t.Errorf("--prefill-lead: %q does not name the schedule's %v", windowed, want.Duration)
	}
	if !strings.Contains(windowed, "--prefill-lead") {
		t.Errorf("--prefill-lead: %q does not say why the clip is shorter than the run", windowed)
	}
	if strings.Contains(windowed, "intro") {
		t.Errorf("--prefill-lead: %q counts an intro the windowed clip does not have", windowed)
	}

	// A named --duration prints the length asked for and says what it costs,
	// rather than a breakdown whose parts do not add up to it.
	line := clipLengthLine(tp, render.Options{FPS: render.DefaultFPS, Duration: 10 * time.Second})
	if !strings.Contains(line, "10.0s") {
		t.Errorf("--duration 10s: %q does not name it", line)
	}
	if !strings.Contains(line, "--n-predict") {
		t.Errorf("--duration 10s: %q does not name the honest way to shorten a clip", line)
	}
}
