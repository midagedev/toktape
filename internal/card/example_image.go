package card

import "github.com/midagedev/toktape/internal/tape"

// ExampleImage returns Example's run sent with `--image builtin` (TTP-208): one
// 640×480 PNG, the embedded test image. The server's prompt figures are
// Example's own, since they already include whatever the prompt held; the only
// thing the picture adds to the card is the Image line. The sha256 is that of
// internal/server/images/testimage.png at the time of writing, which only the
// preview line prints, never the card.
func ExampleImage() *tape.RunSummary {
	s := Example()
	s.Images = []tape.ImageRef{{
		Name:   "builtin",
		SHA256: "327a34bc4d359c64f75e55781994e5f95ab074ce5200315fb5c0ced14cb0265f",
		Bytes:  7646,
		MIME:   "image/png",
		Width:  640,
		Height: 480,
	}}
	return s
}
