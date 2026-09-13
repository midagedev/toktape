// Command tuidump renders frames of the example tape to text files without a
// terminal.
//
// It exists so a layout change can be inspected the way the lead inspects it —
// by reading frames — and so the same frames can be checked mechanically. Each
// frame is written twice: a plain file that is the layout, and an .ansi file
// that is the same layout with the palette on. The two must differ only in
// escape sequences, which is the invariant the package's colour test pins and
// which this command re-checks before it exits.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/midagedev/toktape/internal/card"
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
	flag.Parse()

	grid, err := tui.ParseGrid(*gridSpec)
	if err != nil {
		fail(err)
	}

	if err := os.MkdirAll(*out, 0o755); err != nil {
		fail(err)
	}
	tp := tui.ExampleTapeN(*n)
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
	if len(extras) > 0 {
		offsets = extras
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

		base := filepath.Join(*out, fmt.Sprintf("%dx%d-n%d-%s-p%d-%s", *w, *h, *n, grid.String(), *page, off.name))
		write(base+".txt", p)
		write(base+".ansi", c)
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
		base := filepath.Join(*out, fmt.Sprintf("%dx%d-n%d-%s-p%d-%s", *w, *h, *n, grid.String(), *page, extra.name))
		write(base+".txt", plain)
		write(base+".ansi", colour)
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

func write(name, content string) {
	if err := os.WriteFile(name, []byte(content+"\n"), 0o644); err != nil {
		fail(err)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "tuidump:", err)
	os.Exit(1)
}
