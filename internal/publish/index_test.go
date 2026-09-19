package publish

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/midagedev/toktape/internal/card"
	"github.com/midagedev/toktape/internal/tape"
)

// The fixtures are the card's own examples — the same rig the TUI, the PNG
// and the golden tests draw, so the index is pinned against a run whose
// figures are internally consistent by construction.

func TestIndexOfSingleStream(t *testing.T) {
	idx := IndexOf(&tape.Tape{Summary: *card.Example()})

	want := Index{
		Schema:         IndexSchema,
		ToktapeVersion: "0.1.0",
		RecordedAt:     time.Date(2026, 9, 13, 14, 25, 30, 0, time.UTC),

		ModelRaw: "DeepSeek-R1-Distill-Llama-70B-Q4_K_M.gguf",
		// ModelID: empty. The card's fixture carries no naming-convention
		// fields, and the id is assembled from those alone — never from the
		// file name sitting right there (TTP-119). TestIndexOfModelIdentity
		// covers the tapes that do carry them.
		Params:    70553706496,
		QuantRaw:  "Q4_K_M",
		QuantID:   "q4_k_m",
		QuantBits: 0, // a mix: recognised, bits honestly unknown

		EngineKind:    "llama-server",
		EngineVersion: "b3650",

		OS:      "linux",
		GPUsRaw: []string{"NVIDIA GeForce RTX 3090", "NVIDIA GeForce RTX 3090"},
		GPUID:   "rtx-3090",
		// The membership axis beside the shared id: one entry per card.
		// (Extended 2026-09-19 for the new GPUIDs field — the want grew a
		// field, no assertion was loosened.)
		GPUIDs:    []string{"rtx-3090", "rtx-3090"},
		GPUCount:  2,
		VRAMBytes: 2 * 25769803776,
		RAMBytes:  68719476736,
		HostClass: "2x rtx-3090",

		Sessions:      1,
		DecodePerSec:  17.4, // single stream: the stream's own rate
		PrefillPerSec: 610,
		TTFTp50Ms:     630, // single stream: Timings.TTFTMs, the Prefill row's TTFT
	}
	if !reflect.DeepEqual(idx, want) {
		t.Errorf("IndexOf(single-stream example):\n got  %+v\n want %+v", idx, want)
	}
}

func TestIndexOfConcurrent(t *testing.T) {
	s := card.ExampleConcurrent()
	// The per-stream mean TTFT is NOT the figure a concurrent run carries
	// (TTP-110) — set it apart so the choice is pinned, not accidentally
	// right because both fields agree.
	s.Timings.TTFTMs = 999

	idx := IndexOf(&tape.Tape{Summary: *s})

	if idx.Sessions != 8 {
		t.Errorf("Sessions = %d, want 8", idx.Sessions)
	}
	// Above one stream the figures are the server-wide aggregate (headlineRate,
	// decodeParts/prefillParts, TTP-59), never the per-stream mean.
	if idx.DecodePerSec != 72.9 {
		t.Errorf("DecodePerSec = %v, want 72.9 (the aggregate, not the 9.1 each)", idx.DecodePerSec)
	}
	if idx.PrefillPerSec != 2927 {
		t.Errorf("PrefillPerSec = %v, want 2927 (the aggregate, not the 610 each)", idx.PrefillPerSec)
	}
	// The median over the streams, not the mean of times that started together.
	if idx.TTFTp50Ms != 810 {
		t.Errorf("TTFTp50Ms = %v, want 810 (Aggregate.TTFTp50Ms, not Timings.TTFTMs=999)", idx.TTFTp50Ms)
	}
}

