package ledger

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/midagedev/toktape/internal/card"
	"github.com/midagedev/toktape/internal/tape"
)

// tapeOf wraps a card fixture summary in a tape, the way the recorder does.
func tapeOf(s *tape.RunSummary) *tape.Tape {
	return &tape.Tape{Schema: tape.SchemaVersion, Summary: *s}
}

// get is FromTape plus one column lookup, so a case reads as the column it
// asserts on.
func get(t *testing.T, tp *tape.Tape, col string) string {
	t.Helper()
	if Index(col) < 0 {
		t.Fatalf("%q is not a ledger column", col)
	}
	return FromTape(tp).Get(col)
}

// TestColumnsAreUnique guards the schema itself: a duplicated name would make
// colIndex silently drop a column and every export after it would be shifted.
func TestColumnsAreUnique(t *testing.T) {
	seen := map[string]bool{}
	for i, c := range Columns {
		if seen[c] {
			t.Errorf("column %d %q is a duplicate", i, c)
		}
		seen[c] = true
	}
	if n := len(strings.Split(Header(), "\t")); n != len(Columns) {
		t.Errorf("Header() has %d fields, Columns has %d", n, len(Columns))
	}
}

// TestFromTapeSingleStream is the N=1 contract: the server's own decode rate
// is both the decode and the aggregate column, and the per-stream column says
// nothing because there is one stream.
func TestFromTapeSingleStream(t *testing.T) {
	tp := tapeOf(card.Example())
	want := map[string]string{
		"id":               "20260913-142530-qwen3.5-35b-a3b",
		"tape":             "20260913-142530-qwen3.5-35b-a3b.tape",
		"model":            "Qwen3.5 35B A3B",
		"quant":            "UD-Q4_K_M",
		"params":           "35000000000",
		"size_gb":          "19.8",
		"server":           "llama-server",
		"build":            "b3650",
		"ngl":              "99",
		"flash_attn":       "on",
		"batch":            "2048",
		"ubatch":           "512",
		"cache_type_k":     "q8_0",
		"cache_type_v":     "q8_0",
		"threads":          "16",
		"override_tensor":  `blk\.(3[6-9]|4[0-7])\.ffn_.*_exps=CPU`,
		"concurrency":      "1",
		"prompt_n":         "112",
		"predicted_n":      "128",
		"prefill_tok_s":    "2450.0",
		"decode_tok_s":     "68.4",
		"per_stream_tok_s": "",
		"aggregate_tok_s":  "68.4",
		"ttft_ms":          "143",
		"itl_p50_ms":       "14",
		"itl_p95_ms":       "20",
		"cache_label":      "warm",
		"cache_n":          "400",
		"rss_gb":           "3.4",
		"gpus":             "RTX 3090+RTX 3090",
		"load1":            "1.2",
		"warnings":         "0",
		"toktape_version":  "0.1.0",
	}
	for col, w := range want {
		if got := get(t, tp, col); got != w {
			t.Errorf("%s = %q, want %q", col, got, w)
		}
	}
	// started_at carries this reader's own offset, so it is built through the
	// same conversion rather than hard-coded to one timezone.
	if got, w := get(t, tp, "started_at"), card.Example().StartedAt.Local().Format(time.RFC3339); got != w {
		t.Errorf("started_at = %q, want %q", got, w)
	}
	// The two devices held 9.2 and 8.8 GiB at the end of the run.
	if got := get(t, tp, "vram_gb"); got != "18.0" {
		t.Errorf("vram_gb = %q, want 18.0", got)
	}
	// Never observed, so empty — not "?" and not a 0 nobody measured.
	for _, col := range []string{"tag", "note"} {
		if got := get(t, tp, col); got != "" {
			t.Errorf("%s = %q, want an empty cell", col, got)
		}
	}
	// Both flags were observed on this fixture — it sampled two GPUs and read
	// a load average — so both are a measured 0 rather than an empty cell.
	for _, col := range []string{"throttled", "contended"} {
		if got := get(t, tp, col); got != "0" {
			t.Errorf("%s = %q, want 0", col, got)
		}
	}
}

