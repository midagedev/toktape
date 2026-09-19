package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/midagedev/toktape/internal/card"
	"github.com/midagedev/toktape/internal/publish"
	"github.com/midagedev/toktape/internal/tape"
)

// queryFixture serves the read endpoints: a two-row listing with one row
// full and one row unknown, one run, and its record.
func queryFixture(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/runs", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"scope":"public",` +
			`"runs":[{"id":"fullrun","url":"/r/fullrun","published_at":"2026-09-19 07:04:58",` +
			`"owned":true,"recorded_at":"2026-09-19T07:00:00Z","model_id":"llama-3-8b",` +
			`"model_raw":"llama-3-8b.gguf","repo":"bartowski/repo","quant_id":"q4_k_m",` +
			`"quant_raw":"Q4_K_M","engine_kind":"llama.cpp","engine_version":"b6728",` +
			`"os":"linux","gpu_id":"RTX 4090","gpus_raw":"RTX 4090","gpu_count":1,` +
			`"vram_bytes":25757248,"host_class":"desktop","sessions":4,` +
			`"prompt_set":"sql","decode_per_sec":24.232,"prefill_per_sec":310.5,` +
			`"prompt_n":512,"predicted_n":320,"min_predicted_n":12,` +
			`"ctx_size":16384,"kv_cache":"q8_0",` +
			`"params":35000000000,"active_params":35000000000,` +
			`"caveat_count":2,` +
			`"title":"smoke test"},` +
			`{"id":"thinrun","url":"/r/thinrun","published_at":"2026-09-18 07:04:58",` +
			`"owned":false,"caveat_count":0}],"next":null}`))
	})
	mux.HandleFunc("/r/fullrun.json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"fullrun","published_at":"2026-09-19 07:04:58",` +
			`"private":false,"tape":"/r/fullrun.tape","tape_bytes":25600,` +
			`"card":"/r/fullrun.png","owned":true,` +
			`"index":{"schema":1,"model_id":"llama-3-8b","model_raw":"llama-3-8b.gguf",` +
			`"repo":"bartowski/repo","quant_raw":"Q4_K_M","quant_bits":4.5,` +
			`"engine_kind":"llama.cpp","engine_version":"b6728","os":"linux",` +
			`"gpus_raw":["RTX 4090"],"gpu_id":"RTX 4090","gpu_count":1,` +
			`"vram_bytes":25757248,"host_class":"desktop","sessions":4,` +
			`"decode_per_sec":24.232,"prefill_per_sec":310.5,"ttft_p50_ms":45.6,` +
			`"prompt_n":512,"predicted_n":320,"min_predicted_n":12,"reasoning_n":30,` +
			`"cache_hit_ratio":0.25,"ctx_size":16384,"n_slots":8,` +
			`"fa":"on","kv_cache":"q8_0","batch":"2048","ubatch":"512","ngl":"99",` +
			`"offload":"full","draft_model":"tiny-draft","draft_accept":0.62,` +
			`"power_w":281,"power_limit_w":300,"throttled":true,"cold":true,` +
			`"file_bytes":35000000000,"active_params":3000000000,"params":35000000000,` +
			`"moe":true,"n_experts":128,"n_experts_used":8,` +
			`"caveats":["cold_cache","short_prompt_for_prefill"],"caveat_count":2},` +
			`"author":{"name":"Lab Rat","link":"https://example.com"},` +
			`"title":"smoke test","note":"first paragraph\n\nsecond paragraph"}`))
	})
	mux.HandleFunc("/r/fullrun.toktape", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/gzip")
		_, _ = w.Write([]byte("tape-bytes-here"))
	})
	return httptest.NewServer(mux)
}

// TestRunsTable: the header names every column and each run prints one row,
// with ? where the service said nothing.
func TestRunsTable(t *testing.T) {
	publishHome(t, "")
	srv := queryFixture(t)
	defer srv.Close()

	code, stdout, stderr := exec(t, "runs", "--url", srv.URL)
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	for _, want := range []string{"ID", "DECODE tok/s", "PREFILL tok/s", "MODEL", "PARAMS", "QUANT", "STREAMS", "P/G", "CTX", "KV", "ENGINE", "GPU", "CAVEATS", "PUBLISHED", "TITLE"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("table lacks column %q:\n%s", want, stdout)
		}
	}
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	if len(lines) != 1+2+1 {
		t.Fatalf("%d lines, want header + 2 rows + the count:\n%s", len(lines), stdout)
	}
	if !strings.Contains(lines[1], "llama-3-8b") || !strings.Contains(lines[1], "24.2") || !strings.Contains(lines[1], "2026-09-19") {
		t.Errorf("full row = %q, want the model, the rate and the date", lines[1])
	}
	for _, want := range []string{"310.5", "35B", "512/320·min12", "16k", "q8_0"} {
		if !strings.Contains(lines[1], want) {
			t.Errorf("full row = %q, want the figure %q", lines[1], want)
		}
	}
	if !strings.Contains(lines[2], "thinrun") || !strings.Contains(lines[2], "?") {
		t.Errorf("thin row = %q, want the id and a ? cell", lines[2])
	}
	if !strings.Contains(lines[3], "2 runs shown · newest first") {
		t.Errorf("count = %q, want the tally and the order", lines[3])
	}
}

