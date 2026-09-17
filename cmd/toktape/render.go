package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	// Aliased: the package tests declare a helper called exec, and an import
	// name may not collide with a package-level identifier.
	osexec "os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/midagedev/toktape/internal/render"
	"github.com/midagedev/toktape/internal/tape"
)

// renderUsage is what a mistyped render invocation prints. The verb has more
// flags than fit the one line the main usage text gives it, and a person who
// got the arguments wrong is exactly the person who needs to see them.
const renderUsage = `toktape render — render a recorded run as a clip

Usage:
  toktape render [tape] [flags]

With no tape the newest run in the runs directory is used. With no output flag
a GIF is written next to the tape.

Flags:
  --gif FILE       write an animated GIF
  --mp4 FILE       write an H.264 mp4 (needs ffmpeg on PATH)
  --cast FILE      write an asciicast v2 recording
  --frames DIR     write the PNG frame sequence into DIR (20 px cells — see Canvas)
  --open           open on a shell prompt with the command being typed, before the screen
  --no-poster      do not put the result on the clip's first frame
  --prefill-lead D open this long before the first token instead of at the run's start
  --duration D     length of the clip (default: derived from the run)
  --fps N          frame rate (default 30)
  --size WxH       terminal size in cells, not pixels (default 156x38 for
                   --mp4/--frames, 120x36 for --gif/--cast; see Canvas)
  --out DIR        where to look for the newest run (default ~/.toktape/runs)

Clip length
  A derived clip is 6s cold open (only with --open) + 1s intro + the run at
  1:1 + 5s on the card. The run is the only part you aim, and the direct way
  to aim it is to record with --for D: the run then ends at D seconds
  whatever the token count, so the clip is D + 6s, or D + 12s with --open.

  When you have already measured the machine, the token count says the same
  thing in the other unit:

      run seconds ≈ TTFT + --n-predict ÷ per-stream tok/s

  -n does not lengthen it: the streams run at once, and the run ends when the
  last of them does. This repo's own hero is 4 streams at 42.1 tok/s with a
  5.7s TTFT, recorded with --n-predict 512 and --for 90s. Put those in and the
  formula answers 17.9s. The run was 11.6s — so a 17.7s clip, or 23.7s with
  --open — because neither the cap nor the clock ended it: every stream
  stopped when the model had finished, between 152 and 279 tokens.

  That gap is the formula working, not failing. Aimed with --n-predict it
  answers the longest run those settings can produce, which is the length the
  budget has to cover, and a run that stops early comes in under it. Aim it
  instead with the tokens a run averaged and it answers less than the run: the
  average falls with every stream that stops early while the run still ends
  with the longest one. Here the average is 214 tokens, which predicts 10.8s
  for an 11.6s run.

  Either flag is set when you record. --prefill-lead is the one that is not:
  it opens the clip that long before the first token rather than at the run's
  start, so the wait for prefill is left out while every frame that is in the
  clip is still 1:1. Nothing has to say so — the tile's clock is measured from
  the run's start, so the windowed hero opens on 2/90s instead of 0/90s. It
  replaces the intro, which is a screen for a run that has not started yet.
  The hero is rendered with --prefill-lead 3s: its first token is 5.7s in, so
  2.7s of waiting is cut and the clip is 20.0s rather than 23.7s. The flag is
  worth reaching for on the runs where the first token is tens of seconds out,
  which is where a clip stops being one anybody watches to the end.

  --duration is the dishonest one: it compresses or stretches the same run
  into the time you name, and a viewer then cannot tell whether a stream
  stalled. Slow motion is a lie about the machine.

Canvas, and cutting a window out of a clip
  --size is a cell grid, not a pixel size, and the pixel canvas differs by
  output: --gif and --cast draw 13 px cells, --mp4 and --frames draw 20 px
  ones. The same tape at --size 120x36 comes out 992x684 as a GIF and
  1488x1026 as frames, and no flag reconciles them. So rendering --frames and
  encoding them yourself is not the picture --gif would have made.

  When you want part of a clip, cut the finished GIF rather than re-rendering
  or re-encoding it:

      gifsicle --unoptimize clip.gif '#120-274' -O3 -o cut.gif

  That is lossless and keeps this tool's own palette. Mind the index: frames
  identical to the one before them are folded away, so a GIF holds fewer
  images than seconds x fps, and gifsicle counts images. Run gifsicle --info
  to see how many there are and what each one's delay is.
`

