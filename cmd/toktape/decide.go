package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/midagedev/toktape/internal/recorder"
	"github.com/midagedev/toktape/internal/server"
	"github.com/midagedev/toktape/internal/tape"
)

// `toktape decide` (TTP-192, 2026-10-02): record a decision model.
//
// A decision model (Cloudflare Clef behind POST /v1/systemone) reads a state
// and typed questions and answers each with one probability per option; it
// generates nothing, so none of record's token machinery applies and the verb
// has its own recorder (recorder.RecordDecision). This file is the verb: the
// flags, the tape's path, and a plain summary. A later commit swaps the
// summary for the decision card.

const decideUsage = `toktape decide — record a decision model, one command

Usage:
  toktape decide [flags]

  Sends a suite of requests to POST /v1/systemone: first one pass paced a
  second apart (what a replay shows, one answer at a time), then the same
  suite repeated back to back (what the throughput is measured over). The
  first request is the cold one and is reported apart. Every latency is the
  client's clock, send to the last byte of the answer.

Flags:
  --url URL             server to attach to (default: discover)
  --suite FILE          your own suite: JSONL, one request body per line, with
                        an optional "id" per line that is never sent
                        (default: the built-in eight-case suite)
  --repeat N            back-to-back passes over the suite (default 20,
                        0 = the paced pass only)
  -c N                  requests in flight at once during the back-to-back
                        passes (default 1)
  --gap DURATION        idle time after each paced answer (default 1.2s,
                        0 = none); never part of a latency
  --reference FILE      JSONL of the official model's answers to the same cases
                        ({"id": ..., "answers": ...}); prints how far this
                        engine's probabilities are from them
  --out DIR             where the tape is written (default ~/.toktape/runs)
  --tag TEXT, --note TEXT  label the run
  --host-label TEXT     store TEXT as the hostname
`

// decideRun is the seam the tests use to replace the recorder.
var decideRun = recorder.RecordDecision

func runDecide(ctx context.Context, c *cli, args []string) int {
	fs := newFlagSet("decide")
	url := fs.String("url", "", "server base URL (default: discover)")
	suiteFile := fs.String("suite", "", "suite file")
	repeat := fs.Int("repeat", recorder.DefaultDecisionRepeats, "back-to-back passes")
	conc := fs.Int("c", 1, "requests in flight at once in the back-to-back passes")
	gap := fs.Duration("gap", recorder.DefaultDecisionGap, "idle time after each paced answer")
	refFile := fs.String("reference", "", "reference answers file")
	outDir := fs.String("out", defaultRunsDir(), "where the tape is written")
	tag := fs.String("tag", "", "label the run")
	note := fs.String("note", "", "label the run")
	hostLabel := fs.String("host-label", "", "store this instead of the machine's hostname")
	extra, err := parseArgs(fs, args)
	if err != nil {
		return c.badFlags("decide", decideUsage, fs, args, err)
	}
	if len(extra) > 0 {
		return c.usagef("toktape decide: unexpected argument %q", extra[0])
	}
	switch {
	case *repeat < 0:
		return c.usagef("toktape decide: --repeat %d: use 0 for the paced pass only, or a number of passes", *repeat)
	case *conc < 1:
		return c.usagef("toktape decide: -c %d: at least one request in flight", *conc)
	case *gap < 0:
		return c.usagef("toktape decide: --gap %s: use 0 for no idle time", *gap)
	}

	opts := recorder.DecisionOptions{
		BaseURL:     *url,
		Repeats:     *repeat,
		Concurrency: *conc,
		Gap:         *gap,
		Tag:         *tag,
		Note:        *note,
		Version:     version,
		HostLabel:   recorder.HostLabel{Set: flagSet(fs, "host-label"), Text: *hostLabel},
	}
	// 0 means "none" for the user and "the default" for the zero value.
	if *repeat == 0 {
		opts.Repeats = recorder.NoBurst
	}
	if *gap == 0 {
		opts.Gap = recorder.NoGap
	}
	if *suiteFile != "" {
		raw, err := os.ReadFile(*suiteFile)
		if err != nil {
			return c.usagef("toktape decide: --suite: %v", err)
		}
		opts.Suite, opts.SuiteName = raw, *suiteFile
	}
	if *refFile != "" {
		raw, err := os.ReadFile(*refFile)
		if err != nil {
			return c.usagef("toktape decide: --reference: %v", err)
		}
		opts.Reference, opts.ReferenceName = raw, *refFile
	}
	// The same seam record's tests use to keep the host line off the real
	// machine.
	pinned := pinCollectors(recorder.Options{})
	opts.FSRoot, opts.GPU = pinned.FSRoot, pinned.GPU

	announcedBurst := false
	opts.Progress = func(r tape.DecisionRecord) {
		switch {
		case r.Phase == tape.DecisionPhaseShowcase:
			fmt.Fprintf(c.stderr, "  %-14s %s\n", r.CaseID, decideLatency(r))
		case !announcedBurst:
			announcedBurst = true
			fmt.Fprintln(c.stderr, "  back to back ...")
		}
	}

	tp, err := decideRun(ctx, opts)
	if err != nil {
		return decideFailure(c, err, *url != "")
	}
	path := filepath.Join(*outDir, tp.Summary.ID+tape.Ext)
	// Only the tape is written: the run ledger and the card are token-run
	// surfaces and refuse a decision tape (internal/tape/decision.go).
	if err := tape.Write(path, tp); err != nil {
		return c.usagef("toktape decide: saving the run file: %v", err)
	}
	fmt.Fprint(c.stdout, decideSummary(&tp.Summary, path))
	return exitOK
}

