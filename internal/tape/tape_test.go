package tape

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"
)

func sample() *Tape {
	return &Tape{
		Summary: RunSummary{
			ID:          "20260913-071500-qwen3-35b-a3b",
			Model:       ModelInfo{FileName: "Qwen3.5-35B-A3B-UD-Q4_K_M.gguf", Quant: "UD-Q4_K_M"},
			Concurrency: 1,
			Timings:     TimingsSummary{PredictedN: 128, PredictedPerSecond: 68.4, DecodeLabel: "decode"},
			Cache:       CacheSummary{Label: CacheWarm},
		},
		Requests: []RequestRecord{{
			Slot:   0,
			Prompt: PromptRecord{Messages: []Message{{Role: "user", Content: "안녕, 자기소개 해줘"}}},
			Tokens: []TokenEvent{{T: 120 * time.Millisecond, Index: 0, Text: "안녕"}},
		}},
	}
}

func TestWriteReadRoundTrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "run"+Ext)
	in := sample()
	if err := Write(p, in); err != nil {
		t.Fatal(err)
	}
	out, err := Read(p)
	if err != nil {
		t.Fatal(err)
	}
	if out.Schema != SchemaVersion {
		t.Fatalf("schema: got %d want %d", out.Schema, SchemaVersion)
	}
	if out.Summary.ID != in.Summary.ID || out.Requests[0].Tokens[0].Text != "안녕" || out.Requests[0].Prompt.Messages[0].Content != in.Requests[0].Prompt.Messages[0].Content {
		t.Fatalf("round trip mismatch: %+v", out.Summary)
	}
}

func TestDecodePlainJSON(t *testing.T) {
	in := sample()
	in.Schema = SchemaVersion
	raw, _ := json.Marshal(in)
	out, err := Decode(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if out.Summary.ID != in.Summary.ID {
		t.Fatal("plain json decode mismatch")
	}
}

func TestDecodeRejectsNewerSchema(t *testing.T) {
	raw := []byte(`{"schema": 999}`)
	if _, err := Decode(bytes.NewReader(raw)); err == nil {
		t.Fatal("expected error for newer schema")
	}
}

// TestServerKindSelfDeclared: the closed llama.cpp set against everything
// else. The predicate is what "the engine named itself in /props" means at
// every recorder and card branch that cares (TTP-105, 2026-09-17): a kind
// outside the set is a name an engine gave — exllamav3 included — and a tape
// recorded before the predicate existed answers it correctly, because the
// set was already closed then.
func TestServerKindSelfDeclared(t *testing.T) {
	cases := map[ServerKind]bool{
		"":                       false,
		ServerUnknown:            false,
		ServerLlamaCPP:           false,
		ServerIKLlama:            false,
		ServerExLlamaV3:          true,
		ServerKind("mistral.rs"): true,
	}
	for k, want := range cases {
		if got := k.SelfDeclared(); got != want {
			t.Errorf("ServerKind(%q).SelfDeclared() = %v, want %v", k, got, want)
		}
	}
}

func TestSlugFromModel(t *testing.T) {
	cases := map[string]string{
		"/models/Qwen3.5-35B-A3B-UD-Q4_K_M.gguf":   "qwen3-5-35b-a3b-ud-q4-k",
		"DeepSeek-V4.1-Flash-Q3_K_M-engramQ8.gguf": "deepseek-v4-1-flash-q3-k",
		"weird   name!!.GGUF":                      "weird-name",
		"":                                         "model",
	}
	for in, want := range cases {
		if got := SlugFromModel(in); got != want {
			t.Errorf("SlugFromModel(%q) = %q, want %q", in, got, want)
		}
	}
}
