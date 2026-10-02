package render

import (
	"testing"
	"time"

	"github.com/midagedev/toktape/internal/tape"
)

// TestDecisionRunEnd pins the clip schedule of a decision tape (TTP-192,
// 2026-10-02). Before it RunEnd and FirstToken counted tokens only, so a tape
// with no Requests had a zero-length run and a clip with no body; FAIL-first:
// with the decision branch removed both return 0 and this fails.
func TestDecisionRunEnd(t *testing.T) {
	tp, err := tape.Read("../tape/testdata/decision-example.tape")
	if err != nil {
		t.Fatal(err)
	}
	last := tp.Decisions[len(tp.Decisions)-1]
	if got := RunEnd(tp); got != last.AnsweredAt || got == 0 {
		t.Errorf("RunEnd = %v, want the last answer %v", got, last.AnsweredAt)
	}
	if got, want := FirstToken(tp), tp.Decisions[0].SentAt; got != want || got == 0 {
		t.Errorf("FirstToken = %v, want the first send %v", got, want)
	}

	// A failed last request ends the run at its send time, not at 0.
	failed := *tp
	failed.Decisions = append([]tape.DecisionRecord(nil), tp.Decisions...)
	n := len(failed.Decisions) - 1
	failed.Decisions[n].AnsweredAt = 0
	failed.Decisions[n].Error = "timeout"
	if got, want := RunEnd(&failed), failed.Decisions[n-1].AnsweredAt; got < want {
		t.Errorf("RunEnd with a failed last request = %v, want at least %v", got, want)
	}

	// The schedule has a body and ends on the card.
	_, sched, err := prepare(tp, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if sched.Stream < 20*time.Second {
		t.Errorf("stream phase is %v, want the run's ~24 s", sched.Stream)
	}
}
