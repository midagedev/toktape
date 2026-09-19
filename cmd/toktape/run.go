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
// them without parsing the message. Their names are in exitCodeNames, they are
// listed in usageText, and every one of them is reached through cli.fail, so
// the code, the sentence and the -o json object cannot disagree.
const (
	exitOK    = 0
	exitUsage = 1
	// exitUnreachable: no server answered, or one never finished loading.
	exitUnreachable = 2
	// exitStreams: the server answered and every stream failed.
	exitStreams = 3
	// exitUnavailable: this machine is missing something the requested
	// output needs — today that is ffmpeg for --mp4. It is separate from
	// exitStreams because "this box has no encoder" and "the server answered
	// nothing" are different things for a wrapper script to branch on.
	exitUnavailable = 4
	// exitPublish: nothing was published. The service refused the upload, it
	// could not be reached, or the first-publish question was answered no. It
	// is separate from exitUnreachable because "the publishing service did
	// not answer" and "no llama-server answered" are different problems with
	// different fixes, and a wrapper script has to tell them apart.
	exitPublish = 5
)

const usageText = `toktape — the black-box tape for local LLM serving

Usage:
  toktape [flags]                 record a run (the default verb)
  toktape record [flags]          the same, spelled out
  toktape card <tape> [flags]     re-render a card from a run file
  toktape play <tape> [--speed N] [--grid CxR]  replay a run on the live screen
  toktape render [tape] [flags]   render a run as a GIF, mp4, asciicast or frames
  toktape ls [--out DIR]          list recorded runs
  toktape log [--out DIR]         the experiment ledger of every run
  toktape compare <a> <b>         diff two runs
  toktape publish <tape> [flags]  upload a run and print its link
  toktape runs [flags]            list published runs on the service
  toktape show <id|url> [flags]   read one published run
  toktape version                 print the version
  toktape help agents             the contract a script or coding agent needs

Record flags:
  --for DURATION        end the run after this much wall clock, from the first
                        request (default 20s, --for 0 = none). No stream is
                        cut under 64 tokens, so a slow box runs longer
  -n, --n-predict N     max tokens per stream, as llama-bench's -n. An answer on
                        the same axis, so naming it turns the clock off; name
                        both and the run ends at whichever comes first
  --url URL             server to attach to (default: discover)
  --sessions N          streams sent at once (default 1, at most 8)
  --max-sessions N      raise that ceiling; give it the same N as --sessions
  --prompt TEXT         prompt to send; repeatable, cycled to fill --sessions
  --prompts FILE        a JSONL file, one round of streams per line, e.g.
                        {"name":"sql","prompt":"Write a query that ..."}
  --spec-n-max LIST     run the prompt set once per speculative.n_max (e.g. 3,5)
  --temp N              sampling temperature (0 = greedy; unset = server default)
  --no-think            ask a reasoning model not to think (chat only)
  --think-budget N      cap a reasoning model's thinking at N tokens (chat only)
  --endpoint NAME       chat (templated) or completion (prompt sent verbatim)
  --engine-kind NAME    server protocol: auto (default), llama or openai (any
                        OpenAI-compatible server via /v1/models; client-timed)
  --engine TEXT         name that engine, e.g. "vLLM 0.11" (a claim)
  --param key=value     extra request parameter, repeatable (JSON value if valid)
  --ram-gbs N           host memory bandwidth in GB/s, as you state it
  --ram-gbs-measured N  the same, as STREAM measured it (the tape says which)
  --ram-speed NAME      memory type, e.g. DDR5-5200 (Linux cannot read it)
  --ram-channels N      populated memory channels, e.g. 8 (with --ram-speed)
  --host-label TEXT     store TEXT as the hostname, or none if TEXT is empty,
                        so a tape you post never carried the machine's name
  --out DIR             where run files are written (default ~/.toktape/runs)
  --tag TEXT            label this run for the experiment log (e.g. ngl=40)
  --note TEXT           a free-text note recorded with the run
  --wait DURATION       how long to wait for a loading model. The default is
                        10m, so one invocation can block that long; --wait 0
                        fails fast, which is what a script with a command
                        timeout wants. Naming it also waits for the server
  --tui                 watch the run on the live two-pane screen
  --grid COLSxROWS      tiles per page on the live screen (default 2x4; 0
                        fits it to the terminal). ←/→ change page
  --no-card             do not render or save the card
  -o, --output FORMAT   json, jsonl, md, csv, tsv or sql instead of the card
  --quiet               no progress lines on stderr

Card flags:
  -o, --output FORMAT   any record takes, or png [FILE]: the 1200x675 share image
                        (default: next to the tape). md is the card in a fence,
                        a llama-bench table and a Reproduce block — more than
                        llama-bench's own -o md, which is the table alone
  --copy                also copy the output to the clipboard (OSC 52)
  --explain             why the card says what it says, on stderr as well

Publish flags:
  --dry-run             print what would be uploaded and upload nothing. A run
                        is public and carries its text by default, and this
                        lists field by field what that means for this tape
  --private             keep the run out of the search (the link still works)
  --no-text, --with-text  with or without the prompts and the generated text,
                        over publish_text in ~/.toktape/config.toml
  --yes                 take the first-publish warning as read
  --url URL             the service to publish to

  Hostnames and absolute paths are removed whatever the visibility is.

Examples:
  # One stream against a server toktape finds itself, for 20s. No flags.
  toktape

  # Aim the run at a clip length: a clip is the run at 1:1 plus 6s of frame,
  # 12s with --open, so --for 18s makes a ~30s one. EOS usually arrives first
  # and the run is then shorter — the prompt's doing, not the machine's.
  toktape --for 18s

  # A server on a port discovery does not probe, four streams at once, and 256
  # tokens each instead of a clock. -n is tokens, as in llama-bench.
  toktape --url http://127.0.0.1:9000 --sessions 4 -n 256

  # Machine-readable: the run summary on stdout and nothing else.
  toktape -o json --quiet > run.json

  # Every run recorded here as a SQLite table, its numbers stored as numbers.
  toktape log -o sql | sqlite3 runs.db

  # Re-render the card of a run you already have. Costs nothing and touches
  # no server.
  toktape card ~/.toktape/runs/20260914-070458-my-model.tape

  # A clip of that run. --for aims the run when you record; render --duration
  # is not the same flag — it squeezes the run you have into the time you
  # name. toktape help render has the arithmetic.
  toktape render ~/.toktape/runs/20260914-070458-my-model.tape --mp4 run.mp4 --open

Exit codes:
  0  ok
  1  usage        the invocation or its inputs were rejected: a bad flag, an
                  unreadable tape, an output that could not be written
  2  unreachable  no server answered, or one never finished loading
  3  streams      the server answered and every stream failed
  4  unavailable  this machine lacks something the output needs (ffmpeg)
  5  publish      nothing was published: the service refused or was not
                  reached, or the first-publish question was answered no

With -o json or -o jsonl every outcome is one JSON object on stdout: the run
summary on success, {"error":{"code":...}} on failure, code being the name
above. The fields worth reading: toktape help agents
`

