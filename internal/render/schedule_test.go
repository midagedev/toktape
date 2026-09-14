package render

import (
	"testing"
	"time"

	"github.com/midagedev/toktape/internal/tui"
)

// holds is the fixed part of a clip with the cold open: the open and the two
// holds. Every schedule below asks for the open unless it is the subject.
const holds = OpenHold + IntroHold + CardHold

// TestScheduleWithoutTheColdOpen: the default clip of a run opens on the live
// screen (user, 2026-09-14). FAIL-first: on the source that always drew the
// open, Frame(0) was in the open and the clip was OpenHold longer.
func TestScheduleWithoutTheColdOpen(t *testing.T) {
	s := NewSchedule(0, 7*time.Second, 30, 0, false)
	if s.Open != 0 || s.Duration != MinDuration+7*time.Second {
		t.Errorf("open %v, duration %v; want no open and %v", s.Open, s.Duration, MinDuration+7*time.Second)
	}
	first := s.Frame(0)
	if first.InOpen || first.At != 0 || first.Mode != tui.ModeLive {
		t.Errorf("first frame = {InOpen %v, At %v, mode %v}, want the live screen at the run's start", first.InOpen, first.At, first.Mode)
	}
	for _, f := range s.Frames() {
		if f.InOpen {
			t.Fatalf("frame %d is in the open", f.Index)
		}
	}
	if last := s.Frame(s.Count - 1); last.Mode != tui.ModeCard {
		t.Errorf("the clip does not end on the result")
	}
}

func TestNewScheduleDerivesDuration(t *testing.T) {
	// The clip is as long as the run needs (TTP-27, user 2026-09-13: the old
	// twenty-second budget made the tokens fly). Every case below the
	// MaxStream ceiling is holds + the run, exactly.
	tests := []struct {
		name    string
		runEnd  time.Duration
		wantDur time.Duration
		wantStr time.Duration // streaming phase
	}{
		{"the run is the clip", 10 * time.Second, holds + 10*time.Second, 10 * time.Second},
		{"a short run gives a short clip", 2 * time.Second, holds + 2*time.Second, 2 * time.Second},
		{"the example run today", 6600 * time.Millisecond, 18600 * time.Millisecond, 6600 * time.Millisecond},
		{"the example run once it is lengthened", 25 * time.Second, 37 * time.Second, 25 * time.Second},
		// No ceiling (user, 2026-09-14): the thirty-second cap that
		// compressed a longer run is gone, and a five-minute run is a
		// five-minute stream. FAIL-first: the capped source gave 30 s.
		{"a long run plays whole", 5 * time.Minute, holds + 5*time.Minute, 5 * time.Minute},
		{"an empty run is the holds and nothing else", 0, holds, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := NewSchedule(0, tc.runEnd, DefaultFPS, 0, true)
			if s.Duration != tc.wantDur {
				t.Errorf("duration = %v, want %v", s.Duration, tc.wantDur)
			}
			if s.Stream != tc.wantStr {
				t.Errorf("streaming phase = %v, want %v", s.Stream, tc.wantStr)
			}
			if s.Open != OpenHold || s.Intro != IntroHold || s.Card != CardHold {
				t.Errorf("held phases = %v/%v/%v, want %v/%v/%v",
					s.Open, s.Intro, s.Card, OpenHold, IntroHold, CardHold)
			}
			if got := s.Open + s.Intro + s.Stream + s.Card; got != s.Duration {
				t.Errorf("phases sum to %v, want %v", got, s.Duration)
			}
		})
	}
}