// TestFromTapeConcurrent is the N>1 contract: decode is the per-stream mean a
// reader compares against their single-session number, and the aggregate is
// what the server delivered.
func TestFromTapeConcurrent(t *testing.T) {
	tp := tapeOf(card.ExampleConcurrent())
	want := map[string]string{
		"concurrency":      "8",
		"decode_tok_s":     "12.1",
		"per_stream_tok_s": "12.1",
		"aggregate_tok_s":  "96.8",
		"prefill_tok_s":    "1980.0",
		"ttft_ms":          "210",
		"ttft_p50_ms":      "210",
		"ttft_p95_ms":      "480",
		"cache_label":      "cold",
		"warnings":         "2",
	}
	for col, w := range want {
		if got := get(t, tp, col); got != w {
			t.Errorf("%s = %q, want %q", col, got, w)
		}
	}
	// The cold run's prompt cache hit nothing. A 0 here would be imported as
	// a measurement, so the unobserved figure stays an empty cell.
	if got := get(t, tp, "cache_n"); got != "" {
		t.Errorf("cache_n = %q, want an empty cell", got)
	}
}

// TestFromTapeUnknownsAreEmpty: a tape that observed almost nothing writes
// empty cells, never "?" and never a default.
func TestFromTapeUnknownsAreEmpty(t *testing.T) {
	tp := tapeOf(&tape.RunSummary{ID: "20260101-000000-x", Concurrency: 1})
	r := FromTape(tp)
	for i, col := range Columns {
		switch col {
		case "id", "tape", "concurrency", "warnings":
			continue
		}
		if v := r.Values[i]; v != "" {
			t.Errorf("%s = %q on an empty summary, want an empty cell", col, v)
		}
	}
	if r.Get("warnings") != "0" {
		t.Errorf("warnings = %q, want 0: a run with nothing to warn about observed zero", r.Get("warnings"))
	}
}

// TestEncodeEscapes: the line format has no escape character, so a note with
// a tab or a newline must not be able to split a row.
func TestEncodeEscapes(t *testing.T) {
	s := card.Example()
	s.Tag = "ngl=40"
	s.Note = "two\tcolumns\nand a second line\r"
	line := Encode(FromTape(tapeOf(s)))
	if n := len(strings.Split(line, "\t")); n != len(Columns) {
		t.Fatalf("the encoded row has %d fields, want %d", n, len(Columns))
	}
	if strings.ContainsAny(line, "\n\r") {
		t.Errorf("the encoded row carries a line break: %q", line)
	}
	fields := strings.Split(line, "\t")
	if got, want := fields[Index("note")], "two columns and a second line "; got != want {
		t.Errorf("note = %q, want %q", got, want)
	}
	if got := fields[Index("tag")]; got != "ngl=40" {
		t.Errorf("tag = %q, want ngl=40", got)
	}
}

// writeTape saves a summary as a run file in dir.
func writeTape(t *testing.T, dir string, s *tape.RunSummary) string {
	t.Helper()
	path := filepath.Join(dir, s.ID+tape.Ext)
	if err := tape.Write(path, tapeOf(s)); err != nil {
		t.Fatalf("tape.Write: %v", err)
	}
	return path
}

func readLines(t *testing.T, dir string) []string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, FileName))
	if err != nil {
		t.Fatalf("reading the ledger: %v", err)
	}
	return strings.Split(strings.TrimRight(string(b), "\n"), "\n")
}

// TestAppendCreatesAndGrows: the first run writes a header and a row, the
// second adds exactly one line.
func TestAppendCreatesAndGrows(t *testing.T) {
	dir := t.TempDir()
	first := card.Example()
	writeTape(t, dir, first)
	if err := Append(dir, tapeOf(first)); err != nil {
		t.Fatalf("Append: %v", err)
	}
	lines := readLines(t, dir)
	if len(lines) != 2 {
		t.Fatalf("the ledger has %d lines, want a header and one row:\n%s", len(lines), strings.Join(lines, "\n"))
	}
	if lines[0] != Header() {
		t.Errorf("the first line is not the header: %q", lines[0])
	}

	second := card.ExampleConcurrent()
	writeTape(t, dir, second)
	if err := Append(dir, tapeOf(second)); err != nil {
		t.Fatalf("Append: %v", err)
	}
	lines = readLines(t, dir)
	if len(lines) != 3 {
		t.Fatalf("the ledger has %d lines, want a header and two rows", len(lines))
	}
	rows, err := Read(dir)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(rows) != 2 || rows[0].Get("id") != first.ID || rows[1].Get("id") != second.ID {
		t.Errorf("rows = %v, want the two runs in the order they were appended", rows)
	}
}

