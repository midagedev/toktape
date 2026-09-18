package render

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The banned dependency: reading a GGUF header is recording-time work. At
// render time the tape already carries tape.PlacementSummary, and linking a
// GGUF parser (with its json-iterator and HTTP client) into a renderer drags
// megabytes of dead code into every wasm bundle and binary.
const bannedDep = "github.com/gpustack/gguf-parser-go"

// TestRenderPathHasNoGGUFParser fails if a render-path package can reach the
// GGUF parser. A renderer reads a *tape.Tape and nothing else (the repo
// contract in CLAUDE.md); if this trips, some import chain reconnected the
// parser to the render path — move the file-reading side back to
// internal/placement/gguf, which only the recorder imports.
func TestRenderPathHasNoGGUFParser(t *testing.T) {
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skipf("go binary not on PATH: %v", err)
	}

	repo, err := repoRoot()
	if err != nil {
		t.Fatalf("find repo root: %v", err)
	}

	for _, pkg := range []string{
		"github.com/midagedev/toktape/internal/render",
		"github.com/midagedev/toktape/internal/tui",
	} {
		cmd := exec.Command(goBin, "list", "-deps", pkg)
		cmd.Dir = repo
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("go list -deps %s: %v", pkg, err)
		}
		for _, line := range strings.Split(string(out), "\n") {
			if line == bannedDep || strings.HasPrefix(line, bannedDep+"/") {
				t.Errorf("%s depends on %s: a renderer reads a tape and nothing else — "+
					"reading a GGUF file is recording-time work and belongs in "+
					"internal/placement/gguf, which only the recorder imports",
					pkg, line)
			}
		}
	}
}

// repoRoot walks up from the test's working directory to the directory
// holding go.mod.
func repoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", os.ErrNotExist
		}
		dir = parent
	}
}
