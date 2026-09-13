package procmon_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/midagedev/toktape/internal/procmon"
)

// ikBinary is where the gitnear fixture keeps its server binary, relative to
// the tree's root.
const ikBinary = "/ik_llama.cpp/build/bin/llama-server"

// gitnearTree copies testdata/gitnear into a temporary root and renames its
// dot-git directory to .git. Git refuses to track a path with a .git
// component, so the fixture cannot carry the real name.
func gitnearTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	copyTree(t, filepath.Join("testdata", "gitnear"), root)
	repo := filepath.Join(root, "ik_llama.cpp")
	if err := os.Rename(filepath.Join(repo, "dot-git"), filepath.Join(repo, ".git")); err != nil {
		t.Fatal(err)
	}
	return root
}

func writeFile(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestGitCommitNear is the ik_llama.cpp build line (TTP-33, 2026-09-13): ik's
// /props carries no build_info, so the commit is read from the checkout the
// running binary sits in.
func TestGitCommitNear(t *testing.T) {
	gitDir := filepath.Join("ik_llama.cpp", ".git")
	cases := []struct {
		name  string
		setup func(t *testing.T, root string)
		path  string
		want  string
	}{
		{
			// The fixture packs refs/heads/main at 0badc0de and holds a loose
			// ref at 7b79b229. Git reads the loose file first.
			name: "a loose branch ref outranks packed-refs",
			path: ikBinary,
			want: "7b79b229",
		},
		{
			name: "a branch that exists only in packed-refs",
			setup: func(t *testing.T, root string) {
				writeFile(t, filepath.Join(root, gitDir, "HEAD"), "ref: refs/heads/packed-only\n")
			},
			path: ikBinary,
			want: "5eed5eed",
		},
		{
			name: "packed-refs after the loose ref is gone",
			setup: func(t *testing.T, root string) {
				if err := os.Remove(filepath.Join(root, gitDir, "refs", "heads", "main")); err != nil {
					t.Fatal(err)
				}
			},
			path: ikBinary,
			want: "0badc0de",
		},
		{
			name: "a detached HEAD is the hash",
			setup: func(t *testing.T, root string) {
				writeFile(t, filepath.Join(root, gitDir, "HEAD"), "c0ffee00aabbccddeeff00112233445566778899\n")
			},
			path: ikBinary,
			want: "c0ffee00",
		},
		{
			name: "a sha256 repository",
			setup: func(t *testing.T, root string) {
				writeFile(t, filepath.Join(root, gitDir, "HEAD"),
					"deadbeef00112233445566778899aabbccddeeff00112233445566778899aabb\n")
			},
			path: ikBinary,
			want: "deadbeef",
		},
		{
			// `git worktree add ../wt feature`: .git is a file naming the
			// per-worktree directory, and the branch lives in the common one.
			name: "a worktree with a relative gitdir file",
			setup: func(t *testing.T, root string) {
				wt := filepath.Join(root, "ik_llama.cpp", ".git", "worktrees", "wt")
				writeFile(t, filepath.Join(root, "wt", ".git"), "gitdir: ../ik_llama.cpp/.git/worktrees/wt\n")
				writeFile(t, filepath.Join(wt, "HEAD"), "ref: refs/heads/feature\n")
				writeFile(t, filepath.Join(wt, "commondir"), "../..\n")
				writeFile(t, filepath.Join(root, gitDir, "refs", "heads", "feature"), "fea70e00112233445566778899aabbccddeeff00\n")
			},
			path: "/wt/build/bin/llama-server",
			want: "fea70e00",
		},
		{
			name: "a worktree with an absolute gitdir file",
			setup: func(t *testing.T, root string) {
				wt := filepath.Join(root, "ik_llama.cpp", ".git", "worktrees", "wt")
				writeFile(t, filepath.Join(root, "wt", ".git"), "gitdir: /ik_llama.cpp/.git/worktrees/wt\n")
				writeFile(t, filepath.Join(wt, "HEAD"), "ref: refs/heads/main\n")
				writeFile(t, filepath.Join(wt, "commondir"), "../..\n")
			},
			path: "/wt/build/bin/llama-server",
			want: "7b79b229",
		},
		{
			name: "no repository anywhere above",
			path: "/usr/local/bin/llama-server",
			want: "",
		},
		{
			// The nearest repository is the one the binary was built in. A
			// broken one must not borrow the commit of a repository around it.
			name: "a broken nearest repository does not fall through to an outer one",
			setup: func(t *testing.T, root string) {
				if err := os.MkdirAll(filepath.Join(root, "ik_llama.cpp", "build", ".git"), 0o755); err != nil {
					t.Fatal(err)
				}
			},
			path: ikBinary,
			want: "",
		},
		{
			name: "a HEAD that is neither a ref nor a hash",
			setup: func(t *testing.T, root string) {
				writeFile(t, filepath.Join(root, gitDir, "HEAD"), "not a head\n")
			},
			path: ikBinary,
			want: "",
		},
		{
			name: "a ref that points outside refs/",
			setup: func(t *testing.T, root string) {
				writeFile(t, filepath.Join(root, gitDir, "HEAD"), "ref: ../../../etc/hostname\n")
			},
			path: ikBinary,
			want: "",
		},
		{
			name: "a loose ref holding a short hash",
			setup: func(t *testing.T, root string) {
				writeFile(t, filepath.Join(root, gitDir, "refs", "heads", "main"), "7b79b229\n")
			},
			path: ikBinary,
			want: "",
		},
		{
			name: "a gitdir file without the gitdir prefix",
			setup: func(t *testing.T, root string) {
				writeFile(t, filepath.Join(root, "wt", ".git"), "../ik_llama.cpp/.git\n")
			},
			path: "/wt/build/bin/llama-server",
			want: "",
		},
		{
			name: "an empty path",
			path: "",
			want: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := gitnearTree(t)
			if tc.setup != nil {
				tc.setup(t, root)
			}
			if got := procmon.GitCommitNear(root, tc.path); got != tc.want {
				t.Errorf("GitCommitNear(root, %q) = %q, want %q", tc.path, got, tc.want)
			}
		})
	}
}

