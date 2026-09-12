package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/midagedev/toktape/internal/card"
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
		prompts     repeatedFlag
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

	pr := newProgress(stderr, *quiet)
	opts := recorder.Options{
		BaseURL:     *url,
		Prompts:     promptRequests(prompts),
		Concurrency: *concurrency,
		MaxTokens:   *nPredict,
		Version:     version,
		Progress:    pr.handle,
	}
	opts = pinCollectors(opts)

	pr.start()
	tp, err := recorder.Record(ctx, opts)
	pr.stop()
	if err != nil {
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

	tapePath := filepath.Join(*outDir, tp.Summary.ID+tape.Ext)
	if err := tape.Write(tapePath, tp); err != nil {
		fmt.Fprintf(stderr, "toktape: saving the run file: %v\n", err)
		return exitUsage
	}

	var cardPath string
	if !*noCard {
		cardPath = filepath.Join(*outDir, tp.Summary.ID+".card.txt")
		text := card.Text(&tp.Summary)
		if err := os.WriteFile(cardPath, []byte(text), 0o644); err != nil {
			fmt.Fprintf(stderr, "toktape: saving the card: %v\n", err)
			cardPath = ""
		}
	}

	switch {
	case *asJSON:
		b, err := card.JSON(&tp.Summary)
		if err != nil {
			fmt.Fprintf(stderr, "toktape: %v\n", err)
			return exitUsage
		}
		fmt.Fprintf(stdout, "%s\n", b)
	case !*noCard:
		fmt.Fprint(stdout, card.Text(&tp.Summary))
	}

	if !*quiet {
		fmt.Fprintf(stderr, "✓ Tape saved  %s\n", tildePath(tapePath))
		if cardPath != "" {
			fmt.Fprintf(stderr, "✓ Card saved  %s\n", tildePath(cardPath))
		}
		fmt.Fprintf(stderr, "Share: `toktape card %s --md` copies a Reddit-ready block · `toktape compare a.tape b.tape`\n",
			tildePath(tapePath))
	}
	return exitOK
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
