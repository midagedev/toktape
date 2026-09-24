// Command tuidump renders frames of the example tape to text files without a
// terminal.
//
// It exists so a layout change can be inspected the way the lead inspects it —
// by reading frames — and so the same frames can be checked mechanically. Each
// frame is written twice: a plain file that is the layout, and an .ansi file
// that is the same layout with the palette on. The two must differ only in
// escape sequences, which is the invariant the package's colour test pins and
// which this command re-checks before it exits.
//
// -png adds a third file per frame, the same instant rasterised by the clip
// renderer (render.FrameImage), so a look question is one command away from a
// still instead of a whole clip render (TTP-39).
//
// -tape dumps a recorded run instead of the example (TTP-48, 2026-09-14). The
// code-formatting defect was invisible on the example, whose answers are all
// prose, and was found on a real rig's tape by a throwaway program; a real
// recording is now one flag away from the same frames.
//
// -chat dumps the chat screen instead (TTP-184, 2026-09-24): the scripted
// session of tui.ExampleChatStates, one frame per state the screen can be in —
// measuring, prefill, a code answer streaming, a thought, four turns, the
// context-full notice, the 90-column layout, /help, a scrolled transcript,
// and a twelve-turn session with a stopped turn at both widths.
// -png rasterises them through render.TextImage, since no tape holds them.
package main

import (
	"flag"
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/midagedev/toktape/internal/card"
	"github.com/midagedev/toktape/internal/render"
	"github.com/midagedev/toktape/internal/tape"
	"github.com/midagedev/toktape/internal/tui"
)

