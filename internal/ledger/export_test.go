package ledger

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/midagedev/toktape/internal/card"
	"github.com/midagedev/toktape/internal/tape"
)

// TestEveryColumnHasASQLType: the CREATE TABLE lists every column, in order,
// with a type chosen on purpose. A column appended to Columns without one
// would otherwise become TEXT by default, and the sweep's ORDER BY on it would
// sort lexically — the failure the export rule exists to prevent.
func TestEveryColumnHasASQLType(t *testing.T) {
	for _, col := range Columns {
		switch sqlTypes[col] {
		case "TEXT", "INTEGER", "REAL", "NUMERIC":
		default:
			t.Errorf("column %q has SQL type %q, want one of TEXT, INTEGER, REAL, NUMERIC", col, sqlTypes[col])
		}
	}
	if len(sqlTypes) != len(Columns) {
		t.Errorf("sqlTypes has %d entries, Columns has %d: a type for a column that does not exist", len(sqlTypes), len(Columns))
	}
}

// TestSQLTypesMatchTheCells: every value FromTape writes into an INTEGER or
// REAL column parses as one. The type table is a claim about FromTape's
// formatting, and this is where the claim is checked.
func TestSQLTypesMatchTheCells(t *testing.T) {
	for _, s := range []*tape.RunSummary{card.Example(), card.ExampleConcurrent()} {
		r := FromTape(tapeOf(s))
		for i, col := range Columns {
			v := r.Values[i]
			if v == "" {
				continue
			}
			switch sqlTypes[col] {
			case "INTEGER":
				if _, err := strconv.ParseInt(v, 10, 64); err != nil {
					t.Errorf("%s: %s is INTEGER but holds %q", s.ID, col, v)
				}
			case "REAL":
				if _, err := strconv.ParseFloat(v, 64); err != nil {
					t.Errorf("%s: %s is REAL but holds %q", s.ID, col, v)
				}
			}
		}
	}
}

// TestWriteSQLShape: one CREATE TABLE naming every column in order, then one
// INSERT per run; an empty ledger is the CREATE TABLE alone, the SQL
// equivalent of the header an empty TSV keeps.
func TestWriteSQLShape(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteSQL(&buf, nil); err != nil {
		t.Fatal(err)
	}
	empty := buf.String()
	if !strings.HasPrefix(empty, "CREATE TABLE IF NOT EXISTS runs (") || strings.Contains(empty, "INSERT") {
		t.Errorf("an empty ledger is not the CREATE TABLE alone:\n%s", empty)
	}
	last := -1
	for _, col := range Columns {
		i := strings.Index(empty, `"`+col+`" `)
		if i < 0 {
			t.Errorf("the CREATE TABLE does not declare %s", col)
			continue
		}
		if i < last {
			t.Errorf("the CREATE TABLE declares %s out of order", col)
		}
		last = i
	}

	buf.Reset()
	rows := []Row{FromTape(tapeOf(card.Example())), FromTape(tapeOf(card.ExampleConcurrent()))}
	if err := WriteSQL(&buf, rows); err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(buf.String(), "\nINSERT INTO runs "); n != 2 {
		t.Errorf("%d INSERT statements for two runs:\n%s", n, buf.String())
	}
}

// TestWriteSQLUnknownIsNull: an unobserved value is NULL, never 0 and never
// an empty string, and an observed zero stays a zero.
func TestWriteSQLUnknownIsNull(t *testing.T) {
	r := FromTape(tapeOf(&tape.RunSummary{ID: "20260101-000000-x", Concurrency: 1}))
	var buf bytes.Buffer
	if err := WriteSQL(&buf, []Row{r}); err != nil {
		t.Fatal(err)
	}
	_, insert, _ := strings.Cut(buf.String(), "\nINSERT INTO runs ")
	_, values, ok := strings.Cut(insert, " VALUES (")
	if !ok {
		t.Fatalf("no VALUES list:\n%s", buf.String())
	}
	values = strings.TrimSuffix(strings.TrimSpace(values), ");")
	cells := strings.Split(values, ", ")
	if len(cells) != len(Columns) {
		t.Fatalf("%d values for %d columns: %s", len(cells), len(Columns), values)
	}
	for i, col := range Columns {
		want := "NULL"
		switch col {
		case "id":
			want = "'20260101-000000-x'"
		case "tape":
			want = "'20260101-000000-x.tape'"
		case "concurrency":
			want = "1"
		case "warnings":
			want = "0" // observed: a run with nothing to warn about
		}
		if cells[i] != want {
			t.Errorf("%s = %s, want %s", col, cells[i], want)
		}
	}
}

// TestWriteSQLQuotesText: a note is the user's free text, and a quote in it
// must not end the string literal early — nor may anything after it run.
func TestWriteSQLQuotesText(t *testing.T) {
	s := card.Example()
	s.Note = `it's "fa on"; DROP TABLE runs; --`
	var buf bytes.Buffer
	if err := WriteSQL(&buf, []Row{FromTape(tapeOf(s))}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), `'it''s "fa on"; DROP TABLE runs; --'`) {
		t.Errorf("the note is not one doubled-quote literal:\n%s", buf.String())
	}

	sqlite, err := exec.LookPath("sqlite3")
	if err != nil {
		t.Skip("sqlite3 is not on PATH; the literal was checked, the round trip was not")
	}
	cmd := exec.Command(sqlite, "-bail", filepath.Join(t.TempDir(), "runs.db"))
	cmd.Stdin = strings.NewReader(buf.String() +
		"select note, typeof(ngl), typeof(decode_tok_s), typeof(cache_n), typeof(tag) from runs;\n")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("sqlite3 rejected the export: %v\n%s", err, out)
	}
	want := `it's "fa on"; DROP TABLE runs; --|integer|real|integer|null`
	if got := strings.TrimSpace(string(out)); got != want {
		t.Errorf("sqlite3 read back %q, want %q", got, want)
	}
}

// TestWriteJSONLIsWriteJSONOneLineEach: the two JSON exports carry the same
// objects; jsonl is one per line and nothing else, so it appends.
func TestWriteJSONLIsWriteJSONOneLineEach(t *testing.T) {
	s := card.Example()
	s.Note = "a\nnote"
	rows := []Row{FromTape(tapeOf(s)), FromTape(tapeOf(card.ExampleConcurrent()))}
	var arr, lines bytes.Buffer
	if err := WriteJSON(&arr, rows); err != nil {
		t.Fatal(err)
	}
	if err := WriteJSONL(&lines, rows); err != nil {
		t.Fatal(err)
	}
	var fromArray []map[string]string
	if err := json.Unmarshal(arr.Bytes(), &fromArray); err != nil {
		t.Fatal(err)
	}
	got := strings.Split(strings.TrimRight(lines.String(), "\n"), "\n")
	if len(got) != len(rows) {
		t.Fatalf("jsonl has %d lines for %d rows:\n%s", len(got), len(rows), lines.String())
	}
	for i, l := range got {
		var obj map[string]string
		if err := json.Unmarshal([]byte(l), &obj); err != nil {
			t.Fatalf("line %d: %v", i+1, err)
		}
		a, _ := json.Marshal(obj)
		b, _ := json.Marshal(fromArray[i])
		if string(a) != string(b) {
			t.Errorf("line %d differs from the array's object:\n%s\n%s", i+1, a, b)
		}
	}

	lines.Reset()
	if err := WriteJSONL(&lines, nil); err != nil || lines.Len() != 0 {
		t.Errorf("an empty ledger as jsonl is %q (err %v), want nothing", lines.String(), err)
	}
}
