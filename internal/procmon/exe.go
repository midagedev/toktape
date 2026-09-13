package procmon

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// deletedSuffix is what the kernel appends to /proc/<pid>/exe once the file
// the process was started from has been replaced or removed — which is what
// rebuilding a server that is still running does.
const deletedSuffix = " (deleted)"

// Exe returns the path of the executable pid runs, read from the
// /proc/<pid>/exe link. The kernel's " (deleted)" marker is dropped: the path
// is still where the binary was, and the checkout around it is still there.
//
// The link of another user's process is not readable without privilege, so a
// caller treats any error as "not observed" rather than as a fault.
func Exe(fsRoot string, pid int) (string, error) {
	p := pidPath(fsRoot, pid, "exe")
	target, err := os.Readlink(p)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("procmon: pid %d exe: %w", pid, ErrNotFound)
		}
		return "", fmt.Errorf("procmon: readlink %s: %w", p, err)
	}
	return strings.TrimSuffix(target, deletedSuffix), nil
}

// maxGitWalk is how many parent directories GitCommitNear climbs above the
// binary's own directory. A build tree puts the binary at <repo>/build/bin,
// two levels down; six leaves room for deeper layouts without reaching a
// repository the binary merely happens to live under, such as a dotfiles
// repository at $HOME.
const maxGitWalk = 6

// GitCommit is a commit read from a checkout, with the files it came from.
type GitCommit struct {
	// Hash is the first eight hex digits of the commit, lower case.
	Hash string
	// HeadFile is the checkout's HEAD, without root. Git rewrites it when the
	// checkout switches branch or detaches.
	HeadFile string
	// RefFile is the loose ref file the hash was read from, without root,
	// which git rewrites on every commit, pull or reset of that branch. It is
	// "" for a detached HEAD (HEAD holds the hash) and for a branch resolved
	// only from packed-refs, whose mtime dates the last gc, not a branch move.
	RefFile string
}

// GitCommitNear returns the first eight hex digits of the commit checked out
// in the git repository nearest above path, or "" when there is none or it
// cannot be read. It is FindGitCommit without the provenance.
func GitCommitNear(root, path string) string {
	c, _ := FindGitCommit(root, path)
	return c.Hash
}

// FindGitCommit reads the commit checked out in the git repository nearest
// above path. ok is false exactly when no commit was read, and then the
// GitCommit is zero. The paths it carries are without root.
//
// It is how an ik_llama.cpp server gets a version at all (TTP-33,
// 2026-09-13): ik's /props carries no build_info, and a server built from a
// clone runs out of <clone>/build/bin. The walk starts at filepath.Dir(path)
// and climbs at most maxGitWalk parents. The nearest repository is the answer
// even when it is unreadable — a broken one does not fall through to a
// repository around it, whose commit would be somebody else's.
//
// Only files are read: no git binary is run. root prefixes every path, so
// tests work on a fixture tree; the recorder passes "".
//
// The commit is the checkout's HEAD now, not necessarily the one the binary
// was built from: the clone may have moved since, or carry local commits and
// uncommitted changes (measured on a real ik workstation, 2026-09-13). A
// caller that records it must say where it came from, and
// BinaryOlderThanCheckout tells whether the checkout moved after the build.
func FindGitCommit(root, path string) (GitCommit, bool) {
	if path == "" {
		return GitCommit{}, false
	}
	dir := filepath.Dir(path)
	for i := 0; i <= maxGitWalk; i++ {
		if gitDir, found := findGitDir(root, dir); found {
			return commitOf(root, gitDir)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return GitCommit{}, false
		}
		dir = parent
	}
	return GitCommit{}, false
}

// BinaryOlderThanCheckout reports whether the file at exe was last modified
// before the checkout last moved to c: the later of HeadFile's and RefFile's
// mtimes. A binary older than that was built from an earlier state of the
// checkout, so c is not its commit.
//
// Both files are read, and nothing else, for two reasons (lead, 2026-09-13):
// a branch switch rewrites HEAD only, leaving the branch's ref file older than
// the move, so the ref file alone would miss it; and a gc rewrites packed-refs
// without moving anything, so its mtime would claim an older binary falsely.
// A false "older" on the card is worse than a missing one. For the same
// reason anything that cannot be stat'ed makes the answer false.
func BinaryOlderThanCheckout(root, exe string, c GitCommit) bool {
	if exe == "" || c.Hash == "" {
		return false
	}
	bin, err := os.Stat(under(root, exe))
	if err != nil {
		return false
	}
	var moved time.Time
	for _, f := range []string{c.HeadFile, c.RefFile} {
		if f == "" {
			continue
		}
		if fi, err := os.Stat(under(root, f)); err == nil && fi.ModTime().After(moved) {
			moved = fi.ModTime()
		}
	}
	return !moved.IsZero() && bin.ModTime().Before(moved)
}

// under joins root and an absolute path; an empty root is the live tree.
func under(root, p string) string {
	if root == "" {
		return p
	}
	return filepath.Join(root, p)
}

