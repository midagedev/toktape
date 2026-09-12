package procmon

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// ParseCmdline splits a /proc/<pid>/cmdline blob into argv. The kernel
// NUL-separates the arguments and leaves a trailing NUL, which would otherwise
// produce a phantom empty argument. A kernel thread has an empty cmdline and
// yields a nil argv.
func ParseCmdline(data []byte) []string {
	data = bytes.TrimRight(data, "\x00")
	if len(data) == 0 {
		return nil
	}
	parts := bytes.Split(data, []byte{0})
	argv := make([]string, 0, len(parts))
	for _, p := range parts {
		argv = append(argv, string(p))
	}
	return argv
}

// IsServerCommand reports whether an executable or comm name looks like a
// llama.cpp-family server. The kernel truncates comm to 15 characters, so the
// match is by prefix: "llama-server-cuda" arrives as "llama-server-cu".
func IsServerCommand(name string) bool {
	n := strings.ToLower(strings.TrimSpace(filepath.Base(name)))
	// Some builds append a suffix to the binary, and the comm of a build that
	// names itself in parentheses is "llama-server (cuda)".
	switch {
	case n == "server", n == "llama-server", n == "llama_server":
		return true
	case strings.HasPrefix(n, "llama-server"), strings.HasPrefix(n, "llama_server"):
		return true
	case strings.Contains(n, "ik_llama"), strings.Contains(n, "ik-llama"):
		return true
	}
	return false
}

// matchStrength ranks how well one process's argv names the model.
type matchStrength int

const (
	noMatch matchStrength = iota
	basenameMatch
	exactMatch
)

// modelMatch ranks argv against modelPath. An argument equal to the path, or a
// "--model=<path>" form, is exact; the same file name in another directory, or
// another shard of the same split model, is a basename match, which is what a
// server started with shard 2 of 9 or with a symlinked model directory looks
// like.
func modelMatch(argv []string, modelPath string) matchStrength {
	if modelPath == "" {
		return noMatch
	}
	base := filepath.Base(modelPath)
	best := noMatch
	for _, a := range argv {
		v := a
		if i := strings.IndexByte(a, '='); i >= 0 && strings.HasPrefix(a, "-") {
			v = a[i+1:]
		}
		switch {
		case v == modelPath:
			return exactMatch
		case filepath.Base(v) == base, SameModelFile(modelPath, v):
			if best < basenameMatch {
				best = basenameMatch
			}
		}
	}
	return best
}

// FindPID finds the local llama-server process serving modelPath by scanning
// <fsRoot>/proc/*/cmdline.
//
// A process qualifies only when both halves hold: its argv or its comm names a
// llama.cpp-family server, AND its argv names the model. The command check is
// what keeps a shell, an editor or a checksum run that merely mentions the
// model path out of the result. An exact path match wins over a match on the
// file name alone, and the lowest PID wins a tie, so the answer does not
// depend on readdir order.
//
// It returns an error wrapping ErrNotFound when nothing matched — the normal
// case for a server on another host, where the caller records a warning and
// runs without the /proc view.
func FindPID(fsRoot string, modelPath string) (int, error) {
	if modelPath == "" {
		return 0, fmt.Errorf("procmon: find pid: empty model path")
	}
	root := procPath(fsRoot)
	entries, err := os.ReadDir(root)
	if err != nil {
		return 0, fmt.Errorf("procmon: find pid: read %s: %w", root, err)
	}
	pids := make([]int, 0, len(entries))
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid <= 0 {
			continue // not a process directory
		}
		pids = append(pids, pid)
	}
	sort.Ints(pids)

	best, bestPID := noMatch, 0
	for _, pid := range pids {
		argv, err := Args(fsRoot, pid)
		if err != nil || len(argv) == 0 {
			continue // exited between readdir and read, unreadable, or a kernel thread
		}
		strength := modelMatch(argv, resolveModelPath(fsRoot, pid, modelPath))
		if strength == noMatch {
			continue
		}
		// Qualify before ranking: a shell that merely names the model must be
		// rejected by the command check whatever its PID happens to be.
		if !isServerProcess(fsRoot, pid, argv) {
			continue
		}
		if strength <= best {
			continue
		}
		best, bestPID = strength, pid
		if best == exactMatch {
			break // nothing can beat it, and the lowest such PID comes first
		}
	}
	if bestPID != 0 {
		return bestPID, nil
	}
	if pid := findPIDByMapping(fsRoot, pids, modelPath); pid != 0 {
		return pid, nil
	}
	return 0, fmt.Errorf("procmon: no llama-server process has %s open: %w", modelPath, ErrNotFound)
}

// findPIDByMapping is the fallback for a server started with -hf. /props then
// reports the resolved path under the model cache, and that path is nowhere in
// argv, so the only place the two agree is the mapping list. Only processes
// that already passed the command check are opened, so this reads a handful of
// maps files, not one per process on the box. It returns 0 when nothing
// matched.
func findPIDByMapping(fsRoot string, pids []int, modelPath string) int {
	for _, pid := range pids {
		argv, err := Args(fsRoot, pid)
		if err != nil || len(argv) == 0 {
			continue
		}
		if !isServerProcess(fsRoot, pid, argv) {
			continue
		}
		data, err := os.ReadFile(pidPath(fsRoot, pid, "maps"))
		if err != nil {
			continue // /proc/<pid>/maps of another user's process
		}
		ms, err := ParseMaps(data)
		if err != nil {
			continue
		}
		want := resolveModelPath(fsRoot, pid, modelPath)
		for _, m := range ms {
			if SameModelFile(want, m.Path) {
				return pid
			}
		}
	}
	return 0
}

// resolveModelPath makes a relative model path absolute against the working
// directory of pid.
//
// /props reports model_path as it was passed on the command line, so a server
// started with "-m models/foo.gguf" reports a relative path while
// /proc/<pid>/maps always holds the absolute one. Comparing the two directly
// would drop the exact-path match and report a mapped size of zero. The cwd
// link is readable for one's own processes; when it is not, the relative path
// is returned unchanged and the file-name match still applies.
func resolveModelPath(fsRoot string, pid int, modelPath string) string {
	if modelPath == "" || filepath.IsAbs(modelPath) {
		return modelPath
	}
	cwd, err := os.Readlink(pidPath(fsRoot, pid, "cwd"))
	if err != nil || !filepath.IsAbs(cwd) {
		return modelPath
	}
	return filepath.Join(cwd, modelPath)
}

// isServerProcess checks argv[0] and, failing that, the comm from stat — a
// server started through a wrapper script can have an argv[0] that says
// nothing while comm still reads "llama-server".
func isServerProcess(fsRoot string, pid int, argv []string) bool {
	if len(argv) > 0 && IsServerCommand(argv[0]) {
		return true
	}
	data, err := os.ReadFile(pidPath(fsRoot, pid, "stat"))
	if err != nil {
		return false
	}
	st, err := ParseStat(data)
	if err != nil {
		return false
	}
	return IsServerCommand(st.Comm)
}

// Args returns the full argv of pid. It is the ServerInfo.Args field of the
// tape: the flags the card prints come from it.
func Args(fsRoot string, pid int) ([]string, error) {
	p := pidPath(fsRoot, pid, "cmdline")
	data, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("procmon: pid %d: %w", pid, ErrNotFound)
		}
		return nil, fmt.Errorf("procmon: read %s: %w", p, err)
	}
	return ParseCmdline(data), nil
}
