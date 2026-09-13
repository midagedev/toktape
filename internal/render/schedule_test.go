package render

import (
	"testing"
	"time"

	"github.com/midagedev/toktape/internal/tui"
)

func TestNewScheduleDerivesDuration(t *testing.T) {
	tests := []struct {
		name    string
		runEnd  time.Duration
		wantDur time.Duration
		wantStr time.Duration // streaming phase
	}{
		{"natural length is kept", 7 * time.Second, 11 * time.Second, 7 * time.Second},
		{"a short run is stretched to the floor", 2 * time.Second, MinDuration, MinDuration - 4*time.Second},
		{"a long run is compressed to the ceiling", 5 * time.Minute, MaxDuration, MaxDuration - 4*time.Second},
		{"exactly at the floor", 6 * time.Second, MinDuration, 6 * time.Second},
		{"exactly at the ceiling", 8 * time.Second, MaxDuration, 8 * time.Second},
		{"an empty run still gets a clip", 0, MinDuration, MinDuration - 4*time.Second},
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
			if s.Intro != IntroHold || s.Card != CardHold {
				t.Errorf("holds = %v/%v, want %v/%v", s.Intro, s.Card, IntroHold, CardHold)
			}
			if got := s.Intro + s.Stream + s.Card; got != s.Duration {
				t.Errorf("phases sum to %v, want %v", got, s.Duration)
			}
		})
	}
}

func TestNewScheduleShortExplicitDuration(t *testing.T) {
	// An explicit duration is used as given — the [10s, 12s] clamp describes
	// the derived default, not the API. Both holds shrink so that a clip too
	// short to hold them still has a streaming phase.
	s := NewSchedule(7*time.Second, 30, 2*time.Second)
	if s.Duration != 2*time.Second {
		t.Fatalf("duration = %v, want 2s", s.Duration)
	}
	if s.Stream <= 0 {
		t.Fatalf("streaming phase = %v, want a positive one", s.Stream)
	}
	if got := s.Intro + s.Stream + s.Card; got != s.Duration {
		t.Errorf("phases sum to %v, want %v", got, s.Duration)
	}
	if s.Card != 3*s.Intro {
		t.Errorf("holds = %v/%v, want the 1:3 ratio kept", s.Intro, s.Card)
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
	lastIntro := s.Frame(int(s.Intro*time.Duration(s.FPS)/time.Second) - 1)
	if lastIntro.At != 0 {
		t.Errorf("last intro frame is cut at %v, want 0", lastIntro.At)
	}

	// Half way through the streaming phase is half way through the run.
	mid := s.Frame(int((s.Intro + s.Stream/2) * time.Duration(s.FPS) / time.Second))
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