func TestIndexOfZeroTape(t *testing.T) {
	idx := IndexOf(&tape.Tape{}) // must not panic, and must not invent

	if idx.Schema != IndexSchema {
		t.Errorf("Schema = %d, want %d", idx.Schema, IndexSchema)
	}
	for _, tt := range []struct {
		field, got string
	}{
		{"ModelRaw", idx.ModelRaw},
		{"ModelID", idx.ModelID},
		{"ModelDir", idx.ModelDir},
		{"QuantRaw", idx.QuantRaw},
		{"QuantID", idx.QuantID},
		{"EngineKind", idx.EngineKind},
		{"EngineVersion", idx.EngineVersion},
		{"OS", idx.OS},
		{"GPUID", idx.GPUID},
		{"HostClass", idx.HostClass},
		{"PromptSet", idx.PromptSet},
	} {
		if tt.got != "" {
			t.Errorf("%s = %q, want \"\" on a zero tape", tt.field, tt.got)
		}
	}
	if idx.QuantBits != 0 {
		t.Errorf("QuantBits = %v, want 0", idx.QuantBits)
	}
	if len(idx.GPUsRaw) != 0 || idx.GPUCount != 0 || idx.VRAMBytes != 0 || idx.RAMBytes != 0 {
		t.Errorf("host fields on a zero tape: %+v", idx)
	}
	if idx.CaveatCount != len(idx.Caveats) {
		t.Errorf("CaveatCount = %d, len(Caveats) = %d", idx.CaveatCount, len(idx.Caveats))
	}
}

// A caveat computed by the card must arrive on the row as its code — the
// card's own qualification, not a second derivation of it here.
func TestIndexOfCaveatsTravel(t *testing.T) {
	s := tape.RunSummary{
		Concurrency: 1,
		Timings:     tape.TimingsSummary{PredictedN: 10, DecodeLabel: "decode"},
	}
	idx := IndexOf(&tape.Tape{Summary: s})

	found := false
	for _, c := range idx.Caveats {
		if c == "short_generation" {
			found = true
		}
	}
	if !found {
		t.Errorf("Caveats = %v, want short_generation among them (10 tokens < %d)", idx.Caveats, tape.MinDecodeTokens)
	}
	if idx.CaveatCount != len(idx.Caveats) {
		t.Errorf("CaveatCount = %d, len(Caveats) = %d", idx.CaveatCount, len(idx.Caveats))
	}
}

// The fallbacks: a name with no file shape, a version with no build, and a
// rig with two kinds of card — all observed, none normalised, nothing guessed.
func TestIndexOfFallbacks(t *testing.T) {
	s := tape.RunSummary{
		Server: tape.ServerInfo{Kind: tape.ServerIKLlama, Commit: "a1b2c3d"},
		Model: tape.ModelInfo{
			Name:     "R1 Distill Llama 70B",
			Quant:    "Q6_K",
			NExperts: 128,
		},
		Host: tape.HostInfo{
			OS: "darwin",
			GPUs: []tape.GPUInfo{
				{Name: "NVIDIA GeForce RTX 4090", VRAMBytes: 24},
				{Name: "Apple M2 Max", VRAMBytes: 32},
			},
		},
		Concurrency: 1,
	}
	idx := IndexOf(&tape.Tape{Summary: s})

	if idx.ModelRaw != "R1 Distill Llama 70B" {
		t.Errorf("ModelRaw = %q, want the general.name when no file name was recorded", idx.ModelRaw)
	}
	// A general.name is not a file name and carries no convention, so there
	// is nothing to assemble an id out of. The raw string above is what
	// search has, and that is the honest amount.
	if idx.ModelID != "" {
		t.Errorf("ModelID = %q, want \"\" for a model with no naming-convention fields", idx.ModelID)
	}
	if !idx.MoE {
		t.Errorf("MoE = false, want true (128 experts)")
	}
	if idx.QuantID != "q6_k" || idx.QuantBits != 6.5625 {
		t.Errorf("QuantID = (%q, %v), want (q6_k, 6.5625)", idx.QuantID, idx.QuantBits)
	}
	if idx.EngineKind != "ik_llama.cpp" {
		t.Errorf("EngineKind = %q, want ik_llama.cpp", idx.EngineKind)
	}
	if idx.EngineVersion != "a1b2c3d" {
		t.Errorf("EngineVersion = %q, want the commit when no build was reported", idx.EngineVersion)
	}
	// A mixed rig: no class, no gpu_id, the raw names kept for search.
	if idx.HostClass != "" || idx.GPUID != "" {
		t.Errorf("HostClass = %q, GPUID = %q, want \"\"/\"\" on a mixed rig", idx.HostClass, idx.GPUID)
	}
	if len(idx.GPUsRaw) != 2 || idx.GPUsRaw[0] != "NVIDIA GeForce RTX 4090" || idx.GPUsRaw[1] != "Apple M2 Max" {
		t.Errorf("GPUsRaw = %v, want both raw names", idx.GPUsRaw)
	}
	if idx.GPUCount != 2 || idx.VRAMBytes != 56 {
		t.Errorf("GPUCount/VRAMBytes = %d/%d, want 2/56", idx.GPUCount, idx.VRAMBytes)
	}
}

