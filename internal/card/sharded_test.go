package card

import (
	"strings"
	"testing"

	"github.com/midagedev/toktape/internal/tape"
)

// shardedModel is the rig-log model set (TTP-32, 2026-09-13): two variants of
// one model, hard-linked so every part has the same file name, telling each
// other apart only by the directory they sit in.
func shardedModel() tape.ModelInfo {
	return tape.ModelInfo{
		Path:      "/models/DeepSeek-V4.1-Flash-engramQ8-tokembdBF16/DeepSeek-V4.1-Flash-00001-of-00009.gguf",
		FileName:  "DeepSeek-V4.1-Flash-00001-of-00009.gguf",
		Dir:       "DeepSeek-V4.1-Flash-engramQ8-tokembdBF16",
		Shards:    9,
		Quant:     "Q4_K_M",
		FileBytes: 412316860416,
	}
}

// TestShardedModelLineNamesTheVariant is the TTP-32 contract: the MODEL line of
// a sharded set prints the variant directory and the shard count, because the
// file name alone is the same string for both variants on the rig.
func TestShardedModelLineNamesTheVariant(t *testing.T) {
	s := &tape.RunSummary{Concurrency: 1, Model: shardedModel()}
	got := modelLineOf(t, Text(s))
	for _, want := range []string{"DeepSeek-V4.1-Flash-engramQ8-tokembdBF16", "9 shards", "Q4_K_M"} {
		if !strings.Contains(got, want) {
			t.Errorf("MODEL line %q does not contain %q", got, want)
		}
	}
	if strings.Contains(got, "-00001-of-00009.gguf") {
		t.Errorf("MODEL line %q still prints the bare part name", got)
	}
}

// TestSingleFileModelLineUnchanged: a model that is one file must render the
// way it always has, so every existing golden stays put.
func TestSingleFileModelLineUnchanged(t *testing.T) {
	got := modelLineOf(t, Text(Example()))
	if !strings.Contains(got, "DeepSeek-R1-Distill-Llama-70B-Q4_K_M.gguf") {
		t.Errorf("MODEL line %q lost the file name of a single-file model", got)
	}
	if strings.Contains(got, "shards") {
		t.Errorf("MODEL line %q claims shards for a single file", got)
	}
}

// modelLineOf returns the MODEL row of a rendered card, continuation lines
// joined, so a wrapped line is still one string to assert against.
func modelLineOf(t *testing.T, cardText string) string {
	t.Helper()
	var out []string
	for _, line := range strings.Split(cardText, "\n") {
		body := strings.TrimSuffix(strings.TrimPrefix(line, "│ "), " │")
		switch {
		case strings.HasPrefix(body, "MODEL"):
			out = append(out, strings.TrimSpace(strings.TrimPrefix(body, "MODEL")))
		case len(out) > 0 && strings.HasPrefix(body, strings.Repeat(" ", blockLabelW)):
			out = append(out, strings.TrimSpace(body))
		case len(out) > 0:
			return strings.Join(out, " ")
		}
	}
	if len(out) == 0 {
		t.Fatalf("no MODEL line in:\n%s", cardText)
	}
	return strings.Join(out, " ")
}

func TestModelStem(t *testing.T) {
	cases := []struct{ file, want string }{
		{"DeepSeek-V4.1-Flash-00001-of-00009.gguf", "DeepSeek-V4.1-Flash"},
		{"DeepSeek-V4.1-Flash-00009-of-00009.gguf", "DeepSeek-V4.1-Flash"},
		// Not a part marker, so only the extension goes.
		{"DeepSeek-R1-Distill-Llama-70B-Q4_K_M.gguf", "DeepSeek-R1-Distill-Llama-70B-Q4_K_M"},
		{"Model-0001-of-0009.gguf", "Model-0001-of-0009"},
		{"model", "model"},
		{"", ""},
	}
	for _, c := range cases {
		t.Run(c.file, func(t *testing.T) {
			if got := ModelStem(c.file); got != c.want {
				t.Errorf("ModelStem(%q) = %q, want %q", c.file, got, c.want)
			}
		})
	}
}

