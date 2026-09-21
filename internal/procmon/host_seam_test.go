package procmon_test

import (
	"reflect"
	"testing"

	"github.com/midagedev/toktape/internal/procmon"
	"github.com/midagedev/toktape/internal/tape"
)

// TestHostInfoProcTreeOwnsTheAnswer is the platform-independent half of
// TTP-164's recurrence gate. HostInfo's contract is that a <fsRoot>/proc
// tree — Linux live, or a fixture copied from one — always answers through
// the /proc parsers, on every platform, even on a Mac that stands next to a
// working live reader. The day someone lets the platform reader fire while
// a proc tree is present, this is the test that goes red: the answer below
// is exactly the Linux one, and nothing a sysctl could say may leak into it.
func TestHostInfoProcTreeOwnsTheAnswer(t *testing.T) {
	h, err := procmon.HostInfo(fsRoot)
	if err != nil {
		t.Fatalf("HostInfo(fixture): %v", err)
	}
	want := tape.HostInfo{
		Hostname:   "rig-epyc",
		OS:         "linux",
		Kernel:     "6.8.0-45-generic",
		CPU:        "AMD EPYC 7313 16-Core Processor",
		CPUCores:   32,
		CPUThreads: 64,
		RAMBytes:   int64(792723456) * kB,
	}
	if !reflect.DeepEqual(h, want) {
		t.Errorf("HostInfo(fixture) =\n got %+v\nwant %+v", h, want)
	}
}