// The Worker relies on omitempty: an unset field is ABSENT from the JSON, not
// a zero with a key — a row and its absence must be tellable apart on the
// server, where "" and 0 are the raw strings search falls back to.
func TestIndexJSONRoundTrip(t *testing.T) {
	sparse, err := json.Marshal(Index{Schema: IndexSchema})
	if err != nil {
		t.Fatalf("marshal sparse: %v", err)
	}
	absent := []string{
		"toktape_version", "repo", "model_raw", "model_id", "model_id_source",
		"model_dir", "params", "moe",
		"quant_raw", "quant_id", "quant_bits", "engine_kind", "engine_version",
		"os", "gpus_raw", "gpu_id", "gpu_ids", "gpu_count", "vram_bytes", "ram_bytes",
		"host_class", "sessions", "prompt_set", "decode_per_sec",
		"prefill_per_sec", "ttft_p50_ms", "caveats", "caveat_count",
	}
	for _, key := range absent {
		if bytes.Contains(sparse, []byte(`"`+key+`"`)) {
			t.Errorf("unset field %q present in JSON: %s", key, sparse)
		}
	}
	for _, key := range []string{`"schema":1`, `"recorded_at"`} {
		if !bytes.Contains(sparse, []byte(key)) {
			t.Errorf("%s missing from JSON: %s", key, sparse)
		}
	}

	// A full row survives the round trip unchanged.
	row := IndexOf(&tape.Tape{Summary: *card.Example()})
	b, err := json.Marshal(row)
	if err != nil {
		t.Fatalf("marshal row: %v", err)
	}
	var back Index
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatalf("unmarshal row: %v", err)
	}
	if !reflect.DeepEqual(row, back) {
		t.Errorf("round trip changed the row:\n got  %+v\n want %+v\n json %s", back, row, string(b))
	}
	if !strings.Contains(string(b), `"quant_raw":"Q4_K_M"`) {
		// The raw beside the normalised: on a mix the id travels with its
		// bits empty and the exact sub-type still readable.
		t.Errorf("quant_raw missing beside quant_id in %s", string(b))
	}
}