// findGitDir looks for dir/.git. A directory is the repository itself; a
// file is a worktree or submodule pointer ("gitdir: <path>", relative to dir
// when not absolute). found reports that .git exists at all, so the caller
// stops climbing even when the pointer is malformed and gitDir is "".
func findGitDir(root, dir string) (gitDir string, found bool) {
	dotGit := filepath.Join(dir, ".git")
	fi, err := os.Stat(under(root, dotGit))
	if err != nil {
		return "", false
	}
	if fi.IsDir() {
		return dotGit, true
	}
	data, err := os.ReadFile(under(root, dotGit))
	if err != nil {
		return "", true
	}
	target := parseGitdirFile(data)
	if target == "" {
		return "", true
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(dir, target)
	}
	return filepath.Clean(target), true
}

// parseGitdirFile reads the one line of a .git file: "gitdir: <path>".
func parseGitdirFile(data []byte) string {
	line := strings.TrimSpace(firstLine(data))
	rest, ok := strings.CutPrefix(line, "gitdir:")
	if !ok {
		return ""
	}
	return strings.TrimSpace(rest)
}

// commitOf resolves HEAD in gitDir. A worktree's own directory holds HEAD and
// names the shared one in its commondir file; branches live there.
func commitOf(root, gitDir string) (GitCommit, bool) {
	if gitDir == "" {
		return GitCommit{}, false
	}
	common := gitDir
	if data, err := os.ReadFile(under(root, filepath.Join(gitDir, "commondir"))); err == nil {
		if c := strings.TrimSpace(firstLine(data)); c != "" {
			if !filepath.IsAbs(c) {
				c = filepath.Join(gitDir, c)
			}
			common = filepath.Clean(c)
		}
	}
	headFile := filepath.Join(gitDir, "HEAD")
	data, err := os.ReadFile(under(root, headFile))
	if err != nil {
		return GitCommit{}, false
	}
	refFile := ""
	// A symbolic ref may name another symbolic ref; git follows a handful of
	// hops and so does this, which also ends a loop between two refs.
	for hop := 0; hop < 5; hop++ {
		hash, ref := parseHead(data)
		if hash != "" {
			return GitCommit{Hash: strings.ToLower(hash[:8]), HeadFile: headFile, RefFile: refFile}, true
		}
		if ref == "" {
			return GitCommit{}, false
		}
		data, refFile = resolveRef(root, gitDir, common, ref)
		if data == nil {
			return GitCommit{}, false
		}
	}
	return GitCommit{}, false
}

// parseHead reads a HEAD or loose ref file: either a full object name
// (hash) or "ref: refs/..." (ref). Anything else yields two empty strings.
func parseHead(data []byte) (hash, ref string) {
	line := strings.TrimSpace(firstLine(data))
	if rest, ok := strings.CutPrefix(line, "ref:"); ok {
		return "", validRef(strings.TrimSpace(rest))
	}
	if isObjectName(line) {
		return line, ""
	}
	return "", ""
}

// validRef admits a ref name that stays inside refs/, so a hostile or corrupt
// HEAD cannot turn the lookup into a read of an arbitrary file.
func validRef(ref string) string {
	if !strings.HasPrefix(ref, "refs/") || strings.Contains(ref, "..") {
		return ""
	}
	return ref
}

// resolveRef returns the content a loose ref file would hold for ref, and the
// loose file it was read from: the worktree's own directory, then the common
// one, then the packed-refs line rewritten as a loose file (file "": see
// GitCommit.RefFile). nil means not found.
func resolveRef(root, gitDir, common, ref string) (data []byte, file string) {
	for _, d := range []string{gitDir, common} {
		f := filepath.Join(d, filepath.FromSlash(ref))
		if data, err := os.ReadFile(under(root, f)); err == nil {
			return data, f
		}
	}
	packedFile := filepath.Join(common, "packed-refs")
	packed, err := os.ReadFile(under(root, packedFile))
	if err != nil {
		return nil, ""
	}
	if hash := parsePackedRefs(packed, ref); hash != "" {
		return []byte(hash), ""
	}
	return nil, ""
}

// parsePackedRefs finds ref in a packed-refs file. Lines are
// "<hash> <refname>"; "#" starts the header and "^" a peeled tag object, and
// neither names a ref.
func parsePackedRefs(data []byte, ref string) string {
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || line[0] == '#' || line[0] == '^' {
			continue
		}
		hash, name, ok := strings.Cut(line, " ")
		if ok && strings.TrimSpace(name) == ref && isObjectName(hash) {
			return hash
		}
	}
	return ""
}

// isObjectName reports whether s is a full git object name: 40 hex digits for
// SHA-1, 64 for SHA-256. HEAD and ref files always hold the full name, so a
// short one is corruption, not an abbreviation.
func isObjectName(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'f', r >= 'A' && r <= 'F':
		default:
			return false
		}
	}
	return true
}

// firstLine is data up to the first newline.
func firstLine(data []byte) string {
	if i := bytes.IndexByte(data, '\n'); i >= 0 {
		data = data[:i]
	}
	return string(data)
}
