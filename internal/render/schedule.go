package render

import (
	"time"

	"github.com/midagedev/toktape/internal/tui"
)

// The shape of every clip: the cold open, a held intro, the run, a held card.
const (
	// OpenHold is the cold open: a shell prompt, the command typed into it,
	// and the tool finding the server and attaching — everything before the
	// TUI takes the screen (user, 2026-09-13: "뭔가 셋팅하는 것부터
	// 시작되면 더 좋을 것 같고"). A clip that starts on the finished screen
	// reads as a mock-up; one that starts on an empty prompt reads as a
	// session, and a reader who has never run the tool learns the command
	// from the clip. open.go draws it and owns the beats inside it.
	//
	// It is opt-in (Options.ColdOpen, 2026-09-14): the README hero sells the
	// tool, so it opens on the command; a clip of someone's own run is shared
	// for its numbers, and opens on the screen (user: "실 공유는 시작이
	// 메인화면이어야").
	OpenHold = 6 * time.Second
	// IntroHold is the opening: the screen before the first token, so a
	// viewer reads the rig and the model before anything moves.
	IntroHold = 1 * time.Second
	// CardHold is the payoff: the result card, held long enough to read the
	// two hero numbers and screenshot it.
	CardHold = 5 * time.Second
	// MinDuration is the shortest a derived clip without the cold open can
	// be: a run with no tokens at all, the two holds and nothing between them.
	// A clip with the cold open is OpenHold longer. There is no ceiling: the
	// run plays at 1:1 however long it is (user, 2026-09-13: "토큰 생성하는
	// 화면을 충분히 살펴보기에 재생 시간이 너무 짧아"; 2026-09-14, on the
	// thirty-second cap that compressed longer runs: "꼭 고정된 시간일 필요
	// 없지 않니"). A caller who wants a shorter clip names a Duration.
	MinDuration = IntroHold + CardHold

	// holdShare caps the three held phases at four fifths of a short clip, so
	// an explicitly requested two-second clip still has a streaming phase. It
	// applies only to an explicit Options.Duration; a derived clip is built
	// around the holds and can never be too short for them.
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
	// Anim is the t handed to tui.View, or the open's own clock when InOpen.
	Anim time.Duration
	// CardAge is how long the frame's result modal has been on screen: how
	// far into the card hold the frame's clip time is, clamped to the hold.
	// Zero everywhere except card frames, where it drives the modal's one
	// gleam pass (tui.GleamSweep) — a clock of the card's own, deliberately
	// not Anim: every frame of the hold carries the same Anim (RunEnd), so a
	// gleam driven off Anim would be frozen, and the GIF encoder would drop
	// the hold's frames as identical anyway. The poster frame is the settled
	// end (≥ GleamSweep), never mid-sweep: it is the thumbnail.
	CardAge time.Duration
	// Mode is the screen the frame draws. Open frames are not a tui.Mode —
	// the cold open is not the TUI — so Mode stays ModeLive there and InOpen
	// is what a renderer branches on.
	Mode tui.Mode
	// InOpen marks a frame of the cold open, drawn by OpenScreen rather than
	// by tui.View.
	InOpen bool
	// Open is the instant of the cold open the frame draws, on the open's own
	// nominal OpenHold-long clock. A clip too short to hold a six-second open
	// gets a shorter one, and this rescaling is what lets the whole script
	// still play inside it. Meaningless unless InOpen.
	Open time.Duration
}

