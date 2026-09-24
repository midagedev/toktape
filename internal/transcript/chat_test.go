package transcript

import (
	"testing"

	"github.com/midagedev/toktape/internal/tape"
)

// TestChatCutIsStopped: on a chat tape a Cut turn is one the person stopped
// with ctrl+c (the recorder reuses the clock's mark, internal/recorder
// session.go), so it reads "stopped"; a benchmark tape's Cut is still the
// clock's.
func TestChatCutIsStopped(t *testing.T) {
	tp, err := tape.Read("../card/testdata/chat-e2e.toktape")
	if err != nil {
		t.Fatal(err)
	}
	tp.Requests[1].Prompt.Cut = true
	tp.Requests[1].Prompt.FinishReason = ""
	got := Streams(tp)
	if got[0].Ended != "model stopped" {
		t.Errorf("turn 1 ended %q, want model stopped", got[0].Ended)
	}
	if got[1].Ended != "stopped" {
		t.Errorf("a cut chat turn ended %q, want stopped", got[1].Ended)
	}

	tp.Summary.Mode = ""
	if e := Streams(tp)[1].Ended; e != "clock cut" {
		t.Errorf("a cut benchmark stream ended %q, want clock cut", e)
	}
}
