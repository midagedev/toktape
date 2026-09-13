package recorder_test

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/midagedev/toktape/internal/gpu"
	"github.com/midagedev/toktape/internal/recorder"
	"github.com/midagedev/toktape/internal/server"
	"github.com/midagedev/toktape/internal/tape"
)

// busyRoot is a /proc tree of a box that is loading something from disk while
// the run is measured (TTP-36): /proc/pressure/io some avg10 is 12.30, a
// llama-bench (pid 4242) runs next to the attached llama-server (pid 1234),
// and the load average is a quiet 0.42 — the reading the old gate trusted.
const busyRoot = "../procmon/testdata/busy"

// busyTree copies the busy fixture into a temporary directory, minus the
// files named in drop (paths relative to the tree root, e.g. "proc/loadavg"),
// so a test can take one reading away without a second fixture on disk.
func busyTree(t *testing.T, drop ...string) string {
	t.Helper()
	src, err := filepath.Abs(busyRoot)
	if err != nil {
		t.Fatalf("abs %s: %v", busyRoot, err)
	}
	dst := t.TempDir()
	skip := map[string]bool{}
	for _, d := range drop {
		skip[filepath.FromSlash(d)] = true
	}
	err = filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if skip[rel] {
			return nil
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
	if err != nil {
		t.Fatalf("copy %s: %v", src, err)
	}
	return dst
}

// TestWitnessesRoundsBusyBox (TTP-36): every round carries a start and an end
// reading, both on the clock RequestRecord.StartedAt uses, and a box under IO
// pressure with a foreign llama-bench is contended although its load average
// is low. The reasons name where the worst reading was taken.
func TestWitnessesRoundsBusyBox(t *testing.T) {
	srv := roundsServer(t)
	tp, err := recorder.Record(context.Background(), recorder.Options{
		BaseURL:     srv.URL,
		Concurrency: 2,
		MaxTokens:   64,
		Rounds: []recorder.Round{
			{Name: "a", Prompts: []server.StreamRequest{userPrompt("alpha", 32)}},
			{Name: "b", Prompts: []server.StreamRequest{userPrompt("beta", 32)}},
		},
		SampleInterval: 5 * time.Millisecond,
		FSRoot:         busyTree(t),
		GPU:            gpu.Null{},
		Clock:          fixedClock{time.Date(2026, 9, 13, 7, 15, 0, 0, time.UTC)},
	})
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	// The witnesses must survive the run file (--json and every renderer read
	// the tape that was written): round-trip before asserting.
	path := filepath.Join(t.TempDir(), "run.tape")
	if err := tape.Write(path, tp); err != nil {
		t.Fatalf("tape.Write: %v", err)
	}
	if tp, err = tape.Read(path); err != nil {
		t.Fatalf("tape.Read: %v", err)
	}
	c := tp.Summary.Contention
	if tp.Summary.Server.PID != fixturePID {
		t.Fatalf("PID = %d, want %d (warnings %q)", tp.Summary.Server.PID, fixturePID, tp.Summary.Warnings)
	}
	if !c.Contended {
		t.Errorf("Contended = false, want true (contention %+v)", c)
	}
	wantReasons := []string{
		"io pressure 12.3 > 5 at round 1 start",
		"llama-bench pid 4242 running at round 1 start",
	}
	if !reflect.DeepEqual(c.Reasons, wantReasons) {
		t.Errorf("Reasons =\n %q\nwant %q", c.Reasons, wantReasons)
	}

	type key struct {
		round int
		edge  string
	}
	var got []key
	for _, w := range c.Witnesses {
		got = append(got, key{w.Round, w.Edge})
	}
	want := []key{{1, "start"}, {1, "end"}, {2, "start"}, {2, "end"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("witnesses (round, edge) = %v, want %v: two per round, rounds 1-based", got, want)
	}

	for i, w := range c.Witnesses {
		if w.IOSomeAvg10 == nil || *w.IOSomeAvg10 != 12.3 {
			t.Errorf("witness %d IOSomeAvg10 = %v, want 12.3", i, w.IOSomeAvg10)
		}
		if w.LoadAvg1 != 0.42 {
			t.Errorf("witness %d LoadAvg1 = %v, want 0.42", i, w.LoadAvg1)
		}
		if want := int64(456789012) * 1024; w.PageCacheBytes != want {
			t.Errorf("witness %d PageCacheBytes = %d, want %d", i, w.PageCacheBytes, want)
		}
		if !w.ProcsRead || len(w.LlamaProcs) != 2 {
			t.Fatalf("witness %d procs = %v %+v, want the server and the bench", i, w.ProcsRead, w.LlamaProcs)
		}
		srv, bench := w.LlamaProcs[0], w.LlamaProcs[1]
		if srv.PID != 1234 || srv.Comm != "llama-server" || !srv.Attached {
			t.Errorf("witness %d proc 0 = %+v, want the attached llama-server 1234", i, srv)
		}
		if bench.PID != 4242 || bench.Comm != "llama-bench" || bench.Attached {
			t.Errorf("witness %d proc 1 = %+v, want the foreign llama-bench 4242", i, bench)
		}
		if i > 0 && w.T < c.Witnesses[i-1].T {
			t.Errorf("witness %d T = %v before witness %d T = %v", i, w.T, i-1, c.Witnesses[i-1].T)
		}
	}

	// Same origin as StartedAt: a round's start reading is taken before any
	// of its streams started, its end reading after all of them did.
	for _, rec := range tp.Requests {
		start, end := c.Witnesses[2*rec.Round], c.Witnesses[2*rec.Round+1]
		if start.T > rec.StartedAt {
			t.Errorf("round %d start witness T = %v after stream %d StartedAt %v", rec.Round, start.T, rec.Index, rec.StartedAt)
		}
		if end.T < rec.StartedAt {
			t.Errorf("round %d end witness T = %v before stream %d StartedAt %v", rec.Round, end.T, rec.Index, rec.StartedAt)
		}
	}
}

// TestWitnessesJudgeWithoutGPUBackendOrLoad: a single-round run on a box whose
// load average cannot be read and with no GPU backend used to be "not judged".
// The witnesses are readings, so the run is judged, and the reasons say
// "at start" rather than naming a round.
func TestWitnessesJudgeWithoutGPUBackendOrLoad(t *testing.T) {
	srv := fakeServer(t)
	tp, err := recorder.Record(context.Background(), recorder.Options{
		BaseURL:        srv.URL,
		Concurrency:    2,
		MaxTokens:      64,
		SampleInterval: 20 * time.Millisecond,
		FSRoot:         busyTree(t, "proc/loadavg"),
		GPU:            gpu.Null{},
	})
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	c := tp.Summary.Contention
	if !c.Contended {
		t.Errorf("Contended = false, want true (contention %+v, warnings %q)", c, tp.Summary.Warnings)
	}
	wantReasons := []string{
		"io pressure 12.3 > 5 at start",
		"llama-bench pid 4242 running at start",
	}
	if !reflect.DeepEqual(c.Reasons, wantReasons) {
		t.Errorf("Reasons =\n %q\nwant %q", c.Reasons, wantReasons)
	}
	if len(c.Witnesses) != 2 || c.Witnesses[0].Edge != "start" || c.Witnesses[1].Edge != "end" ||
		c.Witnesses[0].Round != 0 || c.Witnesses[1].Round != 0 {
		t.Errorf("witnesses = %+v, want round 0 start and end", c.Witnesses)
	}
	if warnsAbout(tp.Summary.Warnings, "contention not judged") {
		t.Errorf("a run with witnesses was not judged: %q", tp.Summary.Warnings)
	}
}

// TestNoWitnessesWithoutPID: when the server is not a process on this host,
// this host's pressure says nothing about it. No witness is taken and the
// verdict is exactly what the load average and GPU readings give — even though
// the tree's IO pressure is high.
func TestNoWitnessesWithoutPID(t *testing.T) {
	srv := fakeServer(t)
	tp, err := recorder.Record(context.Background(), recorder.Options{
		BaseURL:        srv.URL,
		Concurrency:    1,
		MaxTokens:      64,
		SampleInterval: 20 * time.Millisecond,
		FSRoot:         busyTree(t, "proc/1234/cmdline"),
		GPU:            gpu.Null{},
	})
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	s := tp.Summary
	if s.Server.PID != 0 {
		t.Fatalf("PID = %d, want 0 with the server's cmdline gone", s.Server.PID)
	}
	c := s.Contention
	if len(c.Witnesses) != 0 {
		t.Errorf("Witnesses = %+v, want none without a local pid", c.Witnesses)
	}
	if c.Contended || len(c.Reasons) != 0 || c.LoadAvg1 != 0.42 {
		t.Errorf("Contention = %+v, want today's verdict: quiet, load 0.42, no reasons", c)
	}
	for _, r := range c.Reasons {
		if strings.Contains(r, "io pressure") || strings.Contains(r, "llama-") {
			t.Errorf("a witness reason without witnesses: %q", r)
		}
	}
	// Round-trip: a tape without witnesses carries no witnesses key.
	path := filepath.Join(t.TempDir(), "run.tape")
	if err := tape.Write(path, tp); err != nil {
		t.Fatalf("tape.Write: %v", err)
	}
	back, err := tape.Read(path)
	if err != nil {
		t.Fatalf("tape.Read: %v", err)
	}
	if back.Summary.Contention.Witnesses != nil {
		t.Errorf("read-back Witnesses = %+v, want nil", back.Summary.Contention.Witnesses)
	}
}