// Run executes one invocation and returns the process exit code. It writes
// the product to stdout and everything else to stderr, so a card can be piped
// while the progress lines stay on the terminal.
func Run(ctx context.Context, stdout, stderr io.Writer, args []string) int {
	card.Version = version

	// The verb sets c.json once its own flags are parsed. Until then the raw
	// arguments are the only answer available, and they are what an
	// invocation rejected before any parsing (an unknown verb) is answered
	// from.
	c := &cli{stdout: stdout, stderr: stderr}

	verb, rest := splitVerb(args)
	switch verb {
	case "help":
		c.json = jsonRequested(args)
		return runHelp(c, rest)
	case "version":
		fmt.Fprintf(stdout, "toktape %s\n", version)
		return exitOK
	case "record":
		return runRecord(ctx, c, rest)
	case "card":
		return runCard(c, rest)
	case "play":
		return runPlay(ctx, c, rest)
	case "render":
		return runRender(c, rest)
	case "ls":
		return runLs(c, rest)
	case "log":
		return runLog(c, rest)
	case "compare":
		return runCompare(c, rest)
	case "publish":
		return runPublish(ctx, c, rest)
	case "runs":
		return runRuns(ctx, c, rest)
	case "show":
		return runShow(ctx, c, rest)
	case "profile":
		return runProfile(c, rest)
	default:
		c.json = jsonRequested(args)
		return c.usageTextf(usageText, "toktape: unknown command %q", verb)
	}
}

// verbs are the commands Run dispatches on. The root verb is "record", so
// `toktape` and `toktape --url ...` both record.
var verbs = map[string]bool{
	"record": true, "card": true, "play": true, "render": true,
	"ls": true, "log": true, "compare": true, "publish": true, "runs": true, "show": true, "profile": true, "version": true,
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
	// `toktape help <topic>` is the one spelling whose remainder matters, so
	// it is recognised first; a help token found anywhere else is the
	// "show me the flags" gesture and takes no topic.
	if len(args) > 0 && args[0] == "help" {
		return "help", args[1:]
	}
	for _, a := range args {
		if a == "-h" || a == "--help" || a == "help" {
			// `toktape render --help` asks about render, not about toktape
			// (TTP-92): the verb it was typed after is the topic, so both
			// spellings resolve through runHelp to the same text.
			if len(args) > 0 && verbs[args[0]] {
				return "help", args[:1]
			}
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
// -o md` would otherwise leave -o unparsed and silently print the plain
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

// newFlagSet returns a flag set that never calls os.Exit, so Run stays
// testable, and that says nothing of its own.
//
// Its output is discarded because a rejected invocation owes three things at
// once — an exit code, a sentence and, under -o json, an object on stdout — and
// the flag package can only provide one of them. cli.badFlags writes all three
// from the error it returns instead (TTP-71).
func newFlagSet(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
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

// noRunsMessage is what every verb says about an empty runs directory. It
// names the way out for the case that reads as data loss (TTP-60,
// 2026-09-14): after `record --out DIR` a bare `toktape ls` prints nothing,
// and a user who does not remember the flag concludes the tape is gone. The
// CLI keeps no state between runs on purpose — a remembered directory would
// be a default nobody asked for — so the message says where to look instead.
func noRunsMessage(dir string) string {
	return fmt.Sprintf("No runs yet in %s. Run `toktape` to record one.\n"+
		"A run recorded with --out DIR is found by `toktape ls --out DIR`.\n", tildePath(dir))
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
