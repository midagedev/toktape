package main

import (
	"image/gif"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/midagedev/toktape/internal/render"
	"github.com/midagedev/toktape/internal/tape"
	"github.com/midagedev/toktape/internal/tui"
)

// writeExampleTape saves the eight-stream example run as a real tape file.
//
// run_test.go's writeTape saves a summary and no tokens, which renders as a
// zero-length run; the render verb needs a timeline, so this one writes the
// fixture every other renderer draws.
func writeExampleTape(t *testing.T, dir string) string {
	t.Helper()
	tp := tui.ExampleTape()
	path := filepath.Join(dir, tp.Summary.ID+tape.Ext)
	if err := tape.Write(path, tp); err != nil {
		t.Fatalf("tape.Write: %v", err)
	}
	return path
}

// shortClip keeps a test's clip to a handful of frames. The schedule and the
// encoders are covered in internal/render over the whole clip; what is under
// test here is the verb's wiring, and 6 frames exercise it exactly as well as
// 345 do.
var shortClip = []string{"--duration", "1s", "--fps", "5"}

// TestRenderVerbDefaultsToAGIF is the zero-flag path: a tape in, a GIF beside
// it, and a line on stdout naming what was written.
func TestRenderVerbDefaultsToAGIF(t *testing.T) {
	dir := t.TempDir()
	tapePath := writeExampleTape(t, dir)

	args := append([]string{"render", tapePath}, shortClip...)
	code, out, errOut := exec(t, args...)
	if code != exitOK {
		t.Fatalf("render: exit %d, stderr %q", code, errOut)
	}

	want := strings.TrimSuffix(tapePath, tape.Ext) + ".gif"
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("no GIF beside the tape: %v", err)
	}
	if !strings.HasPrefix(out, "✓ GIF") {
		t.Errorf("stdout = %q, want a ✓ GIF line", out)
	}
	if !strings.Contains(out, filepath.Base(want)) {
		t.Errorf("stdout = %q, does not name %s", out, filepath.Base(want))
	}

	f, err := os.Open(want)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	g, err := gif.DecodeAll(f)
	if err != nil {
		t.Fatalf("the GIF does not decode: %v", err)
	}
	if len(g.Image) == 0 {
		t.Error("the GIF has no frames")
	}
}

// TestRenderVerbWritesEveryAskedForArtifact: the four outputs are one pipeline
// with four encoders, so one invocation must be able to write all of them.
func TestRenderVerbWritesEveryAskedForArtifact(t *testing.T) {
	dir := t.TempDir()
	tapePath := writeExampleTape(t, dir)
	gifPath := filepath.Join(dir, "clip.gif")
	castPath := filepath.Join(dir, "clip.cast")
	framesDir := filepath.Join(dir, "frames")

	args := append([]string{"render", tapePath,
		"--gif", gifPath, "--cast", castPath, "--frames", framesDir}, shortClip...)
	code, out, errOut := exec(t, args...)
	if code != exitOK {
		t.Fatalf("render: exit %d, stderr %q", code, errOut)
	}
	for _, label := range []string{"✓ Cast", "✓ Frames", "✓ GIF"} {
		if !strings.Contains(out, label) {
			t.Errorf("stdout = %q, missing %s", out, label)
		}
	}
	// No output flag was left unasked-for, so nothing may be written beside
	// the tape: the implicit GIF is a fallback, not an extra.
	if _, err := os.Stat(strings.TrimSuffix(tapePath, tape.Ext) + ".gif"); err == nil {
		t.Error("a GIF was written beside the tape although --gif named a path")
	}

	for _, p := range []string{gifPath, castPath} {
		fi, err := os.Stat(p)
		if err != nil {
			t.Errorf("%s: %v", filepath.Base(p), err)
			continue
		}
		if fi.Size() == 0 {
			t.Errorf("%s is empty", filepath.Base(p))
		}
	}
	frames, err := filepath.Glob(filepath.Join(framesDir, "frame_*.png"))
	if err != nil || len(frames) == 0 {
		t.Fatalf("no frames in %s (%v)", framesDir, err)
	}
	// 1 s at 5 fps is six frames, both endpoints included.
	if len(frames) != 6 {
		t.Errorf("%d frames, want 6", len(frames))
	}
	if !strings.Contains(out, "(6 frames)") {
		t.Errorf("stdout = %q, does not report the frame count", out)
	}
}

