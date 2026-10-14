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
		// Read out of that name, because the fixture recorded no convention
		// parts (lead, 2026-09-19): the id and its source both arrive where
		// the old ModelID returned nothing. FAIL-first: this want fails on
		// the previous derivation, which had no file-name fallback.
		ModelID:       "deepseek-r1-distill-llama-70b",
		ModelIDSource: "filename",
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

		// The card's MEMORY block. The want grew the fields the example
		// tape already carries (GPUsAtEnd used bytes, Host.GPUs capacity,
		// Memory.AtEnd); no assertion was loosened.
		GPUMem: []GPUMem{
			{Index: 0, UsedBytes: 25555748454, VRAMBytes: 25769803776},
			{Index: 1, UsedBytes: 24481361100, VRAMBytes: 25769803776},
		},
		RSSBytes:     1288490189,
		RSSFileBytes: 858993459,
		RSSAnonBytes: 429496730,

		Sessions:      1,
		DecodePerSec:  17.4, // single stream: the stream's own rate
		PrefillPerSec: 610,
		TTFTp50Ms:     630, // single stream: Timings.TTFTMs, the Prefill row's TTFT

		// The figures behind a rate (extended 2026-09-19 for TTP-130 — the
		// want grew fields, no assertion was loosened).
		PromptN:       512, // Cache.PromptTotal, the card's "in" figure
		PredictedN:    320,
		CacheHitRatio: 0.25,
		CtxSize:       16384,
		NSlots:        8,
		FA:            "on",
		KVCache:       "q8_0", // ctk == ctv
		Batch:         "2048",
		UBatch:        "512",
		NGL:           "99",
		Offload:       "full", // two GPUs, no CPU device
		PowerW:        650,    // 340 + 310 over GPUs with PowerW > 0
		FileBytes:     45655502848,
		ActiveParams:  70553706496, // dense: the whole model
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
	// The figures behind a rate (TTP-130, 2026-09-19): none of them may be
	// invented on a tape that measured nothing.
	for _, tt := range []struct {
		field string
		isSet bool
	}{
		{"PromptN", idx.PromptN != 0},
		{"PredictedN", idx.PredictedN != 0},
		{"MinPredictedN", idx.MinPredictedN != 0},
		{"ReasoningN", idx.ReasoningN != 0},
		{"CacheHitRatio", idx.CacheHitRatio != 0},
		{"CtxSize", idx.CtxSize != 0},
		{"NSlots", idx.NSlots != 0},
		{"FA", idx.FA != ""},
		{"KVCache", idx.KVCache != ""},
		{"Batch", idx.Batch != ""},
		{"UBatch", idx.UBatch != ""},
		{"NGL", idx.NGL != ""},
		{"Offload", idx.Offload != ""},
		{"DraftModel", idx.DraftModel != ""},
		{"DraftAccept", idx.DraftAccept != 0},
		{"Throttled", idx.Throttled},
		{"Cold", idx.Cold},
		{"PowerW", idx.PowerW != 0},
		{"PowerLimitW", idx.PowerLimitW != 0},
		{"FileBytes", idx.FileBytes != 0},
		{"ActiveParams", idx.ActiveParams != 0},
		{"NExperts", idx.NExperts != 0},
		{"NExpertsUsed", idx.NExpertsUsed != 0},
	} {
		if tt.isSet {
			t.Errorf("%s is set on a zero tape", tt.field)
		}
	}
	if idx.CaveatCount != len(idx.Caveats) {
		t.Errorf("CaveatCount = %d, len(Caveats) = %d", idx.CaveatCount, len(idx.Caveats))
	}
	// ... and none of them may reach the wire, either: omitempty keeps an
	// unset field absent from the JSON, never a zero with a key.
	b, err := json.Marshal(idx)
	if err != nil {
		t.Fatalf("marshal zero index: %v", err)
	}
	for _, key := range []string{
		"prompt_n", "predicted_n", "min_predicted_n", "reasoning_n",
		"cache_hit_ratio", "ctx_size", "n_slots", "fa", "kv_cache",
		"batch", "ubatch", "ngl", "offload", "draft_model", "draft_accept",
		"throttled", "cold", "power_w", "power_limit_w", "file_bytes",
		"active_params", "n_experts", "n_experts_used",
		"gpu_mem", "rss_bytes", "rss_file_bytes", "rss_anon_bytes",
	} {
		if bytes.Contains(b, []byte(`"`+key+`"`)) {
			t.Errorf("unset field %q present in zero-tape JSON: %s", key, b)
		}
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
		"host_class", "gpu_mem", "rss_bytes", "rss_file_bytes", "rss_anon_bytes",
		"sessions", "prompt_set", "decode_per_sec",
		"prefill_per_sec", "ttft_p50_ms", "caveats", "caveat_count",
		"prompt_n", "predicted_n", "min_predicted_n", "reasoning_n",
		"cache_hit_ratio", "ctx_size", "n_slots", "fa", "kv_cache",
		"batch", "ubatch", "ngl", "offload", "draft_model", "draft_accept",
		"throttled", "cold", "power_w", "power_limit_w", "file_bytes",
		"active_params", "n_experts", "n_experts_used",
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
		// An exl3 model has no GGUF header, but its directory name follows
		// the same convention, and reading it is how such a run becomes
		// reachable on the model axis at all (lead, 2026-09-19). The source
		// says the id came from a name, which is the whole guard.
		name: "a non-GGUF engine: the repo, and an id read off the name",
		model: tape.ModelInfo{
			FileName: "Llama-3.1-8B-exl3", Format: "exl3",
			Repo: "turboderp/Llama-3.1-8B-exl3", RepoSource: "hf-cache",
		},
		repo: "turboderp/Llama-3.1-8B-exl3", id: "llama-3.1-8b", source: "filename",
	}, {
		// The guard that matters: a repo with nothing behind it was guessed,
		// and the search reads this field as exact.
		// The guard is about the repo alone: the id still comes, off the
		// file name, and says so (lead, 2026-09-19).
		name: "a repo with no source never reaches the row",
		model: tape.ModelInfo{
			FileName: "Qwen3-0.6B-Q8_0.gguf",
			Repo:     "unsloth/Qwen3-0.6B-GGUF",
		},
		id: "qwen3-0.6b", source: "filename",
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

// The figures behind a rate (TTP-130): every contract field derived from
// the tape side of the table, each pinned against a tape that carries it.
func TestIndexOfFigures(t *testing.T) {
	intptr := func(n int) *int { return &n }

	t.Run("prompt_n is the card's in figure, cache total first", func(t *testing.T) {
		s := tape.RunSummary{
			Cache:   tape.CacheSummary{PromptTotal: 600},
			Timings: tape.TimingsSummary{PromptN: 100, CacheN: 50},
		}
		if got := IndexOf(&tape.Tape{Summary: s}).PromptN; got != 600 {
			t.Errorf("PromptN = %d, want 600 (Cache.PromptTotal, not 100+50)", got)
		}
		bare := tape.RunSummary{Timings: tape.TimingsSummary{PromptN: 100, CacheN: 50}}
		if got, want := IndexOf(&tape.Tape{Summary: bare}).PromptN, card.PromptTokens(&bare); got != want || got != 150 {
			t.Errorf("PromptN = %d, want card.PromptTokens = %d = 150 (the PromptN+CacheN fallback)", got, want)
		}
	})

	t.Run("predicted, min and reasoning are copied", func(t *testing.T) {
		s := tape.RunSummary{
			Timings:   tape.TimingsSummary{PredictedN: 214, ReasoningN: 30},
			Aggregate: tape.AggregateTimings{MinPredictedN: 12},
		}
		idx := IndexOf(&tape.Tape{Summary: s})
		if idx.PredictedN != 214 || idx.MinPredictedN != 12 || idx.ReasoningN != 30 {
			t.Errorf("got predicted/min/reasoning = %d/%d/%d, want 214/12/30",
				idx.PredictedN, idx.MinPredictedN, idx.ReasoningN)
		}
	})

	t.Run("cache ratio, ctx and slots", func(t *testing.T) {
		s := tape.RunSummary{
			Cache:  tape.CacheSummary{HitRatio: 0.5},
			Server: tape.ServerInfo{CtxSize: 8192, NSlots: 4},
		}
		idx := IndexOf(&tape.Tape{Summary: s})
		if idx.CacheHitRatio != 0.5 || idx.CtxSize != 8192 || idx.NSlots != 4 {
			t.Errorf("got ratio/ctx/slots = %v/%d/%d, want 0.5/8192/4",
				idx.CacheHitRatio, idx.CtxSize, idx.NSlots)
		}
	})

	t.Run("fa, batch, ubatch and ngl travel verbatim", func(t *testing.T) {
		s := tape.RunSummary{
			Server: tape.ServerInfo{Flags: tape.ServerFlags{
				FlashAttn: "auto", Batch: "2048", UBatch: "512", NGL: "99",
			}},
		}
		idx := IndexOf(&tape.Tape{Summary: s})
		if idx.FA != "auto" || idx.Batch != "2048" || idx.UBatch != "512" || idx.NGL != "99" {
			t.Errorf("got fa/batch/ubatch/ngl = %q/%q/%q/%q, want auto/2048/512/99",
				idx.FA, idx.Batch, idx.UBatch, idx.NGL)
		}
	})

	t.Run("kv_cache joins the two cache types", func(t *testing.T) {
		for _, c := range []struct {
			name string
			k, v string
			want string
		}{
			{"agreeing pair is the one", "q8_0", "q8_0", "q8_0"},
			{"a split pair is k/v", "q8_0", "q4_0", "q8_0/q4_0"},
			{"only k is k", "q8_0", "", "q8_0"},
			{"only v is v", "", "q4_0", "q4_0"},
			{"neither is unknown", "", "", ""},
		} {
			t.Run(c.name, func(t *testing.T) {
				s := tape.RunSummary{
					Server: tape.ServerInfo{Flags: tape.ServerFlags{
						CacheTypeK: c.k, CacheTypeV: c.v,
					}},
				}
				if got := IndexOf(&tape.Tape{Summary: s}).KVCache; got != c.want {
					t.Errorf("KVCache = %q, want %q", got, c.want)
				}
			})
		}
	})

	t.Run("offload names where the weights sit", func(t *testing.T) {
		gpu := func() tape.DevicePlacement {
			return tape.DevicePlacement{Device: "GPU0", Classes: map[tape.TensorClass]int64{tape.ClassFFN: 100}}
		}
		for _, c := range []struct {
			name string
			devs []tape.DevicePlacement
			want string
		}{
			{"gpus and no cpu is full", []tape.DevicePlacement{gpu()}, "full"},
			{"gpus and weight bytes on cpu is partial", []tape.DevicePlacement{gpu(),
				{Device: tape.DeviceCPU, Classes: map[tape.TensorClass]int64{tape.ClassFFN: 50}}}, "partial"},
			// llama.cpp's token_embd lives on the host at every -ngl (lead,
			// 2026-09-19): embeddings alone on CPU is still a full offload.
			{"gpus and only embeddings on cpu is full", []tape.DevicePlacement{gpu(),
				{Device: tape.DeviceCPU, Classes: map[tape.TensorClass]int64{tape.ClassEmbed: 50}}}, "full"},
			{"no gpu is cpu", []tape.DevicePlacement{
				{Device: tape.DeviceCPU, Classes: map[tape.TensorClass]int64{tape.ClassFFN: 50}}}, "cpu"},
			{"no devices is unknown", nil, ""},
		} {
			t.Run(c.name, func(t *testing.T) {
				s := tape.RunSummary{Placement: tape.PlacementSummary{Devices: c.devs}}
				if got := IndexOf(&tape.Tape{Summary: s}).Offload; got != c.want {
					t.Errorf("Offload = %q, want %q", got, c.want)
				}
			})
		}
	})

	t.Run("draft_accept needs both halves and a drafted token", func(t *testing.T) {
		draft, accepted := 100, 62
		for _, c := range []struct {
			name string
			n, a *int
			want float64
		}{
			{"62 of 100 is 0.62", intptr(draft), intptr(accepted), 0.62},
			{"no draft count is unknown", nil, intptr(accepted), 0},
			{"no accepted count is unknown", intptr(draft), nil, 0},
			{"zero drafted is unknown, never a division", intptr(0), intptr(0), 0},
		} {
			t.Run(c.name, func(t *testing.T) {
				s := tape.RunSummary{Timings: tape.TimingsSummary{DraftN: c.n, DraftNAccepted: c.a}}
				if got := IndexOf(&tape.Tape{Summary: s}).DraftAccept; got != c.want {
					t.Errorf("DraftAccept = %v, want %v", got, c.want)
				}
			})
		}
	})

	t.Run("throttled fires on any gpu, power sums the readings", func(t *testing.T) {
		s := tape.RunSummary{GPUsAtEnd: []tape.GPUSample{
			{Index: 0, PowerW: 200, PowerLimitW: 300},
			{Index: 1, PowerW: 81, PowerLimitW: 150, Throttled: true},
			{Index: 2}, // no reading: contributes to neither sum
		}}
		idx := IndexOf(&tape.Tape{Summary: s})
		if !idx.Throttled {
			t.Error("Throttled = false, want true (one card throttled)")
		}
		// The card's narrow verdict, not the tape's wide bit (lead,
		// 2026-09-19): a mask that only says "sw power cap" is a card at
		// its own operating point, and the card prints "throttled: no".
		capped := tape.RunSummary{GPUsAtEnd: []tape.GPUSample{{Throttled: true, ThrottleMask: 1 << 10}}}
		if IndexOf(&tape.Tape{Summary: capped}).Throttled {
			t.Error("Throttled = true for a sw-power-cap-only mask; the card says no")
		}
		if idx.PowerW != 281 || idx.PowerLimitW != 450 {
			t.Errorf("power = %v of %v W, want 281 of 450", idx.PowerW, idx.PowerLimitW)
		}
		if got := IndexOf(&tape.Tape{}).Throttled; got {
			t.Error("Throttled on a tape with no readings, want false")
		}
	})

	t.Run("cold follows the cache label, draft model is the base name", func(t *testing.T) {
		s := tape.RunSummary{
			Cache:  tape.CacheSummary{Label: tape.CacheCold},
			Server: tape.ServerInfo{Flags: tape.ServerFlags{DraftModel: "tiny-draft"}},
		}
		idx := IndexOf(&tape.Tape{Summary: s})
		if !idx.Cold {
			t.Error("Cold = false, want true (the cache label says cold)")
		}
		if idx.DraftModel != "tiny-draft" {
			t.Errorf("DraftModel = %q, want the base name verbatim", idx.DraftModel)
		}
	})

	t.Run("file_bytes and the experts ride along", func(t *testing.T) {
		s := tape.RunSummary{Model: tape.ModelInfo{
			FileBytes: 45655502848, NExperts: 128, NExpertsUsed: 8,
		}}
		idx := IndexOf(&tape.Tape{Summary: s})
		if idx.FileBytes != 45655502848 || idx.NExperts != 128 || idx.NExpertsUsed != 8 {
			t.Errorf("got file/experts/used = %d/%d/%d, want 45655502848/128/8",
				idx.FileBytes, idx.NExperts, idx.NExpertsUsed)
		}
	})

	t.Run("active_params is the params touched per token", func(t *testing.T) {
		for _, c := range []struct {
			name  string
			model tape.ModelInfo
			want  int64
		}{
			{"dense is the whole model", tape.ModelInfo{Params: 70553706496}, 70553706496},
			{"moe scales by the byte share", tape.ModelInfo{
				Params: 100_000_000_000, FileBytes: 50_000_000_000,
				ActiveBytesPerToken: 10_000_000_000, NExperts: 128, NExpertsUsed: 8,
			}, 20_000_000_000},
			{"moe with no byte reading is unknown, not a fraction guess", tape.ModelInfo{
				Params: 100_000_000_000, FileBytes: 50_000_000_000, NExperts: 128, NExpertsUsed: 8,
			}, 0},
		} {
			t.Run(c.name, func(t *testing.T) {
				if got := IndexOf(&tape.Tape{Summary: tape.RunSummary{Model: c.model}}).ActiveParams; got != c.want {
					t.Errorf("ActiveParams = %d, want %d", got, c.want)
				}
			})
		}
	})
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

// TestIndexOfQuantFromFileName: the quant falls back to the file name only
// when the header was not read (TTP-119 naming, v0.7.4). The tape keeps what
// was observed; the index is where the name is read, so a re-publish or a
// reindex repairs a tape recorded before the recorder resolved the file.
func TestIndexOfQuantFromFileName(t *testing.T) {
	cases := []struct {
		name  string
		model tape.ModelInfo
		want  string
	}{
		// The real DGX Spark tape: bloomery, a relative -m, no quant observed.
		{"spark tape", tape.ModelInfo{FileName: "Qwen3.6-35B-A3B-Q4_K_M.gguf"}, "Q4_K_M"},
		{"UD multi-part name", tape.ModelInfo{FileName: "DeepSeek-V3-0324-UD-Q4_K_XL-00001-of-00004.gguf"}, "UD-Q4_K_XL"},
		// An observed quant outranks the name, whatever the name says.
		{"header quant kept", tape.ModelInfo{FileName: "Model-Q8_0.gguf", Quant: "Q4_K_M"}, "Q4_K_M"},
		// An engine's verbatim quant is an observation too.
		{"engine quant kept", tape.ModelInfo{FileName: "Model-Q8_0.gguf", Quant: "EXL3 4.05 bpw"}, "EXL3 4.05 bpw"},
		{"name without a quant token", tape.ModelInfo{FileName: "mystery.gguf"}, ""},
		{"no name", tape.ModelInfo{}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tp := &tape.Tape{Summary: tape.RunSummary{Model: tc.model}}
			if got := IndexOf(PublicView(tp, WithText)).QuantRaw; got != tc.want {
				t.Errorf("QuantRaw = %q, want %q", got, tc.want)
			}
			if tp.Summary.Model.Quant != tc.model.Quant {
				t.Errorf("the tape's own quant changed to %q: the index must not write back", tp.Summary.Model.Quant)
			}
		})
	}
}

// TestIndexOfNamesTheCardsTheServerUsed (TTP-209, 2026-10-12): the real clip
// ran on the A6000 of a 3090 + A6000 host, so it lands under the A6000 and
// not the 3090. A tape that never read the server's VRAM keeps both cards.
func TestIndexOfNamesTheCardsTheServerUsed(t *testing.T) {
	tp, err := tape.Read("../tape/testdata/clip-pinned-a6000.tape")
	if err != nil {
		t.Fatal(err)
	}
	idx := IndexOf(tp)
	if want := []string{"rtx-a6000"}; !reflect.DeepEqual(idx.GPUIDs, want) || idx.GPUID != "rtx-a6000" || idx.HostClass != "1x rtx-a6000" {
		t.Errorf("clip: gpu_ids %v gpu_id %q host_class %q, want the A6000 alone", idx.GPUIDs, idx.GPUID, idx.HostClass)
	}
	if want := []string{"NVIDIA RTX A6000"}; !reflect.DeepEqual(idx.GPUsRaw, want) {
		t.Errorf("clip: gpus_raw %v, want %v", idx.GPUsRaw, want)
	}
	tp.Summary.GPUsAtEnd = nil
	for i := range tp.Samples {
		tp.Samples[i].GPUs = nil
	}
	idx = IndexOf(tp)
	if len(idx.GPUIDs) != 2 || len(idx.GPUsRaw) != 2 || idx.GPUCount != 2 {
		t.Errorf("unknown use: gpu_ids %v gpus_raw %v count %d, want both cards", idx.GPUIDs, idx.GPUsRaw, idx.GPUCount)
	}
}

// TestIndexOfMemory pins the figures the card's MEMORY block prints
// (internal/card memorySection / vramLine). They are copied off the tape:
// one gpu_mem entry per GPUsAtEnd sample, vram from Host.GPUs by index and
// omitted at 0, RSS from Memory.AtEnd and omitted when RSSBytes is 0, and
// no gpu_mem on a unified host, where the card prints "Unified …" instead.
func TestIndexOfMemory(t *testing.T) {
	t.Run("two gpus and rss", func(t *testing.T) {
		// Host order is not sample order, and the second card's capacity
		// was not read: vram is matched by index, and 0 stays off the wire.
		s := tape.RunSummary{
			Host: tape.HostInfo{GPUs: []tape.GPUInfo{
				{Index: 1, Name: "NVIDIA GeForce RTX 3060", VRAMBytes: 0},
				{Index: 0, Name: "NVIDIA GeForce RTX 3060", VRAMBytes: 12884901888},
			}},
			GPUsAtEnd: []tape.GPUSample{
				{Index: 0, UsedBytes: 11164188672},
				{Index: 1, UsedBytes: 8589934592},
			},
			Memory: tape.MemorySummary{AtEnd: tape.MemSample{
				RSSBytes:     12456681472,
				RSSFileBytes: 11947323392,
				RSSAnonBytes: 263991296,
			}},
		}
		idx := IndexOf(&tape.Tape{Summary: s})
		want := []GPUMem{
			{Index: 0, UsedBytes: 11164188672, VRAMBytes: 12884901888},
			{Index: 1, UsedBytes: 8589934592},
		}
		if !reflect.DeepEqual(idx.GPUMem, want) {
			t.Errorf("GPUMem = %+v, want %+v", idx.GPUMem, want)
		}
		if idx.RSSBytes != 12456681472 || idx.RSSFileBytes != 11947323392 || idx.RSSAnonBytes != 263991296 {
			t.Errorf("RSS = %d / %d / %d, want 12456681472 / 11947323392 / 263991296",
				idx.RSSBytes, idx.RSSFileBytes, idx.RSSAnonBytes)
		}
		b, err := json.Marshal(idx)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		const gpuMem = `"gpu_mem":[{"index":0,"used_bytes":11164188672,"vram_bytes":12884901888},{"index":1,"used_bytes":8589934592}]`
		if !bytes.Contains(b, []byte(gpuMem)) {
			t.Errorf("gpu_mem JSON = %s, want it to contain %s", b, gpuMem)
		}
		for _, key := range []string{`"rss_bytes":12456681472`, `"rss_file_bytes":11947323392`, `"rss_anon_bytes":263991296`} {
			if !bytes.Contains(b, []byte(key)) {
				t.Errorf("missing %s in %s", key, b)
			}
		}
		// The unread capacity is absent, not a zero.
		if bytes.Contains(b, []byte(`"vram_bytes":0`)) {
			t.Errorf("vram_bytes 0 present in %s", b)
		}
	})

	t.Run("nothing measured", func(t *testing.T) {
		// A host inventory is not a reading. GPUsAtEnd empty and RSS 0
		// leave every memory key off the wire, even when a file byte was
		// filled in beside a zero RSS — the card prints Host RSS only when
		// RSSBytes > 0.
		s := tape.RunSummary{
			Host: tape.HostInfo{GPUs: []tape.GPUInfo{
				{Index: 0, Name: "NVIDIA GeForce RTX 3060", VRAMBytes: 12884901888},
			}},
			Memory: tape.MemorySummary{AtEnd: tape.MemSample{RSSFileBytes: 11947323392}},
		}
		b, err := json.Marshal(IndexOf(&tape.Tape{Summary: s}))
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		for _, key := range []string{"gpu_mem", "rss_bytes", "rss_file_bytes", "rss_anon_bytes"} {
			if bytes.Contains(b, []byte(`"`+key+`"`)) {
				t.Errorf("unset field %q present in %s", key, b)
			}
		}
	})

	t.Run("unified host", func(t *testing.T) {
		// The samples and the capacity are present, so a missing gpu_mem
		// is the unified rule (tape.Unified), not an empty reading. RSS
		// still travels: the card prints Host RSS on a unified host too.
		s := tape.RunSummary{
			Host: tape.HostInfo{
				GPUs: []tape.GPUInfo{{Index: 0, Name: "NVIDIA GB10", VRAMBytes: 128849018880}},
			},
			GPUsAtEnd: []tape.GPUSample{{Index: 0, UsedBytes: 11164188672}},
			Memory: tape.MemorySummary{AtEnd: tape.MemSample{
				RSSBytes:     12456681472,
				RSSFileBytes: 11947323392,
				RSSAnonBytes: 263991296,
			}},
		}
		if tape.Unified(s.Host) == "" {
			t.Fatal("fixture is not unified; the case proves nothing")
		}
		b, err := json.Marshal(IndexOf(&tape.Tape{Summary: s}))
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if bytes.Contains(b, []byte(`"gpu_mem"`)) {
			t.Errorf("gpu_mem present on a unified host: %s", b)
		}
		if !bytes.Contains(b, []byte(`"rss_bytes":12456681472`)) {
			t.Errorf("rss dropped on a unified host: %s", b)
		}
	})
}
