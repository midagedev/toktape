package recorder_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/midagedev/toktape/internal/gpu"
	"github.com/midagedev/toktape/internal/recorder"
	"github.com/midagedev/toktape/internal/server"
	"github.com/midagedev/toktape/internal/tape"
)

// bodyLog keeps every chat request body the fake server received, in arrival
// order.
type bodyLog struct {
	mu     sync.Mutex
	bodies []map[string]any
}

func (l *bodyLog) all() []map[string]any {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]map[string]any(nil), l.bodies...)
}

// sweepServer is roundsServer's happy path that also logs the request bodies,
// which is where a sweep's speculative.n_max has to land.
func sweepServer(t *testing.T) (*httptest.Server, *bodyLog) {
	t.Helper()
	good, err := os.ReadFile(sseFile)
	if err != nil {
		t.Fatalf("read SSE fixture: %v", err)
	}
	log := &bodyLog{}
	mux := http.NewServeMux()
	mux.HandleFunc("/props", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Server", "llama.cpp")
		_, _ = w.Write([]byte(propsJSON))
	})
	mux.HandleFunc("/apply-template", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"prompt": "<|user|>hi<|assistant|>"})
	})
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		if err := json.Unmarshal(raw, &body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		log.mu.Lock()
		log.bodies = append(log.bodies, body)
		log.mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		for _, ev := range bytes.SplitAfter(good, []byte("\n\n")) {
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
	return srv, log
}

// draftProc is a /proc holding one llama-server started on propsJSON's model
// with a draft model, so the recorder reads an argv that names one.
func draftProc(t *testing.T) string {
	t.Helper()
	var props struct {
		ModelPath string `json:"model_path"`
	}
	if err := json.Unmarshal([]byte(propsJSON), &props); err != nil || props.ModelPath == "" {
		t.Fatalf("propsJSON model_path: %q, %v", props.ModelPath, err)
	}
	root := t.TempDir()
	dir := filepath.Join(root, "proc", "1234")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	argv := strings.Join([]string{
		"/usr/local/bin/llama-server", "--model", props.ModelPath, "-ngl", "99",
		"-md", "/models/drafts/DSpark-0.6B-Q8_0.gguf", "--draft-max", "3",
	}, "\x00") + "\x00"
	if err := os.WriteFile(filepath.Join(dir, "cmdline"), []byte(argv), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

// nmaxOf is a body's speculative.n_max as it went over the wire, "-" when the
// body had none.
func nmaxOf(body map[string]any) string {
	v, ok := body["speculative.n_max"]
	if !ok {
		return "-"
	}
	return fmt.Sprint(v)
}

func hasWarning(s tape.RunSummary, prefix string) bool {
	for _, w := range s.Warnings {
		if strings.HasPrefix(w, prefix) {
			return true
		}
	}
	return false
}

// TestRecordSpecNMaxSweep (TTP-35, 2026-09-13) is the FAIL-first gate for a
// sweep end to end: two prompts at n_max 3 and 5 are four rounds, sent in
// that order with the key in every body, reduced into two groups.
func TestRecordSpecNMaxSweep(t *testing.T) {
	srv, log := sweepServer(t)
	tp, err := recorder.Record(context.Background(), recorder.Options{
		BaseURL: srv.URL,
		Rounds: []recorder.Round{
			{Name: "sql", Prompts: []server.StreamRequest{userPrompt("alpha", 0)}},
			{Name: "prose", Prompts: []server.StreamRequest{userPrompt("beta", 0)}},
		},
		SpecNMax:       []int{3, 5},
		SampleInterval: 20 * time.Millisecond,
		FSRoot:         draftProc(t),
		GPU:            gpu.Null{},
	})
	if err != nil {
		t.Fatalf("Record: %v", err)
	}

	bodies := log.all()
	var gotNMax, gotContent []string
	for _, b := range bodies {
		gotNMax = append(gotNMax, nmaxOf(b))
		msgs, _ := b["messages"].([]any)
		if len(msgs) == 1 {
			m, _ := msgs[0].(map[string]any)
			gotContent = append(gotContent, fmt.Sprint(m["content"]))
		}
	}
	if want := []string{"3", "3", "5", "5"}; !reflect.DeepEqual(gotNMax, want) {
		t.Errorf("bodies carry speculative.n_max %v, want %v", gotNMax, want)
	}
	if want := []string{"alpha", "beta", "alpha", "beta"}; !reflect.DeepEqual(gotContent, want) {
		t.Errorf("bodies carry prompts %v, want %v: every value runs the whole set, in order", gotContent, want)
	}

	s := tp.Summary
	if s.Server.Flags.DraftModel != "DSpark-0.6B-Q8_0.gguf" {
		t.Fatalf("DraftModel = %q: the fixture argv was not read, so this test proves nothing about the draft check", s.Server.Flags.DraftModel)
	}
	if hasWarning(s, "no draft model") {
		t.Errorf("a server with a draft model warned: %v", s.Warnings)
	}
	if len(tp.Requests) != 4 || s.Rounds != 4 || len(s.PerRound) != 4 {
		t.Fatalf("%d requests, Rounds %d, PerRound %d; want 4, 4, 4", len(tp.Requests), s.Rounds, len(s.PerRound))
	}
	for k, want := range []struct {
		nmax int
		name string
	}{{3, "sql"}, {3, "prose"}, {5, "sql"}, {5, "prose"}} {
		p, r := s.PerRound[k], tp.Requests[k]
		if p.SpecNMax != want.nmax || p.Name != want.name {
			t.Errorf("PerRound[%d] = n_max %d %q, want %d %q", k, p.SpecNMax, p.Name, want.nmax, want.name)
		}
		if r.Round != k || r.Prompt.Params["speculative.n_max"] != want.nmax {
			t.Errorf("request %d is round %d with params %v", k, r.Round, r.Prompt.Params)
		}
	}
	if !reflect.DeepEqual(s.SpecNMax, []int{3, 5}) {
		t.Errorf("SpecNMax = %v, want [3 5]", s.SpecNMax)
	}
	if len(s.BySpecNMax) != 2 {
		t.Fatalf("BySpecNMax = %+v, want 2 groups", s.BySpecNMax)
	}
	for i, want := range []int{3, 5} {
		g := s.BySpecNMax[i]
		if g.NMax != want || g.Rounds != 2 || g.Spread.PerStreamPredictedPerSecond.Median <= 0 {
			t.Errorf("BySpecNMax[%d] = %+v, want n_max %d over 2 rounds with a rate", i, g, want)
		}
	}
}

// TestRecordSpecNMaxWithoutDraftRunsOnlyTheFirst: a server whose argv was
// read and names no draft model ignores the key, so only the first value runs
// and the card says why. The run has no prompts file, so it is one round of
// the default prompts, capped by --n-predict as a plain run is.
func TestRecordSpecNMaxWithoutDraftRunsOnlyTheFirst(t *testing.T) {
	srv, log := sweepServer(t)
	tp, err := recorder.Record(context.Background(), recorder.Options{
		BaseURL:        srv.URL,
		MaxTokens:      256,
		SpecNMax:       []int{3, 5},
		SampleInterval: 20 * time.Millisecond,
		FSRoot:         absRoot(t),
		GPU:            gpu.Null{},
	})
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	s := tp.Summary
	if len(s.Server.Args) == 0 || s.Server.Flags.DraftModel != "" {
		t.Fatalf("Args %v, DraftModel %q: want the fixture argv read, with no draft", s.Server.Args, s.Server.Flags.DraftModel)
	}
	bodies := log.all()
	if len(bodies) != 1 || nmaxOf(bodies[0]) != "3" {
		t.Fatalf("%d bodies, first n_max %v; want one body at n_max 3", len(bodies), bodies)
	}
	if got := fmt.Sprint(bodies[0]["max_tokens"]); got != "256" {
		t.Errorf("max_tokens = %s, want --n-predict's 256", got)
	}
	const want = "no draft model: --spec-n-max 3,5 ran only n_max 3"
	found := false
	for _, w := range s.Warnings {
		found = found || w == want
	}
	if !found {
		t.Errorf("warnings %q do not contain %q", s.Warnings, want)
	}
	if !reflect.DeepEqual(s.SpecNMax, []int{3}) || s.BySpecNMax != nil {
		t.Errorf("SpecNMax %v, BySpecNMax %+v; want [3] and no groups", s.SpecNMax, s.BySpecNMax)
	}
}

// TestRecordSpecNMaxRemoteRunsEveryValue: with no argv to read the recorder
// cannot know whether a draft model is loaded, so every value runs. Without a
// prompts file each value is one round of the default prompts — one per
// stream, as a plain run sends them.
func TestRecordSpecNMaxRemoteRunsEveryValue(t *testing.T) {
	srv, log := sweepServer(t)
	tp, err := recorder.Record(context.Background(), recorder.Options{
		BaseURL:        srv.URL,
		Concurrency:    2,
		MaxTokens:      256,
		SpecNMax:       []int{3, 5},
		SampleInterval: 20 * time.Millisecond,
		FSRoot:         t.TempDir(),
		GPU:            gpu.Null{},
	})
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	bodies := log.all()
	if len(bodies) != 4 {
		t.Fatalf("%d bodies, want 2 streams × 2 values", len(bodies))
	}
	defaults := map[string]bool{}
	for _, p := range server.DefaultPrompts(2) {
		defaults[p.Messages[0].Content] = true
	}
	for i, b := range bodies {
		if want := []string{"3", "5"}[i/2]; nmaxOf(b) != want {
			t.Errorf("body %d n_max %s, want %s", i, nmaxOf(b), want)
		}
		if got := fmt.Sprint(b["max_tokens"]); got != "256" {
			t.Errorf("body %d max_tokens = %s, want 256", i, got)
		}
		msgs, _ := b["messages"].([]any)
		m, _ := msgs[0].(map[string]any)
		if !defaults[fmt.Sprint(m["content"])] {
			t.Errorf("body %d prompt %q is not a default prompt", i, m["content"])
		}
	}
	s := tp.Summary
	if hasWarning(s, "no draft model") {
		t.Errorf("a remote server warned about its draft: %v", s.Warnings)
	}
	if s.Rounds != 2 || s.Concurrency != 2 || !reflect.DeepEqual(s.SpecNMax, []int{3, 5}) || len(s.BySpecNMax) != 2 {
		t.Errorf("Rounds %d, Concurrency %d, SpecNMax %v, %d groups; want 2, 2, [3 5], 2",
			s.Rounds, s.Concurrency, s.SpecNMax, len(s.BySpecNMax))
	}
	if tp.Requests[0].Prompt.Messages[0].Content == tp.Requests[1].Prompt.Messages[0].Content {
		t.Error("both streams of a round sent the same default prompt; a plain run sends one per stream")
	}
}

// TestRecordRoundsWithoutSweepSendNoNMax: a prompts-file run without the flag
// sends no override and carries no sweep fields.
func TestRecordRoundsWithoutSweepSendNoNMax(t *testing.T) {
	srv, log := sweepServer(t)
	tp, err := recorder.Record(context.Background(), recorder.Options{
		BaseURL: srv.URL,
		Rounds: []recorder.Round{
			{Prompts: []server.StreamRequest{userPrompt("alpha", 0)}},
			{Prompts: []server.StreamRequest{userPrompt("beta", 0)}},
		},
		SampleInterval: 20 * time.Millisecond,
		FSRoot:         draftProc(t),
		GPU:            gpu.Null{},
	})
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	for i, b := range log.all() {
		if nmaxOf(b) != "-" {
			t.Errorf("body %d carries speculative.n_max %s", i, nmaxOf(b))
		}
	}
	s := tp.Summary
	if s.SpecNMax != nil || s.BySpecNMax != nil || s.PerRound[0].SpecNMax != 0 {
		t.Errorf("SpecNMax %v, BySpecNMax %v, PerRound[0].SpecNMax %d; want none", s.SpecNMax, s.BySpecNMax, s.PerRound[0].SpecNMax)
	}
}