func TestScheduleStreamsAtRealSpeed(t *testing.T) {
	// The contract of TTP-27, frame by frame: inside the streaming phase the
	// instant of the run a frame draws is exactly its distance into that
	// phase. Anything else — even a ratio that is 0.998 rather than 1 —
	// scrolls the sparklines and breathes the cursor at the wrong speed, and
	// the error grows across a twenty-five-second stream.
	for _, runEnd := range []time.Duration{6600 * time.Millisecond, 25 * time.Second, 2 * time.Minute} {
		s := NewSchedule(0, runEnd, DefaultFPS, 0, true)
		start := s.Open + s.Intro
		var checked int
		for i := 0; i < s.Count; i++ {
			f := s.Frame(i)
			if f.Clip < start || f.Clip >= start+s.Stream || f.Mode == tui.ModeCard {
				continue
			}
			checked++
			want := f.Clip - start
			if want > runEnd {
				want = runEnd
			}
			if f.At != want {
				t.Fatalf("runEnd %v: frame %d at clip %v is cut at %v, want %v", runEnd, i, f.Clip, f.At, want)
			}
			if f.Anim != f.At {
				t.Fatalf("runEnd %v: frame %d animates on %v, want the run clock %v", runEnd, i, f.Anim, f.At)
			}
		}
		if want := int(s.Stream*time.Duration(s.FPS)/time.Second) - 1; checked < want {
			t.Errorf("runEnd %v: only %d streaming frames seen, want at least %d", runEnd, checked, want)
		}
		// The last streaming frame reaches the run's end rather than stopping
		// a few frames short of it.
		last := s.Frame(int((start+s.Stream)*time.Duration(s.FPS)/time.Second) - 1)
		if gap := runEnd - last.At; gap < 0 || gap > 2*time.Second/DefaultFPS {
			t.Errorf("runEnd %v: the stream's last frame is cut at %v, want within two frames of the end", runEnd, last.At)
		}
	}
}

func TestScheduleNeverCompressesADerivedClip(t *testing.T) {
	// A derived clip plays the run at 1:1 however long it is (user,
	// 2026-09-14). The old thirty-second cap squeezed a two-minute run into
	// thirty seconds; FAIL-first on that source: stream 30 s, not 2 min.
	const runEnd = 2 * time.Minute
	s := NewSchedule(0, runEnd, DefaultFPS, 0, true)
	if s.Stream < runEnd || s.Duration < holds+runEnd {
		t.Fatalf("a %v run gives stream %v of a %v clip, want the whole run", runEnd, s.Stream, s.Duration)
	}
	mid := s.Frame(int((s.Open + s.Intro + runEnd/2) * time.Duration(s.FPS) / time.Second))
	if want, tol := runEnd/2, time.Second/DefaultFPS; mid.At < want-tol || mid.At > want+tol {
		t.Errorf("a minute into the stream is cut at %v, want the run's minute", mid.At)
	}
	if mid.Anim != mid.At {
		t.Errorf("animation clock = %v, want the run clock %v", mid.Anim, mid.At)
	}
}

func TestScheduleExplicitDurationIsExact(t *testing.T) {
	// The override path, kept: a caller who names a length gets it, and the
	// run is compressed or stretched into what the holds leave.
	// All three are long enough to hold the holds; the squeeze below that is
	// TestNewScheduleShortExplicitDuration's subject.
	for _, want := range []time.Duration{15 * time.Second, 30 * time.Second, time.Minute} {
		s := NewSchedule(0, 7*time.Second, DefaultFPS, want, true)
		if s.Duration != want {
			t.Errorf("asked for %v, got %v", want, s.Duration)
		}
		if s.Open != OpenHold || s.Intro != IntroHold || s.Card != CardHold {
			t.Errorf("%v clip holds = %v/%v/%v, want %v/%v/%v", want, s.Open, s.Intro, s.Card, OpenHold, IntroHold, CardHold)
		}
		if got := s.Open + s.Intro + s.Stream + s.Card; got != want {
			t.Errorf("%v clip's phases sum to %v", want, got)
		}
		last := s.Frame(s.Count - 1)
		if last.Clip != want || last.At != s.RunEnd {
			t.Errorf("%v clip ends at clip %v / run %v, want %v / %v", want, last.Clip, last.At, want, s.RunEnd)
		}
	}
}

