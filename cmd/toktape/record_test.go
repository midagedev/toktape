package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/midagedev/toktape/internal/card"
	"github.com/midagedev/toktape/internal/gpu"
	"github.com/midagedev/toktape/internal/ledger"
	"github.com/midagedev/toktape/internal/recorder"
	"github.com/midagedev/toktape/internal/tape"
)

// hermetic pins the collectors for one test so the record verb reads no real
// /proc and execs no nvidia-smi. Without it the CLI tests would scan this
// machine's process table and their warnings would differ per host.
func hermetic(t *testing.T) {
	t.Helper()
	root := t.TempDir()
	prev := pinCollectors
	pinCollectors = func(o recorder.Options) recorder.Options {
		o.FSRoot = root
		o.GPU = gpu.Null{}
		return o
	}
	t.Cleanup(func() { pinCollectors = prev })
}

// The recorded stream belongs to internal/server and is replayed here so the
// record verb is exercised against the same bytes that package is verified
// against.
const cliSSE = "../../internal/server/testdata/stream_basic.sse"

const cliProps = `{
  "model_path": "/models/gguf/Qwen3.5-35B-A3B-UD-Q4_K_M.gguf",
  "build_info": "b4321-abcdef12",
  "chat_template": "chatml",
  "total_slots": 4,
  "default_generation_settings": {"n_ctx": 32768}
}`

// cliServer answers the routes a run touches. The model file it names does
// not exist here and the process is not local, so this is the degraded path:
// no GGUF header, no /proc view, and the run must still produce a tape.
func cliServer(t *testing.T) *httptest.Server {
	t.Helper()
	sse, err := os.ReadFile(cliSSE)
	if err != nil {
		t.Fatalf("read SSE fixture: %v", err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/props", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Server", "llama.cpp")
		_, _ = w.Write([]byte(cliProps))
	})
	mux.HandleFunc("/slots", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[{"id":0,"is_processing":true,"n_ctx":2048,"n_past":12}]`))
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
			if _, err := w.Write(ev); err != nil {
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// TestRecordVerbEndToEnd runs the product's default path: attach, record,
// save the tape and the card, print the card, print the share hint.
func TestRecordVerbEndToEnd(t *testing.T) {
	hermetic(t)
	srv := cliServer(t)
	dir := t.TempDir()

	code, stdout, stderr := exec(t, "--url", srv.URL, "--out", dir, "-n", "2", "--n-predict", "64")
	if code != exitOK {
		t.Fatalf("exit %d\nstderr:\n%s", code, stderr)
	}

	// The card went to stdout, exactly CardWidth columns wide.
	if !strings.Contains(stdout, "toktape") {
		t.Fatalf("no card on stdout:\n%s", stdout)
	}
	for i, line := range strings.Split(strings.TrimRight(stdout, "\n"), "\n") {
		if w := card.Width(line); w != card.CardWidth {
			t.Fatalf("card line %d is %d columns, want %d: %q", i+1, w, card.CardWidth, line)
		}
	}

	// The run notes went to stderr, so a piped card stays a card.
	if !strings.Contains(stderr, "→ llama-server at "+srv.URL) {
		t.Errorf("no attach line on stderr:\n%s", stderr)
	}
	// The run ends with the share block: what exists, and what to do with it.
	for _, want := range []string{"✓ Tape   ", "✓ Card   ", ".card.png", "→ Post it:  toktape card ", "--md --copy"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr is missing %q:\n%s", want, stderr)
		}
	}
	// The first run of a model has nothing to compare against, and a command
	// that cannot be run is worse than a missing line.
	if strings.Contains(stderr, "→ Compare:") {
		t.Errorf("the first run of a model offered a comparison:\n%s", stderr)
	}

	// Both artefacts exist and the tape reloads into the same summary.
	tapes, err := filepath.Glob(filepath.Join(dir, "*"+tape.Ext))
	if err != nil || len(tapes) != 1 {
		t.Fatalf("run files = %v (err %v), want one tape", tapes, err)
	}
	cards, err := filepath.Glob(filepath.Join(dir, "*.card.txt"))
	if err != nil || len(cards) != 1 {
		t.Fatalf("card files = %v (err %v), want one card", cards, err)
	}
	// The image card is written on every run: it is the thing that gets
	// posted, and one that has to be asked for does not exist when the user
	// closes the terminal.
	images, err := filepath.Glob(filepath.Join(dir, "*.card.png"))
	if err != nil || len(images) != 1 {
		t.Fatalf("image cards = %v (err %v), want one", images, err)
	}
	checkShareImage(t, images[0])
	// The run also lands in the experiment ledger, so a sweep is one table
	// without the user having to ask for it.
	rows, err := ledger.Read(dir)
	if err != nil || len(rows) != 1 {
		t.Fatalf("the ledger holds %d rows (err %v), want the run just recorded", len(rows), err)
	}
	if rows[0].Get("id") != strings.TrimSuffix(filepath.Base(tapes[0]), tape.Ext) {
		t.Errorf("the ledger row is %q, not the tape that was written", rows[0].Get("id"))
	}
	tp, err := tape.Read(tapes[0])
	if err != nil {
		t.Fatalf("the saved tape does not reload: %v", err)
	}
	if tp.Summary.Concurrency != 2 || len(tp.Requests) != 2 {
		t.Errorf("-n 2 recorded %d streams", len(tp.Requests))
	}
	if tp.Summary.ToktapeVersion != version {
		t.Errorf("ToktapeVersion = %q, want %q", tp.Summary.ToktapeVersion, version)
	}
	for i, rec := range tp.Requests {
		if len(rec.Tokens) == 0 {
			t.Errorf("stream %d recorded no tokens", i)
		}
	}
	saved, err := os.ReadFile(cards[0])
	if err != nil {
		t.Fatal(err)
	}
	if string(saved) != stdout {
		t.Error("the saved card and the printed card differ")
	}

	// The tape just written is what the other verbs consume.
	if code, _, _ := exec(t, "ls", "--out", dir); code != exitOK {
		t.Errorf("ls on the fresh run directory: exit %d", code)
	}
	if code, out, _ := exec(t, "card", tapes[0], "--md"); code != exitOK || !strings.Contains(out, "```") {
		t.Errorf("card --md on the fresh tape: exit %d", code)
	}
}