// renderFlags is every flag the render verb declares. See recordFlags for why
// it is a struct.
type renderFlags struct {
	gifOut   *string
	mp4Out   *string
	castOut  *string
	framesTo *string
	duration *time.Duration
	coldOpen *bool
	noPoster *bool
	lead     *time.Duration
	fps      *int
	size     *string
	outDir   *string
}

// declareRenderFlags registers the render verb's flags on fs.
func declareRenderFlags(fs *flag.FlagSet) *renderFlags {
	return &renderFlags{
		gifOut:   fs.String("gif", "", "write an animated GIF here"),
		mp4Out:   fs.String("mp4", "", "write an H.264 mp4 here (needs ffmpeg)"),
		castOut:  fs.String("cast", "", "write an asciicast v2 recording here"),
		framesTo: fs.String("frames", "", "write the PNG frame sequence into this directory"),
		duration: fs.Duration("duration", 0, "length of the clip (default: derived from the run)"),
		coldOpen: fs.Bool("open", false, "open on a shell prompt with the command being typed"),
		noPoster: fs.Bool("no-poster", false, "do not put the result on the clip's first frame"),
		lead:     fs.Duration("prefill-lead", 0, "open this long before the first token instead of at the run's start"),
		fps:      fs.Int("fps", render.DefaultFPS, "frame rate"),
		size:     fs.String("size", "", "terminal size in cells, e.g. 120x36"),
		outDir:   fs.String("out", defaultRunsDir(), "directory to take the newest run from"),
	}
}

