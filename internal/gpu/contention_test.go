package gpu

import (
	"reflect"
	"testing"

	"github.com/midagedev/toktape/internal/tape"
)

func TestContention(t *testing.T) {
	tests := []struct {
		name          string
		load1         float64
		cpuThreads    int
		otherGPUProcs int
		serverThreads int
		wantContended bool
		wantReasons   []string
	}{
		{
			name:       "quiet host",
			load1:      2.4,
			cpuThreads: 64,
		},
		{
			name:          "cpu-thread rule fires",
			load1:         61.2,
			cpuThreads:    64,
			wantContended: true,
			wantReasons:   []string{"loadavg 61.2 > 57.6 (90% of 64 threads)"},
		},
		{
			name:       "cpu-thread rule boundary is not contended",
			load1:      57.6,
			cpuThreads: 64,
		},
		{
			name:          "server-thread rule replaces the cpu rule when threads are known",
			load1:         61.2,
			cpuThreads:    64,
			serverThreads: 64,
			// 61.2 is over the cpu threshold (57.6) but well under the
			// server threshold (96.0): a server told to use 64 threads
			// generates that load itself and must not self-flag.
		},
		{
			name:          "server-thread rule boundary is not contended",
			load1:         96.0,
			cpuThreads:    64,
			serverThreads: 64,
		},
		{
			name:          "server-thread rule fires",
			load1:         110.5,
			cpuThreads:    64,
			serverThreads: 64,
			wantContended: true,
			wantReasons:   []string{"loadavg 110.5 > 96.0 (150% of 64 server threads)"},
		},
		{
			name:          "one other gpu process",
			load1:         1.1,
			cpuThreads:    64,
			otherGPUProcs: 1,
			wantContended: true,
			wantReasons:   []string{"1 other GPU compute process"},
		},
		{
			name:          "several other gpu processes",
			load1:         1.1,
			cpuThreads:    64,
			otherGPUProcs: 2,
			wantContended: true,
			wantReasons:   []string{"2 other GPU compute processes"},
		},
		{
			name:          "both reasons",
			load1:         61.2,
			cpuThreads:    64,
			otherGPUProcs: 2,
			wantContended: true,
			wantReasons: []string{
				"loadavg 61.2 > 57.6 (90% of 64 threads)",
				"2 other GPU compute processes",
			},
		},
		{
			name:  "no thread count known: the load rule cannot fire",
			load1: 512.0,
		},
		{
			name:          "no thread count known: the gpu rule still fires",
			load1:         512.0,
			otherGPUProcs: 1,
			wantContended: true,
			wantReasons:   []string{"1 other GPU compute process"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Contention(tc.load1, tc.cpuThreads, tc.otherGPUProcs, tc.serverThreads)
			if got.Contended != tc.wantContended {
				t.Errorf("Contended = %v, want %v (reasons %q)", got.Contended, tc.wantContended, got.Reasons)
			}
			if !reflect.DeepEqual(got.Reasons, tc.wantReasons) {
				t.Errorf("Reasons =\n %q\nwant %q", got.Reasons, tc.wantReasons)
			}
			// Lesson 6: the readings are recorded whatever the verdict.
			if got.LoadAvg1 != tc.load1 {
				t.Errorf("LoadAvg1 = %v, want %v", got.LoadAvg1, tc.load1)
			}
			if got.OtherGPUProcs != tc.otherGPUProcs {
				t.Errorf("OtherGPUProcs = %d, want %d", got.OtherGPUProcs, tc.otherGPUProcs)
			}
		})
	}
}

func f64(v float64) *float64 { return &v }

