package procmon_test

import (
	"errors"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/midagedev/toktape/internal/procmon"
	"github.com/midagedev/toktape/internal/tape"
)

func TestParseBootTime(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(busyRoot, "proc", "stat"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := procmon.ParseBootTime(data)
	if err != nil {
		t.Fatalf("ParseBootTime: %v", err)
	}
	if want := time.Unix(1757750400, 0); !got.Equal(want) {
		t.Errorf("ParseBootTime = %v, want %v", got, want)
	}
	if _, err := procmon.ParseBootTime([]byte("cpu  1 2 3 4\nctxt 5\n")); !errors.Is(err, procmon.ErrNotFound) {
		t.Errorf("no btime line: err = %v, want ErrNotFound", err)
	}
	if _, err := procmon.ParseBootTime([]byte("btime soon\n")); err == nil {
		t.Error("unparseable btime: want an error")
	}
	if _, err := procmon.ReadBootTime(t.TempDir()); !errors.Is(err, procmon.ErrNotFound) {
		t.Errorf("no /proc/stat: err = %v, want ErrNotFound", err)
	}
}

func TestParseStartTime(t *testing.T) {
	// The comm "(llama-server (cuda))" holds a space and parentheses: splitting
	// the line on whitespace would shift field 22 by one.
	data, err := os.ReadFile(filepath.Join("testdata", "proc", "1234", "stat"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := procmon.ParseStartTime(data)
	if err != nil {
		t.Fatalf("ParseStartTime: %v", err)
	}
	if got != 8675309 {
		t.Errorf("ParseStartTime = %d, want 8675309", got)
	}
	for _, bad := range []string{
		"",
		"4242 llama-bench R 1 2 3",
		"4242 (llama-bench) R 2211 4242 2211 0 -1 4194560",
		"4242 (llama-bench) R 2211 4242 2211 0 -1 4194560 1 0 1 0 1 1 0 0 20 0 65 0 soon 1",
	} {
		if _, err := procmon.ParseStartTime([]byte(bad)); err == nil {
			t.Errorf("ParseStartTime(%q): want an error", bad)
		}
	}
}

func TestIsLlamaComm(t *testing.T) {
	tests := []struct {
		comm string
		want bool
	}{
		{"llama-server", true},
		{"llama-bench", true},
		{"llama-perplexi", true}, // llama-perplexity, truncated to 15 bytes
		{"llama-server-cu", true},
		{"llama-cli\n", true},
		{"bash", false},
		{"ollama", false},
		{"python3", false},
		{"llamafile", false},
		{"", false},
	}
	for _, tc := range tests {
		if got := procmon.IsLlamaComm(tc.comm); got != tc.want {
			t.Errorf("IsLlamaComm(%q) = %v, want %v", tc.comm, got, tc.want)
		}
	}
}

func TestLlamaProcsAt(t *testing.T) {
	boot, err := procmon.ReadBootTime(busyRoot)
	if err != nil {
		t.Fatalf("ReadBootTime: %v", err)
	}
	// 87120 s after boot. The server started 8675309 ticks (86753.09 s) after
	// boot and the bench 8700000 ticks (87000.00 s) after boot.
	now := boot.Add(87120 * time.Second)

	got, err := procmon.LlamaProcsAt(busyRoot, 1234, boot, procmon.ClockTicks, now)
	if err != nil {
		t.Fatalf("LlamaProcsAt: %v", err)
	}
	want := []tape.LlamaProc{
		{PID: 1234, Comm: "llama-server", AgeSec: 366.91, Attached: true},
		{PID: 4242, Comm: "llama-bench", AgeSec: 120},
	}
	if len(got) != len(want) {
		t.Fatalf("LlamaProcsAt = %+v, want %+v (pid 999 is bash)", got, want)
	}
	for i := range want {
		g, w := got[i], want[i]
		if g.PID != w.PID || g.Comm != w.Comm || g.Attached != w.Attached || math.Abs(g.AgeSec-w.AgeSec) > 1e-6 {
			t.Errorf("proc %d = %+v, want %+v", i, g, w)
		}
	}

	t.Run("attached is the measured server, not the first match", func(t *testing.T) {
		got, err := procmon.LlamaProcsAt(busyRoot, 4242, boot, procmon.ClockTicks, now)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 2 || got[0].Attached || !got[1].Attached {
			t.Errorf("LlamaProcsAt(self 4242) = %+v", got)
		}
	})
	t.Run("no boot time: ages unknown, processes still listed", func(t *testing.T) {
		got, err := procmon.LlamaProcsAt(busyRoot, 1234, time.Time{}, procmon.ClockTicks, now)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 2 || got[0].AgeSec != 0 || got[1].AgeSec != 0 {
			t.Errorf("LlamaProcsAt(no boot) = %+v, want two entries with AgeSec 0", got)
		}
		got, err = procmon.LlamaProcsAt(busyRoot, 1234, boot, 0, now)
		if err != nil || len(got) != 2 || got[0].AgeSec != 0 {
			t.Errorf("LlamaProcsAt(clkTck 0) = %+v, %v; want ages 0", got, err)
		}
	})
	t.Run("a clock behind the start time never gives a negative age", func(t *testing.T) {
		got, err := procmon.LlamaProcsAt(busyRoot, 1234, boot, procmon.ClockTicks, boot)
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range got {
			if p.AgeSec != 0 {
				t.Errorf("proc %d AgeSec = %v, want 0", p.PID, p.AgeSec)
			}
		}
	})
	t.Run("a tree without comm files lists nothing", func(t *testing.T) {
		// testdata/proc predates comm files; stat's comm is not a fallback,
		// so the end-to-end recorder tests over that tree see no foreign
		// llama process.
		got, err := procmon.LlamaProcsAt("testdata", 1234, boot, procmon.ClockTicks, now)
		if err != nil || len(got) != 0 {
			t.Errorf("LlamaProcsAt(testdata) = %+v, %v; want none, nil", got, err)
		}
	})
	t.Run("no /proc at all is an error, not an empty reading", func(t *testing.T) {
		if _, err := procmon.LlamaProcs(t.TempDir(), 1234, boot, procmon.ClockTicks); err == nil {
			t.Error("LlamaProcs over an empty tree: want an error")
		}
	})
	// TTP-107, 2026-09-17: the server the run measured is listed whatever its
	// comm says. Pid 999 in this fixture is bash, standing in for an engine
	// whose comm is not a llama.cpp name ("mistralrs"): before this it was
	// filtered out, and the witness of a mistral.rs take then said nothing
	// about the process its own figures came from, while the ik take from the
	// same sweep carried {comm: "llama-server", attached: true}.
	//
	// FAIL-first on the unedited source: LlamaProcsAt(busyRoot, 999, ...)
	// returned the two llama processes only, neither of them Attached.
	t.Run("the attached server is listed whatever its comm is", func(t *testing.T) {
		got, err := procmon.LlamaProcsAt(busyRoot, 999, boot, procmon.ClockTicks, now)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 3 {
			t.Fatalf("LlamaProcsAt(self 999) = %+v, want the two llama processes and pid 999", got)
		}
		var self tape.LlamaProc
		for _, p := range got {
			if p.PID == 999 {
				self = p
			}
		}
		if self.PID != 999 || !self.Attached || self.Comm != "bash" {
			t.Errorf("pid 999 = %+v, want it listed, Attached, under its own comm", self)
		}
		// It must not become contention: gpu.Witnessed counts only entries
		// that are not Attached, so the two llama processes are still the
		// only foreign ones and a non-llama server does not flag itself.
		foreign := 0
		for _, p := range got {
			if !p.Attached {
				foreign++
			}
		}
		if foreign != 2 {
			t.Errorf("foreign processes = %d, want 2 (the attached one is never foreign)", foreign)
		}
	})
}

// TestExists: the check the recorder asks about a pid that arrived from
// outside — declared by the server in /props rather than found by scanning
// this tree (TTP-107, 2026-09-17). A declared pid that is not here must be
// reported and searched for, not trusted, so the question has to be cheap and
// have no false positives.
func TestExists(t *testing.T) {
	for _, c := range []struct {
		name string
		root string
		pid  int
		want bool
	}{
		{"a process in the tree", busyRoot, 1234, true},
		{"a pid nothing holds", busyRoot, 31337, false},
		{"zero is not a pid", busyRoot, 0, false},
		{"a negative pid is not a pid", busyRoot, -1, false},
		{"no /proc at all", t.TempDir(), 1234, false},
	} {
		if got := procmon.Exists(c.root, c.pid); got != c.want {
			t.Errorf("%s: Exists(%q, %d) = %v, want %v", c.name, c.root, c.pid, got, c.want)
		}
	}
}

// TestHasProc: the question to ask before blaming a pid for being absent.
// Off Linux, and on any tree without a procfs, no pid is there — and that is
// a fact about the tree, not about the pid a server declared (TTP-107,
// 2026-09-17). Without this the recorder put "server declared pid N, which is
// not running here" on every macOS and Windows run and on every --url attach
// to another host, which is exactly the reader the platform table sends here.
func TestHasProc(t *testing.T) {
	if !procmon.HasProc(busyRoot) {
		t.Errorf("HasProc(%q) = false, want true", busyRoot)
	}
	if empty := t.TempDir(); procmon.HasProc(empty) {
		t.Errorf("HasProc(%q) = true, want false", empty)
	}
}
