package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/midagedev/toktape/internal/tape"
)

// `toktape play` on a chat tape draws the record screen, and a chat has turns,
// not rounds: the title reads "turn 2/2" where a benchmark reads "round 2/2"
// (look round 3, 2026-09-24).
func TestReplayOfAChatSaysTurn(t *testing.T) {
	tp, err := tape.Read("../card/testdata/chat-e2e.toktape")
	if err != nil {
		t.Fatal(err)
	}
	if tp.Summary.Rounds < 2 {
		t.Fatalf("fixture has %d rounds, want a chat of two turns", tp.Summary.Rounds)
	}
	m := ModelAt(tp, time.Hour)
	v := View(m, time.Hour, 140, MinHeight)
	top := strings.SplitN(v, "\n", 2)[0]
	if !strings.Contains(top, "turn 2/2") || strings.Contains(top, "round") {
		t.Errorf("replay title of a chat = %q, want \"turn 2/2\" and no \"round\"", top)
	}
}
