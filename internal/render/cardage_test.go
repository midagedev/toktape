package render

import (
	"testing"
	"time"

	"github.com/midagedev/toktape/internal/tui"
)

// TestCardHoldFramesCarryCardAge is the clock the modal's gleam runs on
// (2026-09-15). Card-hold frames all carry Anim = RunEnd, so the card's own
// elapsed time has to reach the modal by its own field or every frame of the
// hold would draw the same gleam phase — frozen at CardAge 0.
//
// The run is 2 s at 1:1 with a 1 s intro, so the card hold opens at clip
// time 3.0 s and CardHold is 5 s. FAIL-first on the schedule before the
// field was wired: Frame.CardAge existed but Frame never set it, so the
// hold's first frame reported 0 for its mode and its age alike — and the
// frames 0.5 s in reported 0 too.
func TestCardHoldFramesCarryCardAge(t *testing.T) {
	for _, fps := range []int{15, 30} {
		s := NewSchedule(0, 2*time.Second, fps, 0, false)
		if s.Card != CardHold {
			t.Fatalf("fps %d: derived card hold is %v, want %v", fps, s.Card, CardHold)
		}
		start := s.Open + s.Intro + s.Stream

		// The frame at the hold's opening instant: the card has just appeared.
		i0 := int(start.Seconds()) * fps
		if f := s.Frame(i0); f.Mode != tui.ModeCard || f.CardAge != 0 {
			t.Errorf("fps %d frame %d: mode %v CardAge %v, want the card just appeared (ModeCard, 0)", fps, i0, f.Mode, f.CardAge)
		}

		// Half a second in. 30 fps lands a frame on 3.5 s exactly; 15 fps
		// does not — 0.5 s is 7.5 of its frames — so the pinned frame is the
		// one 7 frames in, at 7/15 s, which is the mapping the field promises
		// (age = clip time − hold start) rather than a value a 15 fps clip
		// cannot produce.
		if fps == 30 {
			f := s.Frame(i0 + 15)
			if f.CardAge != 500*time.Millisecond {
				t.Errorf("fps 30, 0.5 s into the hold: CardAge %v, want 500ms", f.CardAge)
			}
		} else {
			f := s.Frame(i0 + 7)
			if want := 7 * time.Second / 15; f.CardAge != want {
				t.Errorf("fps 15, 7 frames into the hold: CardAge %v, want %v", f.CardAge, want)
			}
		}

		// The last frame of the clip sits at the hold's far end: its age is
		// the whole hold, and a frame asked for past the end clamps there
		// rather than ageing past it.
		last := s.Frame(s.Count - 1)
		if last.CardAge != s.Card {
			t.Errorf("fps %d: the last frame's CardAge is %v, want the whole hold %v", fps, last.CardAge, s.Card)
		}
		if beyond := s.Frame(s.Count + 5); beyond.CardAge != s.Card {
			t.Errorf("fps %d: a frame past the end has CardAge %v, want it clamped to %v", fps, beyond.CardAge, s.Card)
		}

		// A streaming frame is not on the card; its CardAge stays zero so a
		// gleam cannot start early.
		if f := s.Frame(int(1.5 * float64(fps))); f.Mode == tui.ModeCard || f.CardAge != 0 {
			t.Errorf("fps %d: a mid-run frame carries mode %v CardAge %v, want live and 0", fps, f.Mode, f.CardAge)
		}
	}
}

// TestPosterFrameIsSettled pins the thumbnail's side of the gleam
// (2026-09-15): the poster frame is the still a feed shows before anyone
// presses play, so it must be the settled card — CardAge at or past
// GleamSweep, never mid-sweep — while every frame after it keeps the phase it
// always had. FAIL-first on the schedule before the field was wired: the
// poster reported CardAge 0, the first frame of a sweep, exactly the state
// this test forbids for the thumbnail.
func TestPosterFrameIsSettled(t *testing.T) {
	s := NewSchedule(0, 2*time.Second, 30, 0, false).WithPoster()
	f := s.Frame(0)
	if f.Mode != tui.ModeCard {
		t.Fatalf("the poster frame's mode is %v, want ModeCard", f.Mode)
	}
	if f.CardAge < tui.GleamSweep {
		t.Errorf("the poster frame's CardAge is %v, want >= GleamSweep (%v): the thumbnail must be settled, never mid-sweep", f.CardAge, tui.GleamSweep)
	}
	// The first frame of the clip proper is the schedule's own, unaffected by
	// the poster in front of it (see WithPoster) — and it is live, so no age.
	next := s.Frame(1)
	if next.Mode == tui.ModeCard || next.CardAge != 0 {
		t.Errorf("the frame after the poster is mode %v CardAge %v, want live and 0", next.Mode, next.CardAge)
	}
}
