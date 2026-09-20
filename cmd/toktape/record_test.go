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
	"sync"
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
		// 32768, the hero rig's room: the slot's context caps the answer a
		// run asks for (TTP-148, 2026-09-20), and a tight fixture would make
		// that cap every CLI test's story instead of the flag each one is
		// about.
		_, _ = w.Write([]byte(`[{"id":0,"is_processing":true,"n_ctx":32768,"n_past":12}]`))
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

	code, stdout, stderr := exec(t, "--url", srv.URL, "--out", dir, "--sessions", "2", "--n-predict", "64")
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
	for _, want := range []string{"✓ Tape   ", "✓ Card   ", ".card.png", "→ Markdown: toktape card ", "-o md --copy"} {
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
		t.Errorf("--sessions 2 recorded %d streams", len(tp.Requests))
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
	if code, out, _ := exec(t, "card", tapes[0], "-o", "md"); code != exitOK || !strings.Contains(out, "```") {
		t.Errorf("card -o md on the fresh tape: exit %d", code)
	}
}

// TestRecordVerbQuietAndJSON: --quiet silences stderr and -o json replaces the
// card with the summary the compare tooling reads.
func TestRecordVerbQuietAndJSON(t *testing.T) {
	hermetic(t)
	srv := cliServer(t)
	dir := t.TempDir()

	code, stdout, stderr := exec(t, "record", "--url", srv.URL, "--out", dir, "-o", "json", "--quiet")
	if code != exitOK {
		t.Fatalf("exit %d\n%s", code, stderr)
	}
	if stderr != "" {
		t.Errorf("--quiet still wrote to stderr:\n%s", stderr)
	}
	var s tape.RunSummary
	if err := json.Unmarshal([]byte(stdout), &s); err != nil {
		t.Fatalf("-o json is not valid JSON: %v", err)
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

// Sampling and the raw path (TTP-55, 2026-09-14). Measured that day: greedy
// /completion 25.6 tok/s median, server-default sampling on /completion 24.5,
// and the chat path with thinking on 22.4 — so the flags below decide which
// of three numbers a card is showing.

// TestSamplingOptions covers what the four flags assemble.
func TestSamplingOptions(t *testing.T) {
	kwargsOf := func(t *testing.T, s sampling) map[string]any {
		t.Helper()
		kw, ok := s.params["chat_template_kwargs"].(map[string]any)
		if !ok {
			t.Fatalf("chat_template_kwargs %+v, want an object", s.params["chat_template_kwargs"])
		}
		return kw
	}

	// An unnamed --temp sends nothing: the server's own default stays in
	// effect, and the card must not print a figure nobody chose.
	got, err := samplingOptions(samplingFlags{endpoint: tape.EndpointChat})
	if err != nil {
		t.Fatalf("plain run: %v", err)
	}
	if got.params != nil {
		t.Fatalf("a plain run sends parameters: %+v", got.params)
	}

	// --temp 0 is greedy and must reach the wire. Zero is the value, not the
	// absence of one.
	got, err = samplingOptions(samplingFlags{endpoint: tape.EndpointChat, tempSet: true})
	if err != nil {
		t.Fatalf("--temp 0: %v", err)
	}
	if got.params["temperature"] != 0.0 {
		t.Fatalf("--temp 0 params %+v, want temperature 0", got.params)
	}

	got, err = samplingOptions(samplingFlags{endpoint: tape.EndpointChat, tempSet: true, temp: 0.7})
	if err != nil {
		t.Fatalf("--temp 0.7: %v", err)
	}
	if got.params["temperature"] != 0.7 {
		t.Fatalf("--temp 0.7 params %+v", got.params)
	}

	// --no-think sends the engine's own switch.
	got, err = samplingOptions(samplingFlags{endpoint: tape.EndpointChat, noThink: true})
	if err != nil {
		t.Fatalf("--no-think: %v", err)
	}
	if kwargsOf(t, got)["enable_thinking"] != false {
		t.Fatalf("--no-think params %+v", got.params)
	}

	// --no-think and a user's own kwargs share that object rather than one
	// overwriting the other.
	got, err = samplingOptions(samplingFlags{endpoint: tape.EndpointChat, noThink: true,
		params: []string{`chat_template_kwargs={"tools":"none"}`}})
	if err != nil {
		t.Fatalf("--no-think with kwargs: %v", err)
	}
	kw := kwargsOf(t, got)
	if kw["enable_thinking"] != false || kw["tools"] != "none" {
		t.Fatalf("kwargs %+v, want both switches", kw)
	}

	// --endpoint completion carries through untouched.
	got, err = samplingOptions(samplingFlags{endpoint: tape.EndpointCompletion, tempSet: true})
	if err != nil {
		t.Fatalf("--endpoint completion: %v", err)
	}
	if got.endpoint != tape.EndpointCompletion {
		t.Fatalf("endpoint %q", got.endpoint)
	}
}

// TestSamplingOptionsRefusals: the three ways of asking for something the
// server cannot honour, each of which would otherwise record a tape that
// describes a request nobody made.
func TestSamplingOptionsRefusals(t *testing.T) {
	for _, tc := range []struct {
		name     string
		endpoint string
		noThink  bool
		params   []string
		want     string
	}{
		{"an endpoint that is not one", "completions", false, nil, "use chat or completion"},
		{"thinking on a raw prompt", tape.EndpointCompletion, true, nil, "thinking is the template's"},
		{"--param messages", tape.EndpointChat, false, []string{`messages=[]`}, "is the request, not a parameter"},
		{"--param prompt", tape.EndpointChat, false, []string{`prompt=hi`}, "is the request, not a parameter"},
		{"--param stream", tape.EndpointChat, false, []string{`stream=false`}, "is the request, not a parameter"},
		{"--param without a value", tape.EndpointChat, false, []string{"seed"}, "expected key=value"},
		{"--param without a key", tape.EndpointChat, false, []string{"=4"}, "expected key=value"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := samplingOptions(samplingFlags{endpoint: tc.endpoint, noThink: tc.noThink, params: tc.params})
			if err == nil {
				t.Fatalf("accepted %v", tc.params)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q, want it to mention %q", err, tc.want)
			}
		})
	}
}

// TestParseParam: a value that is JSON stays typed, and a value that is not is
// the string it was typed as. llama-server rejects "0.7" where it wants 0.7,
// and quoting every bare word would make the flag unusable.
func TestParseParam(t *testing.T) {
	for _, tc := range []struct {
		in   string
		key  string
		want any
	}{
		{"seed=7", "seed", float64(7)},
		{"temperature=0.7", "temperature", 0.7},
		{"cache_prompt=false", "cache_prompt", false},
		{"reasoning_effort=high", "reasoning_effort", "high"},
		{`chat_template_kwargs={"enable_thinking":false}`, "chat_template_kwargs", nil},
		{"top_k=40", "top_k", float64(40)},
	} {
		t.Run(tc.in, func(t *testing.T) {
			key, v, err := parseParam(tc.in)
			if err != nil {
				t.Fatalf("parseParam(%q): %v", tc.in, err)
			}
			if key != tc.key {
				t.Fatalf("key %q, want %q", key, tc.key)
			}
			if tc.want == nil {
				if _, ok := v.(map[string]any); !ok {
					t.Fatalf("value %T, want an object", v)
				}
				return
			}
			if v != tc.want {
				t.Fatalf("value %#v, want %#v", v, tc.want)
			}
		})
	}
}

// TestRecordVerbSamplingUsage: the refusals reach the user as exit 1 with a
// sentence that says what to do instead.
func TestRecordVerbSamplingUsage(t *testing.T) {
	hermetic(t)
	dead := "http://127.0.0.1:1"

	code, _, stderr := exec(t, "--url", dead, "--endpoint", "completion", "--no-think")
	if code != exitUsage || !strings.Contains(stderr, "thinking is the template's") {
		t.Errorf("--no-think on the raw path: exit %d, stderr %q", code, stderr)
	}

	code, _, stderr = exec(t, "--url", dead, "--endpoint", "completions")
	if code != exitUsage || !strings.Contains(stderr, "use chat or completion") {
		t.Errorf("a misspelled endpoint: exit %d, stderr %q", code, stderr)
	}

	code, _, stderr = exec(t, "--url", dead, "--param", "messages=[]")
	if code != exitUsage || !strings.Contains(stderr, "is the request, not a parameter") {
		t.Errorf("--param messages: exit %d, stderr %q", code, stderr)
	}
}

// rawCLIServer answers /props and /completion only. It serves no
// /v1/chat/completions and no /apply-template, so a run that reached for
// either would fail here rather than pass quietly.
func rawCLIServer(t *testing.T) (*httptest.Server, func() []map[string]any) {
	t.Helper()
	sse, err := os.ReadFile("../../internal/server/testdata/completion_basic.sse")
	if err != nil {
		t.Fatalf("read completion fixture: %v", err)
	}
	var mu sync.Mutex
	var bodies []map[string]any
	mux := http.NewServeMux()
	mux.HandleFunc("/props", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Server", "llama.cpp")
		_, _ = w.Write([]byte(cliProps))
	})
	mux.HandleFunc("/completion", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		mu.Lock()
		bodies = append(bodies, body)
		mu.Unlock()
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
	return srv, func() []map[string]any {
		mu.Lock()
		defer mu.Unlock()
		return append([]map[string]any(nil), bodies...)
	}
}

// TestRecordVerbRawEndpoint is the flag end to end: --endpoint completion
// posts the prompt verbatim to /completion, --temp 0 rides along, and the
// tape says which path recorded the rate.
func TestRecordVerbRawEndpoint(t *testing.T) {
	hermetic(t)
	srv, bodies := rawCLIServer(t)
	out := t.TempDir()

	code, stdout, stderr := exec(t,
		"--url", srv.URL, "--out", out, "--quiet",
		"--endpoint", "completion", "--temp", "0",
		"--prompt", "Explain mmap.", "--n-predict", "320")
	if code != exitOK {
		t.Fatalf("exit %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}

	sent := bodies()
	// The prefill probe pass (TTP-137, 2026-09-19) sends its own /completion
	// requests before the run's first one, so the log holds theirs too; the
	// run's request is the one carrying the run's prompt, and there must be
	// exactly one of those. FAIL-first against the pre-probe code: the count
	// below was len(sent) != 1.
	var runSent []map[string]any
	for _, b := range sent {
		if b["prompt"] == "Explain mmap." {
			runSent = append(runSent, b)
		}
	}
	if len(runSent) != 1 {
		t.Fatalf("got %d requests for the run's prompt, want 1 (%d bodies total)", len(runSent), len(sent))
	}
	if runSent[0]["prompt"] != "Explain mmap." {
		t.Fatalf("server saw prompt %v, want the text verbatim", runSent[0]["prompt"])
	}
	if runSent[0]["temperature"] != float64(0) {
		t.Fatalf("temperature %v, want 0 on the wire", runSent[0]["temperature"])
	}
	if runSent[0]["n_predict"] != float64(320) {
		t.Fatalf("n_predict %v, want 320", runSent[0]["n_predict"])
	}
	if _, ok := runSent[0]["messages"]; ok {
		t.Fatalf("the raw path sent messages: %+v", runSent[0])
	}

	tapes, err := filepath.Glob(filepath.Join(out, "*"+tape.Ext))
	if err != nil || len(tapes) != 1 {
		t.Fatalf("glob tapes: %v, %v", tapes, err)
	}
	tp, err := tape.Read(tapes[0])
	if err != nil {
		t.Fatalf("read tape: %v", err)
	}
	if len(tp.Requests) != 1 {
		t.Fatalf("tape has %d records, want 1", len(tp.Requests))
	}
	rec := tp.Requests[0]
	if rec.Prompt.Endpoint != tape.EndpointCompletion {
		t.Fatalf("recorded endpoint %q, want %q", rec.Prompt.Endpoint, tape.EndpointCompletion)
	}
	if len(rec.Prompt.Messages) != 0 {
		t.Fatalf("recorded messages on the raw path: %+v", rec.Prompt.Messages)
	}
	if rec.Prompt.Params["temperature"] != float64(0) {
		t.Fatalf("the tape does not show the temperature that was sent: %+v", rec.Prompt.Params)
	}
	if n := len(rec.Tokens); n != 44 {
		t.Fatalf("recorded %d tokens, want the fixture's 44", n)
	}
}

// TestRecordVerbNoThinkReachesTheWire: --no-think sends the engine's own
// switch on the chat path and the tape records that it was sent.
func TestRecordVerbNoThinkReachesTheWire(t *testing.T) {
	hermetic(t)
	sse, err := os.ReadFile(cliSSE)
	if err != nil {
		t.Fatalf("read SSE fixture: %v", err)
	}
	var mu sync.Mutex
	var bodies []map[string]any
	mux := http.NewServeMux()
	mux.HandleFunc("/props", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Server", "llama.cpp")
		_, _ = w.Write([]byte(cliProps))
	})
	mux.HandleFunc("/apply-template", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"prompt": "<|user|>hi<|assistant|>"})
	})
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		bodies = append(bodies, body)
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write(sse)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	out := t.TempDir()
	code, stdout, stderr := exec(t,
		"--url", srv.URL, "--out", out, "--quiet", "--no-think",
		"--param", "seed=7", "--prompt", "hi")
	if code != exitOK {
		t.Fatalf("exit %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}

	mu.Lock()
	sent := append([]map[string]any(nil), bodies...)
	mu.Unlock()
	if len(sent) != 1 {
		t.Fatalf("got %d requests, want 1", len(sent))
	}
	kw, ok := sent[0]["chat_template_kwargs"].(map[string]any)
	if !ok || kw["enable_thinking"] != false {
		t.Fatalf("the engine's thinking switch never reached the wire: %+v", sent[0])
	}
	if sent[0]["seed"] != float64(7) {
		t.Fatalf("--param seed did not reach the wire: %+v", sent[0])
	}

	tapes, _ := filepath.Glob(filepath.Join(out, "*"+tape.Ext))
	if len(tapes) != 1 {
		t.Fatalf("got %d tapes, want 1", len(tapes))
	}
	tp, err := tape.Read(tapes[0])
	if err != nil {
		t.Fatalf("read tape: %v", err)
	}
	rec := tp.Requests[0]
	if rec.Prompt.Thinking != "off" {
		t.Fatalf("recorded thinking %q, want off", rec.Prompt.Thinking)
	}
	if rec.Prompt.Endpoint != tape.EndpointChat {
		t.Fatalf("recorded endpoint %q, want %q", rec.Prompt.Endpoint, tape.EndpointChat)
	}
	if got := tp.Summary.Template.TemplateKwargs["enable_thinking"]; got != "false" {
		t.Fatalf("summary template kwargs %+v, want enable_thinking false",
			tp.Summary.Template.TemplateKwargs)
	}
}

// TestRecordVerbRawRefusesAConversation: /completion takes one string. A
// prompts file that carries a whole conversation would otherwise be flattened
// to its last turn, and the tape would name a prompt the user never wrote.
func TestRecordVerbRawRefusesAConversation(t *testing.T) {
	hermetic(t)
	path := filepath.Join(t.TempDir(), "prompts.jsonl")
	line := `{"name":"chatty","messages":[{"role":"system","content":"be terse"},{"role":"user","content":"hi"}]}`
	if err := os.WriteFile(path, []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	code, _, stderr := exec(t, "--url", "http://127.0.0.1:1", "--prompts", path, "--endpoint", "completion")
	if code != exitUsage {
		t.Fatalf("exit %d, want %d\nstderr: %s", code, exitUsage, stderr)
	}
	for _, want := range []string{"sends one prompt verbatim", "chatty", "2-message"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr %q, want it to mention %q", stderr, want)
		}
	}

	// The same file on the chat path is fine: it is only the raw endpoint
	// that cannot carry a conversation.
	code, _, stderr = exec(t, "--url", "http://127.0.0.1:1", "--prompts", path)
	if code == exitUsage && strings.Contains(stderr, "sends one prompt verbatim") {
		t.Errorf("the chat path refused a conversation: %s", stderr)
	}

	// An unnamed line is identified by its ROUND number, not its file line:
	// the parser skips blank lines, so the two differ, and the round number
	// is what the card prints for a round without a name. The blank line and
	// the single-prompt line before it are what make the difference visible —
	// the offending round is the 2nd round and the 4th line.
	unnamed := filepath.Join(t.TempDir(), "unnamed.jsonl")
	body := `{"prompt":"first"}` + "\n\n" +
		`{"messages":[{"role":"system","content":"be terse"},{"role":"user","content":"hi"}]}` + "\n"
	if err := os.WriteFile(unnamed, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	code, _, stderr = exec(t, "--url", "http://127.0.0.1:1", "--prompts", unnamed, "--endpoint", "completion")
	if code != exitUsage || !strings.Contains(stderr, "round 2") {
		t.Errorf("exit %d, stderr %q, want a usage error naming round 2", code, stderr)
	}
}

// openaiCLIServer is a generic OpenAI-compatible fake for the record verb:
// 404 on /props and /slots, a one-model /v1/models listing, and a chat route
// that streams nTokens one-word deltas with a final usage chunk and records
// every body it received.
func openaiCLIServer(t *testing.T, nTokens, usageTokens int) (*httptest.Server, func() []map[string]any) {
	t.Helper()
	var mu sync.Mutex
	var bodies []map[string]any
	mux := http.NewServeMux()
	mux.HandleFunc("/props", func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	})
	mux.HandleFunc("/slots", func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	})
	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"cli-model","object":"model"}]}`))
	})
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		mu.Lock()
		bodies = append(bodies, body)
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		emit := func(payload string) {
			_, _ = w.Write([]byte("data: " + payload + "\n\n"))
			if flusher != nil {
				flusher.Flush()
			}
		}
		for i := 0; i < nTokens; i++ {
			emit(`{"choices":[{"index":0,"delta":{"content":"w"}}]}`)
		}
		emit(`{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":8,"completion_tokens":` + itoa(usageTokens) + `}}`)
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, func() []map[string]any {
		mu.Lock()
		defer mu.Unlock()
		return append([]map[string]any(nil), bodies...)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

// TestRecordVerbEngineKindFlags: --engine-kind values, --engine with llama,
// and the completion refusal on openai (TTP-99).
func TestRecordVerbEngineKindFlags(t *testing.T) {
	hermetic(t)
	srv, _ := openaiCLIServer(t, 40, 40)
	out := t.TempDir()

	// An unknown kind is a usage error, not a silent auto.
	// FAIL-first: the recorder accepted any string and behaved as auto.
	if code, _, stderr := exec(t, "--url", srv.URL, "--out", out, "--engine-kind", "vllm"); code != exitUsage {
		t.Errorf("exit %d, want %d\n%s", code, exitUsage, stderr)
	}

	// --engine with --engine-kind llama: a llama-server names itself.
	if code, _, stderr := exec(t, "--url", srv.URL, "--out", out,
		"--engine-kind", "llama", "--engine", "vLLM 0.11"); code != exitUsage {
		t.Errorf("exit %d, want %d\n%s", code, exitUsage, stderr)
	} else if !strings.Contains(stderr, "--engine") {
		t.Errorf("the rejection does not name --engine:\n%s", stderr)
	}

	// --endpoint completion with --engine-kind openai is refused before any
	// request is sent.
	if code, _, stderr := exec(t, "--url", srv.URL, "--out", out,
		"--engine-kind", "openai", "--endpoint", "completion"); code != exitUsage {
		t.Errorf("exit %d, want %d\n%s", code, exitUsage, stderr)
	} else if !strings.Contains(stderr, "/v1/chat/completions") {
		t.Errorf("the refusal does not name the OpenAI route:\n%s", stderr)
	}

	// Both flags are documented in the record usage text.
	if code, help, _ := exec(t, "record", "--help"); code != exitOK {
		t.Fatalf("record --help exit %d", code)
	} else if !strings.Contains(help, "--engine-kind") || !strings.Contains(help, "--engine TEXT") {
		t.Errorf("record --help documents neither flag:\n%s", help)
	}
}

// TestRecordVerbOpenAIEndToEnd: the record verb against a non-llama server —
// the tape's kind and claim, the wire without llama fields, and the card's
// client-timed label on stdout.
func TestRecordVerbOpenAIEndToEnd(t *testing.T) {
	hermetic(t)
	srv, bodies := openaiCLIServer(t, 40, 40)
	out := t.TempDir()

	code, stdout, stderr := exec(t,
		"--url", srv.URL, "--out", out, "--quiet",
		"--engine-kind", "openai", "--engine", "vLLM 0.11",
		"--prompt", "Say hi.", "--n-predict", "64")
	if code != exitOK {
		t.Fatalf("exit %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	sent := bodies()
	if len(sent) != 1 {
		t.Fatalf("got %d requests, want 1", len(sent))
	}
	for _, k := range []string{"timings_per_token", "return_progress"} {
		if _, ok := sent[0][k]; ok {
			t.Errorf("the wire carries %q: %v", k, sent[0])
		}
	}
	if sent[0]["model"] != "cli-model" {
		t.Errorf("wire model = %v, want the /v1/models id", sent[0]["model"])
	}

	tapes, err := filepath.Glob(filepath.Join(out, "*"+tape.Ext))
	if err != nil || len(tapes) != 1 {
		t.Fatalf("run files = %v (err %v), want one tape", tapes, err)
	}
	tp, err := tape.Read(tapes[0])
	if err != nil {
		t.Fatalf("read tape: %v", err)
	}
	if tp.Summary.Server.Kind != tape.ServerOpenAI {
		t.Errorf("Kind = %q, want openai", tp.Summary.Server.Kind)
	}
	if tp.Summary.Server.EngineClaim != "vLLM 0.11" {
		t.Errorf("EngineClaim = %q, want the claim", tp.Summary.Server.EngineClaim)
	}
	if tp.Summary.Timings.Source != "client" || tp.Summary.Timings.PredictedNSource != "usage" {
		t.Errorf("Source = %q/%q, want client/usage",
			tp.Summary.Timings.Source, tp.Summary.Timings.PredictedNSource)
	}
	if !strings.Contains(stdout, "client-timed") {
		t.Errorf("the printed card lacks client-timed:\n%s", stdout)
	}
	if !strings.Contains(stdout, "openai · claim: vLLM 0.11") {
		t.Errorf("the printed card lacks the claim engine line:\n%s", stdout)
	}
}

// TestHeaderLineOpenAI: the attach line prints openai for the kind and ? for
// the build a generic server never reports.
func TestHeaderLineOpenAI(t *testing.T) {
	got := headerLine(&tape.RunSummary{
		Server: tape.ServerInfo{Kind: tape.ServerOpenAI, URL: "http://127.0.0.1:8000"},
	})
	if !strings.Contains(got, "openai") {
		t.Errorf("headerLine = %q, want the openai kind", got)
	}
	if !strings.Contains(got, "(?)") {
		t.Errorf("headerLine = %q, want ? for the unreported build", got)
	}
}
