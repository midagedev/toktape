package card

import (
	"fmt"
	"strings"

	"github.com/midagedev/toktape/internal/tape"
)

// ImageValue is the value of the card's Image line for a run that sent images
// (TTP-208), "" when it sent none:
//
//	1 × 640×480 PNG (builtin) · prefill includes it
//	2 images · 640×480, 1024×768
//
// An undecoded size prints "?". No image-token count and no encode time are
// printed, not even as "?": no engine reports either, so a "?" would imply a
// figure that exists. The prefill row keeps the server's prompt_n and
// prompt_ms, which include the image.
func ImageValue(refs []tape.ImageRef) string {
	return strings.Join(ImageParts(refs), " · ")
}

// ImageParts is ImageValue split at its separators, so the text card's row
// wraps between clauses and never inside one.
func ImageParts(refs []tape.ImageRef) []string {
	switch len(refs) {
	case 0:
		return nil
	case 1:
		r := refs[0]
		return []string{fmt.Sprintf("1 × %s %s (%s)", ImageSize(r), ImageKind(r.MIME), imageName(r.Name)), "prefill includes it"}
	}
	sizes := make([]string, len(refs))
	for i, r := range refs {
		sizes[i] = ImageSize(r)
	}
	return []string{fmt.Sprintf("%d images", len(refs)), strings.Join(sizes, ", ")}
}

// imageName keeps a long file name from taking the row: 24 runes, then an
// ellipsis.
func imageName(n string) string {
	if r := []rune(n); len(r) > 24 {
		return string(r[:23]) + "…"
	}
	return n
}

// ImageSize is "640×480", or "?" when the size was not decoded.
func ImageSize(r tape.ImageRef) string {
	if r.Width <= 0 || r.Height <= 0 {
		return unknown
	}
	return fmt.Sprintf("%d×%d", r.Width, r.Height)
}

// ImageKind is the format as a reader says it: "image/png" is a PNG.
func ImageKind(mime string) string {
	return strings.ToUpper(strings.TrimPrefix(mime, "image/"))
}
