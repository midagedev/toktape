package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/midagedev/toktape/internal/card"
	"github.com/midagedev/toktape/internal/card/png"
	"github.com/midagedev/toktape/internal/recorder"
	"github.com/midagedev/toktape/internal/server"
	"github.com/midagedev/toktape/internal/tape"
)

// defaultNPredict caps the answer so the zero-config run finishes in a few
// seconds and still clears tape.MinDecodeTokens by a wide margin — a rate
// measured over fewer tokens may not be called "decode" at all.
const defaultNPredict = 256

// pinCollectors is the seam the tests use to keep the record verb off this
// machine. In production it is the identity: the recorder then reads the live
// /proc and opens whatever GPU backend is present. A test replaces it so a
// CLI run cannot scan the real process table or exec nvidia-smi, which would
// make the result depend on the box the suite happens to run on.
var pinCollectors = func(o recorder.Options) recorder.Options { return o }

// recordConfig is what the record verb decided from its flags, apart from the
// recorder's own options. It exists so the plain run and the --tui run take
// the same shape and cannot drift in what they save or print.
type recordConfig struct {
	outDir string
	// card is false under --no-card: no card is rendered, saved or printed.
	card bool
	// asJSON prints the summary instead of the card.
	asJSON bool
	quiet  bool
}

// runRecord is the root verb: attach, record, save, print the card.
func runRecord(ctx context.Context, stdout, stderr io.Writer, args []string) int {
	fs := newFlagSet("record", stderr)
	var (
		url         = fs.String("url", "", "server base URL (default: discover)")
		concurrency = fs.Int("concurrency", 0, "concurrent streams")
		nPredict    = fs.Int("n-predict", defaultNPredict, "max tokens per stream")
		outDir      = fs.String("out", defaultRunsDir(), "directory for run files")
		noCard      = fs.Bool("no-card", false, "do not render or save the card")
		asJSON      = fs.Bool("json", false, "print the run summary as JSON")
		quiet       = fs.Bool("quiet", false, "no progress lines on stderr")
		useTUI      = fs.Bool("tui", false, "watch the run on the live two-pane screen")
		wait        = fs.Duration("wait", recorder.DefaultWaitForModel,
			"how long to wait for a server that is still loading its model (0 = fail fast)")
		prompts repeatedFlag
	)
	fs.IntVar(concurrency, "n", 0, "concurrent streams (shorthand)")
	fs.Var(&prompts, "prompt", "prompt to send; repeatable")
	extra, err := parseArgs(fs, args)
	if err != nil {
		return exitUsage
	}
	if len(extra) > 0 {
		fmt.Fprintf(stderr, "toktape record: unexpected argument %q\n", extra[0])
		return exitUsage
	}

	opts := recorder.Options{
		BaseURL:      *url,
		Prompts:      promptRequests(prompts),
		Concurrency:  *concurrency,
		MaxTokens:    *nPredict,
		Version:      version,
		WaitForModel: waitBudget(fs, *wait),
		// Waiting for a server that is not listening yet is only done when
		// the user asked for it by naming a --wait: it is the "I started
		// both at once" case, and without the flag a mistyped --url must
		// still fail in a second rather than in ten minutes.
		WaitForStart: flagSet(fs, "wait") && *wait > 0,
	}
	opts = pinCollectors(opts)
	cfg := recordConfig{outDir: *outDir, card: !*noCard, asJSON: *asJSON, quiet: *quiet}

	if *useTUI {
		if isTTY(stdout) {
			return recordTUI(ctx, stdout, stderr, opts, cfg)
		}
		fmt.Fprintln(stderr, "toktape: --tui needs a terminal on stdout; using progress lines instead")
	}
	return recordPlain(ctx, stdout, stderr, opts, cfg)
}

