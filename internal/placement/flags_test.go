package placement

import (
	"testing"

	"github.com/midagedev/toktape/internal/tape"
)

func TestParseNGL(t *testing.T) {
	tests := []struct {
		in     string
		wantN  int
		wantOK bool
	}{
		{"99", 99, true},
		{"0", 0, true},
		{" 40 ", 40, true},
		{"auto", NGLAll, true},
		{"all", NGLAll, true},
		{"-1", NGLAll, true},
		// Not observed, and not guessable.
		{"", 0, false},
		{"yes", 0, false},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			n, ok := ParseNGL(tc.in)
			if n != tc.wantN || ok != tc.wantOK {
				t.Errorf("ParseNGL(%q) = (%d, %v), want (%d, %v)", tc.in, n, ok, tc.wantN, tc.wantOK)
			}
		})
	}
}

func TestParseCPUMoE(t *testing.T) {
	tests := []struct {
		in      string
		wantAll bool
		wantN   int
		wantOK  bool
	}{
		{"-cmoe", true, 0, true},
		{"--cpu-moe", true, 0, true},
		{"cmoe", true, 0, true},
		{"-ncmoe 12", false, 12, true},
		{"--n-cpu-moe 12", false, 12, true},
		{"-ncmoe=12", false, 12, true},
		{"12", false, 12, true},
		{"", false, 0, false},
		{"-ncmoe", false, 0, false},
		{"-ncmoe x", false, 0, false},
		{"--flash-attn", false, 0, false},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			all, n, ok := ParseCPUMoE(tc.in)
			if all != tc.wantAll || n != tc.wantN || ok != tc.wantOK {
				t.Errorf("ParseCPUMoE(%q) = (%v, %d, %v), want (%v, %d, %v)",
					tc.in, all, n, ok, tc.wantAll, tc.wantN, tc.wantOK)
			}
		})
	}
}

func TestParseOverrides(t *testing.T) {
	rules, warnings := ParseOverrides([]string{
		`blk\.(1[0-9])\.ffn_.*_exps=CPU`,
		`blk\.0\.=CUDA0,blk\.1\.=CUDA1`,
		`attn_.*=Vulkan2`,
		// Host buffer-type variants are still the CPU.
		`blk\.2\.=CPU_REPACK`,
		`blk\.3\.=CUDA_Host`,
	})
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	wantDevices := []string{tape.DeviceCPU, "GPU0", "GPU1", "GPU2", tape.DeviceCPU, tape.DeviceCPU}
	if len(rules) != len(wantDevices) {
		t.Fatalf("got %d rules, want %d: %+v", len(rules), len(wantDevices), rules)
	}
	for i, want := range wantDevices {
		if rules[i].Device != want {
			t.Errorf("rule %d device = %q, want %q", i, rules[i].Device, want)
		}
	}
	// Unanchored, like llama.cpp's std::regex_search.
	if !rules[0].Matches("blk.15.ffn_down_exps.weight") {
		t.Error("rule 0 should match blk.15.ffn_down_exps.weight")
	}
	if rules[0].Matches("blk.5.ffn_down_exps.weight") {
		t.Error("rule 0 should not match blk.5.ffn_down_exps.weight")
	}
}

func TestParseOverridesBadInput(t *testing.T) {
	tests := []struct {
		name  string
		entry string
	}{
		{"no equals sign", `blk\.0\.`},
		{"unknown buffer type", `blk\.0\.=NVME`},
		{"RE2 rejects lookahead", `(?!blk)\.=CPU`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rules, warnings := ParseOverrides([]string{tc.entry})
			if len(rules) != 0 {
				t.Errorf("got %d rules, want 0", len(rules))
			}
			if len(warnings) != 1 {
				t.Fatalf("got %d warnings, want 1: %v", len(warnings), warnings)
			}
			t.Logf("warning: %s", warnings[0])
		})
	}
}

func TestCPUMoEOverridesMatchUpstreamPatterns(t *testing.T) {
	// -cmoe: only the stacked per-expert weights, never the router or the
	// shared expert (llama.cpp common/common.h, LLM_FFN_EXPS_REGEX).
	all := cpuMoEOverrides(true, 0)
	if len(all) != 1 {
		t.Fatalf("-cmoe produced %d rules, want 1", len(all))
	}
	shouldMatch := []string{
		"blk.0.ffn_down_exps.weight",
		"blk.39.ffn_gate_up_exps.weight",
		"blk.7.ffn_up_chexps.weight",
	}
	shouldNotMatch := []string{
		"blk.0.ffn_gate_inp.weight",
		"blk.0.ffn_up_shexp.weight",
		"blk.0.attn_q.weight",
	}
	for _, n := range shouldMatch {
		if !all[0].Matches(n) {
			t.Errorf("-cmoe should match %q", n)
		}
	}
	for _, n := range shouldNotMatch {
		if all[0].Matches(n) {
			t.Errorf("-cmoe should not match %q", n)
		}
	}

	// -ncmoe 2: one rule per layer, and "blk.1" must not catch "blk.12".
	n2 := cpuMoEOverrides(false, 2)
	if len(n2) != 2 {
		t.Fatalf("-ncmoe 2 produced %d rules, want 2", len(n2))
	}
	if !n2[1].Matches("blk.1.ffn_down_exps.weight") {
		t.Error("-ncmoe rule 1 should match blk.1.ffn_down_exps.weight")
	}
	if n2[1].Matches("blk.12.ffn_down_exps.weight") {
		t.Error("-ncmoe rule 1 must not match blk.12.ffn_down_exps.weight")
	}
}
