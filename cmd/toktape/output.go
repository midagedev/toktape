package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/midagedev/toktape/internal/card"
	"github.com/midagedev/toktape/internal/ledger"
	"github.com/midagedev/toktape/internal/tape"
)

// -o FORMAT, in llama-bench's words (TTP-81, 2026-09-14).
//
// llama-bench spells the output as one flag with a closed vocabulary:
// `-o, --output <csv|json|jsonl|md|sql>`. toktape used to spell it as one
// boolean per format, differently per verb (card --md --json --png, log --tsv
// --csv --json --md, record --json), and two of those groups had to refuse
// "alternatives, not a pair" — the shape of a flag that wanted to be an enum.
// The booleans are deleted, not aliased: two spellings for one choice is what
// this removed, and a single flag cannot be given two values at once.
//
// One type, one table of which verb takes which format, one resolver. A verb
// never decides for itself whether it takes a format; it asks outputFor.

// outputFormat is one value of -o. The empty value is the verb's own
// rendering: the text card for record and card, the terminal table for log.
type outputFormat string

const (
	outputDefault outputFormat = ""
	outputJSON    outputFormat = "json"
	outputJSONL   outputFormat = "jsonl"
	outputMD      outputFormat = "md"
	outputCSV     outputFormat = "csv"
	outputTSV     outputFormat = "tsv"
	outputSQL     outputFormat = "sql"
	outputPNG     outputFormat = "png"
)

// outputFormats is every format there is, in the order a refusal lists them:
// llama-bench's five first, then the two toktape adds — tsv, the ledger's own
// file format, and png, the share image.
var outputFormats = []outputFormat{outputJSON, outputJSONL, outputMD, outputCSV, outputTSV, outputSQL, outputPNG}

// verbOutputs is the matrix, the one place it is written down.
//
// A single run is one ledger row, so record and card take csv, tsv and sql as
// that row with exactly the ledger's columns (ledger.FromTape) — a second
// schema for one run would be a table that does not import beside the log.
// md differs between the tools: llama-bench's md is its benchmark table, and
// toktape's card md is the card in a fence, a llama-bench-shaped table and the
// Reproduce block. log's md is the ledger as a Markdown table.
var verbOutputs = map[string][]outputFormat{
	"record": {outputJSON, outputJSONL, outputMD, outputCSV, outputTSV, outputSQL},
	"card":   {outputJSON, outputJSONL, outputMD, outputCSV, outputTSV, outputSQL, outputPNG},
	"log":    {outputJSON, outputJSONL, outputMD, outputCSV, outputTSV, outputSQL},
	// publish's product is a receipt, not a run: the link, the id and the
	// delete token. The ledger formats would have to invent a row for
	// something that is not a measurement, so it takes json alone.
	"publish": {outputJSON, outputJSONL},
	// runs reads the service's listing: the table by default, the
	// service's own body under -o json, one run object per line under
	// -o jsonl. show reads one run: the summary, or the service's body
	// under -o json.
	"runs": {outputJSON, outputJSONL},
	"show": {outputJSON},
}

// outputRefusals is why a format another verb takes is not one this verb
// takes, where there is more to say than the list.
var outputRefusals = map[string]map[outputFormat]string{
	"record": {outputPNG: "every run already writes the share image next to its tape, and toktape card <tape> -o png FILE writes one elsewhere"},
	"log":    {outputPNG: "a share image is one run's, and toktape card <tape> -o png draws it"},
}

// isJSON reports whether a failure under this format owes one JSON object on
// stdout (cli.fail). jsonl counts: an error object is one line of it.
func (f outputFormat) isJSON() bool { return f == outputJSON || f == outputJSONL }

func knownOutput(f outputFormat) bool {
	for _, g := range outputFormats {
		if g == f {
			return true
		}
	}
	return false
}

// declareOutputFlag registers -o and --output on one string, the way -n and
// --n-predict share one int.
func declareOutputFlag(fs *flag.FlagSet) *string {
	p := new(string)
	fs.StringVar(p, "o", "", "output format: json, jsonl, md, csv, tsv, sql or png, where the verb takes it")
	fs.StringVar(p, "output", "", "output format (long form of -o)")
	return p
}

