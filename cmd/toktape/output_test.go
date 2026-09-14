package main

import (
	"encoding/csv"
	"encoding/json"
	"os"
	osexec "os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/midagedev/toktape/internal/card"
	"github.com/midagedev/toktape/internal/ledger"
	"github.com/midagedev/toktape/internal/tape"
)

// -o FORMAT, in llama-bench's words (TTP-81, 2026-09-14).
//
// These tests pin the matrix of which verb takes which format, every cell of
// it, by running the verb — not by reading the table the code dispatches on,
// which would let a wrong table agree with itself.

// wantFormats is the decided matrix. "" is the default rendering.
var wantFormats = map[string]map[string]bool{
	"record": {"": true, "json": true, "jsonl": true, "md": true, "csv": true, "tsv": true, "sql": true, "png": false},
	"card":   {"": true, "json": true, "jsonl": true, "md": true, "csv": true, "tsv": true, "sql": true, "png": true},
	"log":    {"": true, "json": true, "jsonl": true, "md": true, "csv": true, "tsv": true, "sql": true, "png": false},
}

// everyFormat is llama-bench's -o vocabulary plus the two toktape adds (tsv,
// the ledger's own format, and png), and one word that is none of them.
var everyFormat = []string{"", "json", "jsonl", "md", "csv", "tsv", "sql", "png", "xml"}

// TestOutputMatrix runs every verb with every format and checks the product
// has the shape the format promises, or that the refusal names what the verb
// does take.
func TestOutputMatrix(t *testing.T) {
	hermetic(t)
	srv := cliServer(t)
	logDir := seedRuns(t)
	const logRuns = 4

	for _, verb := range []string{"record", "card", "log"} {
		for _, f := range everyFormat {
			name := verb + " -o " + f
			if f == "" {
				name = verb + " (no -o)"
			}
			t.Run(name, func(t *testing.T) {
				var args []string
				rows := 1
				switch verb {
				case "record":
					args = []string{"record", "--url", srv.URL, "--out", t.TempDir(), "--n-predict", "16", "--quiet"}
				case "card":
					args = []string{"card", writeTape(t, t.TempDir(), card.Example())}
				case "log":
					args = []string{"log", "--out", logDir}
					rows = logRuns
				}
				if f != "" {
					args = append(args, "-o", f)
				}
				code, stdout, stderr := exec(t, args...)

				if !wantFormats[verb][f] {
					if code != exitUsage {
						t.Fatalf("exit %d, want %d for a format %s does not take\nstdout:\n%s\nstderr:\n%s", code, exitUsage, verb, stdout, stderr)
					}
					if stdout != "" {
						t.Errorf("a refused format printed to stdout:\n%s", stdout)
					}
					// The refusal names every format the verb does take, so
					// the next attempt is not a guess.
					for g, ok := range wantFormats[verb] {
						if ok && g != "" && !strings.Contains(stderr, g) {
							t.Errorf("the refusal does not name %q, which %s takes:\n%s", g, verb, stderr)
						}
					}
					if !strings.Contains(stderr, "-o "+f) {
						t.Errorf("the refusal does not name what was asked for (-o %s):\n%s", f, stderr)
					}
					return
				}
				if code != exitOK {
					t.Fatalf("exit %d\nstderr:\n%s", code, stderr)
				}
				checkFormat(t, verb, f, stdout, rows)
			})
		}
	}
}

