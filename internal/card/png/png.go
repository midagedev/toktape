// Package png renders a tape.RunSummary as the 1200×675 share card.
//
// This is the image that gets posted: X and Reddit crop an OpenGraph large
// image to exactly 16:9, so the canvas is fixed and every layout constant in
// theme.go is a pixel offset inside it. The text card (internal/card) and this
// one are two renderings of the same contract — same fields, same order, same
// "?" for anything that was not observed (docs/toktape-spec.ko.md §4).
//
// The renderer is pure Go and needs no cgo, no fontconfig and no external
// binary: shapes go through golang.org/x/image/vector, glyphs through
// golang.org/x/image/font/opentype, and both typefaces are embedded from
// assets/fonts. Rendering the same summary twice produces the same bytes; the
// card never reads the clock.
package png

import (
	"fmt"
	"image"
	stdpng "image/png"
	"os"
	"path/filepath"

	"github.com/midagedev/toktape/internal/tape"
)

// Render draws the share card for s.
//
// A nil summary renders the all-unknown card rather than panicking, which is
// what a tape that failed to attach should still produce: the layout is the
// proof that nothing was silently dropped.
func Render(s *tape.RunSummary) (image.Image, error) {
	c, err := renderCanvas(s)
	if err != nil {
		return nil, err
	}
	return c.img, nil
}

// renderCanvas is Render plus the placement ledger the tests assert on.
func renderCanvas(s *tape.RunSummary) (*canvas, error) {
	if s == nil {
		s = &tape.RunSummary{}
	}
	fs, err := newFontSet()
	if err != nil {
		return nil, err
	}
	defer fs.Close()

	c := newCanvas(fs)
	c.draw(s)
	return c, nil
}

// Write renders s and writes it to path as a PNG, creating parent directories.
//
// The file is written through a temporary file in the same directory and
// renamed, so a reader watching the output directory never sees a half-encoded
// card.
func Write(path string, s *tape.RunSummary) error {
	img, err := Render(s)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("png: create %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".toktape-card-*.png")
	if err != nil {
		return fmt.Errorf("png: create temp file in %s: %w", dir, err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	enc := stdpng.Encoder{CompressionLevel: stdpng.BestCompression}
	if err := enc.Encode(tmp, img); err != nil {
		tmp.Close()
		return fmt.Errorf("png: encode %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("png: close %s: %w", tmpName, err)
	}
	// os.CreateTemp makes the file 0600. The card exists to be handed to other
	// people, so it is widened to the usual 0644 before it is put in place.
	if err := os.Chmod(tmpName, 0o644); err != nil {
		return fmt.Errorf("png: chmod %s: %w", tmpName, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("png: write %s: %w", path, err)
	}
	return nil
}
