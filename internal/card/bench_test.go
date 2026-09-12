package card

import (
	"strings"
	"testing"

	"github.com/midagedev/toktape/internal/tape"
)

func TestLlamaBenchTable(t *testing.T) {
	got := LlamaBenchTable(Example())
	lines := strings.Split(strings.TrimSuffix(got, "\n"), "\n")
	if len(lines) != 4 {
		t.Fatalf("table has %d lines, want 4 (header, rule, pp, tg):\n%s", len(lines), got)
	}
	if lines[0] != "| model | size | params | backend | ngl | fa | test | t/s |" {
		t.Errorf("header = %q", lines[0])
	}
	// pp counts the tokens the server actually processed (prompt_n), not the
	// cached prefix, because that is the denominator of prompt_per_second.
	wantPP := "| qwen3moe UD-Q4_K_M | 19.83 GiB | 35.00 B | ? | 99 | on | pp112 | 2450.00 |"
	if lines[2] != wantPP {
		t.Errorf("pp row =\n%q\nwant\n%q", lines[2], wantPP)
	}
	wantTG := "| qwen3moe UD-Q4_K_M | 19.83 GiB | 35.00 B | ? | 99 | on | tg128 | 68.40 |"
	if lines[3] != wantTG {
		t.Errorf("tg row =\n%q\nwant\n%q", lines[3], wantTG)
	}
}

func TestLlamaBenchTableUnknowns(t *testing.T) {
	got := LlamaBenchTable(&tape.RunSummary{})
	lines := strings.Split(strings.TrimSuffix(got, "\n"), "\n")
	want := "| ? | ? | ? | ? | ? | ? | pp0 | ? |"
	if lines[2] != want {
		t.Errorf("pp row = %q, want %q", lines[2], want)
	}
	if strings.Contains(got, "±") {
		t.Error("a single run has no standard deviation to print")
	}
	if strings.Contains(got, "CUDA") {
		t.Error("backend must not be inferred")
	}
	if LlamaBenchTable(nil) == "" {
		t.Error("LlamaBenchTable(nil) should render the unknown table")
	}
}

func TestLlamaBenchTableFallsBackToFileName(t *testing.T) {
	s := &tape.RunSummary{Model: tape.ModelInfo{FileName: "mistral-7b.gguf"}}
	if !strings.Contains(LlamaBenchTable(s), "| mistral-7b.gguf |") {
		t.Errorf("model column should fall back to the file name:\n%s", LlamaBenchTable(s))
	}
}