// TestRunsUsageErrors: a bad sort names the three values, and two scopes
// are refused before any request.
func TestRunsUsageErrors(t *testing.T) {
	publishHome(t, "")
	srv := queryFixture(t)
	defer srv.Close()

	code, _, stderr := exec(t, "runs", "--url", srv.URL, "--sort", "bogus")
	if code != exitUsage {
		t.Errorf("--sort bogus exited %d, want %d", code, exitUsage)
	}
	for _, want := range []string{"newest", "oldest", "decode"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr = %q, want it to name %q", stderr, want)
		}
	}

	code, _, stderr = exec(t, "runs", "--url", srv.URL, "--mine", "--user", "x")
	if code != exitUsage {
		t.Errorf("--mine --user exited %d, want %d", code, exitUsage)
	}
}

// TestRunsJSONIsVerbatim: -o json decodes to the served body decoded —
// unknown stays absent or null, never a zero the client invented.
func TestRunsJSONIsVerbatim(t *testing.T) {
	publishHome(t, "")
	srv := queryFixture(t)
	defer srv.Close()

	code, stdout, stderr := exec(t, "runs", "--url", srv.URL, "-o", "json")
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	var got, want struct {
		Scope string           `json:"scope"`
		Runs  []map[string]any `json:"runs"`
		Next  any              `json:"next"`
	}
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("the table's -o json does not parse: %v", err)
	}
	resp, err := http.Get(srv.URL + "/api/v1/runs")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var raw json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatal(err)
	}
	gotB, _ := json.Marshal(got)
	wantB, _ := json.Marshal(want)
	if string(gotB) != string(wantB) {
		t.Errorf("-o json decodes to %s, want %s", gotB, wantB)
	}
	if _, ok := got.Runs[1]["model_id"]; ok {
		t.Errorf("thin row carries model_id = %v, want it absent", got.Runs[1]["model_id"])
	}
}

// TestRunsJSONL: one run object per line and nothing else.
func TestRunsJSONL(t *testing.T) {
	publishHome(t, "")
	srv := queryFixture(t)
	defer srv.Close()

	code, stdout, stderr := exec(t, "runs", "--url", srv.URL, "-o", "jsonl")
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	if len(lines) != 2 {
		t.Fatalf("%d lines, want one per run:\n%s", len(lines), stdout)
	}
	for _, l := range lines {
		var row map[string]any
		if err := json.Unmarshal([]byte(l), &row); err != nil {
			t.Errorf("line does not parse: %v", err)
		}
	}
}

// TestShowSummary: the fixture's rate prints with one decimal beside its
// unit, and the links are built from the service that was read.
func TestShowSummary(t *testing.T) {
	publishHome(t, "")
	srv := queryFixture(t)
	defer srv.Close()

	code, stdout, stderr := exec(t, "show", "fullrun", "--url", srv.URL)
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	for _, want := range []string{
		"smoke test",
		"model: llama-3-8b",
		"quantisation: Q4_K_M · 4.5 bit",
		"engine: llama.cpp b6728",
		"decode: 24.2 tok/s",
		"caveats: 2: cold_cache, short_prompt_for_prefill",
		"by: Lab Rat <https://example.com>",
		"page: " + srv.URL + "/r/fullrun",
		"record: " + srv.URL + "/r/fullrun.tape, 25 KB",
		"card: " + srv.URL + "/r/fullrun.png",
		"first paragraph",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("summary lacks %q:\n%s", want, stdout)
		}
	}
}

