package card

import (
	"testing"

	"github.com/midagedev/toktape/internal/tape"
)

// TTP-208 item 5: the one line a run that sent images adds to the card. No
// image tokens and no encode time are printed, because no engine reports them.
func TestImageValue(t *testing.T) {
	png := tape.ImageRef{Name: "builtin", MIME: "image/png", Bytes: 7646, Width: 640, Height: 480}
	jpg := tape.ImageRef{Name: "photo.jpg", MIME: "image/jpeg", Bytes: 5, Width: 1024, Height: 768}
	webp := tape.ImageRef{Name: "p.webp", MIME: "image/webp", Bytes: 5}
	for _, tc := range []struct {
		name string
		refs []tape.ImageRef
		want string
	}{
		{"none", nil, ""},
		{"one builtin", []tape.ImageRef{png}, "1 × 640×480 PNG (builtin) · prefill includes it"},
		{"one file", []tape.ImageRef{jpg}, "1 × 1024×768 JPEG (photo.jpg) · prefill includes it"},
		{"size unknown prints ?", []tape.ImageRef{webp}, "1 × ? WEBP (p.webp) · prefill includes it"},
		{"several", []tape.ImageRef{png, jpg}, "2 images · 640×480, 1024×768"},
		{"several, one unsized", []tape.ImageRef{png, webp}, "2 images · 640×480, ?"},
	} {
		if got := ImageValue(tc.refs); got != tc.want {
			t.Errorf("%s: %q, want %q", tc.name, got, tc.want)
		}
	}
}
