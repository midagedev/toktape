package card

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/midagedev/toktape/internal/tape"
)

// shardSuffix matches the part marker llama.cpp writes on a split GGUF:
// "-00001-of-00009.gguf". Both indices are five digits wide, and the file name
// without the marker is the stem the whole set shares.
var shardSuffix = regexp.MustCompile(`-(\d{5})-of-(\d{5})\.gguf$`)

// MinShards is the smallest N that counts as a shard set. A single-part
// "-00001-of-00001" file is one file with a long name, and printing "1 shards"
// beside it would be noise; the recorder normalises such a set to Shards = 0
// and every renderer treats Shards < MinShards as "not sharded".
const MinShards = 2

// Sharded reports whether m is one part of a split GGUF set.
func Sharded(m tape.ModelInfo) bool { return m.Shards >= MinShards }

// ShardCount returns N when fileName is one part of an "-00001-of-0000N" set,
// and 0 otherwise — including for a set of one, which is not a set.
func ShardCount(fileName string) int {
	g := shardSuffix.FindStringSubmatch(fileName)
	if g == nil {
		return 0
	}
	n, err := strconv.Atoi(g[2])
	if err != nil || n < MinShards {
		return 0
	}
	return n
}

// ModelStem is the short name of a model file: the part marker of a split
// GGUF, or else the ".gguf" extension, dropped. It is what the TUI title bar
// and the PNG header call the model when the GGUF header carried no
// general.name, so a sharded set reads as one model rather than as part one.
func ModelStem(fileName string) string {
	if loc := shardSuffix.FindStringIndex(fileName); loc != nil {
		return fileName[:loc[0]]
	}
	return strings.TrimSuffix(fileName, ".gguf")
}

// ModelLabel is what the card's MODEL line calls the model file (TTP-32,
// 2026-09-13).
//
// A single file is its own label, unchanged. A shard set is not: variants of
// one model are often hard-linked side by side — the rig this was written for
// holds DeepSeek-V4.1-Flash-engramQ8-tokembdBF16 and
// DeepSeek-V4.1-Flash-engramQ4-tokembdQ8, and every part inside them has the
// same name — so the directory is the only thing that says which one ran. The
// directory usually already carries the stem as its prefix and then stands
// alone; otherwise the two are joined, and a set whose directory was not
// recorded falls back to the stem.
func ModelLabel(m tape.ModelInfo) string {
	if !Sharded(m) {
		return m.FileName
	}
	stem := ModelStem(m.FileName)
	switch {
	case m.Dir == "":
		return stem
	case stem != "" && strings.HasPrefix(m.Dir, stem):
		return m.Dir
	default:
		return m.Dir + "/" + stem
	}
}

// shardsPart is the "9 shards" element of the MODEL line, empty when the model
// is a single file.
func shardsPart(m tape.ModelInfo) string {
	if !Sharded(m) {
		return ""
	}
	return strconv.Itoa(m.Shards) + " shards"
}
