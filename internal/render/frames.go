package render

import (
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"

	"github.com/midagedev/toktape/internal/tape"
)

// FramePattern is the printf pattern of a frame file name, and the one ffmpeg
// is handed.
const FramePattern = "frame_%05d.png"

// Frames renders every frame of the clip into dir as frame_00000.png,
// frame_00001.png, … and returns the paths it wrote, in order. An unset size
// is VideoWidth×VideoHeight, the 1920×1080 the mp4 is encoded from.
//
// dir is created if it does not exist. Existing frames with the same names are
// overwritten; frames left over from a longer previous clip are not removed,
// because deleting files a caller did not name is not this function's
// business — MP4 renders into a directory of its own for exactly that reason.
func Frames(tp *tape.Tape, opts Options, dir string) ([]string, error) {
	o, sched, err := prepare(tp, opts.withVideoDefaults())
	if err != nil {
		return nil, err
	}
	rs, err := newRasteriser(o.FontSize)
	if err != nil {
		return nil, err
	}
	defer rs.Close()

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("render: frame directory %s: %w", dir, err)
	}

	// One image buffer and one encoder for the whole sequence: at 156×38 and a
	// 20 px cell a frame is 1920×1080, and allocating that 331 times is pure
	// garbage. BestSpeed is the right level here — these PNGs exist to be
	// handed to ffmpeg and deleted, and the default level triples the wall
	// time of an mp4 render for a file nobody keeps.
	img := image.NewRGBA(rs.bounds(o.Width, o.Height))
	enc := png.Encoder{CompressionLevel: png.BestSpeed}

	paths := make([]string, 0, sched.Count)
	for i := 0; i < sched.Count; i++ {
		f := sched.Frame(i)
		rs.drawInto(img, parseScreen(FrameText(tp, o, f), o.Width, o.Height))

		path := filepath.Join(dir, fmt.Sprintf(FramePattern, i))
		if err := writePNG(&enc, path, img); err != nil {
			return paths, err
		}
		paths = append(paths, path)
	}
	return paths, nil
}

func writePNG(enc *png.Encoder, path string, img image.Image) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("render: create %s: %w", path, err)
	}
	if err := enc.Encode(f, img); err != nil {
		f.Close()
		return fmt.Errorf("render: encode %s: %w", path, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("render: close %s: %w", path, err)
	}
	return nil
}

// FrameImage renders a single frame of the clip, which is how a caller asks
// for one still ("the card at the end", "the frame at 4 s") without writing a
// sequence.
func FrameImage(tp *tape.Tape, opts Options, f Frame) (*image.RGBA, error) {
	o := opts.withDefaults()
	if err := o.validate(); err != nil {
		return nil, err
	}
	rs, err := newRasteriser(o.FontSize)
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	return rs.draw(parseScreen(FrameText(tp, o, f), o.Width, o.Height)), nil
}