// runRender turns a tape into the clip you post.
//
// Every output is the same pipeline with a different encoder at the end. Without
// --size each output takes its own default size — 1920×1080 for the mp4 and
// the frames, the smaller inline size for the GIF and the cast — so one
// invocation writing all four is guaranteed the same frames only when --size
// is given. The tape is the record here as everywhere: nothing is recomputed,
// the run is only replayed.
func runRender(c *cli, args []string) int {
	fs := newFlagSet("render")
	f := declareRenderFlags(fs)
	gifOut, mp4Out, castOut, framesTo := f.gifOut, f.mp4Out, f.castOut, f.framesTo
	duration, coldOpen, noPoster, lead := f.duration, f.coldOpen, f.noPoster, f.lead
	fps, size, outDir := f.fps, f.size, f.outDir
	files, err := parseArgs(fs, args)
	if err != nil {
		return c.badFlags("render", renderUsage, args, err)
	}
	if len(files) > 1 {
		return c.usageTextf(renderUsage, "toktape render: expected one tape file, got %d", len(files))
	}
	if *fps <= 0 {
		return c.usagef("toktape render: --fps must be positive, got %d", *fps)
	}
	if *duration < 0 {
		return c.usagef("toktape render: --duration must not be negative, got %v", *duration)
	}
	if *lead < 0 {
		return c.usagef("toktape render: --prefill-lead must not be negative, got %v", *lead)
	}

	opts := render.Options{
		FPS:         *fps,
		Duration:    *duration,
		ColdOpen:    *coldOpen,
		NoPoster:    *noPoster,
		PrefillLead: *lead,
	}
	if *size != "" {
		w, h, err := parseSize(*size)
		if err != nil {
			return c.usagef("toktape render: %v", err)
		}
		opts.Width, opts.Height = w, h
	}

	tapePath := ""
	if len(files) == 1 {
		tapePath = files[0]
	} else {
		tapePath = newestTape(*outDir)
		if tapePath == "" {
			return c.fail(failure{
				code: exitUsage,
				msg:  strings.TrimRight(noRunsMessage(*outDir), "\n"),
				hint: "or name the tape: toktape render <tape>",
			})
		}
		// Say which run was picked. A verb that chooses a file for you must
		// name it, or the clip that comes out is of a run the user did not
		// mean and nothing on screen said so.
		fmt.Fprintf(c.stderr, "→ Newest run: %s\n", tildePath(tapePath))
	}
	tp, err := tape.Read(tapePath)
	if err != nil {
		return c.usagef("toktape: %v", err)
	}
	// The footer the clip's card ends on is the path this tape came from, the
	// same one `record` printed when it saved the run.
	opts.TapePath = tildePath(tapePath)

	// Say how long the clip will be, and out of what, before spending a
	// minute rendering it. The arithmetic is in renderUsage; this is the same
	// arithmetic applied to the tape in hand, which is the question a person
	// actually has ("is this going to be forty seconds?") and the one that
	// used to be answerable only by rendering and looking.
	fmt.Fprint(c.stderr, clipLengthLine(tp, opts))

	// The zero-flag path is the product: `toktape render <tape>` gives you the
	// thing you can drag into a post, named after the run it came from.
	if *gifOut == "" && *mp4Out == "" && *castOut == "" && *framesTo == "" {
		*gifOut = strings.TrimSuffix(tapePath, tape.Ext) + ".gif"
	}

	// Cheapest first, so a run that is going to fail on ffmpeg has already
	// written everything that did not need it.
	if *castOut != "" {
		cast, err := render.Asciicast(tp, opts)
		if err != nil {
			return c.usagef("toktape: %v", err)
		}
		if err := os.WriteFile(*castOut, cast, 0o644); err != nil {
			return c.usagef("toktape: writing the asciicast: %v", err)
		}
		fmt.Fprint(c.stdout, artifactLine("Cast", tildePath(*castOut)))
	}
	if *framesTo != "" {
		paths, err := render.Frames(tp, opts, *framesTo)
		if err != nil {
			return c.usagef("toktape: %v", err)
		}
		fmt.Fprint(c.stdout, artifactLine("Frames",
			fmt.Sprintf("%s (%d frames)", tildePath(*framesTo), len(paths))))
	}
	if *gifOut != "" {
		if err := render.GIF(tp, opts, *gifOut); err != nil {
			return c.usagef("toktape: %v", err)
		}
		fmt.Fprint(c.stdout, artifactLine("GIF", tildePath(*gifOut)))
	}
	if *mp4Out != "" {
		if err := render.MP4(tp, opts, *mp4Out); err != nil {
			// A missing encoder is the environment's answer, not the user's
			// mistake, and it is the one failure a wrapper retries differently.
			if errors.Is(err, osexec.ErrNotFound) {
				return c.fail(failure{
					code: exitUnavailable,
					msg:  fmt.Sprintf("toktape: %v", err),
					hint: "install ffmpeg, or render a GIF instead: toktape render <tape> --gif out.gif",
				})
			}
			return c.usagef("toktape: %v", err)
		}
		fmt.Fprint(c.stdout, artifactLine("mp4", tildePath(*mp4Out)))
	}
	return exitOK
}

