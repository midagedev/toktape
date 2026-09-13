package main

import (
	"encoding/base64"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/midagedev/toktape/internal/card"
	"github.com/midagedev/toktape/internal/card/png"
	"github.com/midagedev/toktape/internal/tape"
)

// cardFlags is every flag the card verb declares. It is a struct for the same
// reason recordFlags is: the examples printed in --help are parsed against the
// real flag set, and a copy of the list in a test is what goes stale.
type cardFlags struct {
	md     *bool
	asJSON *bool
	asPNG  *bool
	copyTo *bool
}

// declareCardFlags registers the card verb's flags on fs.
func declareCardFlags(fs *flag.FlagSet) *cardFlags {
	return &cardFlags{
		md:     fs.Bool("md", false, "render Markdown with a llama-bench table"),
		asJSON: fs.Bool("json", false, "render the run summary as JSON"),
		asPNG:  fs.Bool("png", false, "write the share image instead of printing a card"),
		copyTo: fs.Bool("copy", false, "copy the output to the clipboard (OSC 52)"),
	}
}

// runCard re-renders a recorded run. The tape is the record; this verb never
// recomputes a figure, it only chooses a rendering of the same summary.
func runCard(c *cli, args []string) int {
	fs := newFlagSet("card")
	f := declareCardFlags(fs)
	md, asJSON, asPNG, copyTo := f.md, f.asJSON, f.asPNG, f.copyTo
	var outPath string
	files, err := parseArgs(fs, args)
	if err != nil {
		return c.badFlags("card", usageText, args, err)
	}
	c.json = *asJSON
	// With --png a second positional argument is where the image goes. Without
	// it, a second file is a mistake worth naming rather than ignoring.
	if *asPNG && len(files) == 2 {
		outPath, files = files[1], files[:1]
	}
	if len(files) != 1 {
		return c.usageTextf(usageText, "toktape card: expected one tape file")
	}
	if *md && *asJSON {
		return c.usagef("toktape card: --md and --json are alternatives, not a pair")
	}

	tp, err := tape.Read(files[0])
	if err != nil {
		return c.usagef("toktape: %v", err)
	}

	if *asPNG {
		return writeCardPNG(c, files[0], outPath, &tp.Summary)
	}

	var out string
	switch {
	case *asJSON:
		b, err := card.JSON(&tp.Summary)
		if err != nil {
			return c.usagef("toktape: %v", err)
		}
		out = string(b) + "\n"
	case *md:
		out = card.Markdown(&tp.Summary)
	default:
		out = card.Text(&tp.Summary)
	}
	fmt.Fprint(c.stdout, out)

	if *copyTo {
		copyOSC52(c.stderr, out)
	}
	return exitOK
}

// writeCardPNG renders the share image for a tape.
//
// The default destination is next to the tape, named after it, because that is
// where the recording already put one and a second copy under a different name
// is how two cards of the same run get posted as if they were two runs. The
// path is printed to stdout so a script can pick it up.
func writeCardPNG(c *cli, tapePath, outPath string, s *tape.RunSummary) int {
	if outPath == "" {
		outPath = strings.TrimSuffix(tapePath, tape.Ext) + ".card.png"
	}
	if err := png.Write(outPath, s); err != nil {
		return c.usagef("toktape: %v", err)
	}
	fmt.Fprintln(c.stdout, outPath)
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
