package compare_test

import (
	"strings"
	"testing"

	"github.com/midagedev/toktape/internal/card"
	"github.com/midagedev/toktape/internal/compare"
	"github.com/midagedev/toktape/internal/tape"
)

// TestTapesChat pins the chat rules of Tapes: a chat against a benchmark is an
// error in either order; two chats that opened differently carry a note that
// keeps the width contract; two that opened alike carry none, and their
// rounds count is labelled turns.
func TestTapesChat(t *testing.T) {
	chat, err := tape.Read("../card/testdata/chat-e2e.toktape")
	if err != nil {
		t.Fatal(err)
	}
	bench := &tape.Tape{Summary: *card.Example()}
	for _, pair := range [][2]*tape.Tape{{chat, bench}, {bench, chat}} {
		if _, err := compare.Tapes(pair[0], pair[1]); err == nil || !strings.Contains(err.Error(), "not comparable") {
			t.Errorf("chat against benchmark: err = %v", err)
		}
	}

	other, _ := tape.Read("../card/testdata/chat-e2e.toktape")
	other.Summary.Rounds = 3
	for i := range other.Requests {
		other.Requests[i].Prompt.Messages[0].Content = "something else"
	}
	r, err := compare.Tapes(chat, other)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Notes) != 1 || !strings.Contains(r.Notes[0], "different conversations") {
		t.Errorf("notes = %q", r.Notes)
	}
	out := compare.Text(r)
	for i, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if w := card.Width(line); w > compare.Width {
			t.Errorf("line %d is %d columns, max %d: %q", i+1, w, compare.Width, line)
		}
	}
	if !strings.Contains(out, "turns         2 → 3") {
		t.Errorf("the rounds count is not labelled turns:\n%s", out)
	}

	same, _ := tape.Read("../card/testdata/chat-e2e.toktape")
	if r, _ := compare.Tapes(chat, same); len(r.Notes) != 0 {
		t.Errorf("two chats that opened alike: notes %q", r.Notes)
	}
}
