package render

import (
	"fmt"
	"image"
)

// TextImage rasterises one already-drawn frame — w×h cells of text with the
// palette's escape sequences in it — the way every clip frame is rasterised,
// at DefaultFontSize.
//
// FrameImage draws an instant of a tape; this draws a frame that no tape
// holds. It exists for the chat screen's look loop (TTP-184, 2026-09-24):
// tuidump -chat renders scripted chat states whose frames come from
// tui.ChatView rather than from tui.ModelAt, some of them narrower than the
// record screen's minimum, which FrameImage's size check would refuse.
func TextImage(frame string, w, h int) (*image.RGBA, error) {
	if w <= 0 || h <= 0 {
		return nil, fmt.Errorf("render: frame %d×%d has no cells", w, h)
	}
	rs, err := newRasteriser(DefaultFontSize)
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	return rs.draw(parseScreen(frame, w, h)), nil
}