// checkFormat asserts the shape one format promises.
func checkFormat(t *testing.T, verb, f, out string, rows int) {
	t.Helper()
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	switch f {
	case "":
		if verb == "log" {
			if !strings.HasPrefix(out, "DATE") {
				t.Errorf("log's default is not the terminal table:\n%s", out)
			}
			return
		}
		if !strings.Contains(out, "toktape") || strings.Contains(out, "```") {
			t.Errorf("%s's default is not the text card:\n%s", verb, out)
		}
	case "json":
		if verb == "log" {
			var objs []map[string]string
			if err := json.Unmarshal([]byte(out), &objs); err != nil || len(objs) != rows {
				t.Errorf("log -o json is not an array of %d objects (%v):\n%s", rows, err, out)
			}
			return
		}
		var doc map[string]any
		if err := json.Unmarshal([]byte(out), &doc); err != nil {
			t.Fatalf("-o json is not one object: %v\n%s", err, out)
		}
		if _, ok := doc["caveats"]; !ok {
			t.Error("-o json is not the run summary: it has no caveats")
		}
	case "jsonl":
		if len(lines) != rows {
			t.Fatalf("-o jsonl printed %d lines, want one per run (%d):\n%s", len(lines), rows, out)
		}
		for i, l := range lines {
			var doc map[string]any
			if err := json.Unmarshal([]byte(l), &doc); err != nil {
				t.Errorf("line %d is not one JSON object: %v\n%s", i+1, err, l)
			}
			key := "caveats"
			if verb == "log" {
				key = "decode_tok_s"
			}
			if _, ok := doc[key]; !ok {
				t.Errorf("line %d has no %q: %s", i+1, key, l)
			}
		}
	case "md":
		if verb == "log" {
			if !strings.HasPrefix(out, "| id |") || len(lines) != rows+2 {
				t.Errorf("log -o md is not a Markdown table of %d rows:\n%s", rows, out)
			}
			return
		}
		// toktape's md, not llama-bench's: the card in a fence, then the
		// llama-bench table, then Reproduce.
		for _, want := range []string{"```text\n", "| model", "Reproduce"} {
			if !strings.Contains(out, want) {
				t.Errorf("-o md is missing %q:\n%s", want, out)
			}
		}
	case "csv":
		recs, err := csv.NewReader(strings.NewReader(out)).ReadAll()
		if err != nil {
			t.Fatalf("-o csv is not CSV: %v\n%s", err, out)
		}
		if len(recs) != rows+1 || strings.Join(recs[0], ",") != strings.Join(ledger.Columns, ",") {
			t.Errorf("-o csv is not the ledger's columns and %d rows:\n%s", rows, out)
		}
	case "tsv":
		if lines[0] != ledger.Header() || len(lines) != rows+1 {
			t.Errorf("-o tsv is not the ledger's header and %d rows:\n%s", rows, out)
		}
	case "sql":
		if !strings.HasPrefix(out, "CREATE TABLE IF NOT EXISTS runs (") {
			t.Errorf("-o sql does not start by creating the table:\n%s", out)
		}
		if n := strings.Count(out, "INSERT INTO runs "); n != rows {
			t.Errorf("-o sql has %d INSERTs, want %d:\n%s", n, rows, out)
		}
	case "png":
		path := strings.TrimSpace(out)
		if _, err := os.Stat(path); err != nil {
			t.Errorf("-o png printed %q, which is not a file: %v", path, err)
		}
	default:
		t.Fatalf("no shape check for %q", f)
	}
}

// TestOutputJSONLAppends: the reason jsonl exists is that an agent appends it
// across invocations, so two runs' lines concatenated are two objects.
func TestOutputJSONLAppends(t *testing.T) {
	var all strings.Builder
	for _, s := range []*tape.RunSummary{card.Example(), card.ExampleConcurrent()} {
		code, out, stderr := exec(t, "card", writeTape(t, t.TempDir(), s), "-o", "jsonl")
		if code != exitOK {
			t.Fatalf("exit %d\n%s", code, stderr)
		}
		all.WriteString(out)
	}
	lines := strings.Split(strings.TrimRight(all.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("two invocations appended %d lines, want 2:\n%s", len(lines), all.String())
	}
	for i, l := range lines {
		var doc struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal([]byte(l), &doc); err != nil || doc.ID == "" {
			t.Errorf("line %d is not a run summary (%v): %s", i+1, err, l)
		}
	}
}

// TestOutputSpellings: both names and both value forms reach one flag, and the
// last one given wins, as it does for every other flag.
func TestOutputSpellings(t *testing.T) {
	path := writeTape(t, t.TempDir(), card.Example())
	for _, args := range [][]string{
		{"-o", "jsonl"}, {"--output", "jsonl"}, {"-o=jsonl"}, {"--output=jsonl"}, {"-output", "jsonl"},
		{"-o", "md", "-o", "jsonl"},
	} {
		code, out, stderr := exec(t, append([]string{"card", path}, args...)...)
		if code != exitOK {
			t.Errorf("%v: exit %d\n%s", args, code, stderr)
			continue
		}
		if strings.Count(strings.TrimRight(out, "\n"), "\n") != 0 || !json.Valid([]byte(out)) {
			t.Errorf("%v did not print one JSON line:\n%s", args, out)
		}
	}
}

// TestOutputFailureIsOneObject: the failure half of the JSON contract holds
// under -o json and -o jsonl alike, in every spelling, including the failures
// that happen before the flags parse and the ones that happen after.
func TestOutputFailureIsOneObject(t *testing.T) {
	hermetic(t)
	dir := seedRuns(t)
	path := writeTape(t, t.TempDir(), card.Example())
	for _, args := range [][]string{
		// Rejected by the flag package: the format is recovered from raw args.
		{"card", path, "--nope", "-o", "json"},
		{"card", path, "--nope", "-o", "jsonl"},
		{"card", path, "--nope", "--output=jsonl"},
		{"card", path, "--nope", "-o=json"},
		{"log", "--out", dir, "--nope", "--output", "json"},
		{"--nope", "-output", "jsonl"},
		// Rejected after parsing.
		{"card", "-o", "jsonl"},
		{"log", "--out", dir, "--sort", "vram", "-o", "jsonl"},
		{"log", "--out", dir, "--sort", "vram", "-o", "json"},
		{"record", "--sessions", "0", "-o", "jsonl"},
	} {
		code, stdout, stderr := exec(t, args...)
		if code != exitUsage {
			t.Errorf("%v: exit %d, want %d\n%s", args, code, exitUsage, stderr)
			continue
		}
		if n := strings.Count(strings.TrimRight(stdout, "\n"), "\n"); n != 0 {
			t.Errorf("%v: the failure is %d lines on stdout, want one:\n%s", args, n+1, stdout)
		}
		var got struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(stdout), &got); err != nil || got.Error.Code != "usage" {
			t.Errorf("%v: stdout is not one usage error object (%v):\n%q", args, err, stdout)
		}
	}
	// A format that is not JSON leaves stdout empty on failure: a CSV reader
	// must not receive an error object it cannot parse.
	for _, args := range [][]string{
		{"card", path, "--nope", "-o", "csv"},
		{"card", path, "--nope", "-o", "json", "-o", "sql"},
	} {
		if _, stdout, _ := exec(t, args...); stdout != "" {
			t.Errorf("%v: a non-JSON failure printed to stdout:\n%s", args, stdout)
		}
	}
}

