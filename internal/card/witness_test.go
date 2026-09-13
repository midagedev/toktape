package card

import (
	"strings"
	"testing"

	"github.com/midagedev/toktape/internal/gpu"
	"github.com/midagedev/toktape/internal/tape"
)

func witnessF64(v float64) *float64 { return &v }

// TestWitnessReasonsReachTheTextCard (TTP-36): the witnesses add no row. Their
// reasons print where every contention reason prints, under "contended: yes"
// in the HOST section, whole.
func TestWitnessReasonsReachTheTextCard(t *testing.T) {
	server := tape.LlamaProc{PID: 1234, Comm: "llama-server", AgeSec: 900, Attached: true}
	bench := tape.LlamaProc{PID: 4242, Comm: "llama-bench", AgeSec: 30}
	ghost := tape.LlamaProc{PID: 5150, Comm: "llama-server", AgeSec: 12}

	for _, tc := range []struct {
		name string
		s    *tape.RunSummary
	}{
		{"single round", Example()},
		{"rounds", ExampleRounds()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			quiet := Text(tc.s)
			ws := []tape.ContentionWitness{
				{Round: 1, Edge: "start", LoadAvg1: 1.2, IOSomeAvg10: witnessF64(3.1), ProcsRead: true,
					LlamaProcs: []tape.LlamaProc{server, bench}},
				{Round: 2, Edge: "start", LoadAvg1: 1.4, IOSomeAvg10: witnessF64(12.3), ProcsRead: true,
					LlamaProcs: []tape.LlamaProc{server, bench, ghost}},
			}
			tc.s.Contention = gpu.Witnessed(tc.s.Contention, ws)
			out := Text(tc.s)

			wantReasons := []string{
				"io pressure 12.3 > 5 at round 2 start",
				"llama-bench pid 4242 running at round 1 start (+1 more)",
			}
			if len(tc.s.Contention.Reasons) != len(wantReasons) {
				t.Fatalf("Reasons = %q, want %q", tc.s.Contention.Reasons, wantReasons)
			}
			if !strings.Contains(out, "contended: yes") {
				t.Errorf("card does not say contended: yes:\n%s", out)
			}
			for i, r := range wantReasons {
				if tc.s.Contention.Reasons[i] != r {
					t.Errorf("reason %d = %q, want %q", i, tc.s.Contention.Reasons[i], r)
				}
				if !strings.Contains(out, r) {
					t.Errorf("card does not print the witness reason %q whole:\n%s", r, out)
				}
			}
			if strings.Count(out, "\n") != strings.Count(quiet, "\n")+2 {
				t.Errorf("card grew by %d lines, want the 2 reason lines under HOST:\n%s",
					strings.Count(out, "\n")-strings.Count(quiet, "\n"), out)
			}
		})
	}
}

// TestQuietWitnessesLeaveTheCardByteIdentical: a run whose witnesses found a
// quiet box prints exactly the card it printed before witnesses existed. The
// goldens in card_test.go pin that card; this pins that witnesses do not move
// it.
func TestQuietWitnessesLeaveTheCardByteIdentical(t *testing.T) {
	server := tape.LlamaProc{PID: 1234, Comm: "llama-server", AgeSec: 900, Attached: true}
	for _, tc := range []struct {
		name string
		mk   func() *tape.RunSummary
	}{
		{"example", Example},
		{"example-concurrent", ExampleConcurrent},
		{"example-rounds", ExampleRounds},
		{"example-speculative", ExampleSpeculative},
		{"example-sharded", ExampleSharded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			want := Text(tc.mk())
			s := tc.mk()
			before := s.Contention
			s.Contention = gpu.Witnessed(s.Contention, []tape.ContentionWitness{
				{Round: 0, Edge: "start", LoadAvg1: before.LoadAvg1, IOSomeAvg10: witnessF64(0.21),
					PageCacheBytes: 64 << 30, ProcsRead: true, LlamaProcs: []tape.LlamaProc{server}},
				{Round: 0, Edge: "end", LoadAvg1: before.LoadAvg1, IOSomeAvg10: witnessF64(0),
					PageCacheBytes: 64 << 30, ProcsRead: true, LlamaProcs: []tape.LlamaProc{server}},
			})
			if s.Contention.Contended != before.Contended || len(s.Contention.Reasons) != len(before.Reasons) {
				t.Fatalf("quiet witnesses changed the verdict: %+v -> %+v", before, s.Contention)
			}
			if got := Text(s); got != want {
				t.Errorf("card moved with quiet witnesses.\n--- got ---\n%s\n--- want ---\n%s", got, want)
			}
		})
	}
}