// recordPlain is the non-interactive run: progress lines on stderr, the card
// on stdout.
func recordPlain(ctx context.Context, stdout, stderr io.Writer, opts recorder.Options, cfg recordConfig) int {
	pr := newProgress(stderr, cfg.quiet)
	opts.Progress = pr.handle

	pr.start()
	tp, err := recorder.Record(ctx, opts)
	pr.stop()
	if err != nil {
		return reportRecordError(stderr, err)
	}

	arts, err := saveRun(cfg.outDir, tp, cfg.card)
	if err != nil {
		fmt.Fprintf(stderr, "toktape: %v\n", err)
		if arts.tape == "" {
			return exitUsage
		}
	}
	if code := printCard(stdout, stderr, tp, cfg); code != exitOK {
		return code
	}
	if !cfg.quiet {
		fmt.Fprint(stderr, shareHint(cfg.outDir, tp, arts))
	}
	return exitOK
}

// reportRecordError maps a failed run to its exit code. The codes are part of
// the CLI contract, so the mapping lives in one place.
func reportRecordError(stderr io.Writer, err error) int {
	fmt.Fprintf(stderr, "toktape: %v\n", err)
	switch {
	case errors.Is(err, recorder.ErrUnreachable):
		return exitUnreachable
	case errors.Is(err, recorder.ErrAllStreamsFailed):
		return exitStreams
	default:
		return exitUsage
	}
}

// printCard writes the run's product to stdout: the card, or the summary under
// --json, or nothing under --no-card.
func printCard(stdout, stderr io.Writer, tp *tape.Tape, cfg recordConfig) int {
	switch {
	case cfg.asJSON:
		b, err := card.JSON(&tp.Summary)
		if err != nil {
			fmt.Fprintf(stderr, "toktape: %v\n", err)
			return exitUsage
		}
		fmt.Fprintf(stdout, "%s\n", b)
	case cfg.card:
		fmt.Fprint(stdout, card.Text(&tp.Summary))
	}
	return exitOK
}

// artifacts are the files one run left on disk.
type artifacts struct {
	tape     string
	cardText string
	cardPNG  string
}

// saveRun writes the tape and, unless the run was asked not to, both cards.
//
// The PNG is written on every run rather than on request because it is the
// thing that actually gets posted: a card that has to be asked for is a card
// that does not exist when the user is about to close the terminal. A failure
// to render it is reported and does not fail the run — the tape is the record,
// and it is already on disk by then.
func saveRun(outDir string, tp *tape.Tape, wantCard bool) (artifacts, error) {
	var a artifacts
	a.tape = filepath.Join(outDir, tp.Summary.ID+tape.Ext)
	if err := tape.Write(a.tape, tp); err != nil {
		a.tape = ""
		return a, fmt.Errorf("saving the run file: %w", err)
	}
	if !wantCard {
		return a, nil
	}

	textPath := filepath.Join(outDir, tp.Summary.ID+".card.txt")
	if err := os.WriteFile(textPath, []byte(card.Text(&tp.Summary)), 0o644); err != nil {
		return a, fmt.Errorf("saving the card: %w", err)
	}
	a.cardText = textPath

	pngPath := filepath.Join(outDir, tp.Summary.ID+".card.png")
	if err := png.Write(pngPath, &tp.Summary); err != nil {
		return a, fmt.Errorf("saving the image card: %w", err)
	}
	a.cardPNG = pngPath
	return a, nil
}

// shareHint is the block a finished run ends with.
//
// It is the last thing a first-time user reads, so it names what exists and
// the two commands that do something with it, and nothing else. The compare
// line appears only when there is an earlier run of the same model to compare
// against: an instruction that cannot be followed is worse than no line.
func shareHint(outDir string, tp *tape.Tape, a artifacts) string {
	var b strings.Builder
	if a.tape != "" {
		fmt.Fprintf(&b, "✓ Tape   %s\n", tildePath(a.tape))
	}
	switch {
	case a.cardText != "" && a.cardPNG != "":
		fmt.Fprintf(&b, "✓ Card   %s · %s\n", tildePath(a.cardText), filepath.Base(a.cardPNG))
	case a.cardText != "":
		fmt.Fprintf(&b, "✓ Card   %s\n", tildePath(a.cardText))
	}
	if a.tape == "" {
		return b.String()
	}
	fmt.Fprintf(&b, "→ Post it:  toktape card %s --md --copy    (Reddit-ready, copied to clipboard)\n",
		tildePath(a.tape))
	if prev := previousTape(outDir, tp); prev != "" {
		fmt.Fprintf(&b, "→ Compare:  toktape compare %s %s\n", tildePath(prev), tildePath(a.tape))
	}
	return b.String()
}