func TestNewScheduleShortExplicitDuration(t *testing.T) {
	// An explicit duration is used as given — the [20s, 25s] clamp describes
	// the derived default, not the API. All three held phases shrink so that a
	// clip too short to hold them still has a streaming phase, and they keep
	// their ratio so a squeezed clip is the same clip played fast rather than
	// one with its cold open cut off.
	s := NewSchedule(0, 7*time.Second, 30, 2*time.Second, true)
	if s.Duration != 2*time.Second {
		t.Fatalf("duration = %v, want 2s", s.Duration)
	}
	if s.Stream <= 0 {
		t.Fatalf("streaming phase = %v, want a positive one", s.Stream)
	}
	if got := s.Open + s.Intro + s.Stream + s.Card; got != s.Duration {
		t.Errorf("phases sum to %v, want %v", got, s.Duration)
	}
	// The proportions, to a percent: the flooring costs each phase at most a
	// nanosecond, and the intro is the smallest of the three.
	for _, tc := range []struct {
		name      string
		got, want float64
	}{
		{"open:intro", float64(s.Open) / float64(s.Intro), float64(OpenHold) / float64(IntroHold)},
		{"card:intro", float64(s.Card) / float64(s.Intro), float64(CardHold) / float64(IntroHold)},
	} {
		if tc.got < tc.want*0.99 || tc.got > tc.want*1.01 {
			t.Errorf("%s = %.3f, want the %.3f of the full-length clip", tc.name, tc.got, tc.want)
		}
	}
}

func TestScheduleOpensOnTheColdOpen(t *testing.T) {
	// The clip starts in a terminal, not in the TUI (user, 2026-09-13). The
	// open runs on a clock of its own that covers the whole nominal OpenHold
	// however long the phase itself ended up.
	s := NewSchedule(0, 7*time.Second, 30, 0, true)
	first := s.Frame(0)
	if !first.InOpen || first.Open != 0 {
		t.Errorf("first frame = {InOpen %v, open %v}, want the open at its start", first.InOpen, first.Open)
	}

	lastOpen := s.Frame(int(s.Open*time.Duration(s.FPS)/time.Second) - 1)
	if !lastOpen.InOpen {
		t.Error("the frame before the hand-over is not in the open")
	}
	// Within a frame of the end, plus the nanosecond the float rescaling of
	// the open's clock can cost.
	if want := OpenHold - 2*time.Second/time.Duration(s.FPS); lastOpen.Open < want {
		t.Errorf("the open's last frame is at %v on its own clock, want at least %v", lastOpen.Open, want)
	}

	// The frame at the hand-over is the intro: the TUI, cut at the run's start.
	handover := s.Frame(int(s.Open * time.Duration(s.FPS) / time.Second))
	if handover.InOpen || handover.At != 0 || handover.Mode != tui.ModeLive {
		t.Errorf("hand-over frame = {InOpen %v, At %v, mode %v}, want the live screen at the run's start",
			handover.InOpen, handover.At, handover.Mode)
	}

	// A clip too short for a six-second open plays the whole script faster
	// rather than cutting it off part way through the typing.
	short := NewSchedule(0, 7*time.Second, 30, 2*time.Second, true)
	if short.Open >= OpenHold {
		t.Fatalf("a two-second clip kept a %v open", short.Open)
	}
	lastShort := short.Frame(int(short.Open*time.Duration(short.FPS)/time.Second) - 1)
	if !lastShort.InOpen {
		t.Fatal("the squeezed clip's last open frame is not in the open")
	}
	if lastShort.Open < OpenHold-OpenHold/10 {
		t.Errorf("a squeezed open reaches only %v of its %v script", lastShort.Open, OpenHold)
	}
}

func TestScheduleFrameCount(t *testing.T) {
	for _, fps := range []int{12, 24, 30, 60} {
		// 7.4 s, so the derived clip is not a whole number of seconds and a
		// truncating expectation would be off by a fifth of a second's frames.
		s := NewSchedule(0, 7400*time.Millisecond, fps, 0, true)
		// Rounded, not truncated: a duration of n frames is n×(1s/fps) with the
		// division floored, so it is a few nanoseconds under the exact value.
		want := int((s.Duration*time.Duration(fps) + time.Second/2) / time.Second)
		if diff := s.Count - want; diff < 0 || diff > 1 {
			t.Errorf("fps %d: %d frames, want %d ± 1", fps, s.Count, want)
		}
		last := s.Frame(s.Count - 1)
		if last.Clip != s.Duration {
			t.Errorf("fps %d: last frame at %v, want the clip's end %v", fps, last.Clip, s.Duration)
		}
	}
}