// TestShowSavesTheRecord: --save writes the bytes and names them, and a
// second save without --force is refused while the file keeps its bytes.
func TestShowSavesTheRecord(t *testing.T) {
	publishHome(t, "")
	srv := queryFixture(t)
	defer srv.Close()
	path := filepath.Join(t.TempDir(), "run.tape")

	code, stdout, stderr := exec(t, "show", "fullrun", "--url", srv.URL, "--save", path)
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if !strings.Contains(stdout, "saved 15 bytes to "+path) {
		t.Errorf("stdout = %q, want the byte count and the path", stdout)
	}
	if b, err := os.ReadFile(path); err != nil || string(b) != "tape-bytes-here" {
		t.Errorf("file = %q, %v; want the served bytes", b, err)
	}

	code, _, stderr = exec(t, "show", "fullrun", "--url", srv.URL, "--save", path)
	if code != exitUsage {
		t.Errorf("overwrite exited %d, want %d: %s", code, exitUsage, stderr)
	}
	if b, _ := os.ReadFile(path); string(b) != "tape-bytes-here" {
		t.Errorf("refused overwrite changed the file to %q", b)
	}

	code, _, stderr = exec(t, "show", "fullrun", "--url", srv.URL, "--save", path, "--force")
	if code != exitOK {
		t.Errorf("--force exited %d: %s", code, stderr)
	}
}

// TestShowRefusal: a 404 exits with the service-side code and prints the
// server's line.
func TestShowRefusal(t *testing.T) {
	publishHome(t, "")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("no run with that id"))
	}))
	defer srv.Close()

	code, _, stderr := exec(t, "show", "missingrun", "--url", srv.URL)
	if code != exitPublish {
		t.Errorf("exit %d, want %d", code, exitPublish)
	}
	if !strings.Contains(stderr, "no run with that id") {
		t.Errorf("stderr = %q, want the server's line", stderr)
	}
}

// TestShowLinkSpelling: the run is named by its page link, not just its id.
func TestShowLinkSpelling(t *testing.T) {
	publishHome(t, "")
	srv := queryFixture(t)
	defer srv.Close()

	code, stdout, stderr := exec(t, "show", srv.URL+"/r/fullrun.json", "--url", srv.URL)
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if !strings.Contains(stdout, "model: llama-3-8b") {
		t.Errorf("stdout lacks the model:\n%s", stdout)
	}
}

// TestShowFigures: the index's figures print as their own lines, each only
// when its figures were measured.
func TestShowFigures(t *testing.T) {
	publishHome(t, "")
	srv := queryFixture(t)
	defer srv.Close()

	code, stdout, stderr := exec(t, "show", "fullrun", "--url", srv.URL)
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	for _, want := range []string{
		"workload: 512 in / 320 out (min 12) · cache 25% · 4 streams",
		"context: 16384 window · 8 slots",
		"config: fa on · kv q8_0 · b 2048 · ub 512 · ngl 99 · offload full",
		"draft: tiny-draft · 62% accepted",
		"machine: 281 of 300 W · throttled · cold cache",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("summary lacks %q:\n%s", want, stdout)
		}
	}
}

// TestFigureCells: the table's figure cells and their unknowns.
func TestFigureCells(t *testing.T) {
	intptr := func(n int) *int { return &n }
	i64ptr := func(n int64) *int64 { return &n }
	boolptr := func(b bool) *bool { return &b }
	f64ptr := func(f float64) *float64 { return &f }

	if got := paramsCell(publish.Row{Params: i64ptr(35000000000), ActiveParams: i64ptr(35000000000)}); got != "35B" {
		t.Errorf("paramsCell(dense 35B) = %q", got)
	}
	if got := paramsCell(publish.Row{MoE: boolptr(true), Params: i64ptr(35000000000), ActiveParams: i64ptr(3000000000)}); got != "35B · 3B active" {
		t.Errorf("paramsCell(moe) = %q, want the total with the active count beside it", got)
	}
	if got := paramsCell(publish.Row{}); got != "?" {
		t.Errorf("paramsCell(unknown) = %q, want ?", got)
	}
	if got := pgCell(publish.Row{PromptN: intptr(238), PredictedN: intptr(214)}); got != "238/214" {
		t.Errorf("pgCell = %q", got)
	}
	if got := pgCell(publish.Row{PromptN: intptr(238), PredictedN: intptr(214), MinPredictedN: intptr(12)}); got != "238/214·min12" {
		t.Errorf("pgCell with a short stream = %q", got)
	}
	if got := pgCell(publish.Row{PromptN: intptr(238)}); got != "?" {
		t.Errorf("pgCell(half unknown) = %q, want ?", got)
	}
	if got := ctxCell(intptr(8192)); got != "8k" {
		t.Errorf("ctxCell(8192) = %q, want 8k", got)
	}
	if got := ctxCell(nil); got != "?" {
		t.Errorf("ctxCell(nil) = %q, want ?", got)
	}
	if got := rateCell(f64ptr(310.5)); got != "310.5" {
		t.Errorf("rateCell = %q", got)
	}
}

