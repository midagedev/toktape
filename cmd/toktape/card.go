package main

import (
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/midagedev/toktape/internal/card"
	"github.com/midagedev/toktape/internal/card/png"
	"github.com/midagedev/toktape/internal/tape"
)

// runCard re-renders a recorded run. The tape is the record; this verb never
// recomputes a figure, it only chooses a rendering of the same summary.
func runCard(stdout, stderr io.Writer, args []string) int {
	fs := newFlagSet("card", stderr)
	var (
		md      = fs.Bool("md", false, "render Markdown with a llama-bench table")
		asJSON  = fs.Bool("json", false, "render the run summary as JSON")
		asPNG   = fs.Bool("png", false, "write the share image instead of printing a card")
		copyTo  = fs.Bool("copy", false, "copy the output to the clipboard (OSC 52)")
		outPath string
	)
	files, err := parseArgs(fs, args)
	if err != nil {
		return exitUsage
	}
	// With --png a second positional argument is where the image goes. Without
	// it, a second file is a mistake worth naming rather than ignoring.
	if *asPNG && len(files) == 2 {
		outPath, files = files[1], files[:1]
	}
	if len(files) != 1 {
		fmt.Fprintf(stderr, "toktape card: expected one tape file\n\n%s", usageText)
		return exitUsage
	}
	if *md && *asJSON {
		fmt.Fprintln(stderr, "toktape card: --md and --json are alternatives, not a pair")
		return exitUsage
	}

	tp, err := tape.Read(files[0])
	if err != nil {
		fmt.Fprintf(stderr, "toktape: %v\n", err)
		return exitUsage
	}

	if *asPNG {
		return writeCardPNG(stdout, stderr, files[0], outPath, &tp.Summary)
	}

	var out string
	switch {
	case *asJSON:
		b, err := card.JSON(&tp.Summary)
		if err != nil {
			fmt.Fprintf(stderr, "toktape: %v\n", err)
			return exitUsage
		}
		out = string(b) + "\n"
	case *md:
		out = card.Markdown(&tp.Summary)
	default:
		out = card.Text(&tp.Summary)
	}
	fmt.Fprint(stdout, out)

	if *copyTo {
		copyOSC52(stderr, out)
	}
	return exitOK
}

// writeCardPNG renders the share image for a tape.
//
// The default destination is next to the tape, named after it, because that is
// where the recording already put one and a second copy under a different name
// is how two cards of the same run get posted as if they were two runs. The
// path is printed to stdout so a script can pick it up.
func writeCardPNG(stdout, stderr io.Writer, tapePath, outPath string, s *tape.RunSummary) int {
	if outPath == "" {
		outPath = strings.TrimSuffix(tapePath, tape.Ext) + ".card.png"
	}
	if err := png.Write(outPath, s); err != nil {
		fmt.Fprintf(stderr, "toktape: %v\n", err)
		return exitUsage
	}
	fmt.Fprintln(stdout, outPath)
	return exitOK
}

// copyOSC52 asks the terminal to put s on the system clipboard.
//
// OSC 52 is the only clipboard path that works the same locally and over SSH,
// which is where this tool lives: the sequence is written to the terminal and
// the terminal, not the program, owns the clipboard. It is written to the
// controlling terminal rather than to stdout so a piped or redirected card
// does not gain an escape sequence it must not contain — the card's contract
// is that it carries no ANSI.
//
// The terminal may silently refuse (many do by default), so the message says
// what was attempted rather than claiming success.
func copyOSC52(stderr io.Writer, s string) {
	seq := "\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte(s)) + "\x07"
	tty, err := os.OpenFile("/dev/tty", os.O_WRONLY, 0)
	if err != nil {
		fmt.Fprintln(stderr, "Clipboard: no terminal to copy through (not a tty)")
		return
	}
	defer tty.Close()
	if _, err := io.WriteString(tty, seq); err != nil {
		fmt.Fprintf(stderr, "Clipboard: could not write to the terminal: %v\n", err)
		return
	}
	fmt.Fprintln(stderr, "Clipboard: sent to the terminal via OSC 52 (some terminals need it enabled)")
}
