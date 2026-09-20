package main

import (
	"encoding/csv"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/midagedev/toktape/internal/card"
	"github.com/midagedev/toktape/internal/ledger"
	"github.com/midagedev/toktape/internal/tape"
)

// sweepRun is one run of a -ngl sweep: the shared fixture with a label, a
// flag and a decode rate of its own. The ledger exists to answer "which of
// these won", so the fixtures differ in exactly the columns that decide it.
func sweepRun(id, tag, ngl string, decode float64) *tape.RunSummary {
	s := card.Example()
	s.ID = id
	// The run started when its ID says it did, so the date column and the
	// default ordering are not all the same instant.
	if t, err := time.Parse("20060102-150405", strings.Join(strings.Split(id, "-")[:2], "-")); err == nil {
		s.StartedAt = t.UTC()
		s.FinishedAt = s.StartedAt.Add(2 * time.Second)
	}
	s.Tag = tag
	s.Server.Flags.NGL = ngl
	s.Timings.PredictedPerSecond = decode
	s.Aggregate.AggregatePredictedPerSecond = decode
	s.Aggregate.PerStreamPredictedPerSecond = decode
	return s
}

// seedRuns writes a three-run sweep plus the concurrent fixture and returns
// the directory. The ledger is not written here: every test that uses it goes
// through the verb, which builds it from the tapes.
func seedRuns(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeTape(t, dir, sweepRun("20260913-100000-qwen3.5-35b-a3b", "ngl=40", "40", 41.2))
	writeTape(t, dir, sweepRun("20260913-110000-qwen3.5-35b-a3b", "ngl=99", "99", 68.4))
	writeTape(t, dir, sweepRun("20260913-120000-qwen3.5-35b-a3b", "ngl=60", "60", 55.9))
	writeTape(t, dir, card.ExampleConcurrent())
	return dir
}

// rowsOf returns the table's data lines, header dropped.
func rowsOf(out string) []string {
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) <= 1 {
		return nil
	}
	return lines[1:]
}

// column returns the fields of one table line, which are separated by runs of
// spaces.
func column(line string, i int) string {
	f := strings.Fields(line)
	if i >= len(f) {
		return ""
	}
	return f[i]
}

// TestLogVerbBuildsTheLedger: with no runs.tsv the verb derives it from the
// tapes, prints the table newest first, and says on stderr that it rebuilt.
func TestLogVerbBuildsTheLedger(t *testing.T) {
	dir := seedRuns(t)

	code, stdout, stderr := exec(t, "log", "--out", dir)
	if code != exitOK {
		t.Fatalf("exit %d\n%s", code, stderr)
	}
	if !strings.Contains(stderr, "rebuilt runs.tsv from 4 tapes") {
		t.Errorf("stderr does not report the rebuild:\n%s", stderr)
	}
	if _, err := os.Stat(filepath.Join(dir, ledger.FileName)); err != nil {
		t.Fatalf("the ledger was not written: %v", err)
	}

	header := strings.Fields(strings.SplitN(stdout, "\n", 2)[0])
	want := []string{"DATE", "TAG", "MODEL", "QUANT", "N", "PREFILL", "DECODE", "AGG", "TTFT", "CACHE", "NGL", "FA"}
	if strings.Join(header, " ") != strings.Join(want, " ") {
		t.Errorf("header = %v, want %v", header, want)
	}

	rows := rowsOf(stdout)
	if len(rows) != 4 {
		t.Fatalf("the table has %d rows, want 4:\n%s", len(rows), stdout)
	}
	// Newest first. 2026-09-21 (user): a date is tape.Stamp — ISO 8601 with
	// the offset the run was recorded at, never converted to the reader's
	// zone — so it is one field now ("2026-09-14T00:02+09:00", was the two
	// fields "2026-09-14 00:02"), and TAG below moved from column 2 to 1.
	newest := tape.Stamp(card.ExampleConcurrent().StartedAt)
	if !strings.HasPrefix(rows[0], newest) {
		t.Errorf("the first row is %q, want the newest run at %s", rows[0], newest)
	}
	// The concurrent run has no tag, and on a terminal an unobserved value is
	// "-" (lead, 2026-09-13): an absent tag is "the user gave none", not an
	// unobserved measurement, so it is a dash rather than the "?" the
	// numeric columns use for unknowns.
	if got := column(rows[0], 1); got != "-" {
		t.Errorf("the untagged run's TAG column = %q, want -", got)
	}
	if !strings.Contains(rows[0], "72.9") {
		t.Errorf("the concurrent run does not show its aggregate rate: %q", rows[0])
	}
	if !strings.Contains(stdout, "ngl=99") {
		t.Errorf("the tags are missing from the table:\n%s", stdout)
	}
}