func decideLatency(r tape.DecisionRecord) string {
	if r.Error != "" {
		return "failed: " + clipLine(r.Error, 70)
	}
	return fmt.Sprintf("%s ms", fmtMs(float64(r.Latency())/float64(time.Millisecond)))
}

func clipLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}

// decideFailure is the one place a decide error becomes an exit code: the
// recorder's two fatal conditions get decision-server wording (record's hints
// name llama-server), and anything else is the invocation's (a bad suite).
func decideFailure(c *cli, err error, urlGiven bool) int {
	switch {
	case errors.Is(err, context.Canceled):
		return c.failf(exitUsage, "toktape decide: interrupted before a request was answered")
	case errors.Is(err, recorder.ErrUnreachable):
		hint := fmt.Sprintf("no decision server on the ports toktape probes (%s); name yours with --url http://host:port", server.DefaultPorts())
		if urlGiven {
			hint = "check the host and port of --url, and that the server is up"
		}
		return c.fail(failure{code: exitUnreachable, msg: fmt.Sprintf("toktape: %v", err), hint: hint})
	case errors.Is(err, recorder.ErrAllStreamsFailed):
		return c.fail(failure{
			code: exitStreams,
			msg:  fmt.Sprintf("toktape: %v", err),
			hint: "the server was reachable but answered no request; it has to serve POST /v1/systemone",
		})
	default:
		return c.usagef("toktape decide: %v", err)
	}
}

// decideSummary is the plain closing summary: every figure from the tape's
// DecisionSummary, "?" for a figure that is not there. Short and long prompts
// are shown apart because one long case dominates a whole-suite p95.
func decideSummary(s *tape.RunSummary, path string) string {
	d := s.Decision
	var b strings.Builder
	engine := string(s.Server.Kind)
	if engine == "" {
		engine = "?"
	}
	model := s.Model.FileName
	if model == "" {
		model = d.Model
	}
	if model == "" {
		model = "?"
	}
	fmt.Fprintf(&b, "\n%s on %s (%s)\n", model, engine, s.Server.URL)
	fmt.Fprintf(&b, "  %d requests: %d cases x %d passes, %d in flight in the back-to-back passes\n", d.Requests, d.Cases, d.Repeats, d.Concurrency)
	fmt.Fprintf(&b, "  cold        %s ms   (the first request, apart from every figure below)\n", fmtMs(d.ColdMs))
	fmt.Fprintf(&b, "  warm p50    %s ms   p95 %s ms   mean %s ms\n", fmtMs(d.WarmP50Ms), fmtMs(d.WarmP95Ms), fmtMs(d.WarmMeanMs))
	fmt.Fprintf(&b, "  short       %s ms   p50, under %d input tokens\n", fmtMs(d.ShortWarmP50Ms), tape.DecisionLongPromptTokens)
	fmt.Fprintf(&b, "  long        %s ms   p50, %d input tokens or more\n", fmtMs(d.LongWarmP50Ms), tape.DecisionLongPromptTokens)
	if d.RequestsPerSecond > 0 {
		fmt.Fprintf(&b, "  throughput  %.1f requests/s over the back-to-back passes\n", d.RequestsPerSecond)
	} else {
		fmt.Fprintf(&b, "  throughput  ?   (no back-to-back passes)\n")
	}
	if d.PrefillPerSecond > 0 {
		fmt.Fprintf(&b, "  prefill     %.0f tokens/s (median)\n", d.PrefillPerSecond)
	}
	source := "client clock, send to last byte (end to end)"
	if d.TimingSource == tape.DecisionTimingServer {
		source = "latency: client clock, end to end; prefill: the engine's own timing"
	}
	fmt.Fprintf(&b, "  timing      %s\n", source)
	if d.CacheHits > 0 {
		fmt.Fprintf(&b, "  cache       %d requests reused the engine's cache: their latency is not a full prefill\n", d.CacheHits)
	}
	if d.Errors > 0 {
		fmt.Fprintf(&b, "  errors      %d of %d requests failed and are in no figure\n", d.Errors, d.Requests)
	}
	if r := d.Reference; r != nil {
		fmt.Fprintf(&b, "  reference   %d questions vs %s: max |dp| %.4f, %d top flips, Brier delta %.5f\n",
			r.Questions, r.File, r.MaxAbsDeltaP, r.TopFlips, r.BrierDelta)
	}
	for _, w := range s.Warnings {
		fmt.Fprintf(&b, "  note        %s\n", w)
	}
	fmt.Fprintf(&b, "\n  tape        %s\n", tildePath(path))
	return b.String()
}

// fmtMs prints a millisecond figure with the precision its size can carry;
// 0 is not a figure, it is unknown, and prints "?".
func fmtMs(ms float64) string {
	switch {
	case ms <= 0:
		return "?"
	case ms < 100:
		return fmt.Sprintf("%.1f", ms)
	default:
		return fmt.Sprintf("%.0f", ms)
	}
}
