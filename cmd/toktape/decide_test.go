package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/midagedev/toktape/internal/server"
	"github.com/midagedev/toktape/internal/tape"
)

// decideServer is a minimal SystemOne endpoint: every choice and score puts
// 0.7 on its first option, every noul is 0.9, and the engine names itself with
// a string, the way bloomery does.
func decideServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/props" {
			fmt.Fprint(w, `{"engine":"fake-systemone","build":"t1","model_path":"/m/fake.gguf","quant":"Q4_K_M"}`)
			return
		}
		if r.URL.Path != server.SystemOnePath {
			http.NotFound(w, r)
			return
		}
		body, _ := io.ReadAll(r.Body)
		req, err := server.ParseSystemOneRequest(body)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		answers := map[string]any{}
		for _, q := range req.Questions {
			if q.Type == tape.DecisionNoul {
				answers[q.ID] = map[string]any{"type": "noul", "noul": 0.9}
				continue
			}
			probs := map[string]float64{}
			for i, o := range q.Options {
				probs[o.Key] = 0.3 / float64(len(q.Options)-1)
				if i == 0 {
					probs[o.Key] = 0.7
				}
			}
			a := map[string]any{"type": q.Type, "confidence": 0.7, "probabilities": probs}
			if q.Type == tape.DecisionChoice {
				a["choice"] = q.Options[0].Key
			} else {
				a["score"] = 0.3
			}
			answers[q.ID] = a
		}
		json.NewEncoder(w).Encode(map[string]any{"model": req.Model, "answers": answers,
			"usage": map[string]int{"input_tokens": len(body) / 4, "output_tokens": 0}})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestDecideRecordsATape(t *testing.T) {
	hermetic(t)
	srv := decideServer(t)
	dir := t.TempDir()
	code, stdout, stderr := exec(t, "decide", "--url", srv.URL, "--repeat", "2", "--gap", "0", "--out", dir, "--tag", "t1", "--note", "n1")
	if code != exitOK {
		t.Fatalf("exit %d\nstderr:\n%s", code, stderr)
	}
	tapes, _ := filepath.Glob(filepath.Join(dir, "*"+tape.Ext))
	if len(tapes) != 1 {
		t.Fatalf("tapes = %v", tapes)
	}
	tp, err := tape.Read(tapes[0])
	if err != nil {
		t.Fatal(err)
	}
	if !tp.Summary.IsDecision() || len(tp.Decisions) != 8*3 || tp.Decisions[0].CaseID != "route-01" {
		t.Fatalf("mode %q, %d decisions", tp.Summary.Mode, len(tp.Decisions))
	}
	if tp.Summary.Tag != "t1" || tp.Summary.Note != "n1" || tp.Summary.ToktapeVersion != version {
		t.Errorf("labels %q %q version %q", tp.Summary.Tag, tp.Summary.Note, tp.Summary.ToktapeVersion)
	}
	for i, d := range tp.Decisions {
		if bytes.Contains(d.Request, []byte(`"id"`)) {
			t.Fatalf("record %d carries an id", i)
		}
	}
	for _, want := range []string{"cold", "warm p50", "short", "long", "throughput", "client clock", "tape", filepath.Base(tapes[0]), "fake.gguf", "fake-systemone"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("the summary lacks %q:\n%s", want, stdout)
		}
	}
	if !strings.Contains(stderr, "route-01") || !strings.Contains(stderr, "back to back") {
		t.Errorf("progress lines:\n%s", stderr)
	}
}

func TestDecideSuiteAndShowcaseOnly(t *testing.T) {
	hermetic(t)
	srv := decideServer(t)
	dir := t.TempDir()
	suite := filepath.Join(dir, "mine.jsonl")
	os.WriteFile(suite, []byte(`{"id":"one","model":"m","state":"x","questions":{"n":{"type":"noul"}}}`+"\n"), 0o644)
	code, stdout, stderr := exec(t, "decide", "--url", srv.URL, "--suite", suite, "--repeat", "0", "--gap", "0", "--out", dir)
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	tapes, _ := filepath.Glob(filepath.Join(dir, "*"+tape.Ext))
	tp, err := tape.Read(tapes[0])
	if err != nil || len(tp.Decisions) != 1 || tp.Summary.Decision.Suite != "mine.jsonl" {
		t.Fatalf("%v, %d decisions, suite %q", err, len(tp.Decisions), tp.Summary.Decision.Suite)
	}
	if !strings.Contains(stdout, "throughput  ?") {
		t.Errorf("a run with no back-to-back passes should print ? for throughput:\n%s", stdout)
	}
}

