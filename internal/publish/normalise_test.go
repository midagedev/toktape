package publish

import (
	"testing"

	"github.com/midagedev/toktape/internal/tape"
)

// Every table has at least one case that must NOT normalise. Those are the
// cases that matter: a normaliser that never refuses is the defect this
// package exists to prevent (§9.4 — an unrecognised input must land on "",
// never on a plausible guess).

func TestQuantID(t *testing.T) {
	tests := []struct {
		in       string
		wantID   string
		wantBits float64
	}{
		// Sourced bits: block layouts from ggml-common.h (see quantBits).
		{"Q6_K", "q6_k", 6.5625},
		{"Q8_0", "q8_0", 8.5},
		{"Q4_0", "q4_0", 4.5},
		{"IQ4_XS", "iq4_xs", 4.25},
		{"IQ4_NL", "iq4_nl", 4.5},
		{"IQ1_S", "iq1_s", 1.5625},
		{"IQ2_XXS", "iq2_xxs", 2.0625},
		{"MXFP4", "mxfp4", 4.25},
		{"F16", "f16", 16},
		{"BF16", "bf16", 16},
		{"F32", "f32", 32},
		// Case and spacing tolerance come with the reused recognizer.
		{"q6_k", "q6_k", 6.5625},
		{" Q6_K ", "q6_k", 6.5625},

		// UD- dropped from the id, never making the input unrecognised:
		// UD-Q6_K and Q6_K are one quantisation to a search.
		{"UD-Q6_K", "q6_k", 6.5625},
		{"ud-q6_k", "q6_k", 6.5625},
		{"UD-IQ4_XS", "iq4_xs", 4.25},

		// Recognised mixes: honest 0 bits — the composition is per-model
		// and no single number is sourceable. The id still filters.
		{"Q4_K_M", "q4_k_m", 0},
		{"UD-Q4_K_M", "q4_k_m", 0},
		{"Q3_K_L", "q3_k_l", 0},
		{"IQ3_M", "iq3_m", 0},

		// Must NOT normalise.
		{"Q4", "", 0}, // a shape, but no llama.cpp type of that name
		{"q4", "", 0},
		{"Q99_X", "", 0}, // invented type
		{"F64", "", 0},   // not a weight format this table knows
		{"", "", 0},
		{"Q4_K_MER", "", 0}, // prefix of a type, not a type
	}
	for _, tt := range tests {
		gotID, gotBits := QuantID(tt.in)
		if gotID != tt.wantID || gotBits != tt.wantBits {
			t.Errorf("QuantID(%q) = (%q, %v), want (%q, %v)",
				tt.in, gotID, gotBits, tt.wantID, tt.wantBits)
		}
	}
}

