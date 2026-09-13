package main

import (
	"context"
	"encoding/json"
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
	"github.com/midagedev/toktape/internal/ledger"
	"github.com/midagedev/toktape/internal/recorder"
	"github.com/midagedev/toktape/internal/server"
	"github.com/midagedev/toktape/internal/tape"
	"github.com/midagedev/toktape/internal/tui"
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

// runLabels are the --tag and --note of one invocation: the user's words for
// the experiment this run belongs to.
type runLabels struct{ tag, note string }

// recordLabels is where runRecord leaves them for saveRun.
//
// saveRun is the one place both the plain run and the --tui run pass through
// on their way to the tape, and it lives here; threading the labels through
// its arguments instead would change a signature that cmd/toktape/tui.go
// calls, which this track does not own. One invocation records one run, and
// runRecord always assigns the pair, so nothing leaks between two Run calls
// in the same process.
var recordLabels runLabels

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
		grid        = fs.String("grid", tui.DefaultGrid.String(), "tile grid per page as COLSxROWS (0 = fit to the terminal)")
		wait        = fs.Duration("wait", recorder.DefaultWaitForModel,
			"how long to wait for a server that is still loading its model (0 = fail fast)")
		// tag and note label the experiment this run belongs to. They are
		// recorded in the tape, not only in the ledger, so `toktape log
		// --rebuild` can never lose them.
		tag     = fs.String("tag", "", "label this run for the experiment log (e.g. ngl=40)")
		note    = fs.String("note", "", "a free-text note recorded with the run")
		prompts repeatedFlag
		// promptsFile is the multi-prompt run (TTP-31): each JSONL line is
		// its own round of streams, all in one tape.
		promptsFile = fs.String("prompts", "", "JSONL file, one prompt per line, each sent as its own round")
		// specNMax is the speculative block-size sweep (TTP-35): the prompt
		// set runs once per speculative.n_max value, all in one tape.
		specNMax = fs.String("spec-n-max", "", "run the prompt set once per speculative.n_max (e.g. 3,5)")
		// Sampling and the raw path (TTP-55). Without these the card could
		// not show the engine's real number: measured 2026-09-14, the chat
		// path with thinking on ran 11.6 % slower than greedy /completion on
		// the same prompts and the same server.
		temp     = fs.Float64("temp", 0, "sampling temperature (0 = greedy); unset = the server's default")
		noThink  = fs.Bool("no-think", false, "ask a reasoning model not to think (chat only)")
		endpoint = fs.String("endpoint", tape.EndpointChat, "chat (templated) or completion (the prompt sent verbatim)")
		params   repeatedFlag
	)
	fs.IntVar(concurrency, "n", 0, "concurrent streams (shorthand)")
	fs.Var(&prompts, "prompt", "prompt to send; repeatable")
	fs.Var(&params, "param", "extra request parameter as key=value; repeatable")
	extra, err := parseArgs(fs, args)
	if err != nil {
		return exitUsage
	}
	if len(extra) > 0 {
		fmt.Fprintf(stderr, "toktape record: unexpected argument %q\n", extra[0])
		return exitUsage
	}

	var rounds []recorder.Round
	if flagSet(fs, "prompts") {
		if len(prompts) > 0 {
			fmt.Fprintln(stderr, "toktape record: --prompts and --prompt are alternatives, not a pair")
			return exitUsage
		}
		if *promptsFile == "" {
			fmt.Fprintln(stderr, "toktape record: --prompts needs a file")
			return exitUsage
		}
		rounds, err = readPromptsFile(*promptsFile)
		if err != nil {
			fmt.Fprintf(stderr, "toktape record: --prompts %s: %v\n", *promptsFile, err)
			return exitUsage
		}
	}

	var sweep []int
	if flagSet(fs, "spec-n-max") {
		sweep, err = recorder.ParseSpecNMax(*specNMax)
		if err != nil {
			fmt.Fprintf(stderr, "toktape record: --spec-n-max %s: %v\n", *specNMax, err)
			return exitUsage
		}
	}

	sampling, err := samplingOptions(*endpoint, flagSet(fs, "temp"), *temp, *noThink, params)
	if err != nil {
		fmt.Fprintf(stderr, "toktape record: %v\n", err)
		return exitUsage
	}
	if err := checkRawPrompts(sampling.endpoint, rounds); err != nil {
		fmt.Fprintf(stderr, "toktape record: %v\n", err)
		return exitUsage
	}

	opts := recorder.Options{
		BaseURL:      *url,
		Prompts:      promptRequests(prompts),
		Rounds:       rounds,
		SpecNMax:     sweep,
		Concurrency:  *concurrency,
		MaxTokens:    *nPredict,
		Params:       sampling.params,
		Endpoint:     sampling.endpoint,
		Version:      version,
		WaitForModel: waitBudget(fs, *wait),
		// Waiting for a server that is not listening yet is only done when
		// the user asked for it by naming a --wait: it is the "I started
		// both at once" case, and without the flag a mistyped --url must
		// still fail in a second rather than in ten minutes.
		WaitForStart: flagSet(fs, "wait") && *wait > 0,
	}
	opts = pinCollectors(opts)
	parsedGrid, err := tui.ParseGrid(*grid)
	if err != nil {
		fmt.Fprintf(stderr, "toktape record: --%v\n", err)
		return exitUsage
	}
	recordGrid = parsedGrid
	recordLabels = runLabels{tag: *tag, note: *note}
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
	// The labels go into the tape, not only into the ledger, so
	// `toktape log --rebuild` regenerates the table with them intact.
	tp.Summary.Tag, tp.Summary.Note = recordLabels.tag, recordLabels.note
	a.tape = filepath.Join(outDir, tp.Summary.ID+tape.Ext)
	if err := tape.Write(a.tape, tp); err != nil {
		a.tape = ""
		return a, fmt.Errorf("saving the run file: %w", err)
	}
	// The experiment ledger is a cache of the tapes, and the tape is already
	// safe by now, so a ledger that could not be appended to is a warning the
	// callers print, never a failed run.
	ledgerErr := ledger.Append(outDir, tp)
	if ledgerErr != nil {
		ledgerErr = fmt.Errorf("updating the experiment log: %w", ledgerErr)
	}
	if !wantCard {
		return a, ledgerErr
	}

	textPath := filepath.Join(outDir, tp.Summary.ID+".card.txt")
	if err := os.WriteFile(textPath, []byte(card.Text(&tp.Summary)), 0o644); err != nil {
		return a, errors.Join(ledgerErr, fmt.Errorf("saving the card: %w", err))
	}
	a.cardText = textPath

	pngPath := filepath.Join(outDir, tp.Summary.ID+".card.png")
	if err := png.Write(pngPath, &tp.Summary); err != nil {
		return a, errors.Join(ledgerErr, fmt.Errorf("saving the image card: %w", err))
	}
	a.cardPNG = pngPath
	return a, ledgerErr
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
	// The tape is what separates a posted card from a screenshot: a reader who
	// has the file can replay the run instead of taking the numbers on trust.
	// The hint names the basename, which is what an upload is called.
	fmt.Fprintf(&b, "→ Attach the .tape when you post — reviewers can replay it with: toktape play %s\n",
		filepath.Base(a.tape))
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