func TestDecideRefusals(t *testing.T) {
	hermetic(t)
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.jsonl")
	os.WriteFile(bad, []byte("{nope\n"), 0o644)
	for name, args := range map[string][]string{
		"negative repeat": {"decide", "--repeat", "-1"},
		"no lanes":        {"decide", "-c", "0"},
		"negative gap":    {"decide", "--gap", "-1s"},
		"stray argument":  {"decide", "extra"},
		"missing suite":   {"decide", "--suite", filepath.Join(dir, "none.jsonl")},
		"missing ref":     {"decide", "--reference", filepath.Join(dir, "none.jsonl")},
		"bad suite":       {"decide", "--url", "http://127.0.0.1:1", "--suite", bad},
		"unknown flag":    {"decide", "--sessions", "2"},
	} {
		if code, _, stderr := exec(t, args...); code != exitUsage {
			t.Errorf("%s: exit %d, want %d\n%s", name, code, exitUsage, stderr)
		}
	}
}

func TestDecideUnreachable(t *testing.T) {
	hermetic(t)
	srv := decideServer(t)
	url := srv.URL
	srv.Close()
	code, _, stderr := exec(t, "decide", "--url", url, "--out", t.TempDir())
	if code != exitUnreachable || !strings.Contains(stderr, "--url") || strings.Contains(stderr, "llama-server") {
		t.Errorf("exit %d\n%s", code, stderr)
	}
}

func TestDecideEveryRequestRefused(t *testing.T) {
	hermetic(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/props" {
			fmt.Fprint(w, `{"engine":"x"}`)
			return
		}
		http.Error(w, `{"error":"no such endpoint"}`, http.StatusNotFound)
	}))
	defer srv.Close()
	code, _, stderr := exec(t, "decide", "--url", srv.URL, "--out", t.TempDir(), "--repeat", "0", "--gap", "0")
	if code != exitStreams || !strings.Contains(stderr, "no such endpoint") {
		t.Errorf("exit %d\n%s", code, stderr)
	}
}

func TestDecideHelp(t *testing.T) {
	code, out, _ := exec(t, "help", "decide")
	if code != exitOK || !strings.Contains(out, "--reference") || !strings.Contains(out, "--repeat") {
		t.Errorf("exit %d\n%s", code, out)
	}
}

// TestDecideE2ETape checks a tape recorded by hand against the mock
// (go run ./tools/mock-systemone; toktape decide --repeat 5). It is skipped
// unless TOKTAPE_DECIDE_E2E_TAPE names the tape.
func TestDecideE2ETape(t *testing.T) {
	path := os.Getenv("TOKTAPE_DECIDE_E2E_TAPE")
	if path == "" {
		t.Skip("TOKTAPE_DECIDE_E2E_TAPE not set")
	}
	tp, err := tape.Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if !tp.Summary.IsDecision() || len(tp.Decisions) != 8*6 {
		t.Fatalf("mode %q, %d decisions, want 48", tp.Summary.Mode, len(tp.Decisions))
	}
	if d := tp.Decisions[0]; d.Index != 0 || d.Phase != tape.DecisionPhaseShowcase {
		t.Errorf("first record is not the cold showcase request: %+v", d.Index)
	}
	if got := tp.Summary.Decision.ColdMs; got < 500 {
		t.Errorf("cold %v ms: the mock's cold extra is 600 ms", got)
	}
	for i, d := range tp.Decisions {
		if bytes.Contains(d.Request, []byte(`"id"`)) {
			t.Errorf("record %d: the sent body carries an id", i)
		}
	}
}
