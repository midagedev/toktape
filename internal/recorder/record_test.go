package recorder_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
	"github.com/midagedev/toktape/internal/server"
	"github.com/midagedev/toktape/internal/tape"
)

// The fixture tree and the recorded stream belong to internal/procmon and
// internal/server. They are read here rather than copied so the end-to-end
// test exercises the same bytes those packages are verified against; a copy
// would drift the day one of them re-records its fixture.
const (
	procRoot  = "../procmon/testdata"
	sseFile   = "../server/testdata/stream_basic.sse"
	modelPath = "/models/gguf/DeepSeek-V3-0324-UD-Q4_K_XL-00001-of-00009.gguf"
	// fixturePID is the llama-server in the /proc fixture that serves
	// modelPath by its exact path. 777 names the same file in another
	// directory and 999 is a shell, so procmon.FindPID must pick this one.
	fixturePID = 1234
)

// propsJSON names the model the /proc fixture's pid 1234 was started with, so
// the whole PID -> argv -> flags -> memory chain is exercised. The file does
// not exist on this host, which is also the degraded GGUF path.
const propsJSON = `{
  "model_path": "` + modelPath + `",
  "build_info": "b4321-abcdef12",
  "chat_template": "chatml",
  "total_slots": 4,
  "default_generation_settings": {"n_ctx": 32768}
}`

