package render

import (
	"testing"
	"time"

	"github.com/midagedev/toktape/internal/tui"
)

// holds is the fixed part of every clip: the cold open and the two holds.
const holds = OpenHold + IntroHold + CardHold

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
		{"exactly at the ceiling", MaxStream, MaxDuration, MaxStream},
		{"a second past the ceiling is compressed into it", MaxStream + time.Second, MaxDuration, MaxStream},
		{"a long run is compressed into the ceiling", 5 * time.Minute, MaxDuration, MaxStream},
		{"an empty run is the holds and nothing else", 0, MinDuration, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := NewSchedule(tc.runEnd, DefaultFPS, 0)
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
	for _, runEnd := range []time.Duration{6600 * time.Millisecond, 25 * time.Second, MaxStream} {
		s := NewSchedule(runEnd, DefaultFPS, 0)
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

func TestScheduleCompressesOnlyPastTheCeiling(t *testing.T) {
	// Above MaxStream the phase is capped and the run is squeezed into it
	// linearly — the only case where clip time and run time run at different
	// speeds by default.
	const runEnd = 2 * time.Minute
	s := NewSchedule(runEnd, DefaultFPS, 0)
	if s.Stream != MaxStream || s.Duration != MaxDuration {
		t.Fatalf("a %v run gives stream %v of a %v clip, want %v of %v", runEnd, s.Stream, s.Duration, MaxStream, MaxDuration)
	}
	mid := s.Frame(int((s.Open + s.Intro + s.Stream/2) * time.Duration(s.FPS) / time.Second))
	if want, tol := runEnd/2, time.Second; mid.At < want-tol || mid.At > want+tol {
		t.Errorf("half way through the phase is cut at %v, want ~%v", mid.At, want)
	}
	// Four times life, and the animation clock follows the run so the frames
	// are the ones the operator saw.
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
		s := NewSchedule(7*time.Second, DefaultFPS, want)
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
	s := NewSchedule(7*time.Second, 30, 2*time.Second)
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
	s := NewSchedule(7*time.Second, 30, 0)
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
	short := NewSchedule(7*time.Second, 30, 2*time.Second)
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
		s := NewSchedule(7400*time.Millisecond, fps, 0)
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
	s := NewSchedule(7*time.Second, 30, 0)

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
		s := NewSchedule(runEnd, 30, 0)
		if got := (s.introLead + s.Intro) % shimmerPeriod; got != 0 {
			t.Errorf("runEnd %v: hand-over at %v into a shimmer cycle, want 0", runEnd, got)
		}
		if s.introLead < 0 {
			t.Errorf("runEnd %v: negative intro lead %v", runEnd, s.introLead)
		}
	}
}

func TestScheduleFrameIndexIsClamped(t *testing.T) {
	s := NewSchedule(7*time.Second, 30, 0)
	if got := s.Frame(-5); got.Index != 0 {
		t.Errorf("Frame(-5).Index = %d, want 0", got.Index)
	}
	if got := s.Frame(s.Count + 100); got.Index != s.Count-1 {
		t.Errorf("Frame(past the end).Index = %d, want %d", got.Index, s.Count-1)
	}
}

func TestScheduleFramesAreMonotonic(t *testing.T) {
	s := NewSchedule(7*time.Second, 30, 0)
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