// TestRenderVerbPicksTheNewestRun: with no tape named, the verb renders the
// newest run in the runs directory and says on stderr which one that was.
func TestRenderVerbPicksTheNewestRun(t *testing.T) {
	dir := t.TempDir()
	tp := tui.ExampleTape()
	older := filepath.Join(dir, "20250101-000000-old"+tape.Ext)
	if err := tape.Write(older, tp); err != nil {
		t.Fatal(err)
	}
	newest := writeExampleTape(t, dir) // ID starts 2026…, so it sorts last

	args := append([]string{"render", "--out", dir}, shortClip...)
	code, out, errOut := exec(t, args...)
	if code != exitOK {
		t.Fatalf("render: exit %d, stderr %q", code, errOut)
	}
	if !strings.Contains(errOut, filepath.Base(newest)) {
		t.Errorf("stderr = %q, does not name the run it chose", errOut)
	}
	if strings.Contains(out, "20250101-000000-old") {
		t.Errorf("stdout = %q, rendered the older run", out)
	}
	if _, err := os.Stat(strings.TrimSuffix(newest, tape.Ext) + ".gif"); err != nil {
		t.Errorf("no GIF beside the newest run: %v", err)
	}
}

// TestRenderVerbEmptyRunsDir: nothing to render is not a crash and not a
// silent success; it is the same sentence `ls` says.
func TestRenderVerbEmptyRunsDir(t *testing.T) {
	code, out, errOut := exec(t, "render", "--out", t.TempDir())
	if code != exitUsage {
		t.Errorf("exit %d, want %d", code, exitUsage)
	}
	if out != "" {
		t.Errorf("stdout = %q, want nothing", out)
	}
	if !strings.Contains(errOut, "No runs yet") {
		t.Errorf("stderr = %q, want the no-runs line", errOut)
	}
}

// TestRenderVerbWithoutFFmpeg: --mp4 on a machine with no encoder exits
// exitUnavailable and prints the render package's own sentence, which is the
// one that says what to install.
func TestRenderVerbWithoutFFmpeg(t *testing.T) {
	dir := t.TempDir()
	tapePath := writeExampleTape(t, dir)
	t.Setenv("PATH", "")

	args := append([]string{"render", tapePath, "--mp4", filepath.Join(dir, "clip.mp4")}, shortClip...)
	code, _, errOut := exec(t, args...)
	if code != exitUnavailable {
		t.Fatalf("exit %d, want %d (stderr %q)", code, exitUnavailable, errOut)
	}
	if !strings.Contains(errOut, "ffmpeg") {
		t.Errorf("stderr = %q, does not name ffmpeg", errOut)
	}
	if exitUnavailable == exitStreams || exitUnavailable == exitUnreachable {
		t.Error("exitUnavailable collides with another verb's exit code")
	}
}

func TestRenderVerbUsageErrors(t *testing.T) {
	dir := t.TempDir()
	tapePath := writeExampleTape(t, dir)
	cases := [][]string{
		{"render", tapePath, "b.tape"},
		{"render", tapePath, "--fps", "0"},
		{"render", tapePath, "--size", "120"},
		{"render", tapePath, "--size", "120x"},
		{"render", tapePath, "--size", "80x24"}, // below the layout's floor
		{"render", filepath.Join(dir, "missing.tape")},
	}
	for _, args := range cases {
		code, out, errOut := exec(t, args...)
		if code != exitUsage {
			t.Errorf("%v: exit %d, want %d", args, code, exitUsage)
		}
		if out != "" {
			t.Errorf("%v: stdout = %q, want nothing", args, out)
		}
		if errOut == "" {
			t.Errorf("%v: no message on stderr", args)
		}
	}
}

func TestParseSize(t *testing.T) {
	ok := map[string][2]int{
		"120x36":  {120, 36},
		"120X36":  {120, 36},
		"120×36":  {120, 36},
		" 100x30": {100, 30},
	}
	for in, want := range ok {
		w, h, err := parseSize(in)
		if err != nil {
			t.Errorf("parseSize(%q): %v", in, err)
			continue
		}
		if w != want[0] || h != want[1] {
			t.Errorf("parseSize(%q) = %d,%d, want %d,%d", in, w, h, want[0], want[1])
		}
	}
	for _, in := range []string{"", "120", "120x36x2", "x36", "120x0", "-1x36", "wide x tall"} {
		if w, h, err := parseSize(in); err == nil {
			t.Errorf("parseSize(%q) = %d,%d, want an error", in, w, h)
		}
	}
}

func TestNewestTape(t *testing.T) {
	dir := t.TempDir()
	if got := newestTape(dir); got != "" {
		t.Errorf("empty dir: got %q", got)
	}
	if got := newestTape(filepath.Join(dir, "nope")); got != "" {
		t.Errorf("missing dir: got %q", got)
	}
	for _, name := range []string{
		"20260101-090000-a" + tape.Ext,
		"20260913-150210-b" + tape.Ext,
		"20260501-120000-c" + tape.Ext,
		"not-a-tape.txt",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "99999999-999999-dir"+tape.Ext), 0o755); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dir, "20260913-150210-b"+tape.Ext)
	if got := newestTape(dir); got != want {
		t.Errorf("newestTape = %q, want %q", got, want)
	}
}

