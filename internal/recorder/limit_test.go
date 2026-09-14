package recorder

import (
	"testing"
	"time"

	"github.com/midagedev/toktape/internal/tape"
)

// The table that decides what may end a generation (TTP-76, 2026-09-14).
//
// It is resolved in exactly one function so that the CLI, the stream loop and
// the tape cannot disagree about it, and it is asserted here for the same
// reason: `toktape --help` prints these five rows to a reader who will act on
// them, and a row that drifted would be the tool teaching a lie.
func TestLimitMatrix(t *testing.T) {
	for _, tc := range []struct {
		name      string
		opts      Options
		wantFor   time.Duration
		wantMax   int
		wantFloor int
	}{{
		name:      "neither: the default budget, the runaway cap, or EOS",
		opts:      Options{},
		wantFor:   DefaultFor,
		wantMax:   DefaultMaxTokens,
		wantFloor: tape.MinCutTokens,
	}, {
		name:      "--for only: the clock at D, the runaway cap, or EOS",
		opts:      Options{For: 45 * time.Second},
		wantFor:   45 * time.Second,
		wantMax:   DefaultMaxTokens,
		wantFloor: tape.MinCutTokens,
	}, {
		// The row with a reason behind it: naming a token count is an answer
		// on the "how long" axis, and a default clock would overrule the user
		// on the axis they just spoke on. It is also what keeps this repo's
		// own hero command, `--n-predict 240`, recording what it recorded.
		name:      "--n-predict only: the cap, or EOS. No clock",
		opts:      Options{MaxTokens: 240},
		wantFor:   0,
		wantMax:   240,
		wantFloor: 0,
	}, {
		name:      "both: whichever comes first",
		opts:      Options{For: 8 * time.Second, MaxTokens: 240},
		wantFor:   8 * time.Second,
		wantMax:   240,
		wantFloor: tape.MinCutTokens,
	}, {
		name:      "--for 0: no clock",
		opts:      Options{For: NoClock},
		wantFor:   0,
		wantMax:   DefaultMaxTokens,
		wantFloor: 0,
	}, {
		name:      "--for 0 with a cap: no clock, the cap named",
		opts:      Options{For: NoClock, MaxTokens: 64},
		wantFor:   0,
		wantMax:   64,
		wantFloor: 0,
	}} {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.opts.limit()
			if got.For != tc.wantFor {
				t.Errorf("For = %v, want %v", got.For, tc.wantFor)
			}
			if got.MaxTokens != tc.wantMax {
				t.Errorf("MaxTokens = %d, want %d", got.MaxTokens, tc.wantMax)
			}
			if got.MinTokens != tc.wantFloor {
				t.Errorf("MinTokens = %d, want %d", got.MinTokens, tc.wantFloor)
			}
			if got.CutAt != 0 {
				t.Errorf("CutAt = %v before the run; it is what happened, not what was asked", got.CutAt)
			}
		})
	}
}

// TestEveryRequestCarriesACap: whatever the row, the resolved cap is a positive
// number, because it is what is left when the clock fails.
//
// llama-server with no n_predict generates until the context is full, so a run
// that relied on a clock and lost it would be a runaway — see
// tape.LimitSummary.MaxTokens, which documents the cap as always sent.
func TestEveryRequestCarriesACap(t *testing.T) {
	for _, o := range []Options{
		{},
		{For: time.Second},
		{MaxTokens: 16},
		{For: NoClock},
		{For: -time.Hour}, // any negative reads as NoClock, not as a cap of nothing
	} {
		if got := o.limit().MaxTokens; got <= 0 {
			t.Errorf("Options%+v resolved to a cap of %d; a request must always carry one", o, got)
		}
	}
}

// TestMarkCutLeavesTheServersWordAlone: a stream that reached its own finish
// chunk between the clock's snapshot and its cancel is not a cut stream.
//
// The snapshot is taken one call before the cancel, so the window is tiny but
// real. Marking such a stream would put Cut and a FinishReason on the same
// record, which says two different things about how it ended.
func TestMarkCutLeavesTheServersWordAlone(t *testing.T) {
	recs := []tape.RequestRecord{
		{Error: "server: stream: read: context canceled"},                               // cut
		{Prompt: tape.PromptRecord{FinishReason: "stop"}},                               // finished in the window
		{Error: "server: stream: read: context canceled"},                               // cut
		{Prompt: tape.PromptRecord{FinishReason: "length"}, Error: "should not matter"}, // finished earlier
	}
	live := []bool{true, true, true, false}
	if n := markCut(recs, live); n != 2 {
		t.Fatalf("markCut marked %d streams, want the 2 it actually cut", n)
	}
	for i, want := range []bool{true, false, true, false} {
		if recs[i].Prompt.Cut != want {
			t.Errorf("record %d Cut = %v, want %v", i, recs[i].Prompt.Cut, want)
		}
		if want && recs[i].Error != "" {
			t.Errorf("record %d is cut and still carries Error %q; a cut run would read as a failed one",
				i, recs[i].Error)
		}
		if want && recs[i].Prompt.FinishReason != "" {
			t.Errorf("record %d is cut and carries FinishReason %q; the server never said a word",
				i, recs[i].Prompt.FinishReason)
		}
	}
	if recs[3].Error == "" {
		t.Error("a stream that was not live had its error cleared")
	}
}

// TestLimitRecordsWhetherTheCapWasNamed: the table's "both" row cannot be
// reproduced from a tape that does not say whether its cap was the user's or
// the runaway guard (lead, 2026-09-14). Only a cap the user gave is named.
func TestLimitRecordsWhetherTheCapWasNamed(t *testing.T) {
	for _, tc := range []struct {
		name string
		opts Options
		want bool
	}{
		{"neither", Options{}, false},
		{"--for only", Options{For: 45 * time.Second}, false},
		{"--n-predict only", Options{MaxTokens: 240}, true},
		{"both", Options{For: 8 * time.Second, MaxTokens: 240}, true},
		{"--for 0", Options{For: NoClock}, false},
	} {
		if got := tc.opts.limit().MaxTokensNamed; got != tc.want {
			t.Errorf("%s: MaxTokensNamed = %v, want %v", tc.name, got, tc.want)
		}
	}
}
