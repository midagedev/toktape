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
// one, and the same run with a variant directory long enough that the model
// line's preferred spelling does not fit and the fallback chain has to work.
func shardedFixtures() map[string]*tape.RunSummary {
	long := card.ExampleSharded()
	long.Model.Dir = "DeepSeek-V4.1-Flash-engramQ8-tokembdBF16-rev3-nvme1-hardlink"
	return map[string]*tape.RunSummary{
		"sharded":          card.ExampleSharded(),
		"sharded-long-dir": long,
	}
}

// TestShardedIdentityNamesTheVariant is the TTP-32 contract for the picture
// (re-pinned on the identity band, 2026-09-19): two hard-linked variants of
// one model share general.name and every file name, so the directory has to
// be on the card or the two runs are indistinguishable. The model's line
// carries the directory whole, with the quant and the set's size — the two
// things the old model column spent two rows on. The shards count left the
// image for the tape, the run page and -o md.
func TestShardedIdentityNamesTheVariant(t *testing.T) {
	c, err := renderCanvas(card.ExampleSharded())
	if err != nil {
		t.Fatalf("renderCanvas: %v", err)
	}
	m, ok := c.markByID("ident.model")
	if !ok {
		t.Fatal("ident.model was never drawn")
	}
	for _, sub := range []string{
		"DeepSeek-V4.1-Flash-engramQ8-tokembdBF16",
		"Q4_K_M",
		"400.00 GiB",
	} {
		if !strings.Contains(m.Text, sub) {
			t.Errorf("ident.model = %q, want it to carry %q", m.Text, sub)
		}
	}
	// The part name itself is the one thing that must not be the headline: it
	// is the same string for both variants.
	if strings.Contains(m.Text, "-00001-of-00009") {
		t.Errorf("ident.model = %q, still the bare part name", m.Text)
	}
	// The header's general.name must not reach any mark.
	for _, mk := range c.marks {
		if mk.Kind == "text" && strings.Contains(mk.Text, "DeepSeek V4.1 Flash") {
			t.Errorf("%s printed the header's general.name: %q", mk.ID, mk.Text)
		}
	}
	// And the ids the old test pinned exist on no card.
	for _, id := range []string{"header.model", "header.modelsub", "footer.col0.row0"} {
		if _, ok := c.markByID(id); ok {
			t.Errorf("%s was drawn on a card that no longer has that surface", id)
		}
	}
}

// TestSingleFileIdentityUnchanged: card.ModelName is the file's stem for a
// model that is one file, so a non-sharded card's model line names the file
// that ran, with its quant and size — never a "shard" claim.
func TestSingleFileIdentityUnchanged(t *testing.T) {
	s := card.Example()
	c, err := renderCanvas(s)
	if err != nil {
		t.Fatalf("renderCanvas: %v", err)
	}
	m, ok := c.markByID("ident.model")
	if !ok {
		t.Fatal("ident.model was never drawn")
	}
	// The file's stem, not the header's "R1 Distill Llama 70B" — the file is
	// the model that ran, and its own name already carries the quant
	// (2026-09-15, user: "모델이 다 실제값으로 찍혀야해").
	if stem := card.ModelStem(s.Model.FileName); !strings.Contains(m.Text, stem) {
		t.Errorf("ident.model = %q, want the file stem %q", m.Text, stem)
	}
	for _, want := range []string{s.Model.Quant, formatFileGiB(s.Model.FileBytes)} {
		if !strings.Contains(m.Text, want) {
			t.Errorf("ident.model = %q, want it to carry %q", m.Text, want)
		}
	}
	if strings.Contains(m.Text, "shard") {
		t.Errorf("ident.model = %q claims shards for a single file", m.Text)
	}
	if strings.Contains(m.Text, "R1 Distill") {
		t.Errorf("ident.model = %q printed the GGUF header's general.name", m.Text)
	}
}

