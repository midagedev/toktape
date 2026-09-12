package placement

import "testing"

func TestQuantFromFileName(t *testing.T) {
	tests := []struct {
		file string
		want string
	}{
		// The richer tag in the file name wins: file_type 15 cannot tell an
		// Unsloth dynamic quant from a plain Q4_K_M.
		{"Qwen3.5-35B-A3B-UD-Q4_K_M.gguf", "UD-Q4_K_M"},
		{"Llama-3.1-8B-Instruct-IQ4_NL.gguf", "IQ4_NL"},
		{"Llama-3.1-8B-Instruct-Q8_0.gguf", "Q8_0"},
		{"DeepSeek-V3-Q3_K_M-00001-of-00009.gguf", "Q3_K_M"},
		{"Mistral-7B-v0.1.Q4_K_M.gguf", "Q4_K_M"},
		{"gemma-3-27b-it-Q4_0_4_4.gguf", "Q4_0_4_4"},
		{"gpt-oss-20b-MXFP4.gguf", "MXFP4"},
		{"TriLM-3.9B-TQ2_0.gguf", "TQ2_0"},
		{"Phi-3-mini-4k-instruct-F16.gguf", "F16"},
		{"Phi-3-mini-4k-instruct-BF16.gguf", "BF16"},
		// Lowercase in the wild is normalised; that is the only rewrite.
		{"qwen2-7b-q5_k_m.gguf", "Q5_K_M"},
		// A full path is fine.
		{"/models/gguf/Qwen3-32B-Q6_K.gguf", "Q6_K"},
		// No suffix at all.
		{"Qwen3-32B-Q6_K", "Q6_K"},

		// No tag: return "" so the card prints "?" or falls back to the enum.
		{"ggml-model.gguf", ""},
		{"Meta-Llama-3-8B-Instruct.gguf", ""},
		{"", ""},
	}
	for _, tc := range tests {
		t.Run(tc.file, func(t *testing.T) {
			if got := QuantFromFileName(tc.file); got != tc.want {
				t.Errorf("QuantFromFileName(%q) = %q, want %q", tc.file, got, tc.want)
			}
		})
	}
}

func TestQuantFromFileType(t *testing.T) {
	tests := []struct {
		ft   uint32
		want string
	}{
		{0, "F32"},
		{1, "F16"},
		{7, "Q8_0"},
		{15, "Q4_K_M"},
		{25, "IQ4_NL"},
		{32, "BF16"},
		{38, "MXFP4"},
		// Unknown enum value: "" so nothing wrong is printed.
		{999, ""},
	}
	for _, tc := range tests {
		if got := QuantFromFileType(tc.ft); got != tc.want {
			t.Errorf("QuantFromFileType(%d) = %q, want %q", tc.ft, got, tc.want)
		}
	}
}
