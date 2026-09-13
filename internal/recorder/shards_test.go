package recorder

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/midagedev/toktape/internal/card"
	"github.com/midagedev/toktape/internal/tape"
)

func TestShardCount(t *testing.T) {
	cases := []struct {
		name string
		want int
	}{
		{"DeepSeek-V4.1-Flash-00001-of-00009.gguf", 9},
		{"DeepSeek-V4.1-Flash-00007-of-00009.gguf", 9},
		{"Qwen3-235B-A22B-UD-Q4_K_XL-00003-of-00012.gguf", 12},
		// A set of one is one file with a long name.
		{"Model-00001-of-00001.gguf", 0},
		{"DeepSeek-R1-Distill-Llama-70B-Q4_K_M.gguf", 0},
		// The marker is five digits wide and sits at the end.
		{"Model-0001-of-0009.gguf", 0},
		{"Model-00001-of-00009.gguf.bak", 0},
		{"", 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ShardCount(c.name); got != c.want {
				t.Errorf("ShardCount(%q) = %d, want %d", c.name, got, c.want)
			}
		})
	}
}

func TestModelDir(t *testing.T) {
	cases := []struct{ path, want string }{
		{"/models/DeepSeek-V4.1-Flash-engramQ8-tokembdBF16/DeepSeek-V4.1-Flash-00001-of-00009.gguf", "DeepSeek-V4.1-Flash-engramQ8-tokembdBF16"},
		{"/models/model.gguf", "models"},
		// No directory of its own: the root and the working directory are not
		// names that tell two variants apart.
		{"model.gguf", ""},
		{"./model.gguf", ""},
		{"/model.gguf", ""},
		{"", ""},
	}
	for _, c := range cases {
		t.Run(c.path, func(t *testing.T) {
			if got := ModelDir(c.path); got != c.want {
				t.Errorf("ModelDir(%q) = %q, want %q", c.path, got, c.want)
			}
		})
	}
}

func TestShardPaths(t *testing.T) {
	got := ShardPaths("/models/set/Model-00004-of-00009.gguf")
	if len(got) != 9 {
		t.Fatalf("ShardPaths returned %d paths, want 9: %v", len(got), got)
	}
	if got[0] != "/models/set/Model-00001-of-00009.gguf" {
		t.Errorf("first part = %q", got[0])
	}
	if got[8] != "/models/set/Model-00009-of-00009.gguf" {
		t.Errorf("last part = %q", got[8])
	}
	// Every part is named exactly once, so the sum below counts each file once.
	if slices.Contains(got[1:], got[0]) {
		t.Errorf("a part is listed twice: %v", got)
	}
	if p := ShardPaths("/models/set/Model-Q4_K_M.gguf"); p != nil {
		t.Errorf("ShardPaths of a single file = %v, want nil", p)
	}
	if p := ShardPaths("/models/set/Model-00001-of-00001.gguf"); p != nil {
		t.Errorf("ShardPaths of a one-part set = %v, want nil", p)
	}
}

// writeShards lays out a fake shard set of n parts, each part i bytes long, and
// returns the path of part one. Parts listed in missing are not created.
func writeShards(t *testing.T, n int, missing ...int) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "DeepSeek-V4.1-Flash-engramQ8-tokembdBF16")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	first := ""
	for i := 1; i <= n; i++ {
		p := filepath.Join(dir, sprintfShard(i, n))
		if i == 1 {
			first = p
		}
		if slices.Contains(missing, i) {
			continue
		}
		if err := os.WriteFile(p, make([]byte, i), 0o644); err != nil {
			t.Fatalf("write %s: %v", p, err)
		}
	}
	return first
}

func sprintfShard(i, n int) string {
	return fmt.Sprintf("DeepSeek-V4.1-Flash-%05d-of-%05d.gguf", i, n)
}

// TestFillShardSetSumsEveryPart is the size contract: the card's GiB figure for
// a split model is the size of the model, not the size of part one.
func TestFillShardSetSumsEveryPart(t *testing.T) {
	first := writeShards(t, 9)
	r := &run{model: tape.ModelInfo{Path: first, FileName: filepath.Base(first), FileBytes: 1}}
	r.fillShardSet(first, true)

	if r.model.Shards != 9 {
		t.Errorf("Shards = %d, want 9", r.model.Shards)
	}
	if r.model.Dir != "DeepSeek-V4.1-Flash-engramQ8-tokembdBF16" {
		t.Errorf("Dir = %q", r.model.Dir)
	}
	if want := int64(45); r.model.FileBytes != want { // 1+2+…+9
		t.Errorf("FileBytes = %d, want %d (the sum of all nine parts)", r.model.FileBytes, want)
	}
	if len(r.warnings) != 0 {
		t.Errorf("a complete set warned: %v", r.warnings)
	}
}