func TestScheduleFramePhases(t *testing.T) {
	s := NewSchedule(0, 7*time.Second, 30, 0, true)

	first := s.Frame(0)
	if first.At != 0 || first.Mode != tui.ModeLive {
		t.Errorf("first frame = {At %v, mode %v}, want the live screen at the run's start", first.At, first.Mode)
	}

	// The frame at the intro's last moment still draws the run's start.
	lastIntro := s.Frame(int((s.Open+s.Intro)*time.Duration(s.FPS)/time.Second) - 1)
	if lastIntro.At != 0 || lastIntro.InOpen {
		t.Errorf("last intro frame is cut at %v (InOpen %v), want the TUI at 0", lastIntro.At, lastIntro.InOpen)
	}

	// Half way through the streaming phase is half way through the run.
	mid := s.Frame(int((s.Open + s.Intro + s.Stream/2) * time.Duration(s.FPS) / time.Second))
	if want, tol := s.RunEnd/2, 100*time.Millisecond; mid.At < want-tol || mid.At > want+tol {
		t.Errorf("mid-stream frame is cut at %v, want ~%v", mid.At, want)
	}
	if mid.Anim != mid.At {
		t.Errorf("mid-stream animation clock = %v, want the run clock %v", mid.Anim, mid.At)
	}

	last := s.Frame(s.Count - 1)
	if last.Mode != tui.ModeCard || last.At != s.RunEnd {
		t.Errorf("last frame = {At %v, mode %v}, want the card at the run's end", last.At, last.Mode)
	}
}

func TestScheduleIntroHandsOverOnACycleBoundary(t *testing.T) {
	// The intro animates on clip time and the run animates on run time. They
	// meet at the intro's end, and the offset is chosen so the header shimmer
	// (and the spinner, whose period divides it) is at the same phase on both
	// sides of the seam. Without this the highlight jumps a quarter of the
	// title bar at the one-second mark.
	for _, runEnd := range []time.Duration{0, 2 * time.Second, 7 * time.Second, time.Minute} {
		s := NewSchedule(0, runEnd, 30, 0, true)
		if got := (s.introLead + s.Intro) % shimmerPeriod; got != 0 {
			t.Errorf("runEnd %v: hand-over at %v into a shimmer cycle, want 0", runEnd, got)
		}
		if s.introLead < 0 {
			t.Errorf("runEnd %v: negative intro lead %v", runEnd, s.introLead)
		}
	}
}

func TestScheduleFrameIndexIsClamped(t *testing.T) {
	s := NewSchedule(0, 7*time.Second, 30, 0, true)
	if got := s.Frame(-5); got.Index != 0 {
		t.Errorf("Frame(-5).Index = %d, want 0", got.Index)
	}
	if got := s.Frame(s.Count + 100); got.Index != s.Count-1 {
		t.Errorf("Frame(past the end).Index = %d, want %d", got.Index, s.Count-1)
	}
}

func TestScheduleFramesAreMonotonic(t *testing.T) {
	s := NewSchedule(0, 7*time.Second, 30, 0, true)
	frames := s.Frames()
	if len(frames) != s.Count {
		t.Fatalf("Frames() returned %d, want %d", len(frames), s.Count)
	}
	for i := 1; i < len(frames); i++ {
		if frames[i].Clip < frames[i-1].Clip {
			t.Fatalf("frame %d goes back in clip time: %v after %v", i, frames[i].Clip, frames[i-1].Clip)
		}
		if frames[i].At < frames[i-1].At {
			t.Fatalf("frame %d goes back in run time: %v after %v", i, frames[i].At, frames[i-1].At)
		}
	}
}

