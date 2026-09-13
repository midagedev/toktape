package render

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// WithAgg renders an asciicast to a GIF with agg, asciinema's own renderer.
//
// It is the fallback path, not the main one: agg is a separate install and
// picks its own font from the host, so two machines produce two different
// looking clips from the same tape — which is exactly what this package exists
// to avoid. GIF is the supported route. This exists for the case where someone
// already has agg configured the way they want it, and for checking our own
// raster against a second implementation of the same terminal.
func WithAgg(asciicast []byte, out string) error {
	bin, err := exec.LookPath("agg")
	if err != nil {
		return fmt.Errorf("render: agg is not on PATH (use GIF instead): %w", err)
	}
	if len(asciicast) == 0 {
		return fmt.Errorf("render: agg: empty asciicast")
	}

	dir, err := os.MkdirTemp("", "toktape-cast-")
	if err != nil {
		return fmt.Errorf("render: cast scratch directory: %w", err)
	}
	defer os.RemoveAll(dir)

	cast := filepath.Join(dir, "clip.cast")
	if err := os.WriteFile(cast, asciicast, 0o644); err != nil {
		return fmt.Errorf("render: write %s: %w", cast, err)
	}

	cmd := exec.Command(bin, cast, out)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("render: agg failed: %w\n%s", err, tailLines(stderr.String(), 20))
	}
	return nil
}
