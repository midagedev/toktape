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
	flag.Parse()

	offsets := []struct {
		name string
		at   time.Duration
	}{
		{"t0.0s", 0},
		{"t1.5s", 1500 * time.Millisecond},
		{"t4.0s", 4 * time.Second},
		{"t8.0s-done", 8 * time.Second},
	}

	if err := os.MkdirAll(*out, 0o755); err != nil {
		fail(err)
	}
	tp := tui.ExampleTape()
	bad := 0
	for _, off := range offsets {
		plain := tui.ModelAt(tp, off.at)
		plain.TapePath = "~/.toktape/runs/20260913-150210-qwen3.5-35b-a3b.tape"
		colour := plain
		colour.Theme = tui.ColourTheme()

		p := tui.View(plain, off.at, *w, *h)
		c := tui.View(colour, off.at, *w, *h)

		base := filepath.Join(*out, fmt.Sprintf("%dx%d-%s", *w, *h, off.name))
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
	done := tui.ModelAt(tp, 8*time.Second)
	done.TapePath = "~/.toktape/runs/20260913-150210-qwen3.5-35b-a3b.tape"
	for _, extra := range []struct {
		name string
		mode tui.Mode
	}{{"card", tui.ModeCard}, {"prompt", tui.ModePrompt}} {
		m := done
		m.Mode = extra.mode
		plain := tui.View(m, 8*time.Second, *w, *h)
		m.Theme = tui.ColourTheme()
		colour := tui.View(m, 8*time.Second, *w, *h)
		base := filepath.Join(*out, fmt.Sprintf("%dx%d-%s", *w, *h, extra.name))
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
