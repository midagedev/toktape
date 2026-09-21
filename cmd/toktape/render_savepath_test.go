package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestReplayDoesNotClaimASavePath: rendering a tape must not print
// "tape saved <path>" (TTP-133, 2026-09-21).
//
// The footer's word is a claim about where the RECORDING wrote. A replay
// never watched that happen. `render` used to pass the file it was reading,
// so rendering assets/hero.tape printed "tape saved assets/hero.tape" for a
// run that had been written to ~/.toktape/runs -- a printed fact nobody
// observed, which is the one rule this repo does not bend.
//
// Deriving "~/.toktape/runs/<id>.tape" from the record would be the same
// violation wearing a default, because record takes --out. The tape carries
// no save path, so the honest footer is the one the renderer already has for
// an unsaved run: "run complete".
//
// The cast is the artifact under test because it is the only text one; the
// GIF and mp4 carry the same frames as pixels.
//
// FAIL-first: on the pre-change source this reports the footer claiming
// <tmpdir>/<id>.tape, the path the test itself handed in.
func TestReplayDoesNotClaimASavePath(t *testing.T) {
	dir := t.TempDir()
	tapePath := writeExampleTape(t, dir)
	castPath := filepath.Join(dir, "clip.cast")

	args := append([]string{"render", tapePath, "--cast", castPath}, shortClip...)
	if code, _, errOut := exec(t, args...); code != exitOK {
		t.Fatalf("render: exit %d, stderr %q", code, errOut)
	}
	b, err := os.ReadFile(castPath)
	if err != nil {
		t.Fatalf("no cast written: %v", err)
	}
	cast := string(b)

	// The cast escapes its frames as JSON strings, so the path arrives with
	// its separators intact but the assertion is a plain substring either way.
	if strings.Contains(cast, "tape saved") {
		t.Errorf("the replay's footer claims a save path it did not observe:\n%s", footerContext(cast))
	}
	// And the honest footer is actually there -- otherwise this test would
	// pass on a clip that renders no footer at all.
	if !strings.Contains(cast, "run complete") {
		t.Errorf("the replay's footer says neither %q nor %q", "run complete", "tape saved")
	}
	// The tape's own path must not reach the frames by any other spelling.
	if strings.Contains(cast, filepath.Base(tapePath)) {
		t.Errorf("the replayed file's name reached the clip:\n%s", footerContext(cast))
	}
}

// footerContext is the slice of the cast around whatever it said about the
// tape, so a failure names the sentence rather than the whole recording.
func footerContext(cast string) string {
	for _, needle := range []string{"tape saved", "run complete", ".tape"} {
		if i := strings.Index(cast, needle); i >= 0 {
			lo := max(0, i-120)
			hi := min(len(cast), i+160)
			return cast[lo:hi]
		}
	}
	return "(the cast mentions no tape at all)"
}