// Schedule maps frame indices onto the run's timeline.
//
// A clip is four phases: OpenHold on the cold open, IntroHold on the pre-run
// screen, the run, and CardHold on the result card. The streaming phase is the
// only one whose length depends on the tape: it is the run itself, played at
// 1:1, and a clip is therefore as long as its run needs.
//
// 1:1 is the whole point. Compressing a run to fit a fixed budget makes the
// tokens fly, and a viewer cannot see whether a stream stalled, whether the
// tiles are in step, or what the decode rate actually feels like — which is
// the thing the clip exists to show. A short run gives a short clip; it is not
// stretched to fill a floor, because slow motion is a lie about the machine.
//
// A window, never a fast-forward. RunFrom cuts the front of the run out of the
// clip rather than speeding it up (user, 2026-09-14, on a hero whose first ten
// seconds are a spinner: "나는 빨리감기보다 차라리 프리필 마지막 3초 정도만
// 보여주는게 맞다고 생각해"). Every frame that survives is still the frame the
// operator saw at that instant, at 1:1; what changes is where the clip opens.
// Nothing has to label the cut either, because the screen carries it: the
// tile's own clock is measured from the run's start, so a clip that opens at
// RunFrom opens on "7/30s" rather than "0/30s", and the seconds that are not
// in the clip are on screen from its first frame. That is the honest form of
// the thing --duration does dishonestly.
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
//
// A windowed clip has no intro at all, and that is not a saving: the intro is
// a frozen screen standing in for a run that has not started, and at RunFrom
// the run has started. Its screen already shows the rig, the model and a
// turning spinner, on the one clock that can ease samples. Keeping a second
// frozen second in front of it would either stop the spinner dead or run two
// clocks against each other, so NewSchedule drops it: Intro > 0 and
// RunFrom > 0 never hold together.
//
// The cold open is outside all of that: it never touches the TUI, so it runs
// on a third clock of its own (Frame.Open) and hands over with a hard cut —
// which is what a program taking the screen looks like.
type Schedule struct {
	// Duration is the length of the finished clip.
	Duration time.Duration
	// FPS is the frame rate.
	FPS int
	// Count is the number of frames, both endpoints included.
	Count int
	// RunFrom and RunEnd are the part of the run the clip plays: RunFrom is
	// the instant the streaming phase opens at, RunEnd the instant the run's
	// last token arrived. RunFrom is 0 for a whole-run clip, which is the
	// default and every clip until one asks for a window.
	RunFrom, RunEnd time.Duration
	// Open, Intro, Stream and Card are the four phases, summing to Duration.
	Open, Intro, Stream, Card time.Duration

	// introLead is added to clip time during the intro; see the type comment.
	introLead time.Duration
	// poster marks a clip whose first frame is the result screen; see
	// WithPoster.
	poster bool
	// oneToOne marks the ordinary case: the streaming phase is the run, at
	// its own speed, and Frame maps it by subtraction rather than by a ratio.
	// It is not the same as Stream == RunEnd — snapping the clip to a whole
	// number of frames leaves Stream a fraction of a frame longer — and the
	// difference matters, because a ratio close to but not exactly one drifts
	// the animation clock away from the run clock over a long stream.
	oneToOne bool
}

// NewSchedule plans a clip of the run between runFrom and runEnd. open puts
// the cold open in front of it; without it the clip starts on the live screen.
//
// runFrom of zero is the whole run and is what almost every clip passes. A
// non-zero one is a window: the clip opens with the run already that far in,
// at 1:1 like every other frame, and the intro is dropped (see the type
// comment). It is clamped into [0, runEnd], so a window past the run's end is
// the whole run rather than an empty clip.
//
// dur of zero derives the length, which is the default and the interesting
// case: the cold open if asked for, the intro, the run at 1:1, and the card
// hold. There is no floor and no ceiling on the result beyond what those
// four add up to — see MinDuration, which is that arithmetic and not a clamp.
//
// A non-zero dur is an explicit override and is honoured exactly: the run is
// compressed or stretched into whatever the holds leave, and if the request is
// too short to hold them the holds shrink in proportion too. That is the path
// a test renders a two-second clip on; it is not how a clip anyone watches is
// built.
func NewSchedule(runFrom, runEnd time.Duration, fps int, dur time.Duration, open bool) Schedule {
	if runEnd < 0 {
		runEnd = 0
	}
	if runFrom < 0 || runFrom > runEnd {
		runFrom = 0
	}
	if fps <= 0 {
		fps = DefaultFPS
	}
	openHold := time.Duration(0)
	if open {
		openHold = OpenHold
	}
	// The intro is the screen before the run starts, and a windowed clip opens
	// with it already started — see the type comment.
	introHold := IntroHold
	if runFrom > 0 {
		introHold = 0
	}
	derived := dur <= 0
	oneToOne := derived
	if derived {
		dur = openHold + introHold + (runEnd - runFrom) + CardHold
	}

	// Snap the clip up to a whole number of frames. A derived length almost
	// never is one (a seven-and-a-bit-second run gives a nineteen-and-a-bit-
	// second clip), and a trailing part-frame would make the last frame
	// shorter than every other one and the clip's length disagree with
	// count ÷ fps. Up rather than to-nearest: rounding down would leave the
	// streaming phase a few milliseconds short of the run and drop the last
	// token or two into the cut to the card.
	frames := (dur*time.Duration(fps) + time.Second - 1) / time.Second
	if frames < 1 {
		frames = 1
	}
	// Same expression Frame uses, so the last frame's clip time is the
	// duration exactly rather than a nanosecond either side of it.
	dur = frames * time.Second / time.Duration(fps)

	// The three held phases are fixed until an explicitly requested clip is
	// short enough that they would eat it, at which point all three shrink in
	// proportion and keep their 6:1:5 ratio. Frame rescales the open's own
	// clock by the same factor, so a squeezed clip plays the whole cold open
	// faster rather than cutting it off half way through the typing. A derived
	// clip is built around the holds and never reaches this.
	//
	// The arithmetic goes through float64 because the exact form
	// (room × OpenHold / holds) overflows int64 at these magnitudes: six
	// seconds is 6e9 nanoseconds and the product is past 9.2e18.
	open_, intro, card := openHold, introHold, CardHold
	if holds, room := open_+intro+card, dur-dur/holdShare; !derived && holds > room {
		share := func(d time.Duration) time.Duration {
			return time.Duration(float64(room) * float64(d) / float64(holds))
		}
		open_, intro, card = share(openHold), share(IntroHold), share(CardHold)
	}
	s := Schedule{
		Duration: dur,
		FPS:      fps,
		RunFrom:  runFrom,
		RunEnd:   runEnd,
		Open:     open_,
		Intro:    intro,
		Card:     card,
		// The streaming phase takes the rounding: the four phases must sum to
		// the duration exactly or the last frame lands in the wrong one. In
		// the 1:1 case this leaves it a fraction of a frame longer than the
		// run, which Frame absorbs by clamping At.
		Stream:   dur - open_ - intro - card,
		oneToOne: oneToOne,
	}
	s.Count = int(frames) + 1

	// Land the intro's animation clock on a shimmer boundary at hand-over.
	periods := (intro + shimmerPeriod - 1) / shimmerPeriod
	s.introLead = periods*shimmerPeriod - intro

	return s
}