func TestShardCount(t *testing.T) {
	cases := []struct {
		file string
		want int
	}{
		{"DeepSeek-V4.1-Flash-00001-of-00009.gguf", 9},
		{"Qwen3-235B-A22B-UD-Q4_K_XL-00003-of-00012.gguf", 12},
		// A set of one is one file with a long name; "1 shards" is noise.
		{"Model-00001-of-00001.gguf", 0},
		{"Model-Q4_K_M.gguf", 0},
		{"", 0},
	}
	for _, c := range cases {
		t.Run(c.file, func(t *testing.T) {
			if got := ShardCount(c.file); got != c.want {
				t.Errorf("ShardCount(%q) = %d, want %d", c.file, got, c.want)
			}
		})
	}
}

func TestModelLabel(t *testing.T) {
	cases := []struct {
		name string
		m    tape.ModelInfo
		want string
	}{
		{
			// The rig-log shape: the directory already opens with the stem, so
			// printing both would repeat the model's name twice.
			"variant directory carries the stem",
			shardedModel(),
			"DeepSeek-V4.1-Flash-engramQ8-tokembdBF16",
		}, {
			"unrelated directory is joined to the stem",
			tape.ModelInfo{FileName: "DeepSeek-V4.1-Flash-00001-of-00009.gguf", Dir: "nvme1", Shards: 9},
			"nvme1/DeepSeek-V4.1-Flash",
		}, {
			"no directory recorded leaves the stem alone",
			tape.ModelInfo{FileName: "DeepSeek-V4.1-Flash-00001-of-00009.gguf", Shards: 9},
			"DeepSeek-V4.1-Flash",
		}, {
			// Not sharded: the label is the file name, byte for byte, which is
			// what keeps every existing golden where it is.
			"a single file is its own label",
			Example().Model,
			"DeepSeek-R1-Distill-Llama-70B-Q4_K_M.gguf",
		}, {
			// Unknown stays unknown: orUnknown turns this into "?".
			"nothing observed",
			tape.ModelInfo{},
			"",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ModelLabel(c.m); got != c.want {
				t.Errorf("ModelLabel = %q, want %q", got, c.want)
			}
		})
	}
}

// TestModelName is the 2026-09-15 contract (user: "모델이 다 실제값으로 찍혀야해"):
// the name a surface prints is the model that actually ran — the file's stem,
// or the variant directory a shard set sits in — and never the GGUF header's
// general.name, because a re-quantised variant keeps the base model's header.
func TestModelName(t *testing.T) {
	cases := []struct {
		name      string
		m         tape.ModelInfo
		want      string
		wantQuant string
	}{
		{
			// The real recording the change exists for: nine parts of a Q3_K_M
			// re-quant inside a variant directory whose stem carries the quant.
			"the real recording: the variant directory, not the header",
			tape.ModelInfo{
				Path:     "/models/DeepSeek-V4.1-Flash-Q3_K_M-engramQ8-tokembdBF16/DeepSeek-V4.1-Flash-Q3_K_M-00001-of-00009.gguf",
				FileName: "DeepSeek-V4.1-Flash-Q3_K_M-00001-of-00009.gguf",
				Dir:      "DeepSeek-V4.1-Flash-Q3_K_M-engramQ8-tokembdBF16",
				Shards:   9,
				Name:     "DeepSeek V4.1 Flash",
				Quant:    "Q3_K_M",
			},
			"DeepSeek-V4.1-Flash-Q3_K_M-engramQ8-tokembdBF16",
			// The quant is already inside the name and is not printed twice.
			"DeepSeek-V4.1-Flash-Q3_K_M-engramQ8-tokembdBF16",
		}, {
			// A single file with a header name: the file, not the header.
			"the file wins over the header for a single file",
			Example().Model,
			"DeepSeek-R1-Distill-Llama-70B-Q4_K_M",
			"DeepSeek-R1-Distill-Llama-70B-Q4_K_M",
		}, {
			// The quant already inside, in the other case: lower-case in the
			// file name against the server's upper-case spelling.
			"a lower-case quant in the name is not appended twice",
			tape.ModelInfo{FileName: "qwen3-30b-a3b-q4_k_m.gguf", Quant: "Q4_K_M"},
			"qwen3-30b-a3b-q4_k_m",
			"qwen3-30b-a3b-q4_k_m",
		}, {
			// No file was recorded, so the header's name is the only
			// observation left — and the quant joins it.
			"no file recorded: general.name is all that was observed",
			tape.ModelInfo{Name: "DeepSeek V4.1 Flash", Quant: "Q3_K_M"},
			"DeepSeek V4.1 Flash",
			"DeepSeek V4.1 Flash Q3_K_M",
		}, {
			// Nothing was observed at all.
			"nothing observed",
			tape.ModelInfo{},
			"",
			// Even a quant that somehow arrived alone does not print: callers
			// omit the whole segment when the name is unknown.
			"",
		}, {
			// The one shape that appends: a quant the name does not carry.
			"a quant the name does not carry is appended",
			tape.ModelInfo{FileName: "gpt-oss-120b.gguf", Quant: "Q8_0"},
			"gpt-oss-120b",
			"gpt-oss-120b Q8_0",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ModelName(c.m); got != c.want {
				t.Errorf("ModelName = %q, want %q", got, c.want)
			}
			if got := ModelNameQuant(c.m); got != c.wantQuant {
				t.Errorf("ModelNameQuant = %q, want %q", got, c.wantQuant)
			}
		})
	}
}