// TestAppendRebuildsOnStaleHeader: a ledger written by a build with different
// columns is regenerated from the tapes, never appended to. A row that does
// not match its own header is worse than a slower write.
func TestAppendRebuildsOnStaleHeader(t *testing.T) {
	dir := t.TempDir()
	stale := filepath.Join(dir, FileName)
	if err := os.WriteFile(stale, []byte("id\tfoo\nold-run\t1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := card.Example()
	writeTape(t, dir, s)
	if err := Append(dir, tapeOf(s)); err != nil {
		t.Fatalf("Append: %v", err)
	}

	lines := readLines(t, dir)
	if lines[0] != Header() {
		t.Errorf("the stale header survived: %q", lines[0])
	}
	if len(lines) != 2 {
		t.Fatalf("the ledger has %d lines, want the header and the one tape in the directory:\n%s",
			len(lines), strings.Join(lines, "\n"))
	}
	if strings.Contains(lines[1], "old-run") {
		t.Errorf("the stale row survived the rebuild: %q", lines[1])
	}
	rows, err := Read(dir)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(rows) != 1 || rows[0].Get("id") != s.ID {
		t.Errorf("after the rebuild the ledger holds %d rows, want the one tape", len(rows))
	}
}

// TestAppendRebuildsWhenMissing: a user who deleted runs.tsv gets the whole
// history back on the next run, not a one-row file.
func TestAppendRebuildsWhenMissing(t *testing.T) {
	dir := t.TempDir()
	old := card.Example()
	writeTape(t, dir, old)
	fresh := card.ExampleConcurrent()
	writeTape(t, dir, fresh)

	if err := Append(dir, tapeOf(fresh)); err != nil {
		t.Fatalf("Append: %v", err)
	}
	rows, err := Read(dir)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("the rebuilt ledger has %d rows, want both tapes", len(rows))
	}
}

// TestRebuildCountsUnreadableTapes: a tape that stopped loading is named, not
// skipped in silence — a run directory that quietly shrinks is worse than one
// row saying a file is unreadable.
func TestRebuildCountsUnreadableTapes(t *testing.T) {
	dir := t.TempDir()
	writeTape(t, dir, card.Example())
	bad := filepath.Join(dir, "20260913-999999-broken"+tape.Ext)
	if err := os.WriteFile(bad, []byte("this is not a tape"), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := Rebuild(dir)
	if err != nil {
		t.Fatalf("Rebuild: %v", err)
	}
	if res.Rows != 1 {
		t.Errorf("Rebuild wrote %d rows, want 1", res.Rows)
	}
	if len(res.Unreadable) != 1 || res.Unreadable[0] != filepath.Base(bad) {
		t.Errorf("Unreadable = %v, want the broken tape", res.Unreadable)
	}
	if lines := readLines(t, dir); len(lines) != 2 {
		t.Errorf("the ledger has %d lines, want the header and the readable run", len(lines))
	}
}

// TestRebuildIsChronological: run IDs start with the date, so file-name order
// is the order the runs happened in.
func TestRebuildIsChronological(t *testing.T) {
	dir := t.TempDir()
	writeTape(t, dir, card.ExampleConcurrent()) // 15:02
	writeTape(t, dir, card.Example())           // 14:25

	res, err := Rebuild(dir)
	if err != nil {
		t.Fatalf("Rebuild: %v", err)
	}
	if res.Rows != 2 {
		t.Fatalf("Rebuild wrote %d rows, want 2", res.Rows)
	}
	rows, err := Read(dir)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if rows[0].Get("id") != card.Example().ID {
		t.Errorf("the first row is %q, want the earlier run", rows[0].Get("id"))
	}
}

// TestReadReportsStaleLedger: a ledger this build cannot parse names both
// forms and asks to be rebuilt rather than being repaired in place.
func TestReadReportsStaleLedger(t *testing.T) {
	cases := []struct {
		name, body, wants string
	}{
		{"header", "id\tfoo\nrun\t1\n", "this build writes"},
		{"short row", Header() + "\nonly-one-field\n", "fields"},
		{"no header", "\n", "no header row"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, FileName), []byte(tc.body), 0o644); err != nil {
				t.Fatal(err)
			}
			_, err := Read(dir)
			if err == nil {
				t.Fatal("Read accepted a ledger it cannot have written")
			}
			if !IsStale(err) {
				t.Errorf("IsStale(%v) = false, want true", err)
			}
			if !strings.Contains(err.Error(), tc.wants) {
				t.Errorf("the error does not say %q: %v", tc.wants, err)
			}
		})
	}
}

// TestReadMissingLedger: a directory with no ledger reports it as missing, so
// the caller rebuilds rather than treating it as an empty history.
func TestReadMissingLedger(t *testing.T) {
	_, err := Read(t.TempDir())
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("Read on a directory with no ledger = %v, want os.ErrNotExist", err)
	}
}