func main() {
	out := flag.String("out", "scratch/frames", "directory to write frames into")
	w := flag.Int("w", 120, "frame width in columns")
	h := flag.Int("h", 36, "frame height in rows")
	n := flag.Int("n", 8, "concurrent streams")
	gridSpec := flag.String("grid", tui.DefaultGrid.String(), "tile grid per page as COLSxROWS (0 = fit)")
	page := flag.Int("page", 0, "which page of tiles to dump, from zero")
	// -at exists for the questions the default set cannot answer: whether
	// something that is supposed to move between two instants actually moves.
	// Dumping two frames a fifth of a second apart and diffing them is the
	// cheapest way to check an effect that is a function of clip time, and
	// before this flag it meant writing a program.
	at := flag.String("at", "", "comma-separated clip times to dump instead of the default set, e.g. 12s,12.2s")
	// -rounds dumps the multi-round example (TTP-38): K sequential prompt
	// rounds of -n streams in one tape, the shape `record --prompts` writes.
	// 0 or 1 is the ordinary single-round example.
	rounds := flag.Int("rounds", 0, "sequential prompt rounds (2..3) instead of one; 0 = the single-round example")
	pngOut := flag.Bool("png", false, "also write each frame as a PNG, rasterised the way the clip renderer draws it")
	tapeIn := flag.String("tape", "", "dump a recorded run file instead of the example tape; without -at, frames at a quarter, half and three quarters of its run and at its end")
	chat := flag.Bool("chat", false, "dump the chat screen's scripted states instead of the record screen")
	flag.Parse()

	if *chat {
		if err := os.MkdirAll(*out, 0o755); err != nil {
			fail(err)
		}
		if dumpChat(*out, *pngOut) > 0 {
			os.Exit(1)
		}
		return
	}

	grid, err := tui.ParseGrid(*gridSpec)
	if err != nil {
		fail(err)
	}

	if err := os.MkdirAll(*out, 0o755); err != nil {
		fail(err)
	}
	tp := tui.ExampleTapeN(*n)
	// tag goes into every file name, so a rounds dump never overwrites the
	// single-round frames it is compared against.
	tag := ""
	if *rounds > 1 {
		tp = tui.ExampleRoundsTape(*n, *rounds)
		tag = fmt.Sprintf("-r%d", tp.Summary.Rounds)
	}
	if *tapeIn != "" {
		rec, err := tape.Read(*tapeIn)
		if err != nil {
			fail(err)
		}
		tp = rec
		tag = "-" + strings.TrimSuffix(filepath.Base(*tapeIn), ".tape")
	}
	extras, err := parseOffsets(*at)
	if err != nil {
		fail(err)
	}

	// The instants worth looking at (TTP-28): nothing yet, the first frames
	// with tokens in them, a settled screen three seconds in, the frame the
	// goldens and the hero are framed at (tui.ExampleMidRun, where one stream
	// has just crossed into its answer), and the finished run.
	//
	// The last one is taken from the run rather than assumed: the stream count
	// moves the wall clock (eight streams take ten seconds longer than four),
	// and a "done" frame stamped a few milliseconds early is a live model with
	// one token still pending — which looks finished and is not.
	doneAt := time.Duration(tp.Summary.Aggregate.WallMs*float64(time.Millisecond)) + time.Second
	if *rounds > 1 || *tapeIn != "" {
		// A recorded tape is dated by its last token for the same reason. A
		// rounds run's WallMs is the sum of its rounds' own windows and
		// leaves out the gaps between them, so it ends early on the clip's
		// timeline; the last token is the end that frame has to be past.
		doneAt = tui.ModelAt(tp, time.Duration(1<<62)).RunEnd + time.Second
	}
	offsets := []struct {
		name string
		at   time.Duration
	}{
		{"t0.0s", 0},
		{"t0.7s", 700 * time.Millisecond},
		{"t3.0s", 3 * time.Second},
		{"t10.0s", 10 * time.Second},
		{fmt.Sprintf("t%.1fs-mid", tui.ExampleMidRun.Seconds()), tui.ExampleMidRun},
		{fmt.Sprintf("t%.1fs-done", doneAt.Seconds()), doneAt},
	}
	if *tapeIn != "" {
		// The example's instants mean nothing on another run.
		end := doneAt - time.Second
		offsets = offsets[:0]
		for _, f := range []float64{0.25, 0.5, 0.75} {
			at := time.Duration(float64(end) * f)
			offsets = append(offsets, struct {
				name string
				at   time.Duration
			}{fmt.Sprintf("t%.1fs", at.Seconds()), at})
		}
		offsets = append(offsets, struct {
			name string
			at   time.Duration
		}{fmt.Sprintf("t%.1fs-done", doneAt.Seconds()), doneAt})
	}
	if len(extras) > 0 {
		offsets = extras
	}
	if *pngOut && (grid.String() != tui.DefaultGrid.String() || *page != 0) {
		// render.FrameText cuts the model with ModelAt, which lays tiles out
		// on the default grid from page one; the PNG would not be this frame.
		fmt.Println("note: -png draws the default grid, page 0; the PNGs do not follow -grid/-page")
	}
	bad := 0
	for _, off := range offsets {
		plain := tui.ModelAt(tp, off.at)
		plain.TapePath = "~/.toktape/runs/" + tp.Summary.ID + ".tape"
		plain.Grid, plain.Page = grid, *page
		colour := plain
		colour.Theme = tui.ColourTheme()

		p := tui.View(plain, off.at, *w, *h)
		c := tui.View(colour, off.at, *w, *h)

		base := filepath.Join(*out, fmt.Sprintf("%dx%d-n%d%s-%s-p%d-%s", *w, *h, *n, tag, grid.String(), *page, off.name))
		write(base+".txt", p)
		write(base+".ansi", c)
		if *pngOut {
			writePNG(base+".png", tp, *w, *h, plain.TapePath, render.Frame{At: off.at, Anim: off.at, Mode: tui.ModeLive})
		}
		bad += check(base+".txt", p, *w, *h)
		if card.StripANSI(c) != p {
			fmt.Printf("FAIL %s.ansi: stripping the palette does not reproduce the plain frame\n", base)
			bad++
		}
	}

	// The card view and the prompt modal are the two states a token timeline
	// never reaches on its own, so they get their own frames.
	done := tui.ModelAt(tp, doneAt)
	done.TapePath = "~/.toktape/runs/" + tp.Summary.ID + ".tape"
	done.Grid, done.Page = grid, *page
	for _, extra := range []struct {
		name string
		mode tui.Mode
	}{{"card", tui.ModeCard}, {"prompt", tui.ModePrompt}} {
		m := done
		m.Mode = extra.mode
		plain := tui.View(m, doneAt, *w, *h)
		m.Theme = tui.ColourTheme()
		colour := tui.View(m, doneAt, *w, *h)
		base := filepath.Join(*out, fmt.Sprintf("%dx%d-n%d%s-%s-p%d-%s", *w, *h, *n, tag, grid.String(), *page, extra.name))
		write(base+".txt", plain)
		write(base+".ansi", colour)
		if *pngOut {
			writePNG(base+".png", tp, *w, *h, done.TapePath, render.Frame{At: doneAt, Anim: doneAt, Mode: extra.mode})
		}
		bad += check(base+".txt", plain, *w, *h)
		if card.StripANSI(colour) != plain {
			fmt.Printf("FAIL %s.ansi: stripping the palette does not reproduce the plain frame\n", base)
			bad++
		}
	}
	if bad > 0 {
		os.Exit(1)
	}
}