// TestShardedGolden pins the sharded fixture's card and JSON. It is a separate
// test from TestTextGolden so the two tracks touching this package do not both
// edit card_test.go.
func TestShardedGolden(t *testing.T) {
	golden(t, "example-sharded.txt", []byte(Text(ExampleSharded())))
	b, err := JSON(ExampleSharded())
	if err != nil {
		t.Fatalf("JSON: %v", err)
	}
	golden(t, "example-sharded.json", b)
}

// TestShardedBorderColumn is the width gate of docs/toktape-spec.ko.md §8.3
// applied to the shard form: the MODEL line grew two elements and a long
// variant directory must wrap, never push the right border.
func TestShardedBorderColumn(t *testing.T) {
	longDir := *ExampleSharded()
	// 60 characters, wider than the 63 columns the MODEL gutter leaves once a
	// quant and a size sit beside it, so the line has to wrap.
	longDir.Model.Dir = "DeepSeek-V4.1-Flash-engramQ8-tokembdBF16-rev3-nvme-hardlink1"
	if len(longDir.Model.Dir) != 60 {
		t.Fatalf("the long-directory fixture is %d characters, not the 60 the case is about", len(longDir.Model.Dir))
	}
	for name, s := range map[string]*tape.RunSummary{"sharded": ExampleSharded(), "long-dir": &longDir} {
		t.Run(name, func(t *testing.T) {
			out := Text(s)
			for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
				if got := Width(line); got != CardWidth {
					t.Fatalf("line %q is %d columns wide, want %d", line, got, CardWidth)
				}
			}
			// wrapJoin never drops a part, so the quant, the size and the shard
			// count are all still on the card however long the label is.
			for _, want := range []string{"9 shards", s.Model.Quant, "400.0 GiB"} {
				if !strings.Contains(out, want) {
					t.Errorf("%q did not survive the wrap:\n%s", want, out)
				}
			}
		})
	}
	// The rig's own 40-character directory fits whole on the label line; the
	// 60-character one is a single part wider than the gutter and is truncated
	// with an ellipsis rather than pushing the border, which is what field
	// documents and what the card width above proves it did.
	if out := Text(ExampleSharded()); !strings.Contains(out, ExampleSharded().Model.Dir) {
		t.Errorf("the 40-character variant directory did not fit whole:\n%s", out)
	}
	if out := Text(&longDir); !strings.Contains(out, "…") {
		t.Errorf("a 60-character variant directory was not truncated:\n%s", out)
	}
}