// TestShardedStaysInsideTheBand: the frame does not grow for a split set, and
// the model line that carries the variant must still sit in the content box —
// by its preferred spelling or by a fallback, never past the edge.
func TestShardedStaysInsideTheBand(t *testing.T) {
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
// (user: "모델이 다 실제값으로 찍혀야해"): the model's line is now the whole
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

// TestIdentityModelLineNamesTheModelThatRan (2026-09-15, re-pinned 2026-09-19
// on the identity band): the model's line names the model that ran — the
// variant directory, not the GGUF header's general.name — with the size; the
// quant part is printed only when the name does not already carry it, and
// this 46-character name carries Q3_K_M (2026-09-19). Whatever pickWidest
// picks, the name itself is whole on the card, because every member of the
// chain keeps it and only the size is given up.
func TestIdentityModelLineNamesTheModelThatRan(t *testing.T) {
	s := card.ExampleSharded()
	s.Model = realRecordingModel()
	c, err := renderCanvas(s)
	if err != nil {
		t.Fatalf("renderCanvas: %v", err)
	}

	name := card.ModelName(s.Model)
	m, ok := c.markByID("ident.model")
	if !ok {
		t.Fatal("ident.model was never drawn")
	}
	if !strings.Contains(m.Text, name) {
		t.Errorf("ident.model = %q, want the whole name %q", m.Text, name)
	}
	if strings.HasSuffix(m.Text, ellipsis) {
		t.Errorf("the name was cut to %q — no fallback of this line fits", m.Text)
	}
	// The preferred spelling carries name and size; the quant part is gone
	// because the name already carries it (2026-09-19, defect 1 of the band
	// review — TestModelQuantPrintedOnce's rule). Whether the preferred is
	// the one drawn is pickWidest's measured choice, so the builder is where
	// the content contract is pinned.
	if got, want := contentOf(t, s).ident1.preferred, name+" · 440.52 GiB"; got != want {
		t.Errorf("model preferred = %q, want %q", got, want)
	}
	// The size is formatFileGiB(473000000000) = "440.52 GiB"; the sub-line's
	// figures come from the header the recording really read.
	sub, ok := c.markByID("ident.modelsub")
	if !ok {
		t.Fatal("ident.modelsub was never drawn")
	}
	for _, want := range []string{"deepseek2", "61 layers", "8 of 256 experts"} {
		if !strings.Contains(sub.Text, want) {
			t.Errorf("ident.modelsub = %q lost %q", sub.Text, want)
		}
	}

	// The header's general.name must appear on no mark on the card.
	for _, mk := range c.marks {
		if mk.Kind == "text" && strings.Contains(mk.Text, "DeepSeek V4.1 Flash") {
			t.Errorf("%s printed the header's general.name: %q", mk.ID, mk.Text)
		}
	}
}

// TestModelQuantPrintedOnce is the defect-1 gate (2026-09-19): the model name
// is the file's stem or the variant directory, and both are named after the
// quant, so a standalone quant part beside it says the same thing twice on
// the line a feed-size reader can actually read — the same redundancy the
// header's model round removed. The name is never stripped (the name is the
// name); only the second printing goes.
func TestModelQuantPrintedOnce(t *testing.T) {
	// The hero recording: stem "Qwen3.6-35B-A3B-UD-Q6_K", quant "UD-Q6_K".
	// FAIL-first on the pre-fix tree: the drawn line read
	// "Qwen3.6-35B-A3B-UD-Q6_K · UD-Q6_K · 27.30 GiB".
	s := heroTapeSummary(t)
	c, err := renderCanvas(s)
	if err != nil {
		t.Fatalf("renderCanvas: %v", err)
	}
	m, ok := c.markByID("ident.model")
	if !ok {
		t.Fatal("ident.model was never drawn")
	}
	if got := strings.Count(m.Text, s.Model.Quant); got != 1 {
		t.Errorf("ident.model = %q carries %q %d times, want exactly once",
			m.Text, s.Model.Quant, got)
	}
	if want := formatFileGiB(s.Model.FileBytes); !strings.Contains(m.Text, want) {
		t.Errorf("ident.model = %q lost the file size %q the dedup must keep", m.Text, want)
	}

	// A lower-case file name contains its upper-case quant just as well
	// (card.ModelNameQuant's containsFold rule), so it loses the part too.
	fold := card.Example()
	fold.Model.FileName = "qwen3-30b-a3b-q4_k_m.gguf"
	if got, want := contentOf(t, fold).ident1.preferred,
		"qwen3-30b-a3b-q4_k_m · "+formatFileGiB(fold.Model.FileBytes); got != want {
		t.Errorf("model preferred = %q, want %q — the case-blind quant was printed again", got, want)
	}

	// A name that does not carry its quant keeps the part: the common case
	// for a repo-named directory (the sharded example's dir has no quant in
	// it), and the reason the part exists at all.
	sharded := card.ExampleSharded()
	if got, want := contentOf(t, sharded).ident1.preferred,
		strings.Join([]string{card.ModelName(sharded.Model), sharded.Model.Quant,
			formatFileGiB(sharded.Model.FileBytes)}, " · "); got != want {
		t.Errorf("model preferred = %q, want %q — a quant-less name must keep the part", got, want)
	}
}

// TestShardedModelLineIsNotTruncated is the reason the fallback chain exists:
// the variant is the whole point of the split-set card, and an ellipsis in
// the middle of it says nothing. The plain 36-character directory fits with
// the quant and the size; the 61-character one gives them up and keeps the
// name whole.
func TestShardedModelLineIsNotTruncated(t *testing.T) {
	for name, s := range shardedFixtures() {
		t.Run(name, func(t *testing.T) {
			c, err := renderCanvas(s)
			if err != nil {
				t.Fatalf("renderCanvas: %v", err)
			}
			m, ok := c.markByID("ident.model")
			if !ok {
				t.Fatal("ident.model was never drawn")
			}
			if strings.HasSuffix(m.Text, ellipsis) {
				t.Errorf("ident.model was truncated: %q", m.Text)
			}
			if !strings.Contains(m.Text, card.ModelName(s.Model)) {
				t.Errorf("ident.model = %q, want the whole variant name %q", m.Text, card.ModelName(s.Model))
			}
		})
	}
}