// TestDeletedFormatFlagsNameTheirReplacement: the per-format booleans and
// log's -n are gone, not aliased — and typing one is answered with the
// spelling that replaced it.
func TestDeletedFormatFlagsNameTheirReplacement(t *testing.T) {
	dir := seedRuns(t)
	path := writeTape(t, t.TempDir(), card.Example())
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"card", path, "--md"}, "-o md"},
		{[]string{"card", path, "--json"}, "-o json"},
		{[]string{"card", path, "--png"}, "-o png"},
		{[]string{"log", "--out", dir, "--tsv"}, "-o tsv"},
		{[]string{"log", "--out", dir, "--csv"}, "-o csv"},
		{[]string{"log", "--out", dir, "-n", "2"}, "--limit"},
		{[]string{"--json"}, "-o json"},
		// -ojson is not a flag the flag package accepts: the value needs a
		// space or an "=".
		{[]string{"card", path, "-ojson"}, "-o json"},
		// A path where a format goes is the runs directory's flag.
		{[]string{"log", "-o", dir}, "--out"},
	} {
		code, _, stderr := exec(t, tc.args...)
		if code != exitUsage {
			t.Errorf("%v: exit %d, want %d: the deleted spelling must not parse", tc.args, code, exitUsage)
		}
		if !strings.Contains(stderr, "→ ") || !strings.Contains(stderr[strings.Index(stderr, "→ "):], tc.want) {
			t.Errorf("%v: the hint does not name %q:\n%s", tc.args, tc.want, stderr)
		}
	}
	// The retired --json still asks for a JSON answer, because the caller who
	// types it is the one who parses stdout — and gets told the new spelling
	// in the hint of the object it parses.
	_, stdout, _ := exec(t, "card", path, "--json")
	if hint := errorHint(t, stdout); !strings.Contains(hint, "-o json") {
		t.Errorf("the JSON hint for --json is %q", hint)
	}
}

// TestLogLimit: at most N rows is --limit, which is not -n.
func TestLogLimit(t *testing.T) {
	dir := seedRuns(t)
	code, stdout, stderr := exec(t, "log", "--out", dir, "--limit", "2")
	if code != exitOK {
		t.Fatalf("exit %d\n%s", code, stderr)
	}
	if rows := rowsOf(stdout); len(rows) != 2 {
		t.Errorf("--limit 2 printed %d rows", len(rows))
	}
	_, stdout, _ = exec(t, "log", "--out", dir, "--limit", "1", "-o", "sql")
	if n := strings.Count(stdout, "INSERT INTO runs "); n != 1 {
		t.Errorf("--limit 1 -o sql wrote %d rows", n)
	}
}