// TestPosterIsTheResultInFrontOfTheClip (2026-09-14, user: "랜더링 할때
// 첫프레임을 결과 화면으로 넣을 수 있게 해줘 … 이걸 기본 모드로 하고").
//
// The first frame is the still every platform shows before anyone presses
// play. It is the result, and nothing else about the clip moves: every other
// frame keeps the phase and the run instant it had, one frame later.
func TestPosterIsTheResultInFrontOfTheClip(t *testing.T) {
	const fps = 30
	plain := NewSchedule(0, 7*time.Second, fps, 0, true)
	with := plain.WithPoster()

	if with.Count != plain.Count+1 {
		t.Errorf("the poster added %d frames, want 1", with.Count-plain.Count)
	}
	if want := plain.Duration + time.Second/fps; with.Duration != want {
		t.Errorf("the clip is %v, want %v", with.Duration, want)
	}
	for _, phase := range []struct {
		name string
		a, b time.Duration
	}{
		{"open", plain.Open, with.Open},
		{"intro", plain.Intro, with.Intro},
		{"stream", plain.Stream, with.Stream},
		{"card", plain.Card, with.Card},
	} {
		if phase.a != phase.b {
			t.Errorf("the poster moved the %s phase: %v became %v", phase.name, phase.a, phase.b)
		}
	}

	first := with.Frame(0)
	if first.Mode != tui.ModeCard {
		t.Errorf("the first frame is mode %v, want the result", first.Mode)
	}
	if first.At != with.RunEnd || first.Anim != with.RunEnd {
		t.Errorf("the first frame is cut at %v/%v, want the run's end %v", first.At, first.Anim, with.RunEnd)
	}
	if first.InOpen {
		t.Error("the first frame is a cold-open frame")
	}
	if first.Clip != 0 {
		t.Errorf("the first frame is at %v of the clip, want 0", first.Clip)
	}

	// Every other frame is the schedule's own, one frame later in the clip.
	for i := 0; i < plain.Count; i++ {
		want, got := plain.Frame(i), with.Frame(i+1)
		if got.At != want.At || got.Anim != want.Anim || got.Mode != want.Mode ||
			got.InOpen != want.InOpen || got.Open != want.Open {
			t.Fatalf("frame %d shifted: %+v, want %+v", i+1, got, want)
		}
		// It sits one index later in the finished clip, at that index's own
		// frame time: the times are i×Second/FPS, and Second/FPS on its own
		// truncates, so the shift is compared by index and not by adding a
		// frame's worth of nanoseconds.
		if at := clipTimeAt(with, i+1); got.Clip != at {
			t.Fatalf("frame %d is at %v of the clip, want %v", i+1, got.Clip, at)
		}
	}

	// Asking twice is asking once: a second poster would show the ending
	// twice and lengthen the clip for nothing.
	if again := with.WithPoster(); again.Count != with.Count || again.Duration != with.Duration {
		t.Errorf("WithPoster is not idempotent: %d frames, %v", again.Count, again.Duration)
	}
}

// clipTimeAt is frame i's time in s, the schedule's own arithmetic.
func clipTimeAt(s Schedule, i int) time.Duration {
	return time.Duration(i) * time.Second / time.Duration(s.FPS)
}

// TestNoPosterOptOut: Options.NoPoster gives back the clip that used to be the
// only one, so a caller who minds the one-frame flash at the top of a GIF loop
// has a way out.
func TestNoPosterOptOut(t *testing.T) {
	tp := tui.ExampleTape()
	_, on, err := prepare(tp, Options{})
	if err != nil {
		t.Fatal(err)
	}
	_, off, err := prepare(tp, Options{NoPoster: true})
	if err != nil {
		t.Fatal(err)
	}
	if on.Count != off.Count+1 {
		t.Errorf("the default clip has %d frames and --no-poster %d, want one more", on.Count, off.Count)
	}
	if off.Frame(0).Mode == tui.ModeCard {
		t.Error("--no-poster still opens on the result")
	}
	if on.Frame(0).Mode != tui.ModeCard {
		t.Error("the default clip does not open on the result")
	}
}

// TestWindowedClipIsStillOneToOne is the whole contract of RunFrom (TTP-90,
// user 2026-09-14: "나는 빨리감기보다 차라리 프리필 마지막 3초 정도만 보여주는게
// 맞다고 생각해"). A window moves where the clip opens and changes nothing
// else: the frames that survive are the frames they were, one frame apart in
// the run exactly as they are in the clip.
//
// FAIL-first: on the source before RunFrom existed the constructor did not
// compile with a window, and adding the field without the Frame arithmetic
// gave a clip that opened at the run's start anyway.
func TestWindowedClipIsStillOneToOne(t *testing.T) {
	const (
		runEnd = 30 * time.Second
		from   = 8 * time.Second
	)
	s := NewSchedule(from, runEnd, DefaultFPS, 0, false)

	// The clip is shorter by exactly what was cut, and by nothing else: the
	// intro is gone because the run has already started (see Schedule).
	if want := (runEnd - from) + CardHold; s.Duration != want {
		t.Errorf("duration = %v, want %v (the window plus the card)", s.Duration, want)
	}
	if s.Intro != 0 {
		t.Errorf("a windowed clip holds a %v intro on a run that has already started", s.Intro)
	}

	first := s.Frame(0)
	if first.At != from || first.Anim != from {
		t.Errorf("the clip opens at run %v/%v, want %v", first.At, first.Anim, from)
	}
	if first.Mode != tui.ModeLive || first.InOpen {
		t.Errorf("the first frame is not the live screen: %+v", first)
	}

	// 1:1 through the stream: a frame of clip time is a frame of run time.
	// clipTimeAt is the schedule's own arithmetic, so this compares against
	// the clip's frame times rather than against a second rounding.
	for i := 1; i < s.Count; i++ {
		f := s.Frame(i)
		if f.Mode == tui.ModeCard {
			break
		}
		if want := from + clipTimeAt(s, i); f.At != want {
			t.Fatalf("frame %d is cut at run %v, want %v", i, f.At, want)
		}
		if f.Anim != f.At {
			t.Fatalf("frame %d animates at %v and is cut at %v", i, f.Anim, f.At)
		}
	}
	if last := s.Frame(s.Count - 1); last.Mode != tui.ModeCard || last.At != runEnd {
		t.Errorf("the clip does not end on the result at %v: %+v", runEnd, last)
	}
}

