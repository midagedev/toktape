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
}
