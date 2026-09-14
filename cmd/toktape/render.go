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
  --frames DIR     write the PNG frame sequence into DIR
  --open           open on a shell prompt with the command being typed, before the screen
  --no-poster      do not put the result on the clip's first frame
  --duration D     length of the clip (default: derived from the run)
  --fps N          frame rate (default 30)
  --size WxH       terminal size in cells (default 156x38 for --mp4 and --frames, 120x36 for --gif and --cast)
  --out DIR        where to look for the newest run (default ~/.toktape/runs)

Clip length
  A derived clip is 6s cold open (only with --open) + 1s intro + the run at
  1:1 + 5s on the card. The run is the only part you aim, and the direct way
  to aim it is to record with --for D: the run then ends at D seconds
  whatever the token count, so the clip is D + 6s, or D + 12s with --open.

  When you have already measured the machine, the token count says the same
  thing in the other unit:

      run seconds ≈ TTFT + --n-predict ÷ per-stream tok/s

  -n does not lengthen it: the streams run at once. This repo's own hero is
  2 streams of 215 tokens at 11.2 tok/s with a 10.8s TTFT — a 29.8s run, so a
  35.9s clip, or 41.9s with --open. It was recorded with --for 30s, which is
  the rule above: the clock ended it at 30s and the clip came out 30 + 6.

  Either flag is set when you record. --duration is not: it compresses or
  stretches the same run into the time you name, and a viewer then cannot
  tell whether a stream stalled. Slow motion is a lie about the machine.
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
	duration, coldOpen, noPoster := f.duration, f.coldOpen, f.noPoster
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

	opts := render.Options{FPS: *fps, Duration: *duration, ColdOpen: *coldOpen, NoPoster: *noPoster}
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
	sched := render.NewSchedule(runEnd, fps, opts.Duration, opts.ColdOpen)
	if !opts.NoPoster {
		sched = sched.WithPoster()
	}
	if opts.Duration > 0 {
		// An explicit --duration does not shorten the run, it replays it at
		// the wrong speed, and the phases no longer add up to anything worth
		// printing. A caller who wanted a shorter clip wanted a shorter run,
		// and only a recording can give them one.
		rate := 1.0
		if opts.Duration > 0 && runEnd > 0 {
			rate = float64(runEnd) / float64(opts.Duration-render.MinDuration)
		}
		return fmt.Sprintf("→ Clip %s, the length you named\n"+
			"→ The %s run is replayed at %.2fx to fit it. To change the clip's length honestly, record with --for (or a different --n-predict)\n",
			secs(sched.Duration), secs(runEnd), rate)
	}
	var parts []string
	if opts.ColdOpen {
		parts = append(parts, fmt.Sprintf("%s open", secs(render.OpenHold)))
	}
	parts = append(parts,
		fmt.Sprintf("%s intro", secs(render.IntroHold)),
		fmt.Sprintf("%s run", secs(runEnd)),
		fmt.Sprintf("%s card", secs(render.CardHold)))
	return fmt.Sprintf("→ Clip %s (%s)\n", secs(sched.Duration), strings.Join(parts, " + "))
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
