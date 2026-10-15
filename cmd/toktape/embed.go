package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/midagedev/toktape/internal/card"
	"github.com/midagedev/toktape/internal/recorder"
	"github.com/midagedev/toktape/internal/tape"
)

// `toktape embed` (2026-10-15): record an embedding model. Each input of a
// set is sent on its own to /v1/embeddings (or llama-server's /embedding)
// and the vectors go on the tape; `toktape card` shows them and `toktape
// compare` puts two embedding tapes side by side.

const embedUsage = `toktape embed — record an embedding model, one command

Usage:
  toktape embed [flags]

  Sends each input of a set, one at a time, to an embeddings endpoint and
  keeps every returned vector on the tape. The summary shows per input the
  dimension, norm and first values, and the cosine similarity between every
  pair. ` + "`toktape play a.tape`" + ` replays it on the screen (vector strips and
  a coloured cosine matrix), ` + "`--against b.tape`" + ` adds each input's cosine to
  the same input in b. ` + "`toktape compare a.tape b.tape`" + ` prints the same
  comparison as text: cosine and largest difference per input.

Flags:
  --url URL             server to attach to (default: discover)
  --api NAME            openai (POST /v1/embeddings, default) or llama
                        (POST /embedding, llama-server's own shape)
  --inputs FILE         JSONL, one {"id": ..., "input": ...} per line
                        (default: a built-in English/Korean/code set of
                        query/document pairs)
  --model NAME          "model" to send (openai shape; default: none)
  --dimensions N        "dimensions" to send (openai shape; default: none)
  --encoding-format F   float (default) or base64 (openai shape)
  --vector-cap N        keep at most N values of each vector (default 0 =
                        all); dim, norm and the first values are always kept
  --gap DURATION        idle time after each answer, so a replay shows the
                        inputs arrive one by one (default 600ms, 0 = none);
                        never part of a latency
  --out DIR             where the tape is written (default ~/.toktape/runs)
  --tag TEXT, --note TEXT  label the run
  --host-label TEXT     store TEXT as the hostname
`

// embedRun is the seam the tests use to replace the recorder.
var embedRun = recorder.RecordEmbedding

func runEmbed(ctx context.Context, c *cli, args []string) int {
	fs := newFlagSet("embed")
	url := fs.String("url", "", "server base URL (default: discover)")
	api := fs.String("api", tape.EmbeddingAPIOpenAI, "openai or llama")
	inputsFile := fs.String("inputs", "", "input set file")
	model := fs.String("model", "", "model to send")
	dims := fs.Int("dimensions", 0, "dimensions to send")
	format := fs.String("encoding-format", "float", "float or base64")
	vcap := fs.Int("vector-cap", 0, "values kept per vector")
	gap := fs.Duration("gap", 600*time.Millisecond, "idle time after each answer")
	outDir := fs.String("out", defaultRunsDir(), "where the tape is written")
	tag := fs.String("tag", "", "label the run")
	note := fs.String("note", "", "label the run")
	hostLabel := fs.String("host-label", "", "store this instead of the machine's hostname")
	extra, err := parseArgs(fs, args)
	if err != nil {
		return c.badFlags("embed", embedUsage, fs, args, err)
	}
	if len(extra) > 0 {
		return c.usagef("toktape embed: unexpected argument %q", extra[0])
	}
	switch {
	case *api != tape.EmbeddingAPIOpenAI && *api != tape.EmbeddingAPILlama:
		return c.usagef("toktape embed: --api %q: use openai or llama", *api)
	case *format != "float" && *format != "base64":
		return c.usagef("toktape embed: --encoding-format %q: use float or base64", *format)
	case *dims < 0:
		return c.usagef("toktape embed: --dimensions %d: must be positive", *dims)
	case *gap < 0:
		return c.usagef("toktape embed: --gap %s: use 0 for no idle time", *gap)
	case *vcap < 0:
		return c.usagef("toktape embed: --vector-cap %d: use 0 to keep whole vectors", *vcap)
	}
	opts := recorder.EmbeddingOptions{
		BaseURL: *url, API: *api, Model: *model, Dimensions: *dims, EncodingFormat: *format,
		VectorCap: *vcap, Gap: *gap, Tag: *tag, Note: *note, Version: version,
		HostLabel: recorder.HostLabel{Set: flagSet(fs, "host-label"), Text: *hostLabel},
	}
	if *inputsFile != "" {
		raw, err := os.ReadFile(*inputsFile)
		if err != nil {
			return c.usagef("toktape embed: --inputs: %v", err)
		}
		opts.Inputs, opts.InputsName = raw, *inputsFile
	}
	pinned := pinCollectors(recorder.Options{})
	opts.FSRoot, opts.GPU = pinned.FSRoot, pinned.GPU
	opts.Progress = func(r tape.EmbeddingRecord) {
		if r.Error != "" {
			fmt.Fprintf(c.stderr, "  %-14s failed: %s\n", r.ID, clipLine(r.Error, 70))
			return
		}
		fmt.Fprintf(c.stderr, "  %-14s %d values  %s ms\n", r.ID, r.Dim, fmtMs(float64(r.Latency().Microseconds())/1000))
	}

	tp, err := embedRun(ctx, opts)
	if err != nil {
		switch {
		case errors.Is(err, recorder.ErrUnreachable):
			return c.fail(failure{code: exitUnreachable, msg: fmt.Sprintf("toktape: %v", err),
				hint: "name the server with --url http://host:port; llama-server needs --embeddings"})
		case errors.Is(err, recorder.ErrAllStreamsFailed):
			return c.fail(failure{code: exitStreams, msg: fmt.Sprintf("toktape: %v", err),
				hint: "the server was reachable but embedded nothing; llama-server needs --embeddings"})
		default:
			return c.usagef("toktape embed: %v", err)
		}
	}
	path := filepath.Join(*outDir, tp.Summary.ID+tape.Ext)
	// Only the tape is written: the ledger and the token card refuse an
	// embedding tape.
	if err := tape.Write(path, tp); err != nil {
		return c.usagef("toktape embed: saving the run file: %v", err)
	}
	fmt.Fprintf(c.stdout, "\n%s\n  tape  %s\n", card.EmbeddingText(tp), tildePath(path))
	return exitOK
}