// TestLogVerbSortDecode answers the sweep's question: which setting won.
func TestLogVerbSortDecode(t *testing.T) {
	dir := seedRuns(t)
	_, stdout, stderr := exec(t, "log", "--out", dir, "--tag", "ngl", "--sort", "decode")
	rows := rowsOf(stdout)
	if len(rows) != 3 {
		t.Fatalf("--tag ngl matched %d rows, want the three tagged runs\n%s\n%s", len(rows), stdout, stderr)
	}
	var tags []string
	for _, r := range rows {
		tags = append(tags, column(r, 1))
	}
	if want := []string{"ngl=99", "ngl=60", "ngl=40"}; strings.Join(tags, ",") != strings.Join(want, ",") {
		t.Errorf("--sort decode gave %v, want %v (fastest first)", tags, want)
	}
}

// TestLogVerbFilterAndLimit: --model, --tag and --limit narrow the table.
func TestLogVerbFilterAndLimit(t *testing.T) {
	dir := seedRuns(t)

	_, stdout, _ := exec(t, "log", "--out", dir, "--limit", "2")
	if rows := rowsOf(stdout); len(rows) != 2 {
		t.Errorf("--limit 2 printed %d rows", len(rows))
	}

	// Matched without case, so a tag typed in either case finds the run.
	_, stdout, _ = exec(t, "log", "--out", dir, "--tag", "NGL=40")
	if rows := rowsOf(stdout); len(rows) != 1 || !strings.Contains(rows[0], "ngl=40") {
		t.Errorf("--tag NGL=40 printed %v", rowsOf(stdout))
	}

	_, stdout, _ = exec(t, "log", "--out", dir, "--model", "llama")
	if rows := rowsOf(stdout); len(rows) != 4 {
		t.Errorf("--model llama printed %d rows, want all four", len(rows))
	}

	// A filter that matches nothing is not an empty run directory, and saying
	// "no runs yet" to someone who mistyped a filter names the wrong problem.
	code, stdout, stderr := exec(t, "log", "--out", dir, "--model", "qwen")
	if code != exitOK {
		t.Errorf("a filter that matches nothing is not an error: exit %d", code)
	}
	if stdout != "" {
		t.Errorf("a filter that matches nothing printed a table:\n%s", stdout)
	}
	if !strings.Contains(stderr, `None of the 4 runs`) || !strings.Contains(stderr, `--model "qwen"`) {
		t.Errorf("stderr does not say the filter matched nothing:\n%s", stderr)
	}
	if strings.Contains(stderr, "No runs yet") {
		t.Errorf("a directory holding four runs was reported as empty:\n%s", stderr)
	}
}

