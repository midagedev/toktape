package procmon

import (
	"bufio"
	"bytes"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// Mapping is one line of /proc/<pid>/maps.
type Mapping struct {
	Start   uint64
	End     uint64
	Perms   string // "r--p"
	Offset  uint64
	Dev     string // "103:02"
	Inode   uint64
	Path    string // "" for an anonymous mapping; "[heap]", "[stack]" for the pseudo-paths
	Deleted bool   // the kernel's " (deleted)" suffix, stripped from Path
}

// Size is the length of the mapping in bytes.
func (m Mapping) Size() int64 { return int64(m.End - m.Start) }

// ParseMaps parses /proc/<pid>/maps.
//
// The pathname is the sixth column and may contain spaces, so the first five
// columns are skipped by position and the remainder is taken verbatim; joining
// a Fields split back together would collapse runs of spaces inside a path.
// Lines that are not parseable as a mapping are an error rather than a silent
// skip: a maps file is machine-written, and a line this parser cannot read
// means the format moved.
func ParseMaps(data []byte) ([]Mapping, error) {
	var out []Mapping
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	line := 0
	for sc.Scan() {
		line++
		text := strings.TrimRight(sc.Text(), "\r")
		if strings.TrimSpace(text) == "" {
			continue
		}
		m, err := parseMapsLine(text)
		if err != nil {
			return nil, fmt.Errorf("procmon: maps: line %d: %w", line, err)
		}
		out = append(out, m)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("procmon: maps: %w", err)
	}
	return out, nil
}

func parseMapsLine(line string) (Mapping, error) {
	var cols [5]string
	rest := line
	for i := range cols {
		rest = strings.TrimLeft(rest, " \t")
		if rest == "" {
			return Mapping{}, fmt.Errorf("want 5 columns before the pathname, got %d in %q", i, truncate(line, 96))
		}
		j := strings.IndexAny(rest, " \t")
		if j < 0 {
			cols[i] = rest
			rest = ""
			continue
		}
		cols[i] = rest[:j]
		rest = rest[j+1:]
	}
	dash := strings.IndexByte(cols[0], '-')
	if dash < 0 {
		return Mapping{}, fmt.Errorf("address range %q has no '-'", cols[0])
	}
	start, err := strconv.ParseUint(cols[0][:dash], 16, 64)
	if err != nil {
		return Mapping{}, fmt.Errorf("start address: %w", err)
	}
	end, err := strconv.ParseUint(cols[0][dash+1:], 16, 64)
	if err != nil {
		return Mapping{}, fmt.Errorf("end address: %w", err)
	}
	if end < start {
		return Mapping{}, fmt.Errorf("end address %#x is below start %#x", end, start)
	}
	offset, err := strconv.ParseUint(cols[2], 16, 64)
	if err != nil {
		return Mapping{}, fmt.Errorf("offset: %w", err)
	}
	inode, err := strconv.ParseUint(cols[4], 10, 64)
	if err != nil {
		return Mapping{}, fmt.Errorf("inode: %w", err)
	}
	m := Mapping{
		Start:  start,
		End:    end,
		Perms:  cols[1],
		Offset: offset,
		Dev:    cols[3],
		Inode:  inode,
		Path:   strings.TrimLeft(rest, " \t"),
	}
	if s, ok := strings.CutSuffix(m.Path, " (deleted)"); ok {
		m.Path = s
		m.Deleted = true
	}
	return m, nil
}

// shardRe matches a llama.cpp split-GGUF name: "<prefix>-00001-of-00009.gguf".
var shardRe = regexp.MustCompile(`^(.+)-(\d{5})-of-(\d{5})\.gguf$`)

// SameModelFile reports whether mapped is the model file at modelPath or one
// of its shards: same directory, same prefix, same shard count. A model that
// is not split matches only its exact path, so an unrelated .gguf next to it
// is never counted.
func SameModelFile(modelPath, mapped string) bool {
	if modelPath == "" || mapped == "" {
		return false
	}
	if mapped == modelPath {
		return true
	}
	if filepath.Dir(mapped) != filepath.Dir(modelPath) {
		return false
	}
	want := shardRe.FindStringSubmatch(filepath.Base(modelPath))
	if want == nil {
		return false
	}
	got := shardRe.FindStringSubmatch(filepath.Base(mapped))
	if got == nil {
		return false
	}
	return want[1] == got[1] && want[3] == got[3]
}

// SumModelMappings adds up the sizes of every mapping of the model file and
// its shards. One shard can occupy several VMAs — the kernel splits a mapping
// whenever protections or the backing state diverge — and the ranges are
// disjoint, so summing them is the size of the mapping, not a double count.
//
// A mapping the kernel marks " (deleted)" is counted: the file was unlinked or
// replaced while the server held it open, and the pages the server is serving
// from are still the model's.
func SumModelMappings(ms []Mapping, modelPath string) int64 {
	var total int64
	for _, m := range ms {
		if SameModelFile(modelPath, m.Path) {
			total += m.Size()
		}
	}
	return total
}