func TestGPUID(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		// The contract's three examples.
		{"NVIDIA GeForce RTX 4090", "rtx-4090"},
		{"NVIDIA RTX A6000", "rtx-a6000"},
		{"NVIDIA H100 80GB HBM3", "h100-80gb-hbm3"},
		// Non-NVIDIA names carry no noise to drop and pass through.
		{"Apple M2 Max", "apple-m2-max"},
		// Runs of whitespace collapse to one "-".
		{"NVIDIA   GeForce\tRTX 5090", "rtx-5090"},

		// Must NOT normalise.
		{"", ""},
		{"   ", ""},
		{"NVIDIA GeForce", ""},     // nothing but vendor noise
		{"NVIDIA Corporation", ""}, // same, the suffix nvidia-smi prints
	}
	for _, tt := range tests {
		if got := GPUID(tt.in); got != tt.want {
			t.Errorf("GPUID(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestHostClass(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		want string
	}{
		{"four of one card", []string{"rtx-4090", "rtx-4090", "rtx-4090", "rtx-4090"}, "4x rtx-4090"},
		{"one card", []string{"rtx-4090"}, "1x rtx-4090"},
		// Must NOT normalise.
		{"mixed rig", []string{"rtx-4090", "rtx-a6000"}, ""},
		{"mixed rig reversed", []string{"rtx-a6000", "rtx-4090"}, ""},
		{"one card unnormalised", []string{"rtx-4090", ""}, ""},
		{"leading unnormalised", []string{"", "rtx-4090"}, ""},
		{"no gpus", nil, ""},
		{"empty list", []string{}, ""},
	}
	for _, tt := range tests {
		if got := HostClass(tt.in); got != tt.want {
			t.Errorf("HostClass(%v) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// TestModelID is the reconciliation the measurement of 2026-09-18 asked for:
// two quantizers of one model, disagreeing about how much of the naming
// convention they fold into general.basename, have to land on one id.
func TestModelID(t *testing.T) {
	cases := []struct {
		name                   string
		base, size, fine, want string
	}{{
		name: "unsloth: the size is already in the base name",
		base: "Qwen3-0.6B", size: "0.6B", want: "qwen3-0.6b",
	}, {
		name: "bartowski: the same model, the size appended",
		base: "Qwen3", size: "0.6B", want: "qwen3-0.6b",
	}, {
		name: "and the fine-tune reconciles the same way",
		base: "Qwen3-0.6B-Instruct", size: "0.6B", fine: "Instruct", want: "qwen3-0.6b-instruct",
	}, {
		name: "its counterpart, assembled in the convention's order",
		base: "Qwen3", size: "0.6B", fine: "Instruct", want: "qwen3-0.6b-instruct",
	}, {
		name: "a MoE label is one component run, matched as a whole",
		base: "Qwen3-30B-A3B-Instruct-2507", size: "30B-A3B", want: "qwen3-30b-a3b-instruct-2507",
	}, {
		// The parser hands base names back with hyphens as spaces.
		name: "spaces become hyphens",
		base: "gpt oss", size: "20b", want: "gpt-oss-20b",
	}, {
		name: "a version's dot survives",
		base: "Meta Llama 3.1", size: "8B", want: "meta-llama-3.1-8b",
	}, {
		name: "a size that only looks contained is still appended",
		base: "Qwen3-10.6B", size: "0.6B", want: "qwen3-10.6b-0.6b",
	}, {
		name: "an org-prefixed base name from a file name",
		base: "Qwen_Qwen3", size: "0.6B", want: "qwen-qwen3-0.6b",
	}, {
		name: "no base name, no id: search falls back to the raw string",
		base: "", size: "0.6B", want: "",
	}, {
		name: "a base name of nothing but punctuation is not a name",
		base: "--", size: "0.6B", want: "",
	}, {
		name: "a size label nobody recorded is simply absent",
		base: "Qwen3", size: "", want: "qwen3",
	}}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := tape.ModelInfo{BaseName: c.base, SizeLabel: c.size, FineTune: c.fine, NameSource: "gguf"}
			if got, _ := ModelID(m); got != c.want {
				t.Errorf("ModelID(%q, %q, %q) = %q, want %q", c.base, c.size, c.fine, got, c.want)
			}
		})
	}
}

// A tape with no convention parts is read through the file name, which is
// what every run published before TTP-119 carries (lead, 2026-09-19). The
// source says so, and a name outside the convention still gets nothing.
func TestModelIDFromTheFileName(t *testing.T) {
	for _, c := range []struct {
		name, file, wantID, wantSrc string
	}{
		// The same id a header would give: general.size_label for this model
		// is "35B-A3B", and gguf.NameFromFileName now reads the MoE half out
		// of the name too, so the two sources cannot split one model in the
		// dropdown (TTP-131, closed 2026-09-19).
		{"the convention in a file name", "Qwen3.6-35B-A3B-UD-Q6_K.gguf", "qwen3.6-35b-a3b", "filename"},
		{"a name outside it is not an id", "ggml-model-f16.gguf", "", ""},
		{"no name at all", "", "", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			id, src := ModelID(tape.ModelInfo{FileName: c.file})
			if id != c.wantID || src != c.wantSrc {
				t.Errorf("ModelID(FileName=%q) = %q/%q, want %q/%q", c.file, id, src, c.wantID, c.wantSrc)
			}
		})
	}
}

// The header wins when the tape has it: the file name is only consulted for
// a tape that recorded no parts, never as a second opinion about one that did.
func TestModelIDPrefersTheRecordedParts(t *testing.T) {
	m := tape.ModelInfo{BaseName: "Qwen3", SizeLabel: "0.6B", NameSource: "gguf", FileName: "Something-Else-70B-Q4_K_M.gguf"}
	if id, src := ModelID(m); id != "qwen3-0.6b" || src != "gguf" {
		t.Errorf("ModelID = %q/%q, want qwen3-0.6b/gguf", id, src)
	}
}
