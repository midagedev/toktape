package gguf

import (
	"path/filepath"
	"testing"
)

// TestNameFromFileName is the measurement of 2026-09-18 written down: what
// the upstream convention's parser does with names people actually publish.
// The refusals matter as much as the hits — a name outside the convention has
// to come back empty, because the whole reason to use the upstream parser is
// that it refuses instead of guessing (TTP-119).
func TestNameFromFileName(t *testing.T) {
	cases := []struct {
		file string
		base string
		size string
	}{
		// bartowski prefixes the org onto the file name; the convention reads
		// it as part of the base name. That disagrees with the same model's
		// header ("Qwen3"), which is exactly why the header wins.
		{"Qwen_Qwen3-0.6B-Q4_K_M.gguf", "Qwen_Qwen3", "0.6B"},
		{"Qwen3-0.6B-Q8_0.gguf", "Qwen3", "0.6B"},
		// A quantizer's own tag sits where the encoding should be. The base
		// name and the size label survive it, which is all this reads.
		{"Qwen3-0.6B-UD-Q6_K_XL.gguf", "Qwen3", "0.6B"},
		{"Qwen3-30B-A3B-Instruct-2507-UD-Q4_K_XL.gguf", "Qwen3", "30B-A3B"},
		// The MoE half of the size label, which the upstream parser keeps or
		// drops depending on the quantizer's tag and nothing else (lead,
		// 2026-09-19): the first two come back "35B" from it, and
		// withActiveSize reads the rest out of the name so one model cannot
		// hold two ids. FAIL-first: both failed before that function existed.
		{"Qwen3.6-35B-A3B-UD-Q6_K.gguf", "Qwen3.6", "35B-A3B"},
		{"Qwen3-35B-A3B-UD-Q6_K.gguf", "Qwen3", "35B-A3B"},
		{"Qwen3.6-35B-A3B-Q6_K.gguf", "Qwen3.6", "35B-A3B"},
		// A dense model's label is returned untouched: there is no "-A…"
		// after it to read.
		{"Meta-Llama-3.1-70B-Instruct-Q5_K_M.gguf", "Meta Llama 3.1", "70B"},
		// The convention separates base-name words with hyphens and the
		// parser gives them back as spaces; normalisation, not this, puts
		// them back.
		{"gpt-oss-20b-mxfp4.gguf", "gpt oss", "20b"},
		{"Meta-Llama-3.1-8B-Instruct-Q4_K_M.gguf", "Meta Llama 3.1", "8B"},
		// Refusals: a shard suffix behind a quantizer tag, and two names with
		// no size label at all.
		{"DeepSeek-R1-0528-UD-IQ1_S-00001-of-00004.gguf", "", ""},
		{"ggml-model-f16.gguf", "", ""},
		{"stories15M-q4_0.gguf", "", ""},
		{"", "", ""},
	}
	for _, c := range cases {
		t.Run(c.file, func(t *testing.T) {
			base, size := NameFromFileName(c.file)
			if base != c.base || size != c.size {
				t.Errorf("NameFromFileName(%q) = %q / %q, want %q / %q",
					c.file, base, size, c.base, c.size)
			}
		})
	}
}

// TestModelInfoNaming pins where each naming field comes from. The two header
// cases are the real ones measured that day: unsloth writes the size into the
// base name as well, bartowski does not, and both are recorded verbatim —
// reconciling them is the index's job, not the recorder's.
func TestModelInfoNaming(t *testing.T) {
	cases := []struct {
		name string
		file string
		kvs  []ggufKV

		base, size, fine, source string
		quantizedBy, repoURL     string
	}{{
		name: "unsloth: the header wins, size included in the base name",
		file: "Qwen3-0.6B-UD-Q6_K_XL.gguf",
		kvs: []ggufKV{
			{"general.basename", ggufTypeString, "Qwen3-0.6B"},
			{"general.size_label", ggufTypeString, "0.6B"},
			{"general.quantized_by", ggufTypeString, "Unsloth"},
			{"general.repo_url", ggufTypeString, "https://huggingface.co/unsloth"},
		},
		base: "Qwen3-0.6B", size: "0.6B", source: "gguf",
		quantizedBy: "Unsloth", repoURL: "https://huggingface.co/unsloth",
	}, {
		// The file name here would parse to "Qwen_Qwen3", so this case also
		// proves the header is read rather than the name.
		name: "bartowski: the header wins over an org-prefixed file name",
		file: "Qwen_Qwen3-0.6B-Q4_K_M.gguf",
		kvs: []ggufKV{
			{"general.basename", ggufTypeString, "Qwen3"},
			{"general.size_label", ggufTypeString, "0.6B"},
			{"general.finetune", ggufTypeString, "Instruct"},
		},
		base: "Qwen3", size: "0.6B", fine: "Instruct", source: "gguf",
	}, {
		name: "an older file with none of the keys falls back to its name",
		file: "Qwen3-0.6B-Q8_0.gguf",
		kvs:  nil,
		base: "Qwen3", size: "0.6B", source: "filename",
	}, {
		name: "a name outside the convention leaves every field empty",
		file: "ggml-model-f16.gguf",
		kvs:  nil,
	}}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), c.file)
			kvs := append([]ggufKV{{"general.architecture", ggufTypeString, "qwen3"}}, c.kvs...)
			writeGGUF(t, path, kvs, synFixtureTensors())

			info, _, err := ModelInfo(path)
			if err != nil {
				t.Fatalf("ModelInfo: %v", err)
			}
			if info.BaseName != c.base || info.SizeLabel != c.size || info.FineTune != c.fine {
				t.Errorf("base/size/finetune = %q / %q / %q, want %q / %q / %q",
					info.BaseName, info.SizeLabel, info.FineTune, c.base, c.size, c.fine)
			}
			if info.NameSource != c.source {
				t.Errorf("NameSource = %q, want %q", info.NameSource, c.source)
			}
			if info.QuantizedBy != c.quantizedBy || info.RepoURL != c.repoURL {
				t.Errorf("quantized_by/repo_url = %q / %q, want %q / %q",
					info.QuantizedBy, info.RepoURL, c.quantizedBy, c.repoURL)
			}
			// Nothing here proves a repo, and the recorder is the only thing
			// that reads a path for one.
			if info.Repo != "" || info.RepoSource != "" {
				t.Errorf("Repo/RepoSource = %q / %q, want empty", info.Repo, info.RepoSource)
			}
		})
	}
}
