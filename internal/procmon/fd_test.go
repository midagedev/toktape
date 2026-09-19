package procmon_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/midagedev/toktape/internal/procmon"
)

// FDTarget is how the recorder finds the file a server's stdout or stderr
// was redirected to (TTP-137): the /proc/<pid>/fd/<n> link, read the same
// way Exe reads /proc/<pid>/exe.
func TestFDTarget(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "proc", "42", "fd")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(t.TempDir(), "server.log")
	if err := os.WriteFile(logPath, []byte("llama_init_from_model: KV self size = 640.00 MiB\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(logPath, filepath.Join(dir, "1")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/dev/null", filepath.Join(dir, "2")); err != nil {
		t.Fatal(err)
	}

	got, err := procmon.FDTarget(root, 42, 1)
	if err != nil {
		t.Fatalf("FDTarget fd 1: %v", err)
	}
	if got != logPath {
		t.Errorf("FDTarget fd 1 = %q, want %q", got, logPath)
	}
	// The kernel spells non-files "pipe:[...]"/"socket:[...]"/devices; the
	// link still reads, and the caller decides what to do with the string.
	got, err = procmon.FDTarget(root, 42, 2)
	if err != nil {
		t.Fatalf("FDTarget fd 2: %v", err)
	}
	if got != "/dev/null" {
		t.Errorf("FDTarget fd 2 = %q, want /dev/null", got)
	}

	if _, err := procmon.FDTarget(root, 43, 1); !errors.Is(err, procmon.ErrNotFound) {
		t.Errorf("missing pid: err = %v, want ErrNotFound", err)
	}
	if _, err := procmon.FDTarget(root, 42, 3); !errors.Is(err, procmon.ErrNotFound) {
		t.Errorf("missing fd: err = %v, want ErrNotFound", err)
	}
}