// sampling is what the sampling flags decided: the parameters every request
// carries and the path it is sent to.
type sampling struct {
	params   map[string]any
	endpoint string
}

// reservedParams are the keys --param may not set, because each one is the
// request itself rather than a parameter of it: overriding one would send
// something other than the prompt the tape says it sent.
var reservedParams = []string{"messages", "prompt", "stream"}

// samplingOptions turns --endpoint, --temp, --no-think and --param into the
// request parameters and the path.
//
// tempSet, not the value, decides whether a temperature is sent: `--temp 0` is
// greedy and must reach the server, while an unnamed flag must leave the
// server's own default alone. A number we did not send must never appear on
// the card (the repo's unknown rule), and the only way to keep that true is to
// send nothing when nothing was asked for.
func samplingOptions(endpoint string, tempSet bool, temp float64, noThink bool, params []string) (sampling, error) {
	out := sampling{endpoint: endpoint}
	switch endpoint {
	case tape.EndpointChat, tape.EndpointCompletion:
	default:
		return out, fmt.Errorf("--endpoint %s: use %s or %s", endpoint, tape.EndpointChat, tape.EndpointCompletion)
	}
	if noThink && endpoint == tape.EndpointCompletion {
		return out, fmt.Errorf("--no-think has nothing to turn off on --endpoint %s: thinking is the template's, and a raw prompt has none", tape.EndpointCompletion)
	}

	merged := map[string]any{}
	for _, kv := range params {
		k, v, err := parseParam(kv)
		if err != nil {
			return out, err
		}
		merged[k] = v
	}
	if tempSet {
		merged["temperature"] = temp
	}
	if noThink {
		// The engine's own switch (llama.cpp tools/server/README.md:
		// chat_template_kwargs "allows sending additional parameters to the
		// json templating system. For example: {"enable_thinking": false}").
		// A kwargs object the user supplied is kept and this key added to it,
		// so the two flags cannot silently cancel each other.
		kw, _ := merged["chat_template_kwargs"].(map[string]any)
		if kw == nil {
			kw = map[string]any{}
		}
		kw["enable_thinking"] = false
		merged["chat_template_kwargs"] = kw
	}
	if len(merged) > 0 {
		out.params = merged
	}
	return out, nil
}

