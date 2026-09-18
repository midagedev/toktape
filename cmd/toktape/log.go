package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/midagedev/toktape/internal/ledger"
)

// runLog prints the experiment ledger: one row per recorded run, so a sweep
// over -ngl or a cache type is read as one table instead of as a folder of
// cards.
//
// The ledger file is a cache of the tapes. When it is missing or was written
// by a build with different columns it is regenerated here rather than
// patched, so the answer to "which setting won" is always derived from the
// runs themselves.
//
// The row cap is --limit, not -n (TTP-81, 2026-09-14): -n is record's tokens
// per stream, as in llama-bench, and one letter meaning two things inside one
// tool is the confusion 7960905 removed from record.
func runLog(c *cli, args []string) int {
	fs := newFlagSet("log")
	f := declareLogFlags(fs)
	extra, err := parseArgs(fs, args)
	if err != nil {
		return c.badFlags("log", usageFor("log"), args, err)
	}
	format, refused := outputFor("log", *f.output)
	c.json = format.isJSON()
	if refused != nil {
		return c.fail(*refused)
	}
	if len(extra) > 0 {
		return c.usagef("toktape log: unexpected argument %q", extra[0])
	}
	if !validSort(*f.sortBy) {
		return c.usagef("toktape log: --sort %q is not one of date, decode, prefill, ttft", *f.sortBy)
	}
	outDir, model, tag, limit := f.outDir, f.model, f.tag, f.limit

	rows, err := loadLedger(c.stderr, *outDir, *f.rebuild)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			fmt.Fprint(c.stderr, noRunsMessage(*outDir))
			return exitOK
		}
		return c.usagef("toktape: %v", err)
	}

	recorded := len(rows)
	rows = filterRows(rows, *model, *tag)
	sortRows(rows, *f.sortBy)
	if *limit > 0 && len(rows) > *limit {
		rows = rows[:*limit]
	}

	if format != outputDefault {
		// An export is a file someone pipes somewhere, so an empty ledger
		// still writes its header: an importable table with no rows is a
		// usable answer, a friendly sentence in the middle of a TSV is not.
		if err := writeRows(c.stdout, format, rows); err != nil {
			return c.usagef("toktape: %v", err)
		}
		return exitOK
	}
	if len(rows) == 0 {
		fmt.Fprint(c.stderr, emptyLine(*outDir, recorded, *model, *tag))
		return exitOK
	}
	fmt.Fprint(c.stdout, logTable(rows))
	return exitOK
}

// logFlags is every flag the log verb declares, for the reason recordFlags
// is a struct: the examples in --help are parsed against the real set.
type logFlags struct {
	outDir, model, tag, sortBy, output *string
	limit                              *int
	rebuild                            *bool
}

// declareLogFlags registers the log verb's flags on fs.
func declareLogFlags(fs *flag.FlagSet) *logFlags {
	return &logFlags{
		outDir:  fs.String("out", defaultRunsDir(), "directory holding the runs"),
		model:   fs.String("model", "", "only runs whose model contains this text"),
		tag:     fs.String("tag", "", "only runs whose tag contains this text"),
		sortBy:  fs.String("sort", sortDate, "date | decode | prefill | ttft"),
		limit:   fs.Int("limit", 0, "show at most N runs (0 = all)"),
		output:  declareOutputFlag(fs),
		rebuild: fs.Bool("rebuild", false, "regenerate the ledger from the tapes first"),
	}
}

// emptyLine says why the table is empty. A directory with no runs in it and a
// filter that matched none of the runs in it are two different problems, and
// telling someone who mistyped `--tag ngl=4O` that they have never recorded a
// run names the wrong one.
func emptyLine(dir string, recorded int, model, tag string) string {
	if recorded == 0 {
		return noRunsMessage(dir)
	}
	var by []string
	if model != "" {
		by = append(by, fmt.Sprintf("--model %q", model))
	}
	if tag != "" {
		by = append(by, fmt.Sprintf("--tag %q", tag))
	}
	return fmt.Sprintf("None of the %d runs in %s match %s.\n",
		recorded, tildePath(dir), strings.Join(by, " and "))
}

// The sort keys. "date" is the default because the last thing recorded is
// what a person is usually looking at; "decode" is the sweep's question.
const (
	sortDate    = "date"
	sortDecode  = "decode"
	sortPrefill = "prefill"
	sortTTFT    = "ttft"
)

func validSort(s string) bool {
	switch s {
	case sortDate, sortDecode, sortPrefill, sortTTFT:
		return true
	}
	return false
}

// loadLedger reads the ledger, rebuilding it from the tapes when it is
// missing or stale. It rebuilds at most once, so a directory that cannot
// produce a readable ledger reports that instead of looping.
func loadLedger(stderr io.Writer, dir string, force bool) ([]ledger.Row, error) {
	if force {
		if err := rebuildLedger(stderr, dir); err != nil {
			return nil, err
		}
		return ledger.Read(dir)
	}
	rows, err := ledger.Read(dir)
	if err == nil {
		return rows, nil
	}
	if !errors.Is(err, os.ErrNotExist) && !ledger.IsStale(err) {
		return nil, err
	}
	if rerr := rebuildLedger(stderr, dir); rerr != nil {
		return nil, rerr
	}
	return ledger.Read(dir)
}