// TestGitCommitNearWalkLimit pins the six-parent bound: a binary installed
// deep under an unrelated repository (a dotfiles repo in $HOME, say) must not
// report that repository's commit as the engine's.
func TestGitCommitNearWalkLimit(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "r", ".git", "HEAD"), "c0ffee00aabbccddeeff00112233445566778899\n")
	cases := []struct {
		path string
		want string
	}{
		// Dir is r/1/2/3/4/5/6; its sixth parent is r.
		{"/r/1/2/3/4/5/6/llama-server", "c0ffee00"},
		// Dir is r/1/2/3/4/5/6/7; r is the seventh parent.
		{"/r/1/2/3/4/5/6/7/llama-server", ""},
	}
	for _, tc := range cases {
		if got := procmon.GitCommitNear(root, tc.path); got != tc.want {
			t.Errorf("GitCommitNear(root, %q) = %q, want %q", tc.path, got, tc.want)
		}
	}
}

// TestGitCommitNearLiveRoot is the recorder's call: an empty root reads the
// absolute path as it is.
func TestGitCommitNearLiveRoot(t *testing.T) {
	root := gitnearTree(t)
	if got, want := procmon.GitCommitNear("", root+ikBinary), "7b79b229"; got != want {
		t.Errorf("GitCommitNear(\"\", %q) = %q, want %q", root+ikBinary, got, want)
	}
}

