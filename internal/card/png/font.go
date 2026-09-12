package png

import (
	"fmt"

	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/font/sfnt"

	"github.com/midagedev/toktape/assets/fonts"
)

// weight selects one of the three JetBrains Mono cuts the card uses.
type weight int

const (
	wRegular weight = iota
	wBold
	wExtraBold
)

// fontSet owns the parsed faces for one render.
//
// Neither sfnt.Font nor the faces opentype hands out are documented as safe
// for concurrent use, so a set is built per Render call rather than cached in
// a package variable. Parsing is table indexing, not rasterising, so the cost
// is a fraction of a millisecond against the ~40 ms the card takes to draw.
type fontSet struct {
	latin   map[weight]*sfnt.Font
	hangul  *sfnt.Font
	faces   map[faceKey]*faceSet
	measure sfnt.Buffer
}

type faceKey struct {
	size   float64
	weight weight
}

// faceSet is one type size: the Latin face and the Hangul fallback that
// covers the runes it has no glyph for.
type faceSet struct {
	latin    font.Face
	fallback font.Face
	set      *fontSet
	size     float64
	weight   weight
}

func newFontSet() (*fontSet, error) {
	fs := &fontSet{
		latin: make(map[weight]*sfnt.Font, 3),
		faces: make(map[faceKey]*faceSet, 12),
	}
	sources := map[weight][]byte{
		wRegular:   fonts.JetBrainsMonoRegular,
		wBold:      fonts.JetBrainsMonoBold,
		wExtraBold: fonts.JetBrainsMonoExtraBold,
	}
	for w, b := range sources {
		f, err := sfnt.Parse(b)
		if err != nil {
			return nil, fmt.Errorf("png: parse JetBrains Mono (weight %d): %w", w, err)
		}
		fs.latin[w] = f
	}
	h, err := sfnt.Parse(fonts.D2CodingRegular)
	if err != nil {
		return nil, fmt.Errorf("png: parse D2Coding: %w", err)
	}
	fs.hangul = h
	return fs, nil
}

// face returns the face pair for a size and weight, building it on first use.
func (fs *fontSet) face(size float64, w weight) (*faceSet, error) {
	k := faceKey{size: size, weight: w}
	if f, ok := fs.faces[k]; ok {
		return f, nil
	}
	// DPI 72 makes one point one pixel, so every size in theme.go is a pixel
	// height and the layout constants are readable as pixels.
	opts := &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingFull}
	latin, err := opentype.NewFace(fs.latin[w], opts)
	if err != nil {
		return nil, fmt.Errorf("png: face %.1fpx weight %d: %w", size, w, err)
	}
	fallback, err := opentype.NewFace(fs.hangul, opts)
	if err != nil {
		return nil, fmt.Errorf("png: fallback face %.1fpx: %w", size, err)
	}
	f := &faceSet{latin: latin, fallback: fallback, set: fs, size: size, weight: w}
	fs.faces[k] = f
	return f, nil
}

// faceFor picks the face that actually has a glyph for r.
//
// The Latin face is asked first so digits, punctuation and the box/maths runes
// keep JetBrains Mono's 0.6 em advance; anything it does not cover (Hangul,
// Han, Kana) falls through to D2Coding, where Hangul is exactly 2× the Latin
// advance. A rune neither face has is drawn from the Latin face, which
// renders .notdef rather than silently dropping the character — a visible
// tofu box is a bug report, a missing character is a wrong card.
func (f *faceSet) faceFor(r rune) font.Face {
	if _, ok := f.latin.GlyphAdvance(r); ok {
		return f.latin
	}
	if _, ok := f.fallback.GlyphAdvance(r); ok {
		return f.fallback
	}
	return f.latin
}

// hasGlyph reports whether either face can draw r. Used by tests, not by the
// drawing path.
func (f *faceSet) hasGlyph(r rune) bool {
	if _, ok := f.latin.GlyphAdvance(r); ok {
		return true
	}
	_, ok := f.fallback.GlyphAdvance(r)
	return ok
}

// metrics returns the ascent and descent of the Latin face in pixels. Both are
// positive.
func (f *faceSet) metrics() (ascent, descent int) {
	m := f.latin.Metrics()
	return m.Ascent.Ceil(), m.Descent.Ceil()
}

// Close releases every face in the set.
func (fs *fontSet) Close() {
	for _, f := range fs.faces {
		_ = f.latin.Close()
		_ = f.fallback.Close()
	}
	fs.faces = map[faceKey]*faceSet{}
}
