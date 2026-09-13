package render

import (
	"time"

	"github.com/midagedev/toktape/internal/tui"
)

// The shape of every clip: a held opening, the run, a held card.
const (
	// IntroHold is the opening: the screen before the first token, so a
	// viewer reads the rig and the model before anything moves.
	IntroHold = 1 * time.Second
	// CardHold is the payoff: the result card, held long enough to read the
	// two hero numbers and screenshot it.
	CardHold = 3 * time.Second
	// MinDuration and MaxDuration bound the derived clip length. Under ten
	// seconds the streaming phase is too short to show a stall; over twelve
	// the clip stops being scrollable-past-able on a feed.
	MinDuration = 10 * time.Second
	MaxDuration = 12 * time.Second

	// holdShare caps the two held phases at four fifths of a short clip, so
	// an explicitly requested two-second clip still has a streaming phase.
	holdShare = 5
)

// shimmerPeriod is one pass of the header highlight — tui's shimmerDur, which
// is unexported. It is used for one purpose: to choose the intro's animation
// clock so that it lands on a cycle boundary exactly when the run starts (see
// Schedule.Frame). If tui ever changes the constant, the intro hands over with
// a small phase hop instead of seamlessly; nothing else breaks.
const shimmerPeriod = 4 * time.Second

// Frame is one frame of the clip: when it is shown, what instant of the run it
// draws, and which clock its animation runs on.
type Frame struct {
	// Index is the frame number, from 0.
	Index int
	// Clip is the frame's time in the finished clip — the asciicast event
	// time and the position in the GIF.
	Clip time.Duration
	// At is the instant of the *run* the model is cut at.
	At time.Duration
	// Anim is the t handed to tui.View.
	Anim time.Duration
	// Mode is the screen the frame draws.
	Mode tui.Mode
}

// Schedule maps frame indices onto the run's timeline.
//
// A clip is three phases: IntroHold on the pre-run screen, the run itself
// stretched or compressed uniformly into whatever is left, and CardHold on the
// result card. The middle phase is the only one that scales, so a two-minute
// run and a four-second run produce clips of the same shape — which is what
// makes two clips comparable at a glance.
//
// Two clocks, on purpose. tui.View's t is both an animation phase and a data
// cut: sampleAt(t) and decodeRateAt(t) read it, and ease(prev, cur, since, t)
// measures against sample timestamps that are in *run* time. So during the
// streaming phase Anim is the run time, which reproduces the frames the
// operator actually saw — even where that means a compressed run animates
// faster than life. During the intro the model is frozen at zero and holds no
// sample to ease and no token to rate, so Anim is free: it runs on clip time,
// offset so that it crosses a whole number of shimmer periods exactly as the
// run begins. The spinner (800 ms) and the header shimmer (4 s) both divide
// that offset, so the handover from the intro's clock to the run's clock is
// continuous rather than a visible jump.
type Schedule struct {
	// Duration is the length of the finished clip.
	Duration time.Duration
	// FPS is the frame rate.
	FPS int
	// Count is the number of frames, both endpoints included.
	Count int
	// RunEnd is the instant the run's last token arrived.
	RunEnd time.Duration
	// Intro, Stream and Card are the three phases, summing to Duration.
	Intro, Stream, Card time.Duration

	// introLead is added to clip time during the intro; see the type comment.
	introLead time.Duration
}

// NewSchedule plans a clip of a run that ends at runEnd.
//
// dur of zero derives the length: the natural one-second-plus-run-plus-three
// clip, clamped to [MinDuration, MaxDuration]. A run longer than that is
// compressed uniformly; a shorter one plays in slow motion, which is the right
// reading of a run too fast to watch. A non-zero dur is used as given — the
// clamp describes the default, not the API.
func NewSchedule(runEnd time.Duration, fps int, dur time.Duration) Schedule {
	if runEnd < 0 {
		runEnd = 0
	}
	if fps <= 0 {
		fps = DefaultFPS
	}
	if dur <= 0 {
		dur = IntroHold + runEnd + CardHold
		if dur < MinDuration {
			dur = MinDuration
		}
		if dur > MaxDuration {
			dur = MaxDuration
		}
	}

	// Snap the clip to a whole number of frames. A derived length is almost
	// never one (a seven-and-a-bit-second run gives an eleven-and-a-bit-second
	// clip), and a trailing part-frame would make the last frame shorter than
	// every other one and the clip's length disagree with count ÷ fps.
	frames := (dur*time.Duration(fps) + time.Second/2) / time.Second
	if frames < 1 {
		frames = 1
	}
	// Same expression Frame uses, so the last frame's clip time is the
	// duration exactly rather than a nanosecond either side of it.
	dur = frames * time.Second / time.Duration(fps)

	// The holds are fixed until the clip is short enough that they would eat
	// it, at which point both shrink in proportion and keep their 1:3 ratio.
	intro, card := IntroHold, CardHold
	if room := dur / holdShare; intro > room {
		intro, card = room, 3*room
	}
	s := Schedule{
		Duration: dur,
		FPS:      fps,
		RunEnd:   runEnd,
		Intro:    intro,
		Card:     card,
		Stream:   dur - intro - card,
	}
	s.Count = int(frames) + 1

	// Land the intro's animation clock on a shimmer boundary at hand-over.
	periods := (intro + shimmerPeriod - 1) / shimmerPeriod
	s.introLead = periods*shimmerPeriod - intro

	return s
}

// Frame returns frame i. Indices outside [0, Count) are clamped, so a caller
// that miscounts gets the first or last frame rather than a panic.
func (s Schedule) Frame(i int) Frame {
	if i < 0 {
		i = 0
	}
	if s.Count > 0 && i >= s.Count {
		i = s.Count - 1
	}
	clip := time.Duration(i) * time.Second / time.Duration(s.FPS)
	if clip > s.Duration {
		clip = s.Duration
	}
	f := Frame{Index: i, Clip: clip, Mode: tui.ModeLive}

	switch {
	case clip < s.Intro:
		f.At = 0
		f.Anim = clip + s.introLead
	case clip < s.Intro+s.Stream && s.Stream > 0:
		p := float64(clip-s.Intro) / float64(s.Stream)
		f.At = time.Duration(float64(s.RunEnd) * p)
		f.Anim = f.At
	default:
		f.At = s.RunEnd
		f.Anim = s.RunEnd
		f.Mode = tui.ModeCard
	}
	return f
}

// Frames returns every frame of the schedule in order.
func (s Schedule) Frames() []Frame {
	out := make([]Frame, s.Count)
	for i := range out {
		out[i] = s.Frame(i)
	}
	return out
}
