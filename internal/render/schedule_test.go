package render

import (
	"testing"
	"time"

	"github.com/midagedev/toktape/internal/tui"
)

// holds is the fixed part of every clip: the cold open and the two holds.
const holds = OpenHold + IntroHold + CardHold

func TestNewScheduleDerivesDuration(t *testing.T) {
	tests := []struct {
		name    string
		runEnd  time.Duration
		wantDur time.Duration
		wantStr time.Duration // streaming phase
	}{
		{"natural length is kept", 10 * time.Second, 21 * time.Second, 10 * time.Second},
		{"a short run is stretched to the floor", 2 * time.Second, MinDuration, MinDuration - holds},
		{"a long run is compressed to the ceiling", 5 * time.Minute, MaxDuration, MaxDuration - holds},
		{"exactly at the floor", 9 * time.Second, MinDuration, 9 * time.Second},
		{"exactly at the ceiling", 14 * time.Second, MaxDuration, 14 * time.Second},
		{"an empty run still gets a clip", 0, MinDuration, MinDuration - holds},
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
		s := NewSchedule(7*time.Second, fps, 0)
		want := fps * int(s.Duration/time.Second)
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