// TestRunsFigureFilters: --size and --min-predicted travel as their query
// params, beside the filters that were already there.
func TestRunsFigureFilters(t *testing.T) {
	publishHome(t, "")
	var raw string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"scope":"public","runs":[],"next":null}`))
	}))
	defer srv.Close()

	code, _, stderr := exec(t, "runs", "--url", srv.URL, "--size", "35", "--min-predicted", "12")
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	for _, want := range []string{"size=35", "min_predicted=12"} {
		found := false
		for _, kv := range strings.Split(raw, "&") {
			if kv == want {
				found = true
			}
		}
		if !found {
			t.Errorf("query %q lacks %q", raw, want)
		}
	}
}

// TestReindexRoundTrip: the run's own bytes come back down, are re-indexed
// with this build's figures, and go back up as an "index" PATCH body.
func TestReindexRoundTrip(t *testing.T) {
	publishHome(t, "token = \"tk_journal\"\n")

	var gz bytes.Buffer
	tp := &tape.Tape{Schema: tape.SchemaVersion, Summary: *card.Example()}
	if err := tape.Encode(&gz, tp); err != nil {
		t.Fatalf("encode the hero: %v", err)
	}
	var patchBody map[string]any
	var patchAuth, patchMethod, patchPath string
	mux := http.NewServeMux()
	mux.HandleFunc("/r/herorun.toktape", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/gzip")
		_, _ = w.Write(gz.Bytes())
	})
	mux.HandleFunc("/api/v1/runs/herorun", func(w http.ResponseWriter, r *http.Request) {
		patchMethod, patchAuth, patchPath = r.Method, r.Header.Get("Authorization"), r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&patchBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"herorun"}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	code, stdout, stderr := exec(t, "reindex", "herorun", "--url", srv.URL)
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if patchMethod != http.MethodPatch || patchPath != "/api/v1/runs/herorun" {
		t.Errorf("edit = %s %s, want PATCH /api/v1/runs/herorun", patchMethod, patchPath)
	}
	if patchAuth == "" {
		t.Error("the reindex PATCH carried no journal token")
	}
	raw, ok := patchBody["index"]
	if !ok {
		t.Fatalf("PATCH body = %v, want an \"index\" key", patchBody)
	}
	got, ok := raw.(map[string]any)
	if !ok {
		t.Fatalf("index = %v, want one JSON object", raw)
	}
	if got["schema"] != float64(1) {
		t.Errorf("index.schema = %v, want 1", got["schema"])
	}
	if got["ctx_size"] != float64(16384) {
		t.Errorf("index.ctx_size = %v, want the hero's 16384", got["ctx_size"])
	}
	// The hero is dense at 70.55 B params: the chip rounds to whole billions.
	want := "herorun  reindexed · P512/G320 · ctx 16k · kv q8_0 · 71B"
	if !strings.Contains(stdout, want) {
		t.Errorf("stdout = %q, want it to contain %q", stdout, want)
	}
}

// TestReindexNeedsToken: without a journal token the verb stops before any
// request, with the same sentence publish --edit uses.
func TestReindexNeedsToken(t *testing.T) {
	publishHome(t, "")

	code, _, stderr := exec(t, "reindex", "herorun", "--url", "http://127.0.0.1:1")
	if code != exitUsage {
		t.Errorf("exit %d, want %d", code, exitUsage)
	}
	if !strings.Contains(stderr, "needs a journal token in config.toml") {
		t.Errorf("stderr = %q, want the edit path's token sentence", stderr)
	}
}

// TestReindexFailureIsPublish: a run that cannot come back down is named on
// stderr and skipped, and the exit says nothing was fully published.
func TestReindexFailureIsPublish(t *testing.T) {
	publishHome(t, "token = \"tk_journal\"\n")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("no run with that id"))
	}))
	defer srv.Close()

	code, stdout, stderr := exec(t, "reindex", "missingrun", "--url", srv.URL)
	if code != exitPublish {
		t.Errorf("exit %d, want %d", code, exitPublish)
	}
	if !strings.Contains(stderr, "missingrun") {
		t.Errorf("stderr = %q, want the failed run named", stderr)
	}
	if strings.Contains(stdout, "reindexed") {
		t.Errorf("stdout = %q, want no reindexed line for a failed run", stdout)
	}
}