// TestLogVerbExportsLeaveUnknownsEmpty is the export contract: the human
// table prints "?", every machine format writes an empty cell, so a numeric
// column stays numeric when one run did not observe something.
func TestLogVerbExportsLeaveUnknownsEmpty(t *testing.T) {
	dir := t.TempDir()
	// No tag, no note, and a prompt cache nobody observed: the example rig's
	// prompt does hit the cache (2026-09-13, TTP-28), so the unobserved case
	// this test is about is built here.
	s := card.ExampleConcurrent()
	s.Timings.CacheN = 0
	s.Timings.PromptN = 512
	s.Cache = tape.CacheSummary{PromptTotal: 512, Label: tape.CacheCold}
	writeTape(t, dir, s)

	code, stdout, stderr := exec(t, "log", "--out", dir, "-o", "tsv")
	if code != exitOK {
		t.Fatalf("exit %d\n%s", code, stderr)
	}
	lines := strings.Split(strings.TrimRight(stdout, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("-o tsv printed %d lines, want a header and one row:\n%s", len(lines), stdout)
	}
	if lines[0] != ledger.Header() {
		t.Errorf("the TSV header is not the ledger header:\n%s", lines[0])
	}
	fields := strings.Split(lines[1], "\t")
	if len(fields) != len(ledger.Columns) {
		t.Fatalf("the TSV row has %d fields, want %d", len(fields), len(ledger.Columns))
	}
	if strings.Contains(lines[1], "?") {
		t.Errorf("an export wrote a \"?\": %q", lines[1])
	}
	for _, col := range []string{"tag", "note", "cache_n"} {
		if v := fields[ledger.Index(col)]; v != "" {
			t.Errorf("%s = %q in the TSV, want an empty cell", col, v)
		}
	}
	if v := fields[ledger.Index("aggregate_tok_s")]; v != "72.9" {
		t.Errorf("aggregate_tok_s = %q, want 72.9", v)
	}

	// The same run on the terminal prints "?" for the tag it does not have.
	_, table, _ := exec(t, "log", "--out", dir)
	if got := column(rowsOf(table)[0], 1); got != "-" {
		t.Errorf("the human table's TAG column = %q, want -", got)
	}
}

// TestLogVerbCSVAndJSON: the two structured formats carry the same cells,
// with a note that contains a comma and a quote left intact.
func TestLogVerbCSVAndJSON(t *testing.T) {
	dir := t.TempDir()
	s := sweepRun("20260913-100000-r1-distill-llama-70b", "ngl=40", "40", 41.2)
	s.Note = `one, two "three"`
	// A tensor-override pattern with a pipe in it, which is what the markdown
	// escaping below is about. The example rig splits by layer and carries no
	// -ot rules of its own (2026-09-13, TTP-28).
	s.Server.Flags.OverrideTens = []string{`blk\.(3[6-9]|4[0-7])\.ffn_.*_exps=CPU`}
	writeTape(t, dir, s)

	_, out, stderr := exec(t, "log", "--out", dir, "-o", "csv")
	recs, err := csv.NewReader(strings.NewReader(out)).ReadAll()
	if err != nil {
		t.Fatalf("-o csv is not valid CSV: %v\n%s\n%s", err, out, stderr)
	}
	if len(recs) != 2 {
		t.Fatalf("-o csv wrote %d records, want a header and one row", len(recs))
	}
	if recs[1][ledger.Index("note")] != s.Note {
		t.Errorf("the note survived as %q, want %q", recs[1][ledger.Index("note")], s.Note)
	}

	_, out, _ = exec(t, "log", "--out", dir, "-o", "json")
	var objs []map[string]string
	if err := json.Unmarshal([]byte(out), &objs); err != nil {
		t.Fatalf("-o json is not valid JSON: %v\n%s", err, out)
	}
	if len(objs) != 1 {
		t.Fatalf("-o json wrote %d objects, want 1", len(objs))
	}
	if objs[0]["note"] != s.Note {
		t.Errorf("json note = %q, want %q", objs[0]["note"], s.Note)
	}
	// An unobserved value is an absent key, not an empty string and not "?".
	if _, ok := objs[0]["per_stream_tok_s"]; ok {
		t.Errorf("json carries per_stream_tok_s for a single-stream run: %v", objs[0])
	}

	_, out, _ = exec(t, "log", "--out", dir, "-o", "md")
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 3 || !strings.HasPrefix(lines[0], "| id |") || !strings.HasPrefix(lines[1], "| --- |") {
		t.Fatalf("-o md is not a GitHub table:\n%s", out)
	}
	// The -ot pattern in this fixture contains a pipe, which would end its
	// cell early and shift every column after it; the escaped copies do not
	// count as cell separators.
	cellSeps := func(line string) int { return strings.Count(strings.ReplaceAll(line, `\|`, ""), "|") }
	if n := cellSeps(lines[0]); cellSeps(lines[2]) != n {
		t.Errorf("the markdown row has a different number of cells than its header:\n%s", out)
	}
	if !strings.Contains(lines[2], `\|`) {
		t.Errorf("the pipe inside the -ot pattern was not escaped:\n%s", lines[2])
	}
}

// TestLogVerbEmptyExportKeepsItsHeader: an export is a file someone pipes
// somewhere, so an empty history is an importable table with no rows rather
// than a sentence in the middle of a TSV.
func TestLogVerbEmptyExportKeepsItsHeader(t *testing.T) {
	dir := t.TempDir()
	code, stdout, stderr := exec(t, "log", "--out", dir, "-o", "tsv")
	if code != exitOK {
		t.Fatalf("exit %d\n%s", code, stderr)
	}
	if stdout != ledger.Header()+"\n" {
		t.Errorf("an empty -o tsv printed %q, want just the header", stdout)
	}

	code, stdout, _ = exec(t, "log", "--out", dir, "-o", "json")
	if code != exitOK || strings.TrimSpace(stdout) != "[]" {
		t.Errorf("an empty -o json printed %q", stdout)
	}
}

// TestLogVerbNoRunsAtAll: a directory that does not exist is the first-run
// case, not an error, and it uses the line `ls` uses.
func TestLogVerbNoRunsAtAll(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nothing-here")
	code, stdout, stderr := exec(t, "log", "--out", missing)
	if code != exitOK {
		t.Errorf("exit %d, want 0 on an empty run directory\n%s", code, stderr)
	}
	if stdout != "" {
		t.Errorf("an empty run directory printed a table:\n%s", stdout)
	}
	if !strings.Contains(stderr, "No runs yet in") || !strings.Contains(stderr, "Run `toktape` to record one.") {
		t.Errorf("stderr is not the first-run line:\n%s", stderr)
	}
}

// TestLogVerbRebuildsStaleLedger: a ledger written with different columns is
// regenerated from the tapes rather than read. The tapes are the record.
func TestLogVerbRebuildsStaleLedger(t *testing.T) {
	dir := seedRuns(t)
	path := filepath.Join(dir, ledger.FileName)
	if err := os.WriteFile(path, []byte("id\tfoo\nstale-row\t1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr := exec(t, "log", "--out", dir)
	if code != exitOK {
		t.Fatalf("exit %d\n%s", code, stderr)
	}
	if strings.Contains(stdout, "stale-row") {
		t.Errorf("the stale ledger was read instead of rebuilt:\n%s", stdout)
	}
	if rows := rowsOf(stdout); len(rows) != 4 {
		t.Errorf("the rebuilt table has %d rows, want 4", len(rows))
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(b), ledger.Header()+"\n") {
		t.Errorf("the stale header survived on disk:\n%s", strings.SplitN(string(b), "\n", 2)[0])
	}
}

// TestLogVerbRebuildNamesUnreadableTapes: a run file that stopped loading is
// named on stderr, never dropped in silence.
func TestLogVerbRebuildNamesUnreadableTapes(t *testing.T) {
	dir := seedRuns(t)
	broken := filepath.Join(dir, "20260913-130000-broken"+tape.Ext)
	if err := os.WriteFile(broken, []byte("not a tape"), 0o644); err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr := exec(t, "log", "--out", dir, "--rebuild")
	if code != exitOK {
		t.Fatalf("exit %d\n%s", code, stderr)
	}
	if !strings.Contains(stderr, "rebuilt runs.tsv from 4 tapes") {
		t.Errorf("stderr does not report the rebuild:\n%s", stderr)
	}
	if !strings.Contains(stderr, filepath.Base(broken)) {
		t.Errorf("the unreadable tape is not named on stderr:\n%s", stderr)
	}
	if rows := rowsOf(stdout); len(rows) != 4 {
		t.Errorf("the table has %d rows, want the four readable runs", len(rows))
	}
}

// TestLogVerbUsageErrors: a format log does not take, and the sort key that
// does not exist, fail with the usage code instead of guessing.
//
// stdout stays empty — a failed table is not a table — except under -o json,
// where it carries the error object instead (TTP-71, 2026-09-14). The
// assertion was "stdout is empty" until then; a caller that asked to be
// answered in JSON and got an empty stdout has two parse paths and no way to
// tell which one it is on, so the empty answer was the bug.
func TestLogVerbUsageErrors(t *testing.T) {
	dir := seedRuns(t)
	cases := []struct {
		args []string
		json bool
	}{
		{[]string{"log", "--out", dir, "--sort", "vram"}, false},
		{[]string{"log", "--out", dir, "-o", "png"}, false},
		{[]string{"log", "--out", dir, "--sort", "vram", "-o", "json"}, true},
		{[]string{"log", "--out", dir, "stray-argument"}, false},
	}
	for _, tc := range cases {
		code, stdout, stderr := exec(t, tc.args...)
		if code != exitUsage {
			t.Errorf("%v: exit %d, want %d\n%s", tc.args, code, exitUsage, stderr)
		}
		if !tc.json {
			if stdout != "" {
				t.Errorf("%v printed to stdout:\n%s", tc.args, stdout)
			}
			continue
		}
		var got struct {
			Error struct {
				Code string `json:"code"`
				Exit int    `json:"exit"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(stdout), &got); err != nil {
			t.Errorf("%v: -o json failure is not a JSON object on stdout: %v\nstdout: %q", tc.args, err, stdout)
			continue
		}
		if got.Error.Code != "usage" || got.Error.Exit != exitUsage {
			t.Errorf("%v: error %+v, want the usage code", tc.args, got.Error)
		}
	}
}

// TestRecordVerbTagNoteAndLedger: --tag and --note are recorded in the tape,
// so a rebuild of the ledger cannot lose them, and one run leaves a ledger
// row behind.
func TestRecordVerbTagNoteAndLedger(t *testing.T) {
	hermetic(t)
	srv := cliServer(t)
	dir := t.TempDir()

	code, _, stderr := exec(t, "--url", srv.URL, "--out", dir, "--no-card", "--quiet",
		"--tag", "ngl=40", "--note", "fa on, cold cache")
	if code != exitOK {
		t.Fatalf("exit %d\n%s", code, stderr)
	}

	tapes, err := filepath.Glob(filepath.Join(dir, "*"+tape.Ext))
	if err != nil || len(tapes) != 1 {
		t.Fatalf("run files = %v (err %v)", tapes, err)
	}
	tp, err := tape.Read(tapes[0])
	if err != nil {
		t.Fatal(err)
	}
	if tp.Summary.Tag != "ngl=40" || tp.Summary.Note != "fa on, cold cache" {
		t.Errorf("the tape recorded tag %q note %q", tp.Summary.Tag, tp.Summary.Note)
	}

	// The ledger holds them too, and still does after it is thrown away and
	// derived from the tape again.
	for _, args := range [][]string{{"log", "--out", dir}, {"log", "--out", dir, "--rebuild"}} {
		code, stdout, stderr := exec(t, args...)
		if code != exitOK {
			t.Fatalf("%v: exit %d\n%s", args, code, stderr)
		}
		if !strings.Contains(stdout, "ngl=40") {
			t.Errorf("%v: the tag is not in the table:\n%s", args, stdout)
		}
	}
	_, tsv, _ := exec(t, "log", "--out", dir, "-o", "tsv")
	if !strings.Contains(tsv, "fa on, cold cache") {
		t.Errorf("the note is not in the export:\n%s", tsv)
	}
}

// TestLogVerbOutputForTheRecord prints the two surfaces the track is reviewed
// on. It asserts only that they are non-empty; `go test -run
// TestLogVerbOutputForTheRecord -v ./cmd/toktape/` is how they are read.
func TestLogVerbOutputForTheRecord(t *testing.T) {
	dir := t.TempDir()
	writeTape(t, dir, card.Example())
	writeTape(t, dir, card.ExampleConcurrent())

	code, table, stderr := exec(t, "log", "--out", dir)
	if code != exitOK || table == "" {
		t.Fatalf("log: exit %d\n%s", code, stderr)
	}
	t.Logf("$ toktape log --out <dir>\n%s", table)

	code, md, stderr := exec(t, "log", "--out", dir, "-o", "md")
	if code != exitOK || md == "" {
		t.Fatalf("log -o md: exit %d\n%s", code, stderr)
	}
	t.Logf("$ toktape log --out <dir> -o md\n%s", md)
}