// WithPoster puts the run's result on the front of the clip, as one frame
// before everything else (user, 2026-09-14: "랜더링 할때 첫프레임을 결과 화면으로
// 넣을 수 있게 해줘", then "이걸 기본 모드로 하고").
//
// The first frame is what a still of a clip is: the thumbnail X, Reddit and
// Slack show before anyone presses play, the poster of an <video>, and the
// image a GIF holds while the rest of it loads. Without this that still is an
// empty terminal at a shell prompt, which says nothing about the run — the
// figures a reader is being shown the clip for arrive thirty seconds later.
// With it the still is the result: two rates, the rig, where the model sits.
//
// The cost is one frame, and on a looping GIF it is paid once per loop as a
// flash of the ending before the clip restarts. That is the trade: a still
// that means something, against a sixty-seventh of a second of the end shown
// out of order. Options.NoPoster turns it off for a caller who would rather
// have the loop clean.
//
// It is a frame in front of the schedule rather than a change to it: every
// other frame keeps the phase it had, and the run still plays at 1:1.
func (s Schedule) WithPoster() Schedule {
	if s.poster || s.FPS <= 0 {
		return s
	}
	s.poster = true
	s.Count++
	// From the count, not by adding a frame's duration: Second/FPS truncates
	// (at 30 fps a frame is 33333333ns and three of them are a nanosecond
	// short of 100ms), and a clip whose length disagreed with its frame times
	// by a nanosecond would put the last frame in the wrong phase.
	s.Duration = time.Duration(s.Count-1) * time.Second / time.Duration(s.FPS)
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

	// The poster is the result, drawn before the clip proper starts. Every
	// frame after it is the schedule's own, one frame later in the finished
	// clip but on the same phase it always had — which is why the switch
	// below reads `at` and not `clip`.
	at := clip
	if s.poster {
		if i == 0 {
			// The poster is the card's settled state: the still a feed holds
			// before play, never a frame mid-sweep (see Frame.CardAge).
			f.At, f.Anim, f.Mode, f.CardAge = s.RunEnd, s.RunEnd, tui.ModeCard, tui.GleamSweep
			return f
		}
		// The clip time this frame would have had without the poster, from
		// the index rather than by subtracting a frame — see WithPoster.
		at = time.Duration(i-1) * time.Second / time.Duration(s.FPS)
	}

	switch {
	case at < s.Open && s.Open > 0:
		f.InOpen = true
		f.Open = time.Duration(float64(at) * float64(OpenHold) / float64(s.Open))
		f.At = s.RunFrom
		f.Anim = f.Open
	case at < s.Open+s.Intro:
		// Only ever reached by a whole-run clip, where RunFrom is 0 and the
		// pre-run screen is the run's own zero — NewSchedule drops the intro
		// from a windowed one, which is why this can read its clock off clip
		// time without the two disagreeing.
		f.At = s.RunFrom
		f.Anim = at - s.Open + s.introLead
	case at < s.Open+s.Intro+s.Stream && s.Stream > 0:
		if s.oneToOne {
			// Exactly clip − start, offset to the window: integer subtraction,
			// not a ratio, so the run clock and the clip clock stay locked to
			// the nanosecond and the sparklines scroll at the speed the
			// operator saw. The clamp covers the part-frame the snapping added
			// past the run's end.
			f.At = s.RunFrom + at - s.Open - s.Intro
			if f.At > s.RunEnd {
				f.At = s.RunEnd
			}
		} else {
			p := float64(at-s.Open-s.Intro) / float64(s.Stream)
			f.At = s.RunFrom + time.Duration(float64(s.RunEnd-s.RunFrom)*p)
		}
		f.Anim = f.At
	default:
		f.At = s.RunEnd
		f.Anim = s.RunEnd
		f.Mode = tui.ModeCard
		// The card's own age, from this frame's place in the hold: Anim is
		// the same RunEnd on every card frame, so this is the only clock a
		// one-pass gleam can run on. Clamped at both ends — a frame the
		// snapping pushed past the hold's far end does not age past it.
		f.CardAge = at - (s.Open + s.Intro + s.Stream)
		if f.CardAge < 0 {
			f.CardAge = 0
		}
		if f.CardAge > s.Card {
			f.CardAge = s.Card
		}
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
