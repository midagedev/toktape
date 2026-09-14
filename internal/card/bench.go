package card

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/midagedev/toktape/internal/tape"
)

// LlamaBenchTable renders the run as the markdown table llama-bench prints, so
// a toktape card can be dropped into a thread that is already comparing
// llama-bench numbers (docs/toktape-spec.ko.md §4, "하단에 llama-bench 호환
// markdown 표 한 블록을 붙여 기존 문화에 얹는다").
//
// Two rows come out of one run: pp<prompt_n> carries the server's
// prompt_per_second and tg<predicted_n> its predicted_per_second. pp counts the
// tokens the server actually processed, so a cached prefix is excluded — that
// is the same denominator the rate was computed over, and it is not "fixed" to
// the full prompt length.
//
// backend is not in the schema and is printed as "?" rather than inferred from
// the presence of a GPU. The t/s column carries no "± 0.00": a single run has
// no measured standard deviation.
func LlamaBenchTable(s *tape.RunSummary) string {
	if s == nil {
		s = &tape.RunSummary{}
	}
	model := benchModelName(s.Model)
	size := formatFileGiB(s.Model.FileBytes)
	params := formatParamsB(s.Model.Params)
	backend := unknown
	ngl := orUnknown(s.Server.Flags.NGL)
	// fa is one of the five always-printed flags, so it carries the FLAGS
	// row's distinction between "?" (no argv was read) and "default" (an argv
	// was read and did not set it). ngl is not one of the five and keeps "?":
	// the card omits it entirely when unset, and a llama-bench column cannot.
	fa := flagValue(s.Server.Flags.FlashAttn, argvObserved(s.Server))

	var b strings.Builder
	b.WriteString("| model | size | params | backend | ngl | fa | test | t/s |\n")
	b.WriteString("| --- | ---: | ---: | --- | ---: | --- | --- | ---: |\n")
	for _, r := range []struct {
		test string
		rate float64
	}{
		{"pp" + benchCount(s.Timings.PromptN), s.Timings.PromptPerSecond},
		{"tg" + benchCount(s.Timings.PredictedN), s.Timings.PredictedPerSecond},
	} {
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %s | %s | %s |\n",
			model, size, params, backend, ngl, fa, r.test, benchRate(r.rate))
	}
	// Above one stream both counts are per-stream means (TTP-83, 2026-09-14).
	// In llama-bench tg<N> is the tokens a test generated, and a mean of 10
	// and 300 is 155, a count no stream produced — so the table says it is a
	// mean. It keeps the mean rather than trading it for another count because
	// the t/s beside it is the mean of the per-stream rates: the count and the
	// rate on one row are over the same population.
	//
	// The statement is a line under the table, not words in the test cell
	// (lead, 2026-09-14). This table exists to be pasted into a thread that is
	// already comparing llama-bench results, and "test" is the column those
	// readers compare by eye and their scripts match as tg128; a cell reading
	// "tg307 (per-stream mean)" stops lining up with theirs. A footnote leaves
	// the column exactly llama-bench's and still travels with a pasted block.
	if n := streamsSent(s); n > 1 {
		fmt.Fprintf(&b, "\npp and tg are per-stream means over %d concurrent streams.\n", n)
	}
	return b.String()
}

// benchModelName follows llama-bench's "<arch> <quant>" column, falling back to
// the file name when the GGUF header was not read.
func benchModelName(m tape.ModelInfo) string {
	name := m.Arch
	if name == "" {
		name = m.Name
	}
	if name == "" {
		name = m.FileName
	}
	if m.Quant != "" {
		name = strings.TrimSpace(name + " " + m.Quant)
	}
	return orUnknown(name)
}

func benchCount(n int) string {
	if n <= 0 {
		return "0"
	}
	return strconv.Itoa(n)
}

func benchRate(v float64) string {
	if v <= 0 {
		return unknown
	}
	return strconv.FormatFloat(v, 'f', 2, 64)
}
