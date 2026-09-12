package procmon

import (
	"fmt"
	"os"

	"github.com/midagedev/toktape/internal/tape"
)

// ReadMem reads the process memory picture of pid: the sizes from
// /proc/<pid>/status and the fault counters from /proc/<pid>/stat.
func ReadMem(fsRoot string, pid int) (tape.MemSample, error) {
	statusData, err := readPIDFile(fsRoot, pid, "status")
	if err != nil {
		return tape.MemSample{}, err
	}
	st, err := ParseStatus(statusData)
	if err != nil {
		return tape.MemSample{}, err
	}
	statData, err := readPIDFile(fsRoot, pid, "stat")
	if err != nil {
		return tape.MemSample{}, err
	}
	s, err := ParseStat(statData)
	if err != nil {
		return tape.MemSample{}, err
	}
	return memSample(st, s), nil
}

// memSample joins the two files into the tape's sample.
func memSample(st Status, s Stat) tape.MemSample {
	return tape.MemSample{
		VirtBytes:     st.VirtBytes,
		RSSBytes:      st.RSSBytes,
		RSSFileBytes:  st.RSSFileBytes,
		RSSAnonBytes:  st.RSSAnonBytes,
		RSSShmemBytes: st.RSSShmemBytes,
		SwapBytes:     st.SwapBytes,
		MajFaults:     s.MajFaults,
		MinFaults:     s.MinFaults,
	}
}

// MappedFileBytes is the size of the model's mapping(s) in pid's address
// space: every shard of a split GGUF, summed. It is MemorySummary.
// MappedFileBytes, and it is deliberately not compared with RSS to derive what
// is "loaded" — total minus RSS overstated the never-loaded figure by about
// 50 GB on the reference machine (handover lesson 3). Never-loaded comes from
// the GGUF header instead.
func MappedFileBytes(fsRoot string, pid int, modelPath string) (int64, error) {
	data, err := readPIDFile(fsRoot, pid, "maps")
	if err != nil {
		return 0, err
	}
	ms, err := ParseMaps(data)
	if err != nil {
		return 0, err
	}
	return SumModelMappings(ms, resolveModelPath(fsRoot, pid, modelPath)), nil
}

// SmapsRollup reads /proc/<pid>/smaps_rollup. The file needs no privilege for
// one's own process but is absent on kernels before 4.14, in which case the
// error wraps ErrNotFound and the caller keeps the coarser Status view.
func SmapsRollup(fsRoot string, pid int) (Rollup, error) {
	data, err := readPIDFile(fsRoot, pid, "smaps_rollup")
	if err != nil {
		return Rollup{}, err
	}
	return ParseSmapsRollup(data)
}

// readPIDFile reads one per-process file, mapping "no such file" to
// ErrNotFound so a caller can tell a dead process from a broken read.
func readPIDFile(fsRoot string, pid int, name string) ([]byte, error) {
	p := pidPath(fsRoot, pid, name)
	data, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("procmon: %s: %w", p, ErrNotFound)
		}
		return nil, fmt.Errorf("procmon: read %s: %w", p, err)
	}
	return data, nil
}
