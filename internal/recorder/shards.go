package recorder

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
)

// shardSuffix matches the part marker llama.cpp writes on a split GGUF:
// "-00001-of-00009.gguf". The capture groups are the part index and the count,
// both zero-padded to the same width.
var shardSuffix = regexp.MustCompile(`-(\d{5})-of-(\d{5})\.gguf$`)

// minShards is the smallest N that counts as a set; it mirrors card.MinShards.
// A "-00001-of-00001" file is one file with a long name.
const minShards = 2

// ShardCount returns N when name is one part of an "-00001-of-0000N" set, and
// 0 otherwise — a set of one included.
func ShardCount(name string) int {
	g := shardSuffix.FindStringSubmatch(name)
	if g == nil {
		return 0
	}
	n, err := strconv.Atoi(g[2])
	if err != nil || n < minShards {
		return 0
	}
	return n
}

// ModelDir is the base name of the directory holding path, or "" when the path
// names no directory of its own ("model.gguf", "/model.gguf", "./model.gguf").
// It is what tells two hard-linked variants of one model apart: their files
// have the same names and only the directory differs.
func ModelDir(path string) string {
	dir := filepath.Dir(path)
	if dir == "." || dir == string(filepath.Separator) || dir == "" {
		return ""
	}
	base := filepath.Base(dir)
	if base == "." || base == string(filepath.Separator) {
		return ""
	}
	return base
}

// ShardPaths returns every part of the set path belongs to, in index order,
// beside path itself. A path that is not one part of a set returns nil.
//
// It is a pure function of the string: the parts are named, not listed from
// the directory, so a set with a part missing is still reported as N paths and
// the caller learns which ones are absent by stat'ing them.
func ShardPaths(path string) []string {
	g := shardSuffix.FindStringSubmatchIndex(path)
	if g == nil {
		return nil
	}
	count := path[g[4]:g[5]]
	n, err := strconv.Atoi(count)
	if err != nil || n < minShards {
		return nil
	}
	// The index is rewritten in place, padded to the width the file itself
	// uses, so a hypothetical six-digit set keeps its own spelling.
	prefix, suffix := path[:g[2]], path[g[3]:]
	width := g[3] - g[2]
	out := make([]string, 0, n)
	for i := 1; i <= n; i++ {
		out = append(out, fmt.Sprintf("%s%0*d%s", prefix, width, i, suffix))
	}
	return out
}

// shardSetBytes sums os.Stat over paths and reports how many of them exist.
// A part that cannot be stat'ed is not counted and contributes no bytes, so
// the caller can tell a complete set from a partial one.
func shardSetBytes(paths []string) (total int64, found int) {
	for _, p := range paths {
		fi, err := os.Stat(p)
		if err != nil {
			continue
		}
		total += fi.Size()
		found++
	}
	return total, found
}

// fillShardSet records which variant directory the model came from and, when
// the file is one part of a split GGUF, how many parts there are and how big
// the whole set is.
//
// FileBytes for a shard set is the sum of every part: the reader of the card
// expects the size of the model, and the size of part one is neither that nor
// anything else useful. When a part is missing from this machine the single
// file's size is kept — a partial sum would be a number nobody measured — and
// the card says so.
func (r *run) fillShardSet(path string, statParts bool) {
	r.model.Dir = ModelDir(path)
	n := ShardCount(filepath.Base(path))
	if n == 0 {
		return
	}
	r.model.Shards = n
	if !statParts {
		return
	}
	total, found := shardSetBytes(ShardPaths(path))
	if found == n {
		r.model.FileBytes = total
		return
	}
	r.warn("model: %d of %d shards not found", n-found, n)
}
