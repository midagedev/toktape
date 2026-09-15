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

// ModelName is what every surface calls the model when the name has to be the
// model that actually ran: the variant label a shard set's directory gives,
// else the file's stem.
//
// The precedence is dir-based label > file stem > general.name > "", and
// general.name is last on purpose (2026-09-15, user: "모델이 다 실제값으로
// 찍혀야해"). A re-quantised variant keeps the original's GGUF header — the
// parts of DeepSeek-V4.1-Flash-Q3_K_M-engramQ8-tokembdBF16 still say
// "DeepSeek V4.1 Flash" — so the header names the base model, not the bytes
// that ran. The file system is the only witness of which variant was loaded,
// and it wins whenever anything of it was recorded: the directory a split set
// sits in (ModelLabel), or the file's own name with the extension and the
// part marker dropped. general.name is read only when no file was recorded at
// all — it is observed, so it is printed rather than "?" — and nothing
// observed prints nothing; callers keep their own "?" or omission behaviour.
func ModelName(m tape.ModelInfo) string {
	if Sharded(m) {
		return ModelLabel(m)
	}
	if m.FileName != "" {
		return ModelStem(m.FileName)
	}
	return m.Name
}

// ModelNameQuant is ModelName with the quant appended, unless the name already
// carries it — "DeepSeek-V4.1-Flash-Q3_K_M-engramQ8-tokembdBF16" contains
// Q3_K_M, and a file named qwen3-30b-a3b-q4_k_m contains Q4_K_M just as well
// case-blind. An unknown name stays empty even when a quant was observed:
// callers omit the whole segment rather than print a quant that belongs to no
// model they could name.
func ModelNameQuant(m tape.ModelInfo) string {
	name := ModelName(m)
	if name == "" {
		return ""
	}
	if m.Quant != "" && !containsFold(name, m.Quant) {
		return name + " " + m.Quant
	}
	return name
}

// containsFold reports whether s contains sub, ignoring case, for the quant's
// upper-case spelling against a file name's lower-case one.
func containsFold(s, sub string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(sub))
}