// checkRawPrompts refuses a multi-turn prompt on the raw path.
//
// A prompts file may carry a whole conversation ("messages": [...]), and
// /completion takes one string. Flattening it to the last turn would record a
// tape whose prompt is not the prompt the user wrote, which is the one thing
// the prompt record exists to prevent — so the run stops here instead, while
// the user can still choose between the two endpoints. Every other prompt
// source builds a single turn and passes.
func checkRawPrompts(endpoint string, rounds []recorder.Round) error {
	if endpoint != tape.EndpointCompletion {
		return nil
	}
	for k, rd := range rounds {
		for _, p := range rd.Prompts {
			if len(p.Messages) > 1 {
				// Rounds are numbered, not line-numbered: the parser skips
				// blank lines, so the k-th round is rarely the k-th line of
				// the file. The number here is the one the card prints for an
				// unnamed round, which is what the user will look for.
				name := rd.Name
				if name == "" {
					name = fmt.Sprintf("round %d", k+1)
				}
				return fmt.Errorf("--endpoint %s sends one prompt verbatim, but %s of the prompts file is a %d-message conversation; use --endpoint %s for it",
					tape.EndpointCompletion, name, len(p.Messages), tape.EndpointChat)
			}
		}
	}
	return nil
}

// parseParam splits one --param into its key and its value.
//
// The value is decoded as JSON when it is valid JSON, so a number stays a
// number, a bool a bool and an object an object — llama-server rejects
// "0.7" where it wants 0.7. Anything that is not valid JSON is the string it
// was typed as, which is what makes --param reasoning_effort=high work
// without quoting.
func parseParam(kv string) (string, any, error) {
	key, raw, found := strings.Cut(kv, "=")
	key = strings.TrimSpace(key)
	if !found || key == "" {
		return "", nil, fmt.Errorf("--param %q: expected key=value", kv)
	}
	for _, reserved := range reservedParams {
		if key == reserved {
			return "", nil, fmt.Errorf("--param %s: %s is the request, not a parameter of it; use --prompt or --endpoint", key, key)
		}
	}
	var v any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return key, raw, nil
	}
	return key, v, nil
}

// headerLine is the one line printed the moment the run is attached:
// what server, what build, what model, which process.
func headerLine(s *tape.RunSummary) string {
	// An engine that did not identify itself is "?", the card's word for an
	// unknown, never "unknown" (TTP-37).
	kind := string(s.Server.Kind)
	if kind == "" || s.Server.Kind == tape.ServerUnknown {
		kind = "?"
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