// TestHeroGIFIsPostable guards the file the README actually shows.
//
// The hero is committed, so nothing regenerates it on the way to a release:
// if a change to the TUI or the encoder pushes it past what a forum accepts as
// an inline attachment, the gate has to say so here rather than after someone
// tries to post it. The budget is internal/render's own
// (TestGIFOfTheWholeClipStaysPostable), because two thresholds for one product
// rule is how they drift apart.
// heroFPS mirrors internal/render/cmd/hero's own constant: a test binary
// cannot import package main. The assertion below checks the published file's
// timing against it, so the two cannot drift apart silently.
const heroFPS = 15

func TestHeroGIFIsPostable(t *testing.T) {
	path := filepath.Join("..", "..", "assets", "hero.gif")
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("the README's hero is missing; regenerate it with `go run ./internal/render/cmd/hero`: %v", err)
	}
	// 2026-09-13 (TTP-28): raised from 1.5 MB. The stream phase plays in real
	// time and the example run is now twenty-six seconds rather than seven, so
	// the clip carries three and a half times the text it used to.
	//
	// The lead's decision was to render the GIF at 15 fps and cap it at 3.0 MB
	// on an estimate of 2.4 MB at 30 fps. Measured on the finished fixture the
	// estimate is low, and lowering the frame rate does not buy what it was
	// assumed to. Two rounds of measurement, both on this clip:
	//
	//	fps   before the body-tone ladder   with it
	//	15              4,289,992          4,843,962
	//	12              3,970,308          4,494,352
	//	10              3,975,443          4,482,537
	//
	// A third of the frames saves seven per cent of the bytes, because the
	// file is dominated by how much of the screen changes over the whole clip
	// and not by how many frames that change is divided into: every token's
	// pixels are encoded once whatever the rate. The second column is higher
	// than the first because the write-head glow gives each token two
	// intermediate shades on its way to settling, so a run of text is written
	// to the file three times rather than once. That is the effect, not an
	// accident of encoding.
	//
	// So the rate stays at 15, where the motion still reads, and the cap is
	// 6.0 MB: the measured size with room for the fixture's text to change
	// without re-baselining a gate. Under 10 MB the file still attaches
	// anywhere it needs to, which is GitHub's limit and the reason this gate
	// exists at all.
	if fi.Size() > 6_000_000 {
		t.Errorf("the hero is %d bytes, want under 6.0 MB", fi.Size())
	}

	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	g, err := gif.DecodeAll(f)
	if err != nil {
		t.Fatalf("the hero does not decode: %v", err)
	}
	// The whole clip, not a truncated one: the schedule's frame count is the
	// upper bound, and identical frames are folded into the one before them,
	// so the stored count is at most that and nowhere near it only if the
	// clip stopped early.
	sched := render.NewSchedule(render.RunEnd(tui.ExampleTapeN(4)), heroFPS, 0, true)
	if len(g.Image) > sched.Count {
		t.Errorf("the hero has %d frames, more than the schedule's %d", len(g.Image), sched.Count)
	}
	// The lower bound is the streaming phase, the only phase that must be
	// stored frame by frame: the cold open is static by design and the card
	// hold folds into one frame. Measured 2026-09-13 on the twenty-second
	// hero: 295 of 601 stored — open 29/180, intro 19/30, stream 246/270,
	// card 1/120. (Lead, on the coldopen track's report.)
	//
	// The bound counts in the hero's own frame rate rather than the library
	// default, which the hero no longer uses.
	if want := int(sched.Stream*time.Duration(sched.FPS)/time.Second) * 4 / 5; len(g.Image) < want {
		t.Errorf("the hero has %d frames, want at least the streaming phase's %d", len(g.Image), want)
	}
	if g.Config.Width != 992 {
		t.Errorf("the hero is %d px wide, want the 992 GIFFontSize gives", g.Config.Width)
	}
	// The file's own timing says what it was rendered at: the stored delays
	// are hundredths of a second and sum to the clip's length, so a hero
	// rendered at another rate would land outside a frame of the schedule.
	hundredths := 0
	for _, d := range g.Delay {
		hundredths += d
	}
	if want := sched.Duration.Seconds() * 100; hundredths < int(want)-100 || hundredths > int(want)+100 {
		t.Errorf("the hero runs %.2f s, want the %v the schedule gives at %d fps",
			float64(hundredths)/100, sched.Duration.Round(time.Millisecond), heroFPS)
	}
	t.Logf("hero.gif: %d bytes, %d frames, %d×%d, %.2f s at %d fps",
		fi.Size(), len(g.Image), g.Config.Width, g.Config.Height, float64(hundredths)/100, heroFPS)
}
