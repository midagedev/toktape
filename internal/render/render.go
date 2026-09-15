// Package render turns a tape into the clip you post: an asciicast, a PNG
// frame sequence, a GIF and an mp4 — all without a terminal.
//
// One renderer draws all four. Every frame is tui.View of a tui.Model cut out
// of the tape at an instant, so the clip is the screen the operator watched,
// not a re-imagining of it (docs/toktape-spec.ko.md §1 decision 10). Nothing
// here reads the wall clock: a frame is a function of the tape and the frame
// index, so re-rendering the same tape twice produces the same bytes.
//
// The pipeline has three stages and each one is a pure function of the stage
// before it:
//
//	tape ──Schedule──▶ frame text (ANSI) ──raster──▶ *image.RGBA ──▶ GIF | mp4
//
// The middle stage is a small terminal emulator: it parses the SGR colours
// tui.View emits into a cell grid and draws that grid with the same two faces
// the PNG card uses (assets/fonts). Box-drawing, block-element and braille
// runes are drawn as geometry rather than as glyphs — see glyph.go for why.
package render

import (
	"fmt"
	"time"

	"github.com/midagedev/toktape/internal/tape"
	"github.com/midagedev/toktape/internal/tui"
)

// Defaults for Options. The frame size is the one the TUI is designed around
// (tui.MinWidth × tui.MinHeight is the floor; 120×36 is the shape the panes
// were laid out in). It is the default for the GIF, the asciicast and
// FrameImage; the mp4 and the frame sequence default to VideoWidth×VideoHeight.
const (
	DefaultWidth  = 120
	DefaultHeight = 36
	DefaultFPS    = 30
	// DefaultFontSize is the cell size of a frame in pixels. JetBrains Mono's
	// advance is exactly 0.6 em, so 20 px gives a 12×27 cell with no
	// fractional column to accumulate rounding across 120 of them.
	DefaultFontSize = 20
	// GIFFontSize is the cell size GIF uses when Options.FontSize is unset.
	//
	// A GIF is posted inline and has to stay attachable. Measured 2026-09-13
	// on the twenty-second example clip — the one with the cold open — at
	// 30 fps:
	//
	//	cell 10  744×532  0.85 MB
	//	cell 12  868×646  1.06 MB
	//	cell 13  992×684  1.22 MB   <- here
	//	cell 14  992×760  1.30 MB
	//
	// 13 gives a 992 px frame, which is a Reddit embed at full width rather
	// than a thumbnail, and still leaves a fifth of the 1.5 MB budget spare.
	// 14 is not wider — the advance rounds to the same integer number of
	// pixels — so the extra 80 kB buys nothing but a taller frame. The mp4
	// keeps DefaultFontSize; it has no such ceiling.
	GIFFontSize = 13

	// VideoWidth and VideoHeight are the frame size MP4 and Frames use when
	// Options leaves it unset (TTP-41, user 2026-09-13: "영상의 기본 화면
	// 컬럼 라인수를 좀 늘리는게 좋겠지?"). A cell is 12×27 px at
	// DefaultFontSize and the frame adds 48×54 px of padding, so 156×38 cells
	// is exactly 1920×1080 — 16:9 at a size X, Reddit and YouTube play without
	// scaling. Measured on the four-stream example at 12 s: 120×36 is
	// 1488×1026, 144×40 is 1776×1134, 160×45 is 1968×1270. The GIF keeps
	// DefaultWidth×DefaultHeight: it is posted inline, has a 1.5 MB budget and
	// has to stay legible on a phone (see GIFFontSize).
	VideoWidth  = 156
	VideoHeight = 38
)

// Options configure a clip. The zero value is valid and means "every default".
type Options struct {
	// Width and Height are the terminal size in cells. Below
	// tui.MinWidth×tui.MinHeight the TUI draws its "make the window bigger"
	// frame, so the renderer refuses those sizes instead. Zero means
	// VideoWidth×VideoHeight in MP4 and Frames and DefaultWidth×DefaultHeight
	// everywhere else.
	Width, Height int
	// FPS is the frame rate of the clip.
	FPS int
	// Duration is the length of the finished clip. Zero derives it from the
	// run (see NewSchedule); a non-zero value is used as given, which is how
	// a test asks for a two-second clip.
	Duration time.Duration
	// FontSize is the pixel height of one cell for the raster paths (Frames,
	// GIF, MP4). Zero means DefaultFontSize, except in GIF where it means
	// GIFFontSize.
	FontSize float64
	// TapePath is printed in the answer pane's footer ("✓ tape saved <path>")
	// once the run is done. Empty prints "run complete", which is what an
	// unsaved run shows.
	TapePath string
	// ColdOpen puts the shell prompt, the command being typed and the attach
	// in front of the TUI (OpenHold). Off, the clip opens on the live screen
	// at the run's start: a clip of a run is shared for its numbers, and the
	// hero is the one clip that has to teach the command.
	ColdOpen bool
	// PrefillLead opens the clip this long before the first token instead of
	// at the run's start, cutting the rest of the prefill wait out of it.
	//
	// Zero, the default, plays the whole run. It is opt-in because the wait is
	// part of what a run cost and a clip of someone's own run should show what
	// it cost; it is offered because a clip that sells the tool has to reach
	// the tokens. What it never does is speed anything up — see Schedule.
	PrefillLead time.Duration
	// NoPoster drops the result frame from the front of the clip.
	//
	// The poster is on by default (Schedule.WithPoster): the first frame is
	// the still every platform shows before anyone presses play, and an empty
	// terminal is a poor advertisement for a clip whose whole point is the
	// figures at its end. The cost is a one-frame flash of the ending each
	// time a GIF loops, and this is the way out for a caller who minds it.
	NoPoster bool
	// Timestamp is the asciicast header's recording time. Zero uses the run's
	// own start time, which keeps the header reproducible.
	Timestamp time.Time
}

