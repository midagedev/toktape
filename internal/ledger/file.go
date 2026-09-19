package ledger

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/midagedev/toktape/internal/tape"
)

// Result is what one Rebuild wrote.
//
// The spec's first shape for Rebuild was (n int, err error), but Append calls
// Rebuild whenever the header drifts and must not read "three tapes in this
// directory are unreadable" as "the ledger was not written". Unreadable tapes
// are therefore a field, not an error: err stays reserved for a rebuild that
// actually failed, and nothing is dropped silently.
type Result struct {
	// Rows is the number of runs written to the ledger.
	Rows int
	// Unreadable names the .tape files that did not load. They keep their
	// place in the directory; only the ledger skips them.
	Unreadable []string
}

// Append adds one run to the ledger in dir, creating it when it does not
// exist yet.
//
// When the header on disk is not the header this build writes, the whole file
// is rebuilt from the tapes rather than being appended to: a row whose columns
// do not match its header is worse than a slower write, and the tapes hold
// everything the ledger holds. The same path covers a missing or truncated
// file, so a user who deleted runs.tsv gets the full history back on the next
// run instead of a one-row file.
//
// The caller is expected to have written tp's tape file already, so a rebuild
// triggered here includes tp.
func Append(dir string, tp *tape.Tape) error {
	path := filepath.Join(dir, FileName)
	head, err := readHeader(path)
	switch {
	case err != nil && errors.Is(err, os.ErrNotExist), err == nil && head != Header():
		// Two runs finishing at the same instant on a directory with no
		// ledger both rebuild, and the later rename wins; a tape written
		// after the winner's scan is then missing one row until the next
		// run appends or `toktape log --rebuild` regenerates. The ledger is
		// a cache of the tapes, so this heals itself rather than losing
		// anything — which is why it is not worth a lock file.
		if _, rerr := Rebuild(dir); rerr != nil {
			return rerr
		}
		return nil
	case err != nil:
		return fmt.Errorf("reading %s: %w", path, err)
	}

	// One line, built in memory and written with one syscall on an O_APPEND
	// descriptor. A ledger line is far under the 4 KB a Linux write to a
	// regular file completes without interleaving in practice, so two runs
	// finishing at the same moment produce two whole lines rather than a
	// spliced one. This is the reason the line is never written in pieces.
	line := Encode(FromTape(tp)) + "\n"
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o644)
	if err != nil {
		return fmt.Errorf("opening %s: %w", path, err)
	}
	if _, err := f.WriteString(line); err != nil {
		_ = f.Close()
		return fmt.Errorf("appending to %s: %w", path, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("closing %s: %w", path, err)
	}
	return nil
}

// Rebuild regenerates the ledger in dir from every tape it holds and writes
// it atomically (temp file + rename), so a reader either sees the old file or
// the whole new one.
//
// Tapes are read in file-name order, which is chronological: a run ID starts
// with the date and time.
func Rebuild(dir string) (Result, error) {
	var res Result
	entries, err := os.ReadDir(dir)
	if err != nil {
		return res, fmt.Errorf("reading %s: %w", dir, err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !tape.IsRunFile(e.Name()) {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)

	var b strings.Builder
	b.WriteString(Header())
	b.WriteByte('\n')
	for _, name := range names {
		tp, err := tape.Read(filepath.Join(dir, name))
		if err != nil {
			// Counted, never silently dropped: a ledger that quietly shrinks
			// is worse than one that says a tape stopped loading.
			res.Unreadable = append(res.Unreadable, name)
			continue
		}
		b.WriteString(Encode(FromTape(tp)))
		b.WriteByte('\n')
		res.Rows++
	}
	if err := writeAtomic(filepath.Join(dir, FileName), []byte(b.String())); err != nil {
		return res, err
	}
	return res, nil
}

// Read parses the ledger in dir.
//
// A missing file returns an error wrapping os.ErrNotExist; a header or a line
// that does not match this build returns a *StaleError. Both mean the same
// thing to a caller — rebuild from the tapes — and neither is repaired here,
// because the ledger is a cache and the tapes are the record.
func Read(dir string) ([]Row, error) {
	path := filepath.Join(dir, FileName)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) == "" {
		return nil, &StaleError{Path: path, Reason: "the ledger has no header row"}
	}
	if lines[0] != Header() {
		return nil, &StaleError{
			Path: path,
			Reason: fmt.Sprintf("header is %q, this build writes %q",
				strings.ReplaceAll(lines[0], "\t", " "),
				strings.ReplaceAll(Header(), "\t", " ")),
		}
	}
	rows := make([]Row, 0, len(lines)-1)
	for i, l := range lines[1:] {
		if strings.TrimSpace(l) == "" {
			continue
		}
		fields := strings.Split(l, "\t")
		if len(fields) != len(Columns) {
			return nil, &StaleError{
				Path: path,
				Reason: fmt.Sprintf("line %d has %d fields, the header has %d",
					i+2, len(fields), len(Columns)),
			}
		}
		rows = append(rows, Row{Values: fields})
	}
	return rows, nil
}

// readHeader returns the ledger's first line without reading the rest of it.
// An empty file reports an empty header, which the caller treats as "not a
// ledger yet".
func readHeader(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	// A header is one line of column names; the default 64 KB buffer is far
	// more than it can need, and a file whose first "line" is longer than
	// that is not a ledger.
	if !sc.Scan() {
		if err := sc.Err(); err != nil {
			return "", err
		}
		return "", nil
	}
	return sc.Text(), nil
}

// writeAtomic replaces path with data through a temp file in the same
// directory, so a reader never sees a half-written ledger.
func writeAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+FileName+"-*")
	if err != nil {
		return fmt.Errorf("creating a temp file in %s: %w", dir, err)
	}
	tmpName := tmp.Name()
	ok := false
	defer func() {
		if !ok {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("writing %s: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing %s: %w", tmpName, err)
	}
	// CreateTemp makes the file 0600; the ledger is meant to be read and
	// pasted like any other run artefact.
	if err := os.Chmod(tmpName, 0o644); err != nil {
		return fmt.Errorf("chmod %s: %w", tmpName, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("renaming %s to %s: %w", tmpName, path, err)
	}
	ok = true
	return nil
}
