package gpu

import (
	"reflect"
	"testing"
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