// TestRecordVerbQuietAndJSON: --quiet silences stderr and --json replaces the
// card with the summary the compare tooling reads.
func TestRecordVerbQuietAndJSON(t *testing.T) {
	hermetic(t)
	srv := cliServer(t)
	dir := t.TempDir()

	code, stdout, stderr := exec(t, "record", "--url", srv.URL, "--out", dir, "--json", "--quiet")
	if code != exitOK {
		t.Fatalf("exit %d\n%s", code, stderr)
	}
	if stderr != "" {
		t.Errorf("--quiet still wrote to stderr:\n%s", stderr)
	}
	var s tape.RunSummary
	if err := json.Unmarshal([]byte(stdout), &s); err != nil {
		t.Fatalf("--json is not valid JSON: %v", err)
	}
	if s.Concurrency != 1 {
		t.Errorf("default concurrency = %d, want 1", s.Concurrency)
	}
}

// TestRecordVerbNoCard: --no-card leaves the tape and writes no card.
func TestRecordVerbNoCard(t *testing.T) {
	hermetic(t)
	srv := cliServer(t)
	dir := t.TempDir()

	code, stdout, stderr := exec(t, "--url", srv.URL, "--out", dir, "--no-card")
	if code != exitOK {
		t.Fatalf("exit %d\n%s", code, stderr)
	}
	if stdout != "" {
		t.Errorf("--no-card printed a card:\n%s", stdout)
	}
	if cards, _ := filepath.Glob(filepath.Join(dir, "*.card.txt")); len(cards) != 0 {
		t.Errorf("--no-card saved %v", cards)
	}
	if images, _ := filepath.Glob(filepath.Join(dir, "*.card.png")); len(images) != 0 {
		t.Errorf("--no-card saved %v", images)
	}
	if tapes, _ := filepath.Glob(filepath.Join(dir, "*"+tape.Ext)); len(tapes) != 1 {
		t.Errorf("--no-card did not save the run file: %v", tapes)
	}
	if strings.Contains(stderr, "Card saved") {
		t.Error("--no-card still claims a card was saved")
	}
}

// TestRecordVerbUnreachable: exit code 2 is the contract for a server that
// does not answer.
func TestRecordVerbUnreachable(t *testing.T) {
	hermetic(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	code, _, stderr := exec(t, "--url", srv.URL, "--out", t.TempDir())
	if code != exitUnreachable {
		t.Errorf("exit %d, want %d\n%s", code, exitUnreachable, stderr)
	}
}

// TestRecordVerbAllStreamsFailed: exit code 3 is the contract for a run in
// which nothing generated.
func TestRecordVerbAllStreamsFailed(t *testing.T) {
	hermetic(t)
	bad, err := os.ReadFile("../../internal/server/testdata/stream_error.sse")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/props", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(cliProps))
	})
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write(bad)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	code, _, stderr := exec(t, "--url", srv.URL, "--out", t.TempDir(), "--quiet")
	if code != exitStreams {
		t.Errorf("exit %d, want %d\n%s", code, exitStreams, stderr)
	}
}

// TestRecordVerbCancelled: a run interrupted by its context does not hang.
func TestRecordVerbCancelled(t *testing.T) {
	hermetic(t)
	srv := cliServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var out, errOut bytes.Buffer
	done := make(chan int, 1)
	go func() {
		done <- Run(ctx, &out, &errOut, []string{"--url", srv.URL, "--out", t.TempDir(), "--quiet"})
	}()
	select {
	case code := <-done:
		if code == exitOK {
			t.Error("a cancelled run reported success")
		}
	case <-time.After(15 * time.Second):
		t.Fatal("a cancelled run did not return")
	}
}