// TestFillShardSetMissingParts: a partial sum is a number nobody measured, so
// the single part's size is kept and the card is told why.
func TestFillShardSetMissingParts(t *testing.T) {
	first := writeShards(t, 9, 4, 7)
	r := &run{model: tape.ModelInfo{Path: first, FileName: filepath.Base(first), FileBytes: 1}}
	r.fillShardSet(first, true)

	if r.model.Shards != 9 {
		t.Errorf("Shards = %d, want 9", r.model.Shards)
	}
	if r.model.FileBytes != 1 {
		t.Errorf("FileBytes = %d, want the named part's own size (1)", r.model.FileBytes)
	}
	if len(r.warnings) != 1 || !strings.Contains(r.warnings[0], "2 of 9 shards not found") {
		t.Errorf("warnings = %v, want one naming the two missing parts", r.warnings)
	}
}

// TestFillShardSetRemote is the remote-server case: the model file is not on
// this machine, so the parts are named but never stat'ed and no size is
// invented.
func TestFillShardSetRemote(t *testing.T) {
	path := "/models/DeepSeek-V4.1-Flash-engramQ8-tokembdBF16/DeepSeek-V4.1-Flash-00001-of-00009.gguf"
	r := &run{model: tape.ModelInfo{Path: path, FileName: filepath.Base(path)}}
	r.fillShardSet(path, false)

	if r.model.Shards != 9 || r.model.Dir != "DeepSeek-V4.1-Flash-engramQ8-tokembdBF16" {
		t.Errorf("Shards/Dir = %d/%q, want 9 and the variant directory", r.model.Shards, r.model.Dir)
	}
	if r.model.FileBytes != 0 {
		t.Errorf("FileBytes = %d, want 0: nothing was stat'ed", r.model.FileBytes)
	}
	if len(r.warnings) != 0 {
		t.Errorf("the remote case warned about missing shards: %v", r.warnings)
	}
}

// TestFillShardSetSingleFile: an ordinary model gets its directory recorded and
// nothing else, so every single-file card stays exactly as it was.
func TestFillShardSetSingleFile(t *testing.T) {
	path := "/models/DeepSeek-R1-Distill-Llama-70B-Q4_K_M.gguf"
	r := &run{model: tape.ModelInfo{Path: path, FileName: filepath.Base(path), FileBytes: 42}}
	r.fillShardSet(path, true)

	if r.model.Shards != 0 {
		t.Errorf("Shards = %d, want 0", r.model.Shards)
	}
	if r.model.FileBytes != 42 {
		t.Errorf("FileBytes = %d, want 42", r.model.FileBytes)
	}
	if r.model.Dir != "models" {
		t.Errorf("Dir = %q, want %q", r.model.Dir, "models")
	}
}

// TestShardParsersAgreeWithTheCard pins the two copies of the part-marker rule
// together: the recorder decides how many shards there are and the card decides
// what to call the set, and a card that disagreed with the tape it renders
// would print a shard count for a name it did not shorten.
func TestShardParsersAgreeWithTheCard(t *testing.T) {
	names := []string{
		"DeepSeek-V4.1-Flash-00001-of-00009.gguf",
		"Qwen3-235B-A22B-UD-Q4_K_XL-00003-of-00012.gguf",
		"Model-00001-of-00001.gguf",
		"Model-Q4_K_M.gguf",
		"Model-0001-of-0009.gguf",
		"",
	}
	for _, n := range names {
		t.Run(n, func(t *testing.T) {
			if got, want := ShardCount(n), card.ShardCount(n); got != want {
				t.Errorf("recorder.ShardCount(%q) = %d, card.ShardCount = %d", n, got, want)
			}
			// A name the recorder calls sharded is one the card shortens: the
			// count and the label must not be read off different rules.
			if ShardCount(n) == 0 {
				return
			}
			if stem := card.ModelStem(n); stem == strings.TrimSuffix(n, ".gguf") {
				t.Errorf("card.ModelStem(%q) = %q did not drop the part marker", n, stem)
			}
		})
	}
}
