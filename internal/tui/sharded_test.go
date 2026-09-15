package tui

import (
	"strings"
	"testing"

	"github.com/midagedev/toktape/internal/card"
	"github.com/midagedev/toktape/internal/tape"
)

// TestModelNameDropsThePartMarker: the title bar names the model, and a split
// set's part one is not a different model from part nine (TTP-32, 2026-09-13).
//
// 2026-09-15 (user: "모델이 다 실제값으로 찍혀야해"): the first case asserted
// "the GGUF's own name still wins" — exactly the behaviour the user rejected,
// because a re-quantised variant keeps the base model's header and every
// variant of one set shares it. The case is inverted and the test now points
// at the owner, card.ModelName, which the bar prints via ModelNameQuant; the
// tui-side rendering is pinned by TestResultModalNamesTheModelThatRan.
func TestModelNameDropsThePartMarker(t *testing.T) {
	cases := []struct {
		name string
		mi   tape.ModelInfo
		want string
	}{
		{
			// Inverted 2026-09-15: the variant directory wins, not the header.
			"the variant directory, not the header's name",
			card.ExampleSharded().Model,
			"DeepSeek-V4.1-Flash-engramQ8-tokembdBF16",
		}, {
			"a split set with no directory recorded falls back to the stem",
			tape.ModelInfo{FileName: "DeepSeek-V4.1-Flash-00001-of-00009.gguf", Shards: 9},
			"DeepSeek-V4.1-Flash",
		}, {
			// Unchanged: every non-sharded title is what it always was.
			"a single file keeps its whole name",
			tape.ModelInfo{FileName: "DeepSeek-R1-Distill-Llama-70B-Q4_K_M.gguf"},
			"DeepSeek-R1-Distill-Llama-70B-Q4_K_M",
		}, {
			"nothing observed",
			tape.ModelInfo{},
			"",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := card.ModelName(c.mi); got != c.want {
				t.Errorf("ModelName = %q, want %q", got, c.want)
			}
		})
	}
}

// realVariantModel is the recording the 2026-09-15 change exists for (user:
// "모델이 다 실제값으로 찍혀야해"): a re-quantised DeepSeek-V4.1-Flash variant at
// Q3_K_M whose GGUF header still carries the base model's general.name
// "DeepSeek V4.1 Flash", so only the directory tells which model ran.
func realVariantModel() tape.ModelInfo {
	return tape.ModelInfo{
		Path:      "/models/DeepSeek-V4.1-Flash-Q3_K_M-engramQ8-tokembdBF16/DeepSeek-V4.1-Flash-Q3_K_M-00001-of-00009.gguf",
		FileName:  "DeepSeek-V4.1-Flash-Q3_K_M-00001-of-00009.gguf",
		Dir:       "DeepSeek-V4.1-Flash-Q3_K_M-engramQ8-tokembdBF16",
		Shards:    9,
		Name:      "DeepSeek V4.1 Flash",
		Quant:     "Q3_K_M",
		FileBytes: 473000000000, // 440.49 GiB across the nine parts
	}
}

// TestResultModalNamesTheModelThatRan (2026-09-15, user: "모델이 다 실제값으로
// 찍혀야해"): a re-quantised variant's GGUF header still carries the base
// model's general.name, so the modal's title rule has to name the variant
// directory the file sits in — and the quant segment is omitted when the name
// already carries it, because "Q3_K_M · Q3_K_M" reads as a mistake while
// "DeepSeek V4.1 Flash" names a model that did not run.
func TestResultModalNamesTheModelThatRan(t *testing.T) {
	const variant = "DeepSeek-V4.1-Flash-Q3_K_M-engramQ8-tokembdBF16"
	m := goldenModel(t, doneAt)
	m.Mode = ModeCard
	// Settled (CardAge = GleamSweep): the state the clip rests on, and the one
	// whose widths the fit assertion below is about.
	m.CardAge = GleamSweep
	m.Summary.Model = realVariantModel()

	frame := View(m, doneAt, 120, 36)
	checkFrame(t, frame, 120, 36)
	plain := card.StripANSI(frame)
	// fmtG(473000000000) = 440.52 GiB, printed as the rounded "441G".
	for _, want := range []string{variant, "441G"} {
		if !strings.Contains(plain, want) {
			t.Errorf("the modal does not carry %q:\n%s", want, plain)
		}
	}
	for _, bad := range []string{"Q3_K_M · Q3_K_M", "DeepSeek V4.1 Flash"} {
		if strings.Contains(plain, bad) {
			t.Errorf("the frame printed %q:\n%s", bad, plain)
		}
	}
	// The title stays beside the date at the supported widths (boxW 86): the
	// variant name and the size are both present whole, so the truncate that
	// guards the rule never reached either.
	// And the live title bar names the same model under its own rule.
	if row := strings.Split(plain, "\n")[0]; !strings.Contains(row, variant) {
		t.Errorf("the title bar row does not name the variant:\n%s", row)
	}
}
