package ledger

import (
	"bufio"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// The export encoders all follow one rule: an unobserved value is an empty
// cell, never "?" and never a 0 nobody measured. The human table prints "?"
// because a person reads it; a "?" in a TSV column makes sqlite import the
// column as text the moment one run did not observe something, and then every
// `ORDER BY decode_tok_s` in the sweep sorts lexically.

// WriteTSV writes the header and every row, tab-separated. This is the
// ledger's own format; a SQLite table with typed columns is WriteSQL's.
func WriteTSV(w io.Writer, rows []Row) error {
	bw := bufio.NewWriter(w)
	fmt.Fprintln(bw, Header())
	for _, r := range rows {
		fmt.Fprintln(bw, Encode(r))
	}
	return bw.Flush()
}

// WriteCSV writes the same table through encoding/csv, which quotes the
// commas and quotation marks a --note may contain.
func WriteCSV(w io.Writer, rows []Row) error {
	cw := csv.NewWriter(w)
	if err := cw.Write(Columns); err != nil {
		return fmt.Errorf("writing the csv header: %w", err)
	}
	for _, r := range rows {
		if err := cw.Write(cells(r)); err != nil {
			return fmt.Errorf("writing a csv row: %w", err)
		}
	}
	cw.Flush()
	return cw.Error()
}

// WriteJSON writes an array of objects keyed by column name, one object per
// line so the output stays greppable. Empty values are omitted rather than
// written as "": a consumer asking for a key it does not find learns the run
// did not observe it, which is the same statement the empty cell makes.
//
// Every value is a JSON string, including the numeric columns. The ledger is
// a table of formatted cells; a consumer that wants numbers parses them, the
// same way it would from the TSV. The one export that types its columns is
// WriteSQL, because a SQL table cannot be created without a type per column.
func WriteJSON(w io.Writer, rows []Row) error {
	bw := bufio.NewWriter(w)
	// An empty ledger is "[]", not an array spread over two lines: a caller
	// piping this into jq or a script reads one token either way.
	bw.WriteString("[")
	for i, r := range rows {
		if i > 0 {
			bw.WriteString(",")
		}
		obj, err := rowObject(r)
		if err != nil {
			return err
		}
		bw.WriteString("\n  ")
		bw.Write(obj)
	}
	if len(rows) > 0 {
		bw.WriteString("\n")
	}
	bw.WriteString("]\n")
	return bw.Flush()
}

// WriteJSONL writes the objects WriteJSON puts in its array, one per line and
// nothing else on the line, so output from two invocations appended to one
// file is still one object per line. An empty ledger writes nothing: zero
// lines is the empty jsonl file.
func WriteJSONL(w io.Writer, rows []Row) error {
	bw := bufio.NewWriter(w)
	for _, r := range rows {
		obj, err := rowObject(r)
		if err != nil {
			return err
		}
		bw.Write(obj)
		bw.WriteString("\n")
	}
	return bw.Flush()
}

// rowObject is one row as a JSON object on one line: the unit WriteJSON and
// WriteJSONL share, so the two cannot carry different objects.
func rowObject(r Row) ([]byte, error) {
	var b strings.Builder
	b.WriteString("{")
	first := true
	for c, name := range Columns {
		v := ""
		if c < len(r.Values) {
			v = r.Values[c]
		}
		if v == "" {
			continue
		}
		if !first {
			b.WriteString(", ")
		}
		first = false
		key, err := json.Marshal(name)
		if err != nil {
			return nil, fmt.Errorf("encoding the column name %q: %w", name, err)
		}
		// Sanitised like every other export, so a note with a line break
		// cannot be the one value that differs between them.
		val, err := json.Marshal(sanitise(v))
		if err != nil {
			return nil, fmt.Errorf("encoding %s: %w", name, err)
		}
		b.Write(key)
		b.WriteString(": ")
		b.Write(val)
	}
	b.WriteString("}")
	return []byte(b.String()), nil
}

// SQLTable is the table WriteSQL creates and inserts into.
const SQLTable = "runs"

// sqlTypes is the declared type of every ledger column (TTP-81, 2026-09-14).
//
// It is the one place the ledger admits its cells have types, and it exists
// because `CREATE TABLE` cannot be written without them. TestEveryColumnHasASQLType
// fails when a column is appended to Columns without an entry here, and
// TestSQLTypesMatchTheCells checks the INTEGER and REAL claims against what
// FromTape actually writes.
//
// NUMERIC is for the server's own argv (-ngl, -b, -ub, -t), which toktape
// records as typed: almost always a number, but llama-server also takes words
// such as -ngl auto. SQLite's NUMERIC affinity stores "99" as the integer 99
// and "auto" as the text it is, so a sweep's ORDER BY ngl sorts numerically
// without an unexpected word breaking the import.
var sqlTypes = map[string]string{
	"id": "TEXT", "started_at": "TEXT", "tape": "TEXT", "tag": "TEXT", "note": "TEXT",
	"model": "TEXT", "quant": "TEXT", "params": "INTEGER", "size_gb": "REAL",
	"server": "TEXT", "build": "TEXT",
	"ngl": "NUMERIC", "flash_attn": "TEXT", "batch": "NUMERIC", "ubatch": "NUMERIC",
	"cache_type_k": "TEXT", "cache_type_v": "TEXT",
	"threads": "NUMERIC", "override_tensor": "TEXT",
	"concurrency": "INTEGER", "prompt_n": "INTEGER", "predicted_n": "INTEGER",
	"prefill_tok_s": "REAL", "decode_tok_s": "REAL", "per_stream_tok_s": "REAL", "aggregate_tok_s": "REAL",
	"ttft_ms": "INTEGER", "ttft_p50_ms": "INTEGER", "ttft_p95_ms": "INTEGER",
	"itl_p50_ms": "INTEGER", "itl_p95_ms": "INTEGER",
	"cache_label": "TEXT", "cache_n": "INTEGER",
	"rss_gb": "REAL", "vram_gb": "REAL", "gpus": "TEXT", "load1": "REAL",
	"throttled": "INTEGER", "contended": "INTEGER",
	"warnings": "INTEGER", "toktape_version": "TEXT",
	"for_s": "REAL", "cut_at_s": "REAL",
}

// WriteSQL writes a CREATE TABLE IF NOT EXISTS for the runs table and one
// INSERT per row, so `toktape log -o sql | sqlite3 runs.db` is a database and
// a second export into the same file appends to it.
//
// The table lists every column in Columns order, which is append-only, so a
// table created by an older export is a prefix of today's; an INSERT naming a
// column the old table lacks fails by that column's name rather than landing
// in the wrong one. Unknown is NULL — the SQL spelling of the empty cell.
// Identifiers are double-quoted so a future column named like a keyword
// cannot break the statement.
func WriteSQL(w io.Writer, rows []Row) error {
	bw := bufio.NewWriter(w)
	quoted := make([]string, len(Columns))
	defs := make([]string, len(Columns))
	for i, col := range Columns {
		typ := sqlTypes[col]
		if typ == "" {
			return fmt.Errorf("column %q has no SQL type", col)
		}
		quoted[i] = sqlIdent(col)
		defs[i] = "  " + quoted[i] + " " + typ
	}
	fmt.Fprintf(bw, "CREATE TABLE IF NOT EXISTS %s (\n%s\n);\n", SQLTable, strings.Join(defs, ",\n"))
	head := fmt.Sprintf("INSERT INTO %s (%s) VALUES (", SQLTable, strings.Join(quoted, ", "))
	for _, r := range rows {
		cs := cells(r)
		vals := make([]string, len(cs))
		for i, v := range cs {
			vals[i] = sqlLiteral(v, sqlTypes[Columns[i]])
		}
		fmt.Fprintf(bw, "%s%s);\n", head, strings.Join(vals, ", "))
	}
	return bw.Flush()
}

// sqlIdent double-quotes an identifier.
func sqlIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// sqlLiteral renders one cell. Text is a single-quoted literal with its quotes
// doubled, which is the whole of SQL's escaping: there is no backslash rule,
// so a --note carrying a quote, a semicolon or "--" stays inside the string.
// A cell in a numeric column is written bare only when it is a plain decimal
// number, so nothing but digits ever reaches the statement unquoted.
func sqlLiteral(v, typ string) string {
	if v == "" {
		return "NULL"
	}
	if typ != "TEXT" && isDecimal(v) {
		return v
	}
	return "'" + strings.ReplaceAll(v, "'", "''") + "'"
}

// isDecimal reports whether s is an optional minus, digits, and at most one
// decimal point followed by digits.
func isDecimal(s string) bool {
	s = strings.TrimPrefix(s, "-")
	whole, frac, hasPoint := strings.Cut(s, ".")
	if whole == "" || (hasPoint && frac == "") {
		return false
	}
	for _, part := range []string{whole, frac} {
		for _, c := range part {
			if c < '0' || c > '9' {
				return false
			}
		}
	}
	return true
}

// WriteMarkdown writes a GitHub table — the form a sweep gets pasted into a
// Reddit comment or an issue as.
func WriteMarkdown(w io.Writer, rows []Row) error {
	bw := bufio.NewWriter(w)
	fmt.Fprintf(bw, "| %s |\n", strings.Join(Columns, " | "))
	fmt.Fprintf(bw, "| %s |\n", strings.Join(repeat("---", len(Columns)), " | "))
	for _, r := range rows {
		cs := cells(r)
		for i, c := range cs {
			// A pipe inside a value would end the cell early and shift every
			// column after it.
			cs[i] = strings.ReplaceAll(c, "|", `\|`)
		}
		fmt.Fprintf(bw, "| %s |\n", strings.Join(cs, " | "))
	}
	return bw.Flush()
}

// cells returns a row padded to the full column list, sanitised.
func cells(r Row) []string {
	out := make([]string, len(Columns))
	for i := range out {
		if i < len(r.Values) {
			out[i] = sanitise(r.Values[i])
		}
	}
	return out
}

func repeat(s string, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = s
	}
	return out
}
