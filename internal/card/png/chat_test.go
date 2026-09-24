package png

import (
	"testing"

	"github.com/midagedev/toktape/internal/tape"
)

// TestChatRoundsClauseSaysTurns: the share image's form of the rounds row
// counts a chat tape's turns, from the same noun the text card uses, and a
// benchmark tape keeps "prompts" (TestRoundsClauseReplacesThePerStreamLine).
func TestChatRoundsClauseSaysTurns(t *testing.T) {
	tp, err := tape.Read("../testdata/chat-e2e.toktape")
	if err != nil {
		t.Fatal(err)
	}
	const want = "83.8 median of 2 turns · 81.9–85.6 tok/s"
	if got := roundsString(&tp.Summary); got != want {
		t.Errorf("roundsString = %q, want %q", got, want)
	}
}
