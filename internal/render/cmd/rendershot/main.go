// Command rendershot writes the clip's artifacts for a look-and-adjust round:
// a few stills, the GIF, the asciicast and, when ffmpeg is there, the mp4.
//
// It exists for the same reason internal/tui's tuidump does — a visual change
// is judged by looking at what it renders, and nobody should have to write a
// program to see one. Every still is named with both clocks: the time in the
// finished clip and the instant of the run it draws, which are not the same
// number once the streaming phase has been stretched or compressed.
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
	at := flag.String("at", "0,1.5,4,8", "clip times to write stills for, in seconds")
	flag.Parse()

	tp := tui.ExampleTape()
	opts := render.Options{
		Width:    *w,
		Height:   *h,
		FPS:      *fps,
		FontSize: *size,
		TapePath: "~/.toktape/runs/20260913-150210-qwen3.5-35b-a3b.tape",
	}
	sched := render.NewSchedule(render.RunEnd(tp), *fps, 0)

	if err := os.MkdirAll(*out, 0o755); err != nil {
		fail(err)
	}
	fmt.Printf("run ends at %v; clip is %v at %d fps (%d frames)\n",
		sched.RunEnd.Round(time.Millisecond), sched.Duration.Round(time.Millisecond), sched.FPS, sched.Count)
	fmt.Printf("phases: intro %v · stream %v · card %v\n\n", sched.Intro, sched.Stream, sched.Card)

	for _, secs := range parseTimes(*at) {
		writeStill(tp, opts, sched, secs, *out)
	}
	// The last frame of the clip: the card, held.
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
	if f.Mode == tui.ModeCard {
		return "card"
	}
	return "live"
}

func modeSuffix(f render.Frame) string {
	if f.Mode == tui.ModeCard {
		return "-card"
	}
	return ""
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