// fakeServer answers the four routes a run touches and replays the recorded
// SSE stream for every chat request.
func fakeServer(t *testing.T) *httptest.Server {
	t.Helper()
	sse, err := os.ReadFile(sseFile)
	if err != nil {
		t.Fatalf("read SSE fixture: %v", err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/props", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Server", "llama.cpp")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(propsJSON))
	})
	mux.HandleFunc("/slots", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[
		  {"id":0,"is_processing":true,"n_ctx":2048,"n_past":312},
		  {"id":1,"is_processing":true,"n_ctx":2048,"n_past":40},
		  {"id":2,"is_processing":false,"n_ctx":2048,"n_past":0}
		]`))
	})
	mux.HandleFunc("/apply-template", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Messages []tape.Message `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var b strings.Builder
		for _, m := range in.Messages {
			b.WriteString("<|" + m.Role + "|>" + m.Content)
		}
		b.WriteString("<|assistant|></think>")
		_ = json.NewEncoder(w).Encode(map[string]string{"prompt": b.String()})
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
			time.Sleep(time.Millisecond)
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// fakeGPU serves the two nvidia-smi queries from fixture CSVs.
func fakeGPU(t *testing.T) gpu.Collector {
	t.Helper()
	read := func(name string) string {
		b, err := os.ReadFile(filepath.Join("testdata", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		return string(b)
	}
	runner := func(ctx context.Context, args ...string) (string, error) {
		for _, a := range args {
			switch {
			case strings.HasPrefix(a, "--query-compute-apps="):
				return read("nvidia-compute-apps.csv"), nil
			case strings.HasPrefix(a, "--query-gpu="):
				return read("nvidia-query-gpu.csv"), nil
			}
		}
		return "", errors.New("unexpected nvidia-smi query")
	}
	c, warns := gpu.OpenWith(context.Background(), runner)
	if c.Name() != "nvidia-smi" {
		t.Fatalf("fake GPU backend is %q, warnings %v", c.Name(), warns)
	}
	t.Cleanup(c.Close)
	return c
}

// fixedClock pins the wall-clock stamps so the run ID is deterministic.
type fixedClock struct{ t time.Time }

func (c fixedClock) Now() time.Time { return c.t }

func absRoot(t *testing.T) string {
	t.Helper()
	abs, err := filepath.Abs(procRoot)
	if err != nil {
		t.Fatalf("abs %s: %v", procRoot, err)
	}
	return abs
}

// TestRecordEndToEnd drives a whole run against a fake server, a fake /proc
// tree and a fake nvidia-smi, and checks the wiring the card depends on.
func TestRecordEndToEnd(t *testing.T) {
	srv := fakeServer(t)
	const streams = 3

	var (
		mu     sync.Mutex
		kinds  = map[recorder.EventKind]int{}
		tokens = map[int]int{}
	)
	opts := recorder.Options{
		BaseURL:        srv.URL,
		Concurrency:    streams,
		MaxTokens:      256,
		SampleInterval: 20 * time.Millisecond,
		FSRoot:         absRoot(t),
		GPU:            fakeGPU(t),
		Clock:          fixedClock{time.Date(2026, 9, 13, 7, 15, 0, 0, time.UTC)},
		Version:        "0.1.0-test",
		Progress: func(ev recorder.Event) {
			mu.Lock()
			kinds[ev.Kind]++
			if ev.Kind == recorder.EventToken {
				tokens[ev.Stream]++
			}
			mu.Unlock()
		},
	}

	tp, err := recorder.Record(context.Background(), opts)
	if err != nil {
		t.Fatalf("Record: %v", err)
	}

	// Round-trip through the run file: the tape the card reads is the tape
	// that was written, not the one still in memory.
	path := filepath.Join(t.TempDir(), "run.tape")
	if err := tape.Write(path, tp); err != nil {
		t.Fatalf("tape.Write: %v", err)
	}
	got, err := tape.Read(path)
	if err != nil {
		t.Fatalf("tape.Read: %v", err)
	}
	s := got.Summary

	if s.Concurrency != streams {
		t.Errorf("Concurrency = %d, want %d", s.Concurrency, streams)
	}
	if len(got.Requests) != streams {
		t.Fatalf("requests = %d, want %d", len(got.Requests), streams)
	}
	for i, rec := range got.Requests {
		if rec.Error != "" {
			t.Errorf("stream %d failed: %s", i, rec.Error)
		}
		if len(rec.Tokens) == 0 {
			t.Errorf("stream %d recorded no tokens", i)
		}
		if rec.Timings.PredictedN == 0 {
			t.Errorf("stream %d has no server timings", i)
		}
		if rec.Prompt.RenderedPrompt == "" {
			t.Errorf("stream %d has no rendered prompt (/apply-template was not applied)", i)
		}
	}

	// The GGUF is not on this host, so the header path must degrade with a
	// warning rather than fail the run.
	if !warnsAbout(s.Warnings, "model file not readable") {
		t.Errorf("Warnings do not mention the missing GGUF: %q", s.Warnings)
	}
	// A warning is printed verbatim on the card, which is 72 columns wide, so
	// none of them may be a wrapped Go error chain.
	for _, w := range s.Warnings {
		if len(w) > 120 || strings.Contains(w, ": open ") {
			t.Errorf("warning is not a short human sentence: %q", w)
		}
	}
	if s.Placement.Source != "unknown" {
		t.Errorf("Placement.Source = %q, want %q with no GGUF header", s.Placement.Source, "unknown")
	}
	if s.Model.FileName == "" || s.Model.Quant != "UD-Q4_K_XL" {
		t.Errorf("Model = %q / %q, want the file name and quant recovered from model_path", s.Model.FileName, s.Model.Quant)
	}

	// The /proc view.
	if s.Server.PID != fixturePID {
		t.Errorf("PID = %d, want %d", s.Server.PID, fixturePID)
	}
	if s.Server.Flags.NGL != "99" || s.Server.Flags.FlashAttn != "on" {
		t.Errorf("flags from argv = %+v, want -ngl 99 -fa on", s.Server.Flags)
	}
	if s.Memory.AtEnd.RSSBytes == 0 {
		t.Error("Memory.AtEnd.RSSBytes = 0, the process sampler produced nothing")
	}
	if s.Memory.MappedFileBytes == 0 {
		t.Error("Memory.MappedFileBytes = 0, the model mapping was not measured")
	}
	if s.Host.CPUThreads == 0 || s.Host.RAMBytes == 0 {
		t.Errorf("HostInfo did not load: threads=%d ram=%d", s.Host.CPUThreads, s.Host.RAMBytes)
	}

	// GPU and contention.
	if len(s.Host.GPUs) != 2 {
		t.Errorf("GPUs = %d, want 2", len(s.Host.GPUs))
	}
	if len(s.GPUsAtEnd) != 2 {
		t.Errorf("GPUsAtEnd = %d, want 2", len(s.GPUsAtEnd))
	}
	if !s.Contention.Contended {
		t.Errorf("Contention = %+v, want contended (pid 9001 holds VRAM)", s.Contention)
	}

	// Run-level bookkeeping.
	// The slug is tape.SlugFromModel of the file name, capped at 24 runes.
	if want := "20260913-071500-deepseek-v3-0324-ud-q4-k"; s.ID != want {
		t.Errorf("ID = %q, want %q", s.ID, want)
	}
	if s.ToktapeVersion != "0.1.0-test" {
		t.Errorf("ToktapeVersion = %q", s.ToktapeVersion)
	}
	if s.Aggregate.Streams != streams || s.Aggregate.StreamsFailed != 0 {
		t.Errorf("Aggregate streams = %d/%d", s.Aggregate.Streams, s.Aggregate.StreamsFailed)
	}
	if s.Aggregate.SlotsBusyMax != 2 {
		t.Errorf("SlotsBusyMax = %d, want 2 (/slots reports two busy)", s.Aggregate.SlotsBusyMax)
	}
	if s.Timings.PredictedN == 0 || s.Timings.DecodeLabel != "decode" {
		t.Errorf("summary timings = %d tokens labelled %q", s.Timings.PredictedN, s.Timings.DecodeLabel)
	}
	if len(got.Samples) == 0 {
		t.Error("no host samples were taken")
	}
	if s.Template.ChatTemplate != "chatml" || !s.Template.RenderedHasThinkClose {
		t.Errorf("Template = %+v, want chatml with </think> seen", s.Template)
	}
	if s.Server.CtxSize != 32768 || s.Server.NSlots != 4 || s.Server.Build != "b4321" {
		t.Errorf("ServerInfo = %+v", s.Server)
	}

	mu.Lock()
	defer mu.Unlock()
	for _, k := range []recorder.EventKind{
		recorder.EventDiscovered, recorder.EventProps, recorder.EventPIDFound,
		recorder.EventStreamStarted, recorder.EventToken, recorder.EventSample,
		recorder.EventDone, recorder.EventWarning,
	} {
		if kinds[k] == 0 {
			t.Errorf("progress never reported %q", k)
		}
	}
	for i := 0; i < streams; i++ {
		if tokens[i] == 0 {
			t.Errorf("progress reported no token for stream %d", i)
		}
	}
}

// TestRecordWithoutProcOrGPU is the fully degraded run: no /proc view, no GPU
// backend, no GGUF. It must still produce a tape with tokens in it, because a
// missing collector is a warning and never a failure.
func TestRecordWithoutProcOrGPU(t *testing.T) {
	srv := fakeServer(t)
	tp, err := recorder.Record(context.Background(), recorder.Options{
		BaseURL:        srv.URL,
		FSRoot:         t.TempDir(), // an empty tree: no proc at all
		GPU:            gpu.Null{},
		SampleInterval: 20 * time.Millisecond,
		Clock:          fixedClock{time.Date(2026, 9, 13, 7, 15, 0, 0, time.UTC)},
	})
	if err != nil {
		t.Fatalf("Record failed on a host with no /proc and no GPU: %v", err)
	}
	s := tp.Summary
	if s.Concurrency != 1 || len(tp.Requests) != 1 {
		t.Errorf("default run is not single-stream: %d / %d", s.Concurrency, len(tp.Requests))
	}
	if len(tp.Requests[0].Tokens) == 0 {
		t.Error("degraded run recorded no tokens")
	}
	if s.Server.PID != 0 {
		t.Errorf("PID = %d, want 0 with no /proc", s.Server.PID)
	}
	if !warnsAbout(s.Warnings, "pid not found") {
		t.Errorf("Warnings do not mention the missing pid: %q", s.Warnings)
	}
	if s.Memory.MajFaultsTotal != 0 || s.Memory.AtEnd.RSSBytes != 0 {
		t.Errorf("memory was invented without a /proc view: %+v", s.Memory)
	}
	if len(s.Host.GPUs) != 0 || len(s.GPUsAtEnd) != 0 {
		t.Error("GPUs were invented without a backend")
	}
	if s.Cache.Label == tape.CacheCold {
		t.Error("run labelled cold without a fault counter to measure it")
	}
}

// TestRecordUnreachable: a server that does not answer /props is the one
// failure the CLI maps to its own exit code.
func TestRecordUnreachable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	_, err := recorder.Record(context.Background(), recorder.Options{
		BaseURL: srv.URL,
		FSRoot:  t.TempDir(),
		GPU:     gpu.Null{},
	})
	if !errors.Is(err, recorder.ErrUnreachable) {
		t.Fatalf("error = %v, want ErrUnreachable", err)
	}
}

// TestRecordAllStreamsFailed: every stream erroring is the other fatal case.
func TestRecordAllStreamsFailed(t *testing.T) {
	bad, err := os.ReadFile("../server/testdata/stream_error.sse")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/props", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(propsJSON))
	})
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write(bad)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	_, err = recorder.Record(context.Background(), recorder.Options{
		BaseURL:     srv.URL,
		Concurrency: 2,
		FSRoot:      t.TempDir(),
		GPU:         gpu.Null{},
	})
	if !errors.Is(err, recorder.ErrAllStreamsFailed) {
		t.Fatalf("error = %v, want ErrAllStreamsFailed", err)
	}
}

// TestRecordCyclesPrompts: fewer prompts than streams are cycled, so the
// caller's -n always decides the stream count.
func TestRecordCyclesPrompts(t *testing.T) {
	srv := fakeServer(t)
	tp, err := recorder.Record(context.Background(), recorder.Options{
		BaseURL:     srv.URL,
		Concurrency: 4,
		Prompts: []server.StreamRequest{
			{Messages: []tape.Message{{Role: "user", Content: "alpha"}}},
			{Messages: []tape.Message{{Role: "user", Content: "beta"}}},
		},
		FSRoot:         t.TempDir(),
		GPU:            gpu.Null{},
		SampleInterval: 50 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	if len(tp.Requests) != 4 {
		t.Fatalf("requests = %d, want 4", len(tp.Requests))
	}
	want := []string{"alpha", "beta", "alpha", "beta"}
	for i, w := range want {
		if got := tp.Requests[i].Prompt.Messages[0].Content; got != w {
			t.Errorf("request %d prompt = %q, want %q", i, got, w)
		}
	}
}

func warnsAbout(warnings []string, substr string) bool {
	for _, w := range warnings {
		if strings.Contains(w, substr) {
			return true
		}
	}
	return false
}