func TestExe(t *testing.T) {
	cases := []struct {
		name, link, want string
	}{
		{"a running binary", "/opt/ik_llama.cpp/build/bin/llama-server", "/opt/ik_llama.cpp/build/bin/llama-server"},
		// The kernel appends " (deleted)" once the file was replaced, which is
		// what rebuilding a server that is still running does.
		{"a binary rebuilt under the running server", "/opt/ik_llama.cpp/build/bin/llama-server (deleted)", "/opt/ik_llama.cpp/build/bin/llama-server"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			dir := filepath.Join(root, "proc", "1234")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(tc.link, filepath.Join(dir, "exe")); err != nil {
				t.Fatal(err)
			}
			got, err := procmon.Exe(root, 1234)
			if err != nil {
				t.Fatalf("Exe: %v", err)
			}
			if got != tc.want {
				t.Errorf("Exe = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestExeMissingPID(t *testing.T) {
	_, err := procmon.Exe(t.TempDir(), 1234)
	if !errors.Is(err, procmon.ErrNotFound) {
		t.Fatalf("Exe error = %v, want ErrNotFound", err)
	}
}

// TestExeThenGitCommitNear is the chain the recorder runs on a local ik
// server: the exe link names a binary inside a checkout, and the checkout
// names the commit.
func TestExeThenGitCommitNear(t *testing.T) {
	tree := gitnearTree(t)
	proc := t.TempDir()
	dir := filepath.Join(proc, "proc", "77")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(tree+ikBinary, filepath.Join(dir, "exe")); err != nil {
		t.Fatal(err)
	}
	exe, err := procmon.Exe(proc, 77)
	if err != nil {
		t.Fatalf("Exe: %v", err)
	}
	if got, want := procmon.GitCommitNear("", exe), "7b79b229"; got != want {
		t.Errorf("GitCommitNear(Exe) = %q, want %q", got, want)
	}
}

// TestFindGitCommitProvenance: the files whose mtimes date the commit, which
// the older-binary check stats (engine addendum, 2026-09-13).
func TestFindGitCommitProvenance(t *testing.T) {
	gitDir := "/ik_llama.cpp/.git"
	cases := []struct {
		name  string
		setup func(t *testing.T, root string)
		path  string
		want  procmon.GitCommit
		ok    bool
	}{
		{
			name: "a loose branch ref",
			path: ikBinary,
			want: procmon.GitCommit{Hash: "7b79b229", HeadFile: gitDir + "/HEAD", RefFile: gitDir + "/refs/heads/main"},
			ok:   true,
		},
		{
			// packed-refs' mtime dates a gc, not a branch move, so it is not
			// offered as the ref file.
			name: "a branch resolved in packed-refs has no ref file",
			setup: func(t *testing.T, root string) {
				writeFile(t, filepath.Join(root, "ik_llama.cpp", ".git", "HEAD"), "ref: refs/heads/packed-only\n")
			},
			path: ikBinary,
			want: procmon.GitCommit{Hash: "5eed5eed", HeadFile: gitDir + "/HEAD"},
			ok:   true,
		},
		{
			name: "a detached HEAD dates itself",
			setup: func(t *testing.T, root string) {
				writeFile(t, filepath.Join(root, "ik_llama.cpp", ".git", "HEAD"), "c0ffee00aabbccddeeff00112233445566778899\n")
			},
			path: ikBinary,
			want: procmon.GitCommit{Hash: "c0ffee00", HeadFile: gitDir + "/HEAD"},
			ok:   true,
		},
		{
			name: "a worktree's branch lives in the common directory",
			setup: func(t *testing.T, root string) {
				wt := filepath.Join(root, "ik_llama.cpp", ".git", "worktrees", "wt")
				writeFile(t, filepath.Join(root, "wt", ".git"), "gitdir: ../ik_llama.cpp/.git/worktrees/wt\n")
				writeFile(t, filepath.Join(wt, "HEAD"), "ref: refs/heads/main\n")
				writeFile(t, filepath.Join(wt, "commondir"), "../..\n")
			},
			path: "/wt/build/bin/llama-server",
			want: procmon.GitCommit{Hash: "7b79b229", HeadFile: gitDir + "/worktrees/wt/HEAD", RefFile: gitDir + "/refs/heads/main"},
			ok:   true,
		},
		{
			name: "no repository",
			path: "/usr/local/bin/llama-server",
			ok:   false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := gitnearTree(t)
			if tc.setup != nil {
				tc.setup(t, root)
			}
			got, ok := procmon.FindGitCommit(root, tc.path)
			if ok != tc.ok || got != tc.want {
				t.Errorf("FindGitCommit(root, %q) = %+v, %v; want %+v, %v", tc.path, got, ok, tc.want, tc.ok)
			}
		})
	}
}

// TestBinaryOlderThanCheckout: a binary last written before the checkout last
// moved (the later of HEAD and the loose ref) was not built from that commit.
func TestBinaryOlderThanCheckout(t *testing.T) {
	base := time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)
	hour := time.Hour
	cases := []struct {
		name               string
		head               string // HEAD content; "" keeps the fixture's
		bin, headAt, refAt time.Duration
		wantOlder          bool
	}{
		{name: "built after the last commit", bin: 2 * hour, headAt: 0, refAt: hour, wantOlder: false},
		{name: "a commit after the build", bin: hour, headAt: 0, refAt: 2 * hour, wantOlder: true},
		{name: "the same instant is not older", bin: hour, headAt: 0, refAt: hour, wantOlder: false},
		{name: "detached HEAD moved after the build", head: "c0ffee00aabbccddeeff00112233445566778899\n", bin: hour, headAt: 2 * hour, refAt: 0, wantOlder: true},
		// A branch switch rewrites HEAD and leaves the branch's ref file old.
		{name: "a branch switch after the build", bin: hour, headAt: 2 * hour, refAt: 0, wantOlder: true},
		// A gc rewrites packed-refs without moving anything; HEAD is older than
		// the binary, so no claim (lead, 2026-09-13).
		{name: "packed-refs rewritten by a gc after the build", head: "ref: refs/heads/packed-only\n", bin: hour, headAt: 0, refAt: 3 * hour, wantOlder: false},
		{name: "a packed-only branch switched to after the build", head: "ref: refs/heads/packed-only\n", bin: hour, headAt: 2 * hour, refAt: 0, wantOlder: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := gitnearTree(t)
			repo := filepath.Join(root, "ik_llama.cpp")
			if tc.head != "" {
				writeFile(t, filepath.Join(repo, ".git", "HEAD"), tc.head)
			}
			touch := func(rel string, at time.Duration) {
				if err := os.Chtimes(filepath.Join(repo, rel), base.Add(at), base.Add(at)); err != nil {
					t.Fatal(err)
				}
			}
			touch("build/bin/llama-server", tc.bin)
			touch(".git/HEAD", tc.headAt)
			touch(".git/refs/heads/main", tc.refAt)
			touch(".git/packed-refs", tc.refAt)
			c, ok := procmon.FindGitCommit(root, ikBinary)
			if !ok {
				t.Fatal("FindGitCommit found nothing")
			}
			if got := procmon.BinaryOlderThanCheckout(root, ikBinary, c); got != tc.wantOlder {
				t.Errorf("BinaryOlderThanCheckout = %v, want %v (commit %+v)", got, tc.wantOlder, c)
			}
		})
	}
}

// TestBinaryOlderThanCheckoutUnobserved: nothing stat'ed, nothing claimed.
func TestBinaryOlderThanCheckoutUnobserved(t *testing.T) {
	root := gitnearTree(t)
	c, ok := procmon.FindGitCommit(root, ikBinary)
	if !ok {
		t.Fatal("FindGitCommit found nothing")
	}
	if procmon.BinaryOlderThanCheckout(root, "/ik_llama.cpp/build/bin/gone", c) {
		t.Error("a missing binary was reported older than the checkout")
	}
	if procmon.BinaryOlderThanCheckout(root, ikBinary, procmon.GitCommit{}) {
		t.Error("an empty commit was compared")
	}
}