// previousTape is the newest earlier run of the same model in outDir, or "".
//
// Run IDs are <date>-<time>-<model slug>, so the same model sorts together and
// lexical order is chronological order. Only the names are read: opening every
// tape in a directory to answer one hint line would make a finished run wait
// on disk it does not need.
func previousTape(outDir string, tp *tape.Tape) string {
	slug := tape.SlugFromModel(tp.Summary.Model.FileName)
	if slug == "" {
		return ""
	}
	matches, err := filepath.Glob(filepath.Join(outDir, "*-"+slug+tape.Ext))
	if err != nil {
		return ""
	}
	current := filepath.Join(outDir, tp.Summary.ID+tape.Ext)
	best := ""
	for _, m := range matches {
		if m == current {
			continue
		}
		if filepath.Base(m) > filepath.Base(current) {
			continue // a run from the future is not a previous run
		}
		if best == "" || filepath.Base(m) > filepath.Base(best) {
			best = m
		}
	}
	return best
}

// waitBudget turns the --wait flag into Options.WaitForModel. An explicit
// `--wait 0` is fail fast, which the recorder spells NoWait because zero is
// the zero value and the zero value has to stay the sane default.
func waitBudget(fs *flag.FlagSet, wait time.Duration) time.Duration {
	if flagSet(fs, "wait") && wait <= 0 {
		return recorder.NoWait
	}
	return wait
}

// flagSet reports whether the named flag was given on the command line, as
// opposed to left at its default.
func flagSet(fs *flag.FlagSet, name string) bool {
	found := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == name {
			found = true
		}
	})
	return found
}

// promptRequests turns the repeated --prompt values into stream requests. No
// prompt at all means the recorder picks its deterministic default set, which
// is what makes the zero-argument run comparable between two machines.
func promptRequests(prompts []string) []server.StreamRequest {
	if len(prompts) == 0 {
		return nil
	}
	out := make([]server.StreamRequest, 0, len(prompts))
	for _, p := range prompts {
		out = append(out, server.StreamRequest{
			Messages: []tape.Message{{Role: "user", Content: p}},
		})
	}
	return out
}

// headerLine is the one line printed the moment the run is attached:
// what server, what build, what model, which process.
func headerLine(s *tape.RunSummary) string {
	kind := string(s.Server.Kind)
	if kind == "" {
		kind = string(tape.ServerUnknown)
	}
	parts := []string{fmt.Sprintf("→ %s at %s", kind, s.Server.URL)}
	if s.Server.Build != "" {
		parts[0] += " (" + s.Server.Build + ")"
	}

	// The quant is appended only when the label does not already carry it.
	// Falling back to the file name means the label is often
	// "Qwen3.5-35B-A3B-UD-Q4_K_M", and appending the quant to that would
	// print it twice.
	model := s.Model.Name
	if model == "" {
		model = strings.TrimSuffix(s.Model.FileName, ".gguf")
	}
	if model == "" {
		model = "?"
	}
	if q := s.Model.Quant; q != "" && !strings.Contains(model, q) {
		model += " " + q
	}
	parts = append(parts, model)

	if s.Server.PID > 0 {
		parts = append(parts, fmt.Sprintf("pid %d", s.Server.PID))
	} else {
		parts = append(parts, "no /proc view")
	}
	return strings.Join(parts, " · ")
}
