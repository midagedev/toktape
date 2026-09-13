package recorder

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/midagedev/toktape/internal/server"
	"github.com/midagedev/toktape/internal/tape"
)

// TestWaitReasonBusy: a busy server is worth waiting for, and the CLI must be
// able to say why it is waiting (TTP-33, 2026-09-13).
func TestWaitReasonBusy(t *testing.T) {
	r := &run{}
	cases := []struct {
		err      error
		reason   string
		waitable bool
	}{
		{fmt.Errorf("x: %w", server.ErrBusy), ReasonBusy, true},
		{fmt.Errorf("x: %w", server.ErrLoading), ReasonLoading, true},
		{fmt.Errorf("x: %w", server.ErrUnreachable), "", false},
	}
	for _, tc := range cases {
		reason, waitable := r.waitReason(tc.err)
		if reason != tc.reason || waitable != tc.waitable {
			t.Errorf("waitReason(%v) = %q, %v; want %q, %v", tc.err, reason, waitable, tc.reason, tc.waitable)
		}
	}
}

const engineModel = "/models/gguf/DeepSeek-V3-0324-UD-Q4_K_XL-00001-of-00009.gguf"

// engineProc is a /proc holding one llama-server whose exe link points at
// exeTarget, or has no exe link when exeTarget is empty.
func engineProc(t *testing.T, exeTarget string) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "proc", "1234")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	argv := "/usr/local/bin/llama-server\x00--model\x00" + engineModel + "\x00-ngl\x0099\x00"
	if err := os.WriteFile(filepath.Join(dir, "cmdline"), []byte(argv), 0o644); err != nil {
		t.Fatal(err)
	}
	if exeTarget != "" {
		if err := os.Symlink(exeTarget, filepath.Join(dir, "exe")); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// ikCheckout copies internal/procmon/testdata/gitnear (an ik_llama.cpp clone
// on branch main at 7b79b229) into a temporary directory, renames its dot-git
// to .git, and stamps the binary and the checkout with controlled mtimes.
// binaryOlder puts the binary an hour before the branch last moved; otherwise
// an hour after. It returns the binary's path.
func ikCheckout(t *testing.T, binaryOlder bool) string {
	t.Helper()
	root := t.TempDir()
	src := filepath.Join("..", "procmon", "testdata", "gitnear")
	err := filepath.WalkDir(src, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		dst := filepath.Join(root, rel)
		if d.IsDir() {
			return os.MkdirAll(dst, 0o755)
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(dst, data, 0o644)
	})
	if err != nil {
		t.Fatalf("copy gitnear: %v", err)
	}
	repo := filepath.Join(root, "ik_llama.cpp")
	if err := os.Rename(filepath.Join(repo, "dot-git"), filepath.Join(repo, ".git")); err != nil {
		t.Fatal(err)
	}
	moved := time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)
	built := moved.Add(time.Hour)
	if binaryOlder {
		built = moved.Add(-time.Hour)
	}
	bin := filepath.Join(repo, "build", "bin", "llama-server")
	for p, at := range map[string]time.Time{
		bin:                                 built,
		filepath.Join(repo, ".git", "HEAD"): moved.Add(-24 * time.Hour),
		filepath.Join(repo, ".git", "refs", "heads", "main"): moved,
	} {
		if err := os.Chtimes(p, at, at); err != nil {
			t.Fatal(err)
		}
	}
	return bin
}

// TestCollectProcessRefinesEngine: on a local attach the process names the
// engine /props did not, and the checkout names the commit (TTP-33). A commit
// read from the checkout carries a warning saying so, because the checkout's
// HEAD is not necessarily what the binary was built from (engine addendum,
// 2026-09-13).
func TestCollectProcessRefinesEngine(t *testing.T) {
	const (
		fromCheckout = "engine commit 7b79b229 read from the checkout next to the binary, not from the binary"
		olderBinary  = fromCheckout + "; the binary is older than that commit"
	)
	cases := []struct {
		name                string
		exe                 string // "ik" and "ik-old" are the checkout's binary
		kind                tape.ServerKind
		build, commit       string
		wantKind            tape.ServerKind
		wantBuild, wantComm string
		wantWarnings        []string
	}{
		{"ik /props, binary built after the commit", "ik", tape.ServerUnknown, "", "", tape.ServerIKLlama, "", "7b79b229", []string{fromCheckout}},
		{"ik /props, binary older than the commit", "ik-old", tape.ServerUnknown, "", "", tape.ServerIKLlama, "", "7b79b229", []string{olderBinary}},
		{"props already said ik, commit from the checkout", "ik", tape.ServerIKLlama, "", "", tape.ServerIKLlama, "", "7b79b229", []string{fromCheckout}},
		// The server's own build_info is the record; the checkout is only read
		// when /props gave no commit, and then nothing needs explaining.
		{"props gave a commit, it is kept", "ik-old", tape.ServerLlamaCPP, "b3650", "abcdef12", tape.ServerIKLlama, "b3650", "abcdef12", nil},
		{"a bare llama-server binary stays unknown", "/usr/local/bin/llama-server", tape.ServerUnknown, "", "", tape.ServerUnknown, "", "", nil},
		// An unreadable exe link is normal (another user's process) and is
		// not a caveat worth a line on the card.
		{"no exe link: nothing refined", "", tape.ServerUnknown, "", "", tape.ServerUnknown, "", "", nil},
		{"mainline is not read from a checkout", "/usr/local/bin/llama-server", tape.ServerLlamaCPP, "b4321", "", tape.ServerLlamaCPP, "b4321", "", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			exe := tc.exe
			switch exe {
			case "ik":
				exe = ikCheckout(t, false)
			case "ik-old":
				exe = ikCheckout(t, true)
			}
			r := &run{
				opts:   Options{FSRoot: engineProc(t, exe)},
				props:  &server.Props{ModelPath: engineModel},
				kind:   tc.kind,
				build:  tc.build,
				commit: tc.commit,
			}
			r.collectProcess()
			if r.pid != 1234 {
				t.Fatalf("pid = %d, want 1234 (warnings %q)", r.pid, r.warnings)
			}
			if len(r.args) == 0 {
				t.Errorf("argv was not read")
			}
			if r.kind != tc.wantKind || r.build != tc.wantBuild || r.commit != tc.wantComm {
				t.Errorf("kind, build, commit = %q, %q, %q; want %q, %q, %q",
					r.kind, r.build, r.commit, tc.wantKind, tc.wantBuild, tc.wantComm)
			}
			if !slices.Equal(r.warnings, tc.wantWarnings) {
				t.Errorf("warnings = %q, want %q", r.warnings, tc.wantWarnings)
			}
		})
	}
}