// deletedFlag matches a format boolean this CLI no longer declares.
var deletedFlag = regexp.MustCompile(`--(json|jsonl|md|csv|tsv|png|sql)\b`)

// logDashN matches `toktape log ... -n`, the row limit that is --limit now.
var logDashN = regexp.MustCompile(`toktape log\b[^\n]*\s-n\b`)

// TestNoSurfaceSpellsADeletedFormatFlag: every place a reader copies a command
// from spells the format -o FORMAT. The Korean and Japanese READMEs are
// checked through their code blocks only; their prose is the lead's.
func TestNoSurfaceSpellsADeletedFormatFlag(t *testing.T) {
	surfaces := map[string]string{
		"--help":          usageText,
		"help agents":     agentsTopic(),
		"help render":     renderUsage,
		"docs/agents.md":  repoFile(t, "docs/agents.md"),
		"README.md":       repoFile(t, "README.md"),
		"CONTRIBUTING.md": repoFile(t, "CONTRIBUTING.md"),
	}
	for _, name := range readmes {
		var blocks []string
		for _, m := range fence.FindAllStringSubmatch(repoFile(t, name), -1) {
			blocks = append(blocks, m[1])
		}
		surfaces[name+" code blocks"] = strings.Join(blocks, "\n")
	}
	for name, text := range surfaces {
		// sqlite3's own `.import --tsv` is sqlite's flag, not toktape's.
		text = strings.ReplaceAll(text, ".import --tsv", ".import")
		if m := deletedFlag.FindString(text); m != "" {
			t.Errorf("%s still spells %s", name, m)
		}
		if m := logDashN.FindString(text); m != "" {
			t.Errorf("%s still limits log with -n: %q", name, m)
		}
	}
	for name, want := range map[string][]string{
		"--help":         {"-o, --output FORMAT", "llama-bench"},
		"help agents":    {"-o json", "-o jsonl"},
		"docs/agents.md": {"-o json"},
		"README.md":      {"-o sql", "--limit"},
	} {
		for _, w := range want {
			if !strings.Contains(surfaces[name], w) {
				t.Errorf("%s does not mention %q", name, w)
			}
		}
	}
}

// TestSQLPipesIntoSQLite is the claim the README makes: `toktape log -o sql`
// piped into sqlite3 is a table whose numeric columns are numbers.
func TestSQLPipesIntoSQLite(t *testing.T) {
	sqlite := lookSQLite(t)
	dir := seedRuns(t)
	code, sql, stderr := exec(t, "log", "--out", dir, "-o", "sql")
	if code != exitOK {
		t.Fatalf("exit %d\n%s", code, stderr)
	}
	// Twice: CREATE TABLE IF NOT EXISTS lets a second export append.
	out := runSQLite(t, sqlite, filepath.Join(t.TempDir(), "runs.db"), sql+sql+
		"select typeof(decode_tok_s), count(*), max(decode_tok_s) from runs group by 1;\n")
	// The fastest single-stream run of the sweep is 68.4; the concurrent run's
	// decode is its per-stream 9.1, so a max over text would be "9.1".
	if want := "real|8|68.4"; strings.TrimSpace(out) != want {
		t.Errorf("sqlite3 read the export as %q, want %q", strings.TrimSpace(out), want)
	}

	// The sweep's own question, read the way a reviewer reads it: `go test
	// -run TestSQLPipesIntoSQLite -v ./cmd/toktape/`.
	q := "select tag, ngl, decode_tok_s from runs order by decode_tok_s desc;\n"
	t.Logf("$ toktape log -o sql | sqlite3 :memory: %q\n%s", q, runSQLite(t, sqlite, ":memory:", sql+q))
}

// lookSQLite finds the sqlite3 binary or skips: the module is stdlib-only, so
// the real importer is the only independent reader of the SQL there is.
func lookSQLite(t *testing.T) string {
	t.Helper()
	p, err := osexec.LookPath("sqlite3")
	if err != nil {
		t.Skip("sqlite3 is not on PATH")
	}
	return p
}

// runSQLite feeds script to sqlite3 on db and returns stdout. -bail makes a
// statement that does not parse a failure rather than a skipped line.
func runSQLite(t *testing.T, sqlite, db, script string) string {
	t.Helper()
	cmd := osexec.Command(sqlite, "-bail", db)
	cmd.Stdin = strings.NewReader(script)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("sqlite3: %v\n%s", err, out)
	}
	return string(out)
}
