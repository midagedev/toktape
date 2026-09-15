package png

import (
	"image"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/midagedev/toktape/internal/card"
	"github.com/midagedev/toktape/internal/tape"
)

// shardedFixtures are the split-set summaries this file runs over: the plain
// one, the same run with a variant directory long enough to wrap inside the
// footer column, and the real 2026-09-15 recording whose directory carries the
// quant in its stem.
func shardedFixtures() map[string]*tape.RunSummary {
	long := card.ExampleSharded()
	long.Model.Dir = "DeepSeek-V4.1-Flash-engramQ8-tokembdBF16-rev3-nvme1-hardlink"
	return map[string]*tape.RunSummary{
		"sharded":          card.ExampleSharded(),
		"sharded-long-dir": long,
	}
}

// TestShardedFooterNamesTheVariant is the TTP-32 contract for the picture: two
// hard-linked variants of one model share general.name and every file name, so
// the directory has to be on the card or the two runs are indistinguishable.
func TestShardedFooterNamesTheVariant(t *testing.T) {
	c, err := renderCanvas(card.ExampleSharded())
	if err != nil {
		t.Fatalf("renderCanvas: %v", err)
	}
	want := map[string]string{
		// 2026-09-15 (user: "모델이 다 실제값으로 찍혀야해"): the variant directory
		// is row 0 itself now — the model that ran — where it used to sit in
		// the size row as a tag under a general.name headline. FAIL-first: the
		// old rows printed "DeepSeek V4.1 Flash" and "… GiB · engramQ8-tokembdBF16".
		"footer.col0.row0": "DeepSeek-V4.1-Flash-engramQ8-tokembdBF16",
		"footer.col0.row1": "9 shards",
		"footer.col0.row2": "400.00 GiB",
		"header.model":     "DeepSeek-V4.1-Flash-engramQ8-tokembdBF16",
		"header.modelsub":  "9 shards",
	}
	for id, sub := range want {
		m, ok := c.markByID(id)
		if !ok {
			t.Errorf("%s was never drawn", id)
			continue
		}
		if !strings.Contains(m.Text, sub) {
			t.Errorf("%s = %q, want it to carry %q", id, m.Text, sub)
		}
	}
	// The part name itself is the one thing that must not be the headline: it
	// is the same string for both variants.
	if m, ok := c.markByID("header.model"); ok && strings.Contains(m.Text, "-00001-of-00009") {
		t.Errorf("header.model = %q, still the bare part name", m.Text)
	}
	// 2026-09-15: the header's general.name must not reach any mark.
	for _, m := range c.marks {
		if m.Kind == "text" && strings.Contains(m.Text, "DeepSeek V4.1 Flash") {
			t.Errorf("%s printed the header's general.name: %q", m.ID, m.Text)
		}
	}
}

// TestSingleFileFooterUnchanged: card.ModelLabel is the file name for a model
// that is one file and shardsPart is empty, so every non-sharded card renders
// the strings it always did.
func TestSingleFileFooterUnchanged(t *testing.T) {
	s := card.Example()
	c, err := renderCanvas(s)
	if err != nil {
		t.Fatalf("renderCanvas: %v", err)
	}
	if m, _ := c.markByID("header.model"); m.Text != s.Model.FileName {
		t.Errorf("header.model = %q, want the file name %q", m.Text, s.Model.FileName)
	}
	// 2026-09-15 (user: "모델이 다 실제값으로 찍혀야해"): row 0 names the file's
	// stem, not the header's "R1 Distill Llama 70B" — the file is the model
	// that ran, and its own name already carries the quant. FAIL-first: the
	// old row 0 printed the general.name.
	if m, _ := c.markByID("footer.col0.row0"); m.Text != card.ModelStem(s.Model.FileName) {
		t.Errorf("footer.col0.row0 = %q, want the file stem %q", m.Text, card.ModelStem(s.Model.FileName))
	}
	if m, _ := c.markByID("footer.col0.row1"); m.Text != s.Model.Quant {
		t.Errorf("footer.col0.row1 = %q, want the bare quant %q", m.Text, s.Model.Quant)
	}
	for _, id := range []string{"header.model", "header.modelsub", "footer.col0.row1", "footer.col0.row2"} {
		if m, _ := c.markByID(id); strings.Contains(m.Text, "shard") {
			t.Errorf("%s = %q claims shards for a single file", id, m.Text)
		}
	}
}