// withDefaults returns o with every unset field filled in.
func (o Options) withDefaults() Options {
	if o.Width == 0 {
		o.Width = DefaultWidth
	}
	if o.Height == 0 {
		o.Height = DefaultHeight
	}
	if o.FPS <= 0 {
		o.FPS = DefaultFPS
	}
	if o.FontSize <= 0 {
		o.FontSize = DefaultFontSize
	}
	return o
}

// withVideoDefaults is withDefaults for the video paths (MP4, Frames): an
// unset size is VideoWidth×VideoHeight rather than the GIF's
// DefaultWidth×DefaultHeight. A size the caller gave is used as given.
func (o Options) withVideoDefaults() Options {
	if o.Width == 0 {
		o.Width = VideoWidth
	}
	if o.Height == 0 {
		o.Height = VideoHeight
	}
	return o.withDefaults()
}

// validate rejects a geometry the TUI cannot draw. A frame smaller than the
// layout's floor renders as a one-line apology, and a clip of 330 apologies is
// worse than an error.
func (o Options) validate() error {
	if o.Width < tui.MinWidth || o.Height < tui.MinHeight {
		return fmt.Errorf("render: frame %d×%d is below the %d×%d the TUI lays out for",
			o.Width, o.Height, tui.MinWidth, tui.MinHeight)
	}
	if o.FPS > 120 {
		return fmt.Errorf("render: %d fps is past anything a clip needs", o.FPS)
	}
	return nil
}

// prepare fills the defaults in, validates them and builds the schedule.
func prepare(tp *tape.Tape, o Options) (Options, Schedule, error) {
	o = o.withDefaults()
	if err := o.validate(); err != nil {
		return o, Schedule{}, err
	}
	sched := NewSchedule(RunFrom(tp, o.PrefillLead), RunEnd(tp), o.FPS, o.Duration, o.ColdOpen)
	if !o.NoPoster {
		sched = sched.WithPoster()
	}
	return o, sched, nil
}

// RunEnd is when the last token of the run arrived.
//
// It counts tokens and nothing else, the same rule tui.ModelAt applies, so the
// instant the schedule stops streaming at is the instant the model reports the
// run finished. A tape whose samples run past its last token still ends with
// the token.
func RunEnd(tp *tape.Tape) time.Duration {
	if tp == nil {
		return 0
	}
	var end time.Duration
	for _, req := range tp.Requests {
		if n := len(req.Tokens); n > 0 {
			if e := req.StartedAt + req.Tokens[n-1].T; e > end {
				end = e
			}
		}
	}
	return end
}

// FirstToken is when the first token of the run arrived — the earliest over
// every stream, so it is the instant the screen stopped waiting rather than
// the instant any one tile did.
//
// It is RunEnd's mirror and is read the same way: tokens and nothing else. A
// run that produced none has no first token and this is 0, which reads as "no
// wait to skip" at every call site.
func FirstToken(tp *tape.Tape) time.Duration {
	if tp == nil {
		return 0
	}
	first := time.Duration(0)
	for _, req := range tp.Requests {
		if len(req.Tokens) == 0 {
			continue
		}
		if at := req.StartedAt + req.Tokens[0].T; first == 0 || at < first {
			first = at
		}
	}
	return first
}

// RunFrom is the instant a clip should open at to show lead of the prefill
// wait before the first token, and 0 for a clip that shows all of it.
//
// The wait is the one part of a run that is worth less than the time it takes
// to watch. On this repo's own hero the first token is ten seconds in — a
// fixed PCIe cost the box pays per prefill, not a rate anything can be read
// off — and a reader who has to sit through it to reach the tokens usually
// does not (user, 2026-09-14: "히어로에 실제 토큰출력까지 기다리는 시간이 너무
// 길다"). lead keeps enough of it to see the spinner turn and the prefill bar
// stand where it is, which is the whole of what those seconds show.
//
// A lead at least as long as the wait is a no-op rather than a negative
// offset: there is nothing to cut, and the clip is the ordinary whole-run one.
func RunFrom(tp *tape.Tape, lead time.Duration) time.Duration {
	if lead <= 0 {
		return 0
	}
	if first := FirstToken(tp); first > lead {
		return first - lead
	}
	return 0
}

// FrameText renders one frame of tp as a block of ANSI-coloured text, exactly
// o.Height lines of o.Width display columns.
//
// This is the only place the tape meets the TUI. Everything downstream —
// asciicast events, PNG frames, GIF frames, mp4 input — is this string.
//
// A frame of the cold open comes from OpenScreen instead: it is a terminal
// before the tool has taken the screen, so there is no model to cut.
func FrameText(tp *tape.Tape, o Options, f Frame) string {
	if f.InOpen {
		return OpenScreen(tp, o.Width, o.Height, f.Open)
	}
	m := tui.ModelAt(tp, f.At)
	m.Theme = tui.ColourTheme()
	m.Mode = f.Mode
	m.CardAge = f.CardAge
	m.TapePath = o.TapePath
	m.Replay = true
	return tui.View(m, f.Anim, o.Width, o.Height)
}
