package render

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/midagedev/toktape/internal/tui"
)

func TestMP4(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg is not on PATH; mp4 rendering is an exec of it and cannot be exercised here")
	}
	out := filepath.Join(t.TempDir(), "clip.mp4")
	// Four frames at a small cell: enough for libx264 to produce a real file
	// without rendering a full clip inside a unit test.
	if err := MP4(tui.ExampleTape(), Options{FPS: 2, Duration: 2 * time.Second, FontSize: 10}, out); err != nil {
		t.Fatalf("MP4: %v", err)
	}
	fi, err := os.Stat(out)
	if err != nil {
		t.Fatalf("stat %s: %v", out, err)
	}
	if fi.Size() == 0 {
		t.Error("the mp4 is empty")
	}
}

func TestMP4LeavesNoFramesBehind(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg is not on PATH")
	}
	dir := t.TempDir()
	out := filepath.Join(dir, "clip.mp4")
	if err := MP4(tui.ExampleTape(), Options{FPS: 2, Duration: time.Second, FontSize: 10}, out); err != nil {
		t.Fatalf("MP4: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	// The mp4 is the artifact; the PNGs it was built from go to a temporary
	// directory of their own and are removed.
	if len(entries) != 1 {
		names := make([]string, len(entries))
		for i, e := range entries {
			names[i] = e.Name()
		}
		t.Errorf("the output directory holds %v, want only the mp4", names)
	}
}

func TestWithAggReportsAMissingBinaryClearly(t *testing.T) {
	if _, err := exec.LookPath("agg"); err == nil {
		t.Skip("agg is installed; this test pins the message when it is not")
	}
	err := WithAgg([]byte("{\"version\":2}\n"), filepath.Join(t.TempDir(), "out.gif"))
	if err == nil {
		t.Fatal("WithAgg succeeded without agg on PATH")
	}
	// The message has to name the missing tool and the way out of it, or the
	// user is left reading "exec: not found" about a program they never asked
	// for.
	if !strings.Contains(err.Error(), "agg") || !strings.Contains(err.Error(), "GIF") {
		t.Errorf("error is %q, want it to name agg and point at GIF", err)
	}
}

func TestWithAggRejectsAnEmptyRecording(t *testing.T) {
	if _, err := exec.LookPath("agg"); err != nil {
		t.Skip("agg is not on PATH")
	}
	if err := WithAgg(nil, filepath.Join(t.TempDir(), "out.gif")); err == nil {
		t.Error("WithAgg accepted an empty recording")
	}
}
