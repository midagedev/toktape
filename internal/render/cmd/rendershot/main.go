// Command rendershot writes the clip's artifacts for a look-and-adjust round:
// a few stills, the GIF, the asciicast and, when ffmpeg is there, the mp4.
//
// It exists for the same reason internal/tui's tuidump does — a visual change
// is judged by looking at what it renders, and nobody should have to write a
// program to see one. Every still is named with both clocks: the time in the
// finished clip and the instant of the run it draws. Those differ by the cold
// open and the intro while the run plays at 1:1, and by more than that only
// when the clip length was overridden or the run was long enough to compress
// (see render.MaxStream).
package main

import (
	"flag"
	"fmt"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/midagedev/toktape/internal/render"
	"github.com/midagedev/toktape/internal/tape"
	"github.com/midagedev/toktape/internal/tui"
)

func main() {
	out := flag.String("out", "scratch", "directory to write the artifacts into")
	w := flag.Int("w", render.DefaultWidth, "frame width in columns")
	h := flag.Int("h", render.DefaultHeight, "frame height in rows")
	fps := flag.Int("fps", render.DefaultFPS, "frame rate")
	size := flag.Float64("size", render.DefaultFontSize, "cell size in pixels for the stills and the mp4")
	// The default set walks the whole clip: the empty prompt, the command
	// half typed, the search, the streams in prefill, the TUI just after it
	// takes the screen, one second into the run and five seconds into it. The
	// last two are the stills a look round is actually judged on, so they are
	// placed relative to the streaming phase (OpenHold + IntroHold = 7 s) and
	// not at absolute times that would drift as the run gets longer. The last
	// two frames of the clip are added below.
	at := flag.String("at", "0.4,1.8,3.5,5,6.5,8,12", "clip times to write stills for, in seconds")
	flag.Parse()

	// Four streams, the shape the README hero uses (tui.ExampleTapeN).
	tp := tui.ExampleTapeN(4)
	opts := render.Options{
		Width:    *w,
		Height:   *h,
		FPS:      *fps,
		FontSize: *size,
		// Derived from the run being drawn, not written out: a hardcoded
		// path went stale the moment the example model changed and put a
		// tape name in the footer that names no run in the clip
		// (2026-09-13, TTP-28).
		TapePath: "~/.toktape/runs/" + tp.Summary.ID + tape.Ext,
	}
	sched := render.NewSchedule(render.RunEnd(tp), *fps, 0)

	if err := os.MkdirAll(*out, 0o755); err != nil {
		fail(err)
	}
	fmt.Printf("run ends at %v; clip is %v at %d fps (%d frames)\n",
		sched.RunEnd.Round(time.Millisecond), sched.Duration.Round(time.Millisecond), sched.FPS, sched.Count)
	fmt.Printf("phases: open %v · intro %v · stream %v · card %v\n\n",
		sched.Open, sched.Intro, sched.Stream, sched.Card)

	for _, secs := range parseTimes(*at) {
		writeStill(tp, opts, sched, secs, *out)
	}
	// The two ends of the card hold: the frame half a second before the clip
	// stops, and the last one.
	writeStill(tp, opts, sched, sched.Duration.Seconds()-0.5, *out)
	writeStill(tp, opts, sched, sched.Duration.Seconds(), *out)

	gifPath := filepath.Join(*out, "clip.gif")
	start := time.Now()
	if err := render.GIF(tp, render.Options{Width: *w, Height: *h, FPS: *fps, TapePath: opts.TapePath}, gifPath); err != nil {
		fail(err)
	}
	report("GIF", gifPath, start)

	castPath := filepath.Join(*out, "clip.cast")
	start = time.Now()
	cast, err := render.Asciicast(tp, opts)
	if err != nil {
		fail(err)
	}
	if err := os.WriteFile(castPath, cast, 0o644); err != nil {
		fail(err)
	}
	report("asciicast", castPath, start)

	if _, err := exec.LookPath("ffmpeg"); err != nil {
		fmt.Println("mp4: skipped, ffmpeg is not on PATH")
		return
	}
	mp4Path := filepath.Join(*out, "clip.mp4")
	start = time.Now()
	if err := render.MP4(tp, opts, mp4Path); err != nil {
		fail(err)
	}
	report("mp4", mp4Path, start)
}

// writeStill renders the frame nearest a clip time and names the file with
// both clocks.
func writeStill(tp *tape.Tape, opts render.Options, sched render.Schedule, secs float64, dir string) {
	i := int(secs*float64(sched.FPS) + 0.5)
	f := sched.Frame(i)
	img, err := render.FrameImage(tp, opts, f)
	if err != nil {
		fail(err)
	}
	name := fmt.Sprintf("clip-%s-run-%s%s.png", trim(f.Clip), trim(f.At), modeSuffix(f))
	path := filepath.Join(dir, name)
	file, err := os.Create(path)
	if err != nil {
		fail(err)
	}
	if err := png.Encode(file, img); err != nil {
		fail(err)
	}
	if err := file.Close(); err != nil {
		fail(err)
	}
	fi, _ := os.Stat(path)
	fmt.Printf("still  frame %4d  clip %-8v run %-8v %-6s %s (%d×%d, %s)\n",
		f.Index, f.Clip.Round(time.Millisecond), f.At.Round(time.Millisecond),
		modeName(f), path, img.Bounds().Dx(), img.Bounds().Dy(), human(fi.Size()))
}

func modeName(f render.Frame) string {
	switch {
	case f.InOpen:
		return "open"
	case f.Mode == tui.ModeCard:
		return "card"
	default:
		return "live"
	}
}

func modeSuffix(f render.Frame) string {
	switch {
	case f.InOpen:
		return "-open"
	case f.Mode == tui.ModeCard:
		return "-card"
	default:
		return ""
	}
}

// trim formats a duration as a short, file-name-safe number of seconds.
func trim(d time.Duration) string {
	s := strconv.FormatFloat(d.Seconds(), 'f', 2, 64)
	s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	if s == "" {
		s = "0"
	}
	return s + "s"
}

func parseTimes(list string) []float64 {
	var out []float64
	for _, f := range strings.Split(list, ",") {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		v, err := strconv.ParseFloat(f, 64)
		if err != nil {
			fail(fmt.Errorf("bad time %q: %w", f, err))
		}
		out = append(out, v)
	}
	return out
}

func report(kind, path string, start time.Time) {
	fi, err := os.Stat(path)
	if err != nil {
		fail(err)
	}
	fmt.Printf("%-10s %s (%s, %v)\n", kind, path, human(fi.Size()), time.Since(start).Round(time.Millisecond))
}

func human(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0f kB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%d B", n)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "rendershot:", err)
	os.Exit(1)
}