// TestIndexOfModelIdentity covers the two facets the row carries for "which
// model": the exact repo when a tape proved one, and the convention's slug
// otherwise. They are independent — a run can have either, both or neither.
func TestIndexOfModelIdentity(t *testing.T) {
	cases := []struct {
		name  string
		model tape.ModelInfo

		repo, id, source string
	}{{
		name: "a file from a Hugging Face cache: both facets",
		model: tape.ModelInfo{
			FileName: "Qwen3-0.6B-UD-Q6_K_XL.gguf",
			Repo:     "unsloth/Qwen3-0.6B-GGUF", RepoSource: "hf-cache",
			BaseName: "Qwen3-0.6B", SizeLabel: "0.6B", NameSource: "gguf",
		},
		repo: "unsloth/Qwen3-0.6B-GGUF", id: "qwen3-0.6b", source: "gguf",
	}, {
		name: "the same model from a plain directory: the slug alone",
		model: tape.ModelInfo{
			FileName: "Qwen_Qwen3-0.6B-Q4_K_M.gguf",
			BaseName: "Qwen3", SizeLabel: "0.6B", NameSource: "gguf",
		},
		id: "qwen3-0.6b", source: "gguf",
	}, {
		// The --url case: no header was readable, so the name is all there is.
		name: "an id read out of a file name says so",
		model: tape.ModelInfo{
			FileName: "Qwen3-0.6B-Q8_0.gguf",
			BaseName: "Qwen3", SizeLabel: "0.6B", NameSource: "filename",
		},
		id: "qwen3-0.6b", source: "filename",
	}, {
		// An exl3 model has no GGUF and no convention, but its directory can
		// still sit under a snapshot.
		name: "a non-GGUF engine: the repo without a slug",
		model: tape.ModelInfo{
			FileName: "Llama-3.1-8B-exl3", Format: "exl3",
			Repo: "turboderp/Llama-3.1-8B-exl3", RepoSource: "hf-cache",
		},
		repo: "turboderp/Llama-3.1-8B-exl3",
	}, {
		// The guard that matters: a repo with nothing behind it was guessed,
		// and the search reads this field as exact.
		name: "a repo with no source never reaches the row",
		model: tape.ModelInfo{
			FileName: "Qwen3-0.6B-Q8_0.gguf",
			Repo:     "unsloth/Qwen3-0.6B-GGUF",
		},
	}}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			idx := IndexOf(&tape.Tape{Summary: tape.RunSummary{Model: c.model}})
			if idx.Repo != c.repo {
				t.Errorf("Repo = %q, want %q", idx.Repo, c.repo)
			}
			if idx.ModelID != c.id {
				t.Errorf("ModelID = %q, want %q", idx.ModelID, c.id)
			}
			if idx.ModelIDSource != c.source {
				t.Errorf("ModelIDSource = %q, want %q", idx.ModelIDSource, c.source)
			}
			// The raw string is always there for search to fall back on.
			if idx.ModelRaw != c.model.FileName {
				t.Errorf("ModelRaw = %q, want %q", idx.ModelRaw, c.model.FileName)
			}
		})
	}
}

// GPUIDs is the membership axis a mixed rig is reachable by (TTP-124,
// 2026-09-19): one id per card in card order, empty ids dropped, while
// GPUID/HostClass stay exactly as they were for their other consumers.
func TestIndexOfGPUIDs(t *testing.T) {
	cases := []struct {
		name string
		gpus []tape.GPUInfo
		want []string
		id   string
	}{{
		name: "a mixed rig keeps one id per card and no shared id",
		gpus: []tape.GPUInfo{
			{Name: "NVIDIA RTX A6000"},
			{Name: "NVIDIA GeForce RTX 3090"},
		},
		want: []string{"rtx-a6000", "rtx-3090"},
	}, {
		name: "a single kind repeats its id once per card",
		gpus: []tape.GPUInfo{
			{Name: "NVIDIA GeForce RTX 4090"},
			{Name: "NVIDIA GeForce RTX 4090"},
		},
		want: []string{"rtx-4090", "rtx-4090"},
		id:   "rtx-4090",
	}, {
		name: "a name that was only vendor noise is not a card",
		gpus: []tape.GPUInfo{
			{Name: "NVIDIA GeForce RTX 4090"},
			{Name: "NVIDIA"},
		},
		want: []string{"rtx-4090"},
	}}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			idx := IndexOf(&tape.Tape{Summary: tape.RunSummary{
				Host: tape.HostInfo{GPUs: c.gpus},
			}})
			if !reflect.DeepEqual(idx.GPUIDs, c.want) {
				t.Errorf("GPUIDs = %v, want %v", idx.GPUIDs, c.want)
			}
			if idx.GPUID != c.id {
				t.Errorf("GPUID = %q, want %q", idx.GPUID, c.id)
			}
		})
	}
}

// The prompt set is passed through, never re-derived: it decides what a run is
// comparable with, and a second opinion here would be a second owner of it.
func TestIndexOfPromptSet(t *testing.T) {
	idx := IndexOf(&tape.Tape{Summary: tape.RunSummary{PromptSet: "prompts@v1"}})
	if idx.PromptSet != "prompts@v1" {
		t.Errorf("PromptSet = %q, want prompts@v1", idx.PromptSet)
	}
	// A run on the user's own prompts is not in a comparison set, and the row
	// must say that rather than name one it did not run.
	if got := IndexOf(&tape.Tape{}).PromptSet; got != "" {
		t.Errorf("PromptSet = %q on a tape that named none, want \"\"", got)
	}
}