// dumpChat writes every example chat state as .txt, .ansi and, with -png, a
// still. Each state carries its own size, so -w and -h do not apply.
func dumpChat(out string, withPNG bool) int {
	bad := 0
	for _, st := range tui.ExampleChatStates() {
		plain := tui.ChatView(st.Model(tui.PlainTheme()), st.At, st.W, st.H)
		colour := tui.ChatView(st.Model(tui.ColourTheme()), st.At, st.W, st.H)
		base := filepath.Join(out, fmt.Sprintf("chat-%dx%d-%s", st.W, st.H, st.Name))
		write(base+".txt", plain)
		write(base+".ansi", colour)
		if withPNG {
			img, err := render.TextImage(colour, st.W, st.H)
			if err != nil {
				fail(err)
			}
			f, err := os.Create(base + ".png")
			if err != nil {
				fail(err)
			}
			if err := png.Encode(f, img); err != nil {
				f.Close()
				fail(err)
			}
			if err := f.Close(); err != nil {
				fail(err)
			}
		}
		bad += check(base+".txt", plain, st.W, st.H)
		if card.StripANSI(colour) != plain {
			fmt.Printf("FAIL %s.ansi: stripping the palette does not reproduce the plain frame\n", base)
			bad++
		}
	}
	return bad
}

// parseOffsets reads the -at list. Each name carries the millisecond so two
// frames a fifth of a second apart do not collide on one filename.
func parseOffsets(spec string) ([]struct {
	name string
	at   time.Duration
}, error) {
	var out []struct {
		name string
		at   time.Duration
	}
	if strings.TrimSpace(spec) == "" {
		return nil, nil
	}
	for _, f := range strings.Split(spec, ",") {
		d, err := time.ParseDuration(strings.TrimSpace(f))
		if err != nil {
			return nil, fmt.Errorf("-at %q: %w", f, err)
		}
		out = append(out, struct {
			name string
			at   time.Duration
		}{fmt.Sprintf("t%.3fs", d.Seconds()), d})
	}
	return out, nil
}

// check reports whether every line of a frame is exactly w columns and the
// frame is exactly h rows, and prints the measurement either way.
func check(name, frame string, w, h int) int {
	lines := strings.Split(frame, "\n")
	minW, maxW := 1<<30, 0
	for _, l := range lines {
		n := card.Width(l)
		if n < minW {
			minW = n
		}
		if n > maxW {
			maxW = n
		}
	}
	status := "ok  "
	bad := 0
	if minW != w || maxW != w || len(lines) != h {
		status = "FAIL"
		bad = 1
	}
	fmt.Printf("%s %-52s rows=%d want %d  width min=%d max=%d want %d\n",
		status, filepath.Base(name), len(lines), h, minW, maxW, w)
	return bad
}

// writePNG rasterises one instant of tp through the clip renderer. At is the
// run instant the model is cut at and Anim the t handed to tui.View; tuidump
// dumps a tape at the instant it names, so the two are the same.
func writePNG(name string, tp *tape.Tape, w, h int, tapePath string, f render.Frame) {
	img, err := render.FrameImage(tp, render.Options{Width: w, Height: h, TapePath: tapePath}, f)
	if err != nil {
		fail(err)
	}
	out, err := os.Create(name)
	if err != nil {
		fail(err)
	}
	if err := png.Encode(out, img); err != nil {
		out.Close()
		fail(fmt.Errorf("encode %s: %w", name, err))
	}
	if err := out.Close(); err != nil {
		fail(fmt.Errorf("close %s: %w", name, err))
	}
}

func write(name, content string) {
	if err := os.WriteFile(name, []byte(content+"\n"), 0o644); err != nil {
		fail(err)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "tuidump:", err)
	os.Exit(1)
}
