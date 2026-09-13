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
// ledger's own format: `sqlite3 runs.db ".import --tsv runs.tsv runs"`.
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
// a table of formatted cells and a per-column type table would be a second
// schema to keep in step with Columns; a consumer that wants numbers parses
// them, the same way it would from the TSV.
func WriteJSON(w io.Writer, rows []Row) error {
	bw := bufio.NewWriter(w)
	// An empty ledger is "[]", not an array spread over two lines: a caller
	// piping this into jq or a script reads one token either way.
	bw.WriteString("[")
	for i, r := range rows {
		if i > 0 {
			bw.WriteString(",")
		}
		bw.WriteString("\n  {")
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
				bw.WriteString(", ")
			}
			first = false
			key, err := json.Marshal(name)
			if err != nil {
				return fmt.Errorf("encoding the column name %q: %w", name, err)
			}
			val, err := json.Marshal(v)
			if err != nil {
				return fmt.Errorf("encoding %s: %w", name, err)
			}
			bw.Write(key)
			bw.WriteString(": ")
			bw.Write(val)
		}
		bw.WriteString("}")
	}
	if len(rows) > 0 {
		bw.WriteString("\n")
	}
	bw.WriteString("]\n")
	return bw.Flush()
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