// TestWitnessed (TTP-36): the witnesses add reasons to a verdict, never take
// one away, and each reason names the worst reading and where it was taken.
func TestWitnessed(t *testing.T) {
	server := tape.LlamaProc{PID: 1234, Comm: "llama-server", AgeSec: 366.9, Attached: true}
	bench := tape.LlamaProc{PID: 4242, Comm: "llama-bench", AgeSec: 120}
	other := tape.LlamaProc{PID: 5150, Comm: "llama-server", AgeSec: 40}
	ppl := tape.LlamaProc{PID: 6001, Comm: "llama-perplexi", AgeSec: 3}
	loadReason := "loadavg 61.2 > 57.6 (90% of 64 threads)"

	tests := []struct {
		name          string
		info          tape.ContentionInfo
		ws            []tape.ContentionWitness
		wantContended bool
		wantReasons   []string
	}{
		{
			name: "no witnesses: unchanged",
			info: tape.ContentionInfo{LoadAvg1: 2.4},
		},
		{
			name: "quiet witnesses: recorded, not contended",
			info: tape.ContentionInfo{LoadAvg1: 2.4},
			ws: []tape.ContentionWitness{
				{Round: 1, Edge: "start", IOSomeAvg10: f64(0.4), ProcsRead: true, LlamaProcs: []tape.LlamaProc{server}},
				{Round: 1, Edge: "end", IOSomeAvg10: f64(0), ProcsRead: true, LlamaProcs: []tape.LlamaProc{server}},
			},
		},
		{
			name: "io pressure exactly at the threshold is not contended",
			ws:   []tape.ContentionWitness{{Edge: "start", IOSomeAvg10: f64(tape.ContendedIOSomeAvg10)}},
		},
		{
			name: "unreadable io pressure is not a reading",
			ws:   []tape.ContentionWitness{{Edge: "start"}, {Edge: "end"}},
		},
		{
			name: "the worst io reading is named with its round and edge",
			ws: []tape.ContentionWitness{
				{Round: 1, Edge: "start", IOSomeAvg10: f64(6.2)},
				{Round: 1, Edge: "end", IOSomeAvg10: f64(1.0)},
				{Round: 2, Edge: "start", IOSomeAvg10: f64(12.34)},
				{Round: 2, Edge: "end", IOSomeAvg10: f64(9.0)},
			},
			wantContended: true,
			wantReasons:   []string{"io pressure 12.3 > 5 at round 2 start"},
		},
		{
			name: "a tie names the earliest reading",
			ws: []tape.ContentionWitness{
				{Round: 1, Edge: "end", IOSomeAvg10: f64(7.5)},
				{Round: 3, Edge: "start", IOSomeAvg10: f64(7.5)},
			},
			wantContended: true,
			wantReasons:   []string{"io pressure 7.5 > 5 at round 1 end"},
		},
		{
			name: "single-round run says start or end",
			ws: []tape.ContentionWitness{
				{Round: 0, Edge: "start", IOSomeAvg10: f64(2)},
				{Round: 0, Edge: "end", IOSomeAvg10: f64(48.25)},
			},
			wantContended: true,
			wantReasons:   []string{"io pressure 48.2 > 5 at end"},
		},
		{
			name: "the attached server alone is not contention",
			ws: []tape.ContentionWitness{
				{Edge: "start", ProcsRead: true, LlamaProcs: []tape.LlamaProc{server}},
			},
		},
		{
			name: "one foreign llama process",
			ws: []tape.ContentionWitness{
				{Round: 1, Edge: "start", ProcsRead: true, LlamaProcs: []tape.LlamaProc{server}},
				{Round: 1, Edge: "end", ProcsRead: true, LlamaProcs: []tape.LlamaProc{server, bench}},
				{Round: 2, Edge: "start", ProcsRead: true, LlamaProcs: []tape.LlamaProc{server, bench}},
			},
			wantContended: true,
			wantReasons:   []string{"llama-bench pid 4242 running at round 1 end"},
		},
		{
			name: "the first foreign process is named, other distinct pids counted",
			ws: []tape.ContentionWitness{
				{Round: 1, Edge: "end", ProcsRead: true, LlamaProcs: []tape.LlamaProc{server, bench}},
				{Round: 2, Edge: "start", ProcsRead: true, LlamaProcs: []tape.LlamaProc{server, bench, other}},
				{Round: 2, Edge: "end", ProcsRead: true, LlamaProcs: []tape.LlamaProc{ppl}},
			},
			wantContended: true,
			wantReasons:   []string{"llama-bench pid 4242 running at round 1 end (+2 more)"},
		},
		{
			name: "existing reasons stay first, witness reasons follow in rule order",
			info: tape.ContentionInfo{Contended: true, LoadAvg1: 61.2, Reasons: []string{loadReason}},
			ws: []tape.ContentionWitness{
				{Edge: "start", IOSomeAvg10: f64(12.3), ProcsRead: true, LlamaProcs: []tape.LlamaProc{server, bench}},
				{Edge: "end", IOSomeAvg10: f64(3), ProcsRead: true, LlamaProcs: []tape.LlamaProc{server}},
			},
			wantContended: true,
			wantReasons: []string{
				loadReason,
				"io pressure 12.3 > 5 at start",
				"llama-bench pid 4242 running at start",
			},
		},
		{
			name:          "a contended verdict is never cleared by quiet witnesses",
			info:          tape.ContentionInfo{Contended: true, OtherGPUProcs: 1, Reasons: []string{"1 other GPU compute process"}},
			ws:            []tape.ContentionWitness{{Edge: "start", IOSomeAvg10: f64(0), ProcsRead: true}},
			wantContended: true,
			wantReasons:   []string{"1 other GPU compute process"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Witnessed(tc.info, tc.ws)
			if got.Contended != tc.wantContended {
				t.Errorf("Contended = %v, want %v (reasons %q)", got.Contended, tc.wantContended, got.Reasons)
			}
			if !reflect.DeepEqual(got.Reasons, tc.wantReasons) {
				t.Errorf("Reasons =\n %q\nwant %q", got.Reasons, tc.wantReasons)
			}
			if !reflect.DeepEqual(got.Witnesses, tc.ws) {
				t.Errorf("Witnesses = %+v, want %+v", got.Witnesses, tc.ws)
			}
			if got.LoadAvg1 != tc.info.LoadAvg1 || got.OtherGPUProcs != tc.info.OtherGPUProcs {
				t.Errorf("readings changed: %+v -> %+v", tc.info, got)
			}
		})
	}
}
