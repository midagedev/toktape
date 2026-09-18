package main

import (
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/midagedev/toktape/internal/bandwidth"
	"github.com/midagedev/toktape/internal/card"
	"github.com/midagedev/toktape/internal/card/png"
	"github.com/midagedev/toktape/internal/tape"
)

// cardFlags is every flag the card verb declares. It is a struct for the same
// reason recordFlags is: the examples printed in --help are parsed against the
// real flag set, and a copy of the list in a test is what goes stale.
type cardFlags struct {
	output  *string
	copyTo  *bool
	explain *bool
}

// declareCardFlags registers the card verb's flags on fs.
func declareCardFlags(fs *flag.FlagSet) *cardFlags {
	return &cardFlags{
		output: declareOutputFlag(fs),
		copyTo: fs.Bool("copy", false, "copy the output to the clipboard (OSC 52)"),
		// One flag for two packages (TTP-74/TTP-56, 2026-09-14). The question
		// it answers is "why does this card not say X", and the reader asking
		// it does not know whether the qualification was never raised
		// (internal/card) or a bandwidth clause found nothing to divide
		// (internal/bandwidth) — which is exactly what they are running this
		// to find out. Two flags would ask them to guess the answer in order
		// to choose the flag.
		explain: fs.Bool("explain", false, "on stderr: every qualification check and every bandwidth figure, fired or not"),
	}
}

// runCard re-renders a recorded run. The tape is the record; this verb never
// recomputes a figure, it only chooses a rendering of the same summary.
func runCard(c *cli, args []string) int {
	fs := newFlagSet("card")
	f := declareCardFlags(fs)
	var outPath string
	files, err := parseArgs(fs, args)
	if err != nil {
		return c.badFlags("card", usageText, args, err)
	}
	format, refused := outputFor("card", *f.output)
	c.json = format.isJSON()
	if refused != nil {
		return c.fail(*refused)
	}
	// With -o png a second positional argument is where the image goes.
	// Without it, a second file is a mistake worth naming rather than ignoring.
	if format == outputPNG && len(files) == 2 {
		outPath, files = files[1], files[:1]
	}
	if len(files) != 1 {
		return c.usageTextf(usageText, "toktape card: expected one tape file")
	}

	tp, err := tape.Read(files[0])
	if err != nil {
		return c.usagef("toktape: %v", err)
	}

	if *f.explain {
		explainCard(c.stderr, &tp.Summary)
	}

	if format == outputPNG {
		return writeCardPNG(c, files[0], outPath, &tp.Summary)
	}

	out, err := renderRun(tp, format)
	if err != nil {
		return c.usagef("toktape: %v", err)
	}
	fmt.Fprint(c.stdout, out)

	if *f.copyTo {
		copyToClipboard(c.stderr, out)
	}
	return exitOK
}

// explainCard writes the two explainers for s: every qualification check the
// card can make, fired or not, and every figure the bandwidth clauses are
// built from, including the ones that made a clause report nothing.
//
// It goes to stderr, which is where this program's diagnostics go (see Run):
// stdout carries the product, and with -o json it carries exactly one JSON
// object, which is a contract a diagnostic must not be able to break. That
// also makes the flag an ADDITION to whatever rendering was asked for rather
// than an alternative to it — `card -o json --explain 2>why.txt` leaves a
// parseable object on stdout and the workings in a file.
//
// Neither explainer can fail. A run they can say nothing about produces a
// listing whose every verdict is "no" and whose every figure is "?", which is
// itself the answer to "why is the card not printing it".
//
// An engine run (2026-09-15, ExLlamaV3) gets a third listing first: where its
// figures came from and what the recorder made of them. It goes first because
// it reframes the other two — a "?" in the caveat listing of a run whose
// placement was never replayed means something different from a "?" on a
// GGUF run, and the reader should know which card they are reading before the
// verdicts start.
func explainCard(w io.Writer, s *tape.RunSummary) {
	if e := card.ExplainEnginePlacement(s); e != "" {
		fmt.Fprint(w, e)
		fmt.Fprintln(w)
	}
	fmt.Fprint(w, card.ExplainCaveats(s))
	fmt.Fprintln(w)
	fmt.Fprint(w, bandwidth.Explain(s).String())
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
