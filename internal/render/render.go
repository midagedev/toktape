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
// were laid out in).
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
)

// Options configure a clip. The zero value is valid and means "every default".
type Options struct {
	// Width and Height are the terminal size in cells. Below
	// tui.MinWidth×tui.MinHeight the TUI draws its "make the window bigger"
	// frame, so the renderer refuses those sizes instead.
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
	// TapePath is printed in the card's footer ("✓ saved <path>"). Empty
	// leaves the footer to the key hints, which is what an unsaved run shows.
	TapePath string
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
	return o, NewSchedule(RunEnd(tp), o.FPS, o.Duration), nil
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
	m.TapePath = o.TapePath
	return tui.View(m, f.Anim, o.Width, o.Height)
}