// TestShardedStaysInsideTheGrid: the frame does not grow for a split set, and
// the two rows that gained an element must still sit in their column.
func TestShardedStaysInsideTheGrid(t *testing.T) {
	for name, s := range shardedFixtures() {
		t.Run(name, func(t *testing.T) {
			img, err := Render(s)
			if err != nil {
				t.Fatalf("Render: %v", err)
			}
			if got, want := img.Bounds(), image.Rect(0, 0, Width, Height); got != want {
				t.Fatalf("bounds = %v, want %v — the frame moved for a split set", got, want)
			}
			c, err := renderCanvas(s)
			if err != nil {
				t.Fatalf("renderCanvas: %v", err)
			}
			left, right := contentL, contentL+footerColW
			for _, part := range []string{"label", "row0", "row1", "row2", "row3"} {
				m, ok := c.markByID(colID(0, part))
				if !ok {
					t.Fatalf("%s was never drawn", colID(0, part))
				}
				if m.Rect.Min.X < left || m.Rect.Max.X > right {
					t.Errorf("%s (%q) spans x=%d..%d, outside its column (%d..%d)",
						m.ID, m.Text, m.Rect.Min.X, m.Rect.Max.X, left, right)
				}
			}
			for _, m := range c.marks {
				if m.Kind != "text" || m.Text == "" {
					continue
				}
				if m.Rect.Min.X < contentL || m.Rect.Max.X > contentR {
					t.Errorf("%s (%q) spans x=%d..%d, outside the content box (%d..%d)",
						m.ID, m.Text, m.Rect.Min.X, m.Rect.Max.X, contentL, contentR)
				}
			}
		})
	}
}

// TestWriteShardedCard drops the split-set card in testdata/out beside the
// others for the lead's visual check.
func TestWriteShardedCard(t *testing.T) {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", outDir, err)
	}
	path := filepath.Join(outDir, "card-sharded.png")
	if err := Write(path, card.ExampleSharded()); err != nil {
		t.Fatalf("Write(%s): %v", path, err)
	}
	abs, _ := filepath.Abs(path)
	t.Logf("wrote %s", abs)
}

// TestVariantTag was deleted on 2026-09-15 along with variantTag itself
// (user: "모델이 다 실제값으로 찍혀야해"): the footer's row 0 is now the whole
// variant directory — card.ModelName — so the tag that used to repeat the part
// of it a general.name headline did not say would print the variant twice.

// realRecordingModel is the run the 2026-09-15 change exists for (user:
// "모델이 다 실제값으로 찍혀야해"): a re-quantised DeepSeek-V4.1-Flash variant at
// Q3_K_M whose GGUF header still carries the base model's general.name
// "DeepSeek V4.1 Flash", so only the directory tells which model ran.
func realRecordingModel() tape.ModelInfo {
	return tape.ModelInfo{
		Path:      "/models/DeepSeek-V4.1-Flash-Q3_K_M-engramQ8-tokembdBF16/DeepSeek-V4.1-Flash-Q3_K_M-00001-of-00009.gguf",
		FileName:  "DeepSeek-V4.1-Flash-Q3_K_M-00001-of-00009.gguf",
		Dir:       "DeepSeek-V4.1-Flash-Q3_K_M-engramQ8-tokembdBF16",
		Shards:    9,
		Name:      "DeepSeek V4.1 Flash",
		Arch:      "deepseek2",
		Quant:     "Q3_K_M",
		FileBytes: 473000000000, // 440.52 GiB across the nine parts
		Params:    671030000000,
		NLayers:   61,
		NExperts:  256, NExpertsUsed: 8,
	}
}