// clipLengthLine is the one line that says what is about to be rendered.
//
// It reads the same schedule the renderer will build, so it cannot disagree
// with the file that comes out; the breakdown names the phases because the
// only one a caller can change is the run, and the way to change it is to
// record with a different --for (or --n-predict), not to render with
// --duration.
func clipLengthLine(tp *tape.Tape, opts render.Options) string {
	runEnd := render.RunEnd(tp)
	fps := opts.FPS
	if fps <= 0 {
		fps = render.DefaultFPS
	}
	runFrom := render.RunFrom(tp, opts.PrefillLead)
	sched := render.NewSchedule(runFrom, runEnd, fps, opts.Duration, opts.ColdOpen)
	if !opts.NoPoster {
		sched = sched.WithPoster()
	}
	if opts.Duration > 0 {
		// An explicit --duration does not shorten the run, it replays it at
		// the wrong speed, and the phases no longer add up to anything worth
		// printing. A caller who wanted a shorter clip wanted a shorter run,
		// and only a recording can give them one.
		// What is replayed is the window, not the run: with --prefill-lead the
		// clip already leaves the front out, and the holds it is squeezed
		// against are the schedule's own — a windowed clip has no intro.
		// It is the schedule's own streaming phase rather than the duration
		// minus the holds, so the figure is the ratio the renderer will
		// really replay at — holds shrink when the clip is short enough.
		played := runEnd - runFrom
		rate := 1.0
		if played > 0 && sched.Stream > 0 {
			rate = float64(played) / float64(sched.Stream)
		}
		return fmt.Sprintf("→ Clip %s, the length you named\n"+
			"→ The %s run is replayed at %.2fx to fit it. To change the clip's length honestly, record with --for (or a different --n-predict)\n",
			secs(sched.Duration), secs(played), rate)
	}
	var parts []string
	if opts.ColdOpen {
		parts = append(parts, fmt.Sprintf("%s open", secs(render.OpenHold)))
	}
	if sched.Intro > 0 {
		parts = append(parts, fmt.Sprintf("%s intro", secs(render.IntroHold)))
	}
	parts = append(parts,
		fmt.Sprintf("%s run", secs(runEnd-runFrom)),
		fmt.Sprintf("%s card", secs(render.CardHold)))
	line := fmt.Sprintf("→ Clip %s (%s)\n", secs(sched.Duration), strings.Join(parts, " + "))
	if runFrom > 0 {
		// The cut is named here because it is the one thing in the breakdown a
		// reader could otherwise mistake for a shorter run. On screen it needs
		// no label: the clip opens on a clock that already reads runFrom.
		line += fmt.Sprintf("→ The first %s of the run are not in it: --prefill-lead opens the clip %s before the first token. Everything in it is still 1:1\n",
			secs(runFrom), secs(opts.PrefillLead))
	}
	return line
}

// secs renders a duration the way the help text quotes one: one decimal.
func secs(d time.Duration) string {
	return fmt.Sprintf("%.1fs", d.Seconds())
}

// artifactLine is one "here is what was written" line, in the shape the end of
// a recording uses (record.go's shareHint), so a clip and a card read as the
// same tool talking. what is the path, already shortened, plus any note that
// belongs on the same line.
func artifactLine(label, what string) string {
	return fmt.Sprintf("✓ %-6s %s\n", label, what)
}

// newestTape is the most recent run file in dir, or "".
//
// Run IDs are <date>-<time>-<model slug>, so lexical order over the names is
// chronological order and the newest run is found without opening a single
// tape — the same rule previousTape and ls rely on.
func newestTape(dir string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	best := ""
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != tape.Ext {
			continue
		}
		if best == "" || e.Name() > best {
			best = e.Name()
		}
	}
	if best == "" {
		return ""
	}
	return filepath.Join(dir, best)
}

// parseSize reads a "120x36" terminal size.
//
// The separator is spelled three ways in the wild and all three mean the same
// thing, so all three are accepted; what is not accepted is a size with a
// missing half, because guessing the other number would render a clip the
// caller did not ask for.
func parseSize(s string) (w, h int, err error) {
	norm := strings.NewReplacer("X", "x", "×", "x", "*", "x").Replace(strings.TrimSpace(s))
	parts := strings.Split(norm, "x")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("--size %q is not WxH, e.g. %dx%d", s, render.DefaultWidth, render.DefaultHeight)
	}
	if w, err = strconv.Atoi(strings.TrimSpace(parts[0])); err != nil {
		return 0, 0, fmt.Errorf("--size %q: bad width: %w", s, err)
	}
	if h, err = strconv.Atoi(strings.TrimSpace(parts[1])); err != nil {
		return 0, 0, fmt.Errorf("--size %q: bad height: %w", s, err)
	}
	if w <= 0 || h <= 0 {
		return 0, 0, fmt.Errorf("--size %q: both halves must be positive", s)
	}
	return w, h, nil
}
