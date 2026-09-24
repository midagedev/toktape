package ledger

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/midagedev/toktape/internal/card"
	"github.com/midagedev/toktape/internal/tape"
)

// TestChatTapesStayOutOfTheLedger: the experiment log is a table of runs
// meant to be compared, and a chat is a conversation, never comparable with
// them (tape.RunSummary.Mode). Append refuses it quietly — nil, so a saved
// chat prints no ledger warning — and Rebuild, which Append itself falls back
// to and `toktape log --rebuild` calls, skips it, so the row cannot come back
// through the other door.
func TestChatTapesStayOutOfTheLedger(t *testing.T) {
	dir := t.TempDir()
	chat, err := tape.Read("../card/testdata/chat-e2e.toktape")
	if err != nil {
		t.Fatal(err)
	}
	if err := tape.Write(filepath.Join(dir, chat.Summary.ID+tape.Ext), chat); err != nil {
		t.Fatal(err)
	}

	// No ledger yet: Append takes the rebuild path.
	if err := Append(dir, chat); err != nil {
		t.Fatalf("Append(chat): %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, FileName)); err == nil {
		if rows, _ := Read(dir); len(rows) != 0 {
			t.Errorf("the ledger holds %d rows after a chat, want 0", len(rows))
		}
	}

	bench := card.Example()
	writeTape(t, dir, bench)
	if err := Append(dir, tapeOf(bench)); err != nil {
		t.Fatalf("Append(bench): %v", err)
	}
	// A ledger exists now: Append takes the one-line path.
	if err := Append(dir, chat); err != nil {
		t.Fatalf("Append(chat): %v", err)
	}
	rows, err := Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Get("id") != bench.ID {
		t.Errorf("rows = %v, want the benchmark run alone", rows)
	}

	res, err := Rebuild(dir)
	if err != nil {
		t.Fatal(err)
	}
	if res.Rows != 1 || len(res.Unreadable) != 0 {
		t.Errorf("Rebuild = %+v, want one row and nothing unreadable", res)
	}
	if rows, _ := Read(dir); len(rows) != 1 {
		t.Errorf("after Rebuild the ledger holds %d rows, want 1", len(rows))
	}
}