// TestWindowBeyondTheRunIsTheWholeRun: a lead longer than the wait, a window
// past the run's end, and a negative one all mean "there is nothing to cut".
// The alternative to clamping is a clip that opens after it ends.
func TestWindowBeyondTheRunIsTheWholeRun(t *testing.T) {
	whole := NewSchedule(0, 7*time.Second, DefaultFPS, 0, true)
	for _, from := range []time.Duration{-time.Second, 8 * time.Second} {
		s := NewSchedule(from, 7*time.Second, DefaultFPS, 0, true)
		if s.RunFrom != 0 || s.Duration != whole.Duration || s.Intro != whole.Intro {
			t.Errorf("a window from %v gives {RunFrom %v, %v, intro %v}, want the whole run",
				from, s.RunFrom, s.Duration, s.Intro)
		}
	}
}

// TestRunFromReadsTheFirstToken: the lead is measured from the first token any
// stream produced, not from the first request or the fastest stream's own
// tile, because that is the instant the screen stops waiting.
func TestRunFromReadsTheFirstToken(t *testing.T) {
	tp := tui.ExampleTape()
	first := FirstToken(tp)
	if first <= 0 || first >= RunEnd(tp) {
		t.Fatalf("the example run's first token is at %v, inside a run ending at %v — the fixture changed", first, RunEnd(tp))
	}
	lead := first / 2
	if got := RunFrom(tp, lead); got != first-lead {
		t.Errorf("a %v lead opens at %v, want %v", lead, got, first-lead)
	}
	// No lead asked for is the whole run, and so is a lead nobody waited.
	if got := RunFrom(tp, 0); got != 0 {
		t.Errorf("no lead opens at %v, want the run's start", got)
	}
	if got := RunFrom(tp, first+time.Second); got != 0 {
		t.Errorf("a lead longer than the wait opens at %v, want the run's start", got)
	}
	if got := RunFrom(nil, time.Second); got != 0 {
		t.Errorf("a nil tape opens at %v", got)
	}
}

// TestWindowedExplicitDurationStaysInTheWindow: --duration and a window are
// both allowed and compose the only way they can — the ratio path replays the
// window, not the run. A clip that named ten seconds and replayed the cut
// prefill inside them would be the worst of both.
func TestWindowedExplicitDurationStaysInTheWindow(t *testing.T) {
	const (
		runEnd = 30 * time.Second
		from   = 8 * time.Second
	)
	s := NewSchedule(from, runEnd, DefaultFPS, 20*time.Second, false)
	if s.Frame(0).At != from {
		t.Errorf("the clip opens at %v, want %v", s.Frame(0).At, from)
	}
	// Halfway through the streaming phase is halfway through the window.
	mid := s.Frame(int(float64(s.Count) * float64(s.Intro+s.Stream/2) / float64(s.Duration)))
	if want, tol := from+(runEnd-from)/2, 300*time.Millisecond; mid.At < want-tol || mid.At > want+tol {
		t.Errorf("the middle of the clip is cut at %v, want about %v", mid.At, want)
	}
	if last := s.Frame(s.Count - 1); last.At != runEnd {
		t.Errorf("the clip ends cut at %v, want %v", last.At, runEnd)
	}
}
