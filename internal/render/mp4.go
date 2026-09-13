package render

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"

	"github.com/midagedev/toktape/internal/tape"
)

// CRF is the x264 quality target. 21 is visually lossless on flat terminal
// colour and still small enough to attach; the frames are hard edges on a
// solid background, which is the easiest thing a codec is ever asked to do.
const CRF = 21

// MP4 renders the clip as an H.264 mp4 at out, using ffmpeg.
//
// ffmpeg is the one thing in this package that is not pure Go. Encoding H.264
// in-process would mean cgo and a licence question, and the spec's answer is
// to shell out and say so plainly when the binary is not there.
//
// The frames go to a temporary directory that is removed afterwards: an mp4
// is the artifact, and three hundred PNGs left in the user's working directory
// are litter. A caller who wants to keep them calls Frames directly.
func MP4(tp *tape.Tape, opts Options, out string) error {
	bin, err := exec.LookPath("ffmpeg")
	if err != nil {
		return fmt.Errorf("render: mp4 needs ffmpeg on PATH (install it, or use GIF): %w", err)
	}
	o := opts.withDefaults()

	dir, err := os.MkdirTemp("", "toktape-frames-")
	if err != nil {
		return fmt.Errorf("render: frame scratch directory: %w", err)
	}
	defer os.RemoveAll(dir)

	paths, err := Frames(tp, o, dir)
	if err != nil {
		return err
	}
	if len(paths) == 0 {
		return fmt.Errorf("render: mp4: the schedule produced no frames")
	}

	// yuv420p is what a browser and a phone will play; it needs even frame
	// dimensions, which rasteriser.bounds guarantees.
	args := []string{
		"-y",
		"-framerate", strconv.Itoa(o.FPS),
		"-i", filepath.Join(dir, FramePattern),
		"-c:v", "libx264",
		"-pix_fmt", "yuv420p",
		"-crf", strconv.Itoa(CRF),
		out,
	}
	cmd := exec.Command(bin, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("render: ffmpeg failed: %w\n%s", err, tailLines(stderr.String(), 20))
	}
	return nil
}

// tailLines returns the last n lines of s, which is the part of ffmpeg's
// output that says what went wrong.
func tailLines(s string, n int) string {
	lines := bytes.Split([]byte(s), []byte("\n"))
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return string(bytes.Join(lines, []byte("\n")))
}
