package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	// Aliased: the package tests declare a helper called exec, and an import
	// name may not collide with a package-level identifier.
	osexec "os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/midagedev/toktape/internal/render"
	"github.com/midagedev/toktape/internal/tape"
)

// exitUnavailable says the machine is missing something the requested output
// needs — today that is ffmpeg for --mp4. It is separate from exitStreams
// because "this box has no encoder" and "the server answered nothing" are
// different things for a wrapper script to branch on, and the exit codes are
// one contract across every verb (see the block in run.go, where this belongs
// once the lead moves it there).
const exitUnavailable = 4

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
  --duration D     length of the clip (default: derived, 10s–12s)
  --fps N          frame rate (default 30)
  --size WxH       terminal size in cells (default 120x36)
  --out DIR        where to look for the newest run (default ~/.toktape/runs)
`

// runRender turns a tape into the clip you post.
//
// Every output is the same pipeline with a different encoder at the end, so
// one invocation can write all four and they are guaranteed to be the same
// frames. The tape is the record here as everywhere: nothing is recomputed,
// the run is only replayed.
func runRender(stdout, stderr io.Writer, args []string) int {
	fs := newFlagSet("render", stderr)
	fs.Usage = func() { fmt.Fprint(stderr, renderUsage) }
	var (
		gifOut   = fs.String("gif", "", "write an animated GIF here")
		mp4Out   = fs.String("mp4", "", "write an H.264 mp4 here (needs ffmpeg)")
		castOut  = fs.String("cast", "", "write an asciicast v2 recording here")
		framesTo = fs.String("frames", "", "write the PNG frame sequence into this directory")
		duration = fs.Duration("duration", 0, "length of the clip (default: derived from the run)")
		fps      = fs.Int("fps", render.DefaultFPS, "frame rate")
		size     = fs.String("size", "", "terminal size in cells, e.g. 120x36")
		outDir   = fs.String("out", defaultRunsDir(), "directory to take the newest run from")
	)
	files, err := parseArgs(fs, args)
	if err != nil {
		return exitUsage
	}
	if len(files) > 1 {
		fmt.Fprintf(stderr, "toktape render: expected one tape file, got %d\n\n%s", len(files), renderUsage)
		return exitUsage
	}
	if *fps <= 0 {
		fmt.Fprintf(stderr, "toktape render: --fps must be positive, got %d\n", *fps)
		return exitUsage
	}
	if *duration < 0 {
		fmt.Fprintf(stderr, "toktape render: --duration must not be negative, got %v\n", *duration)
		return exitUsage
	}

	opts := render.Options{FPS: *fps, Duration: *duration}
	if *size != "" {
		w, h, err := parseSize(*size)
		if err != nil {
			fmt.Fprintf(stderr, "toktape render: %v\n", err)
			return exitUsage
		}
		opts.Width, opts.Height = w, h
	}

	tapePath := ""
	if len(files) == 1 {
		tapePath = files[0]
	} else {
		tapePath = newestTape(*outDir)
		if tapePath == "" {
			fmt.Fprintf(stderr, "No runs yet in %s. Run `toktape` to record one.\n", tildePath(*outDir))
			return exitUsage
		}
		// Say which run was picked. A verb that chooses a file for you must
		// name it, or the clip that comes out is of a run the user did not
		// mean and nothing on screen said so.
		fmt.Fprintf(stderr, "→ Newest run: %s\n", tildePath(tapePath))
	}
	tp, err := tape.Read(tapePath)
	if err != nil {
		fmt.Fprintf(stderr, "toktape: %v\n", err)
		return exitUsage
	}
	// The footer the clip's card ends on is the path this tape came from, the
	// same one `record` printed when it saved the run.
	opts.TapePath = tildePath(tapePath)

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
			fmt.Fprintf(stderr, "toktape: %v\n", err)
			return exitUsage
		}
		if err := os.WriteFile(*castOut, cast, 0o644); err != nil {
			fmt.Fprintf(stderr, "toktape: writing the asciicast: %v\n", err)
			return exitUsage
		}
		fmt.Fprint(stdout, artifactLine("Cast", tildePath(*castOut)))
	}
	if *framesTo != "" {
		paths, err := render.Frames(tp, opts, *framesTo)
		if err != nil {
			fmt.Fprintf(stderr, "toktape: %v\n", err)
			return exitUsage
		}
		fmt.Fprint(stdout, artifactLine("Frames",
			fmt.Sprintf("%s (%d frames)", tildePath(*framesTo), len(paths))))
	}
	if *gifOut != "" {
		if err := render.GIF(tp, opts, *gifOut); err != nil {
			fmt.Fprintf(stderr, "toktape: %v\n", err)
			return exitUsage
		}
		fmt.Fprint(stdout, artifactLine("GIF", tildePath(*gifOut)))
	}
	if *mp4Out != "" {
		if err := render.MP4(tp, opts, *mp4Out); err != nil {
			fmt.Fprintf(stderr, "toktape: %v\n", err)
			// A missing encoder is the environment's answer, not the user's
			// mistake, and it is the one failure a wrapper retries differently.
			if errors.Is(err, osexec.ErrNotFound) {
				return exitUnavailable
			}
			return exitUsage
		}
		fmt.Fprint(stdout, artifactLine("mp4", tildePath(*mp4Out)))
	}
	return exitOK
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