// outputFor resolves one verb's -o. A format the verb does not take is refused
// by name with the list it does take, never quietly replaced by one it does.
func outputFor(verb, value string) (outputFormat, *failure) {
	f := outputFormat(value)
	takes := verbOutputs[verb]
	if f == outputDefault {
		return f, nil
	}
	for _, g := range takes {
		if g == f {
			return f, nil
		}
	}
	fl := &failure{
		code: exitUsage,
		msg:  fmt.Sprintf("toktape %s: -o %s is not an output format", verb, value),
		hint: fmt.Sprintf("%s takes -o %s", verb, formatList(takes)),
	}
	switch ext := outputFormat(strings.TrimPrefix(filepath.Ext(value), ".")); {
	case knownOutput(f):
		fl.msg = fmt.Sprintf("toktape %s: -o %s is not an output of %s", verb, f, verb)
		if why := outputRefusals[verb][f]; why != "" {
			fl.hint += "; " + why
		}
	case strings.ContainsAny(value, `/\`) || strings.HasPrefix(value, "~"):
		// llama-bench's -o is a format, and so is this one; the directory a
		// reader probably meant has a flag of its own.
		fl.hint = fmt.Sprintf("-o names a format, not a place: the runs directory is --out DIR, and %s", fl.hint)
	case ext != outputDefault && knownOutput(ext):
		fl.hint = fmt.Sprintf("-o names a format, not a file: -o %s > %s; %s", ext, value, fl.hint)
	}
	return f, fl
}

// formatHintFor answers an undefined flag that is spelled like a format: one
// of the per-format booleans -o replaced (--json, --md, --png ...), or -ojson,
// which Go's flag package reads as a flag named "ojson" rather than as -o with
// a value attached. cli.badFlags reads it through retiredFlagHint.
func formatHintFor(verb, name string) string {
	if _, ok := verbOutputs[verb]; !ok {
		return ""
	}
	f := outputFormat(name)
	lead := "the output format is"
	if !knownOutput(f) {
		rest, ok := strings.CutPrefix(name, "o")
		if !ok || !knownOutput(outputFormat(rest)) {
			return ""
		}
		f = outputFormat(rest)
		lead = "a flag's value follows a space or \"=\":"
	}
	if _, refused := outputFor(verb, string(f)); refused != nil {
		return refused.hint
	}
	return fmt.Sprintf("%s -o %s", lead, f)
}

// formatList is "json, jsonl, md or sql".
func formatList(fs []outputFormat) string {
	names := make([]string, len(fs))
	for i, f := range fs {
		names[i] = string(f)
	}
	if len(names) < 2 {
		return strings.Join(names, "")
	}
	return strings.Join(names[:len(names)-1], ", ") + " or " + names[len(names)-1]
}

// renderRun is one run in one text format, as record prints it and card
// re-prints it. png is not here: it is a file rather than text, and only card
// writes one on request.
func renderRun(tp *tape.Tape, f outputFormat) (string, error) {
	s := &tp.Summary
	switch f {
	case outputDefault:
		return card.Text(s), nil
	case outputMD:
		return card.Markdown(s), nil
	case outputJSON, outputJSONL:
		b, err := card.JSON(s)
		if err != nil {
			return "", err
		}
		if f == outputJSON {
			return string(b), nil
		}
		// card.JSON indents, and internal/card is not this file's to change;
		// compacting its bytes keeps the object identical and puts it on the
		// one line jsonl allows.
		var line bytes.Buffer
		if err := json.Compact(&line, bytes.TrimSpace(b)); err != nil {
			return "", fmt.Errorf("compacting the run summary: %w", err)
		}
		return line.String() + "\n", nil
	case outputCSV, outputTSV, outputSQL:
		var b strings.Builder
		if err := writeRows(&b, f, []ledger.Row{ledger.FromTape(tp)}); err != nil {
			return "", err
		}
		return b.String(), nil
	}
	return "", fmt.Errorf("no rendering of a run as %q", f)
}

// writeRows writes ledger rows in one export format.
func writeRows(w io.Writer, f outputFormat, rows []ledger.Row) error {
	switch f {
	case outputTSV:
		return ledger.WriteTSV(w, rows)
	case outputCSV:
		return ledger.WriteCSV(w, rows)
	case outputJSON:
		return ledger.WriteJSON(w, rows)
	case outputJSONL:
		return ledger.WriteJSONL(w, rows)
	case outputMD:
		return ledger.WriteMarkdown(w, rows)
	case outputSQL:
		return ledger.WriteSQL(w, rows)
	}
	return fmt.Errorf("no ledger export as %q", f)
}
