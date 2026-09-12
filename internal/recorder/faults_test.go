package recorder_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/midagedev/toktape/internal/gpu"
	"github.com/midagedev/toktape/internal/recorder"
)

// baseMajFaults is the major-fault counter the /proc fixture starts at: field
// 12 of internal/procmon/testdata/proc/1234/stat.
const baseMajFaults = 12345

// livingProc is a writable copy of the /proc fixture whose major-fault
// counter can be advanced while a run is in flight.
//
// The counter is rewritten IN PLACE, at a fixed field width, rather than
// replaced with a temp file and a rename. procfs regenerates a stat file on
// every read at offset 0, and procmon.Sampler relies on that: it opens the
// file once and re-reads through the same handle. A rename would leave the
// sampler reading the original inode forever, so the fixture would be a
// simulation of a filesystem the code does not run against.
type livingProc struct {
	root     string
	statPath string

	mu     sync.Mutex
	prefix string
	fields []string
	maj    uint64
}

// majIndex is the position of majflt among the fields that follow the comm's
// closing parenthesis: after it the first token is field 3, so field 12 sits
// at index 9 (procmon.ParseStat uses the same arithmetic).
const majIndex = 9

func newLivingProc(t *testing.T) *livingProc {
	t.Helper()
	root := t.TempDir()
	copyTree(t, absRoot(t), root)

	statPath := filepath.Join(root, "proc", "1234", "stat")
	raw, err := os.ReadFile(statPath)
	if err != nil {
		t.Fatalf("read fixture stat: %v", err)
	}
	line := strings.TrimRight(string(raw), "\n")
	end := strings.LastIndex(line, ")")
	if end < 0 {
		t.Fatal("fixture stat has no comm field")
	}
	p := &livingProc{
		root:     root,
		statPath: statPath,
		fields:   strings.Fields(line[end+1:]),
		maj:      baseMajFaults,
	}
	if p.fields[majIndex] != fmt.Sprint(baseMajFaults) {
		t.Fatalf("fixture majflt = %q, want %d; the stat layout moved", p.fields[majIndex], baseMajFaults)
	}
	p.prefix = line[:end+1]
	p.write(t)
	return p
}

// write rewrites the stat line with the current counter. The counter is
// zero-padded to a constant width so every rewrite is the same number of
// bytes and a concurrent reader sees one whole line or the other, never a
// truncated one.
func (p *livingProc) write(t *testing.T) {
	p.fields[majIndex] = fmt.Sprintf("%08d", p.maj)
	line := p.prefix + " " + strings.Join(p.fields, " ") + "\n"
	f, err := os.OpenFile(p.statPath, os.O_WRONLY|os.O_CREATE, 0o644)
	if err != nil {
		t.Fatalf("open stat for write: %v", err)
	}
	defer f.Close()
	if _, err := f.WriteAt([]byte(line), 0); err != nil {
		t.Fatalf("write stat: %v", err)
	}
}

// bump advances the counter by one, as one page fault would.
func (p *livingProc) bump(t *testing.T) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.maj++
	p.write(t)
}

func (p *livingProc) current() uint64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.maj
}

func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		// The fixture carries symlinks that mirror the real /proc (cwd, exe)
		// and point nowhere. Nothing this test exercises reads them, and
		// copying a dangling link is not something os.ReadFile can do.
		if !d.Type().IsRegular() {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o644)
	})
	if err != nil {
		t.Fatalf("copy fixture tree: %v", err)
	}
}

// TestMajorFaultsAreConserved is the arithmetic behind the sparkline, which
// is the most watched number in the clip and the one figure that decides the
// cold label.
//
// The server advances the process's major-fault counter by exactly one before
// each token it emits, so the run's per-token deltas must sum to the movement
// of the raw counter. They do not if anything other than the token hooks
// latches procmon.Sampler: the periodic host sampler ticking between two
// tokens would consume the faults of that gap and hand them to nobody, and
// the card would under-report page faults by however often the sampler
// happened to fire. That is the regression this test exists to catch.
func TestMajorFaultsAreConserved(t *testing.T) {
	proc := newLivingProc(t)

	sse, err := os.ReadFile(sseFile)
	if err != nil {
		t.Fatalf("read SSE fixture: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/props", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Server", "llama.cpp")
		_, _ = w.Write([]byte(propsJSON))
	})
	mux.HandleFunc("/apply-template", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"prompt": "<|user|>hi<|assistant|>"})
	})
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		for _, ev := range bytes.SplitAfter(sse, []byte("\n\n")) {
			if len(bytes.TrimSpace(ev)) == 0 {
				continue
			}
			// One fault per token that carries text, charged before the
			// token is written so the client's read of the counter, which
			// happens after the token arrives, always sees it.
			if bytes.Contains(ev, []byte(`"content":"`)) && !bytes.Contains(ev, []byte(`"content":""`)) {
				proc.bump(t)
			}
			if _, err := w.Write(ev); err != nil {
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
			time.Sleep(2 * time.Millisecond)
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	// One stream: the counter is a single process-wide number, so several
	// streams racing on it would make the expected total a scheduling
	// artefact rather than an invariant.
	tp, err := recorder.Record(context.Background(), recorder.Options{
		BaseURL: srv.URL,
		// Fast enough to tick many times inside the run, which is what makes
		// the sampler steal latches if it shares them.
		SampleInterval: 5 * time.Millisecond,
		FSRoot:         proc.root,
		GPU:            gpu.Null{},
		Clock:          fixedClock{time.Date(2026, 9, 13, 7, 15, 0, 0, time.UTC)},
	})
	if err != nil {
		t.Fatalf("Record: %v", err)
	}

	if tp.Summary.Server.PID != fixturePID {
		t.Fatalf("PID = %d, the writable fixture was not used", tp.Summary.Server.PID)
	}

	charged := proc.current() - baseMajFaults
	if charged == 0 {
		t.Fatal("the fixture counter never moved; the test proves nothing")
	}

	var summed uint64
	for _, rec := range tp.Requests {
		for _, tok := range rec.Tokens {
			summed += tok.MajFaultsDelta
		}
	}
	if summed != charged {
		t.Errorf("per-token major faults sum to %d, but the process took %d: %d fault(s) were lost, most likely to a periodic sampler sharing the token hooks' latch",
			summed, charged, charged-summed)
	}
	if got := tp.Summary.Memory.MajFaultsTotal; got != charged {
		t.Errorf("MemorySummary.MajFaultsTotal = %d, want %d", got, charged)
	}
	if len(tp.Samples) < 2 {
		t.Fatalf("only %d host samples were taken; the sampler barely ran, so it had no chance to steal a latch", len(tp.Samples))
	}
	// The samples must still carry the live counter, so a replay can plot it.
	if last := tp.Samples[len(tp.Samples)-1].Mem.MajFaults; last != proc.current() {
		t.Errorf("last sample reports majflt %d, want the live %d", last, proc.current())
	}
}
