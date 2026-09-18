package publish

import "testing"

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