// TestFooterModelColumnNamesTheModelThatRan (2026-09-15, user: "모델이 다
// 실제값으로 찍혀야해"): the footer's model column names the model that ran —
// the variant directory, not the GGUF header's general.name. The directory is
// wider than the 346 px column (376 px measured), so it wraps at the last "-"
// that lets row 0 fit and the two rows it frees spend as one joined row; rows
// 0 and 1 together still spell the whole name.
func TestFooterModelColumnNamesTheModelThatRan(t *testing.T) {
	s := card.ExampleSharded()
	s.Model = realRecordingModel()
	c, err := renderCanvas(s)
	if err != nil {
		t.Fatalf("renderCanvas: %v", err)
	}

	name := card.ModelName(s.Model)
	row0, ok := c.markByID(colID(0, "row0"))
	if !ok {
		t.Fatalf("%s was never drawn", colID(0, "row0"))
	}
	row1, ok := c.markByID(colID(0, "row1"))
	if !ok {
		t.Fatalf("%s was never drawn", colID(0, "row1"))
	}
	if joined := row0.Text + row1.Text; joined != name && !strings.HasSuffix(joined, ellipsis) {
		t.Errorf("rows 0+1 spell %q, want the whole name %q (or a reported '…' cut)",
			joined, name)
	} else if strings.HasSuffix(joined, ellipsis) {
		t.Errorf("the name was cut to %q + %q — report the measured widths", row0.Text, row1.Text)
	}
	// The separator the split keeps is at the end of row 0, so the name reads
	// as one string folded, not as two glued words.
	if !strings.HasSuffix(row0.Text, "-") && !strings.HasSuffix(row0.Text, "_") && !strings.HasSuffix(row0.Text, ".") {
		t.Errorf("row 0 = %q does not end on the separator it split at", row0.Text)
	}

	left, right := contentL, contentL+footerColW
	for _, part := range []string{"label", "row0", "row1", "row2", "row3"} {
		m, ok := c.markByID(colID(0, part))
		if !ok {
			t.Fatalf("%s was never drawn", colID(0, part))
		}
		if m.Rect.Min.X < left || m.Rect.Max.X > right {
			t.Errorf("%s (%q) spans x=%d..%d, outside its column (%d..%d)",
				m.ID, m.Text, m.Rect.Min.X, m.Rect.Max.X, left, right)
		}
	}

	// The joined row keeps the quant and the size — the two things the column
	// exists to say — and the architecture row stays the last row. The size is
	// formatFileGiB(473000000000) = "440.52 GiB".
	row2, _ := c.markByID(colID(0, "row2"))
	for _, want := range []string{"Q3_K_M", "440.52 GiB"} {
		if !strings.Contains(row2.Text, want) {
			t.Errorf("the joined row = %q lost %q", row2.Text, want)
		}
	}
	if row3, _ := c.markByID(colID(0, "row3")); !strings.Contains(row3.Text, "deepseek2") {
		t.Errorf("the architecture row = %q is not where it always was", row3.Text)
	}

	// The header's general.name must appear on no mark on the card.
	for _, m := range c.marks {
		if m.Kind == "text" && strings.Contains(m.Text, "DeepSeek V4.1 Flash") {
			t.Errorf("%s printed the header's general.name: %q", m.ID, m.Text)
		}
	}
}

// TestShardedModelColumnIsNotTruncated is the reason the rows were rearranged:
// the variant is the whole point of the split-set card, and an ellipsis in the
// middle of it says nothing. A 59-character directory is longer than the column
// and is expected to cut; the rig's own 40-character one must not.
func TestShardedModelColumnIsNotTruncated(t *testing.T) {
	c, err := renderCanvas(card.ExampleSharded())
	if err != nil {
		t.Fatalf("renderCanvas: %v", err)
	}
	for _, part := range []string{"row0", "row1", "row2", "row3"} {
		m, ok := c.markByID(colID(0, part))
		if !ok {
			t.Fatalf("%s was never drawn", colID(0, part))
		}
		if strings.HasSuffix(m.Text, ellipsis) {
			t.Errorf("%s was truncated: %q", m.ID, m.Text)
		}
	}
}
