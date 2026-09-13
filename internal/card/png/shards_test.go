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
// one, and the same run with a variant directory long enough to threaten the
// four-column footer grid.
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
		"footer.col0.row1": "9 shards",
		"footer.col0.row2": "engramQ8-tokembdBF16",
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

// TestVariantTag pins what the footer prints for the variant: the part of the
// directory the model name above it does not already say.
func TestVariantTag(t *testing.T) {
	cases := []struct {
		name string
		m    tape.ModelInfo
		want string
	}{
		{
			"the shared stem comes off",
			card.ExampleSharded().Model,
			"engramQ8-tokembdBF16",
		}, {
			"an unrelated directory stands as it is",
			tape.ModelInfo{FileName: "DeepSeek-V4.1-Flash-00001-of-00009.gguf", Dir: "nvme1", Shards: 9},
			"nvme1",
		}, {
			"nothing recorded stays nothing",
			tape.ModelInfo{FileName: "DeepSeek-V4.1-Flash-00001-of-00009.gguf", Shards: 9},
			"",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := variantTag(c.m); got != c.want {
				t.Errorf("variantTag = %q, want %q", got, c.want)
			}
		})
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