// rebuildLedger regenerates the ledger and says so on stderr, so the product
// on stdout stays a table.
func rebuildLedger(stderr io.Writer, dir string) error {
	res, err := ledger.Rebuild(dir)
	if err != nil {
		return err
	}
	fmt.Fprintf(stderr, "rebuilt %s from %d tapes\n", ledger.FileName, res.Rows)
	if len(res.Unreadable) > 0 {
		// Named rather than counted away: a run file that stopped loading is
		// the user's problem to look at, not the ledger's to hide.
		fmt.Fprintf(stderr, "toktape: %d unreadable: %s\n",
			len(res.Unreadable), strings.Join(res.Unreadable, ", "))
	}
	return nil
}

// filterRows keeps the rows whose model and tag contain the given text,
// matched without case so `--tag NGL=40` finds `ngl=40`.
func filterRows(rows []ledger.Row, model, tag string) []ledger.Row {
	if model == "" && tag == "" {
		return rows
	}
	out := rows[:0:0]
	for _, r := range rows {
		if model != "" && !containsFold(r.Get("model"), model) {
			continue
		}
		if tag != "" && !containsFold(r.Get("tag"), tag) {
			continue
		}
		out = append(out, r)
	}
	return out
}

func containsFold(haystack, needle string) bool {
	return strings.Contains(strings.ToLower(haystack), strings.ToLower(needle))
}

// sortRows orders the table by one key. Every ordering puts the runs that did
// not observe the key last, whichever direction it sorts: a missing
// measurement is not a slow one, and it must not win the "which setting won"
// question by being empty.
func sortRows(rows []ledger.Row, key string) {
	switch key {
	case sortDecode:
		sortByNumber(rows, "decode_tok_s", false)
	case sortPrefill:
		sortByNumber(rows, "prefill_tok_s", false)
	case sortTTFT:
		sortByNumber(rows, "ttft_ms", true)
	default:
		sortByDate(rows)
	}
}

func sortByNumber(rows []ledger.Row, col string, ascending bool) {
	sort.SliceStable(rows, func(i, j int) bool {
		a, aOK := parseFloat(rows[i].Get(col))
		b, bOK := parseFloat(rows[j].Get(col))
		if aOK != bOK {
			return aOK
		}
		if !aOK {
			return rows[i].Get("id") > rows[j].Get("id")
		}
		if a == b {
			return rows[i].Get("id") > rows[j].Get("id")
		}
		if ascending {
			return a < b
		}
		return a > b
	})
}

// sortByDate orders newest first. started_at carries each run's own UTC
// offset, so the strings are parsed rather than compared: two machines in
// two timezones sort by the instant, not by the text.
func sortByDate(rows []ledger.Row) {
	sort.SliceStable(rows, func(i, j int) bool {
		a, aOK := parseTime(rows[i].Get("started_at"))
		b, bOK := parseTime(rows[j].Get("started_at"))
		if aOK != bOK {
			return aOK
		}
		if !aOK || a.Equal(b) {
			return rows[i].Get("id") > rows[j].Get("id")
		}
		return a.After(b)
	})
}

func parseFloat(s string) (float64, bool) {
	if s == "" {
		return 0, false
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

func parseTime(s string) (time.Time, bool) {
	if s == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

// logColumns are the ledger columns the human table shows, in order: the ones
// a sweep is actually read on. Everything else is one `-o tsv` away.
var logColumns = []struct {
	head  string
	col   string
	right bool
}{
	{"DATE", "started_at", false},
	{"TAG", "tag", false},
	{"MODEL", "model", false},
	{"QUANT", "quant", false},
	{"N", "concurrency", true},
	{"PREFILL", "prefill_tok_s", true},
	{"DECODE", "decode_tok_s", true},
	{"AGG", "aggregate_tok_s", true},
	{"TTFT", "ttft_ms", true},
	{"CACHE", "cache_label", false},
	{"NGL", "ngl", true},
	{"FA", "flash_attn", false},
}

// logTable renders the terminal view. Unknown prints "?" here and only here:
// this is the surface a person reads, and a blank cell on a terminal reads as
// a rendering bug rather than as a measurement nobody took.
func logTable(rows []ledger.Row) string {
	header := make([]string, len(logColumns))
	right := make(map[int]bool, len(logColumns))
	for i, c := range logColumns {
		header[i] = c.head
		right[i] = c.right
	}
	out := make([][]string, 0, len(rows))
	for _, r := range rows {
		cells := make([]string, len(logColumns))
		for i, c := range logColumns {
			v := r.Get(c.col)
			if c.col == "started_at" {
				v = shortDate(v)
			}
			// A missing tag or note is "the user gave none", not an
			// unobserved measurement, so it prints as a dash, not "?".
			if c.col == "tag" || c.col == "note" {
				if v == "" {
					v = "-"
				}
				cells[i] = v
				continue
			}
			cells[i] = orUnknown(v)
		}
		out = append(out, cells)
	}
	return table(header, out, right)
}

// shortDate trims a stored RFC3339 timestamp to the minute, in the reader's
// own timezone. A ledger copied from another machine keeps its offset, so the
// conversion is what makes two rigs' rows comparable at a glance.
func shortDate(v string) string {
	t, ok := parseTime(v)
	if !ok {
		return ""
	}
	return t.Local().Format("2006-01-02 15:04")
}
