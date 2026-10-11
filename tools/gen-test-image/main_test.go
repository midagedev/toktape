package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// committed is the file the generator exists to reproduce.
const committed = "../../internal/server/images/testimage.png"

// TestGeneratorReproducesTheCommittedImageByteForByte runs the generator into a
// temp file and compares it with the committed PNG (TTP-208 gate 10). The
// bytes depend on this Go toolchain's compress/flate; if a toolchain bump ever
// moves them, regenerate with `go run ./tools/gen-test-image` and commit the
// new file in the same change — the image's sha256 is recorded in tapes, so a
// silent drift would split comparable runs.
func TestGeneratorReproducesTheCommittedImageByteForByte(t *testing.T) {
	out := filepath.Join(t.TempDir(), "testimage.png")
	if code := run([]string{"-o", out}, io.Discard); code != 0 {
		t.Fatalf("generator exited %d", code)
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(committed)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("generator output (%d bytes) differs from the committed %s (%d bytes); run `go run ./tools/gen-test-image`", len(got), committed, len(want))
	}
	if len(want) >= 64<<10 {
		t.Fatalf("the built-in image is %d bytes; the budget is under 64 KB", len(want))
	}
}
