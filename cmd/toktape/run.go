package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/midagedev/toktape/internal/card"
)

// Exit codes. They are part of the CLI contract: a wrapper script branches on
// them without parsing the message.
const (
	exitOK          = 0
	exitUsage       = 1
	exitUnreachable = 2
	exitStreams     = 3
)

const usageText = `toktape — the black-box tape for local LLM serving

Usage:
  toktape [flags]                 record a run (the default verb)
  toktape record [flags]          the same, spelled out
  toktape card <tape> [flags]     re-render a card from a run file
  toktape play <tape> [--speed N] replay a run on the live screen
  toktape render [tape] [flags]   render a run as a GIF, mp4, asciicast or frames
  toktape ls [--out DIR]          list recorded runs
  toktape log [--out DIR]         the experiment ledger of every run
  toktape compare <a> <b>         diff two runs
  toktape version                 print the version

Record flags:
  --url URL             server to attach to (default: discover)
  -n, --concurrency N   concurrent streams (default 1)
  --prompt TEXT         prompt to send; repeatable, cycled to fill -n
  --n-predict N         max tokens per stream (default 256)
  --out DIR             where run files are written (default ~/.toktape/runs)
  --tag TEXT            label this run for the experiment log (e.g. ngl=40)
  --note TEXT           a free-text note recorded with the run
  --wait DURATION       how long to wait for a loading model (default 10m,
                        0 = fail fast; naming it also waits for the server
                        itself to come up)
  --tui                 watch the run on the live two-pane screen
  --no-card             do not render or save the card
  --json                print the run summary as JSON instead of the card
  --quiet               no progress lines on stderr

Card flags:
  --md                  Markdown: the card in a fence plus a llama-bench table
  --json                the run summary as JSON
  --png [FILE]          write the 1200x675 share image (default: next to the tape)
  --copy                also copy the output to the clipboard (OSC 52)
`

// Run executes one invocation and returns the process exit code. It writes
// the product to stdout and everything else to stderr, so a card can be piped
// while the progress lines stay on the terminal.
func Run(ctx context.Context, stdout, stderr io.Writer, args []string) int {
	card.Version = version

	verb, rest := splitVerb(args)
	switch verb {
	case "help":
		fmt.Fprint(stdout, usageText)
		return exitOK
	case "version":
		fmt.Fprintf(stdout, "toktape %s\n", version)
		return exitOK
	case "record":
		return runRecord(ctx, stdout, stderr, rest)
	case "card":
		return runCard(stdout, stderr, rest)
	case "play":
		return runPlay(ctx, stdout, stderr, rest)
	case "render":
		return runRender(stdout, stderr, rest)
	case "ls":
		return runLs(stdout, stderr, rest)
	case "log":
		return runLog(stdout, stderr, rest)
	case "compare":
		return runCompare(stdout, stderr, rest)
	default:
		fmt.Fprintf(stderr, "toktape: unknown command %q\n\n%s", verb, usageText)
		return exitUsage
	}
}

// verbs are the commands Run dispatches on. The root verb is "record", so
// `toktape` and `toktape --url ...` both record.
var verbs = map[string]bool{
	"record": true, "card": true, "play": true, "ls": true, "log": true, "compare": true, "version": true,
}

// splitVerb picks the verb out of the argument list.
//
// A help token anywhere wins, wherever it sits: `toktape --url X --help` must
// print the text, not attach to X and start generating. The scan is over
// every argument rather than up to the first flag for exactly that reason.
//
// A leading word that is not a known verb is returned as the verb so Run can
// say "unknown command". Falling through to record instead would answer a
// mistyped `toktape recrd` with a complaint about a stray argument, which
// names the wrong problem.
func splitVerb(args []string) (verb string, rest []string) {
	for _, a := range args {
		if a == "-h" || a == "--help" || a == "help" {
			return "help", nil
		}
	}
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		return args[0], args[1:]
	}
	return "record", args
}

// parseArgs parses flags that are interspersed with positional arguments and
// returns the positional ones.
//
// Go's flag package stops at the first non-flag, so `toktape card run.tape
// --md` would otherwise leave --md unparsed and silently print the plain
// card. Every verb here takes its file arguments before its flags, because
// that is the order a person types them.
func parseArgs(fs *flag.FlagSet, args []string) ([]string, error) {
	var positional []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		if fs.NArg() == 0 {
			return positional, nil
		}
		positional = append(positional, fs.Arg(0))
		args = fs.Args()[1:]
	}
}

// newFlagSet returns a flag set that reports its errors on stderr and never
// calls os.Exit, so Run stays testable.
func newFlagSet(name string, stderr io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprint(stderr, usageText) }
	return fs
}

// repeatedFlag collects a flag that may be given more than once.
type repeatedFlag []string

func (r *repeatedFlag) String() string { return strings.Join(*r, ", ") }

func (r *repeatedFlag) Set(v string) error {
	*r = append(*r, v)
	return nil
}

// defaultRunsDir is where runs are kept: ~/.toktape/runs. When the home
// directory is unreadable the current directory is used, because losing the
// run file is worse than writing it somewhere unexpected.
func defaultRunsDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Join(".toktape", "runs")
	}
	return filepath.Join(home, ".toktape", "runs")
}

// tildePath shortens a path under the home directory for display. The paths
// the CLI prints are read by a person, and "~/.toktape/runs/..." is the form
// they can paste back.
func tildePath(p string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return p
	}
	if p == home {
		return "~"
	}
	if rel, err := filepath.Rel(home, p); err == nil && !strings.HasPrefix(rel, "..") {
		return filepath.Join("~", rel)
	}
	return p
}
