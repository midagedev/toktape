package png

import (
	"fmt"
	"image"
	"strings"
	"testing"

	"github.com/midagedev/toktape/internal/card"
	"github.com/midagedev/toktape/internal/tape"
)

// 2026-09-15, ExLlamaV3: the flag strip and the footer's engine column learn
// a second client — an engine that reports its own argv. Everything they
// printed for a llama-server run is pinned here, literals taken from the
// renderer as it stood BEFORE that change, so a predicate that moves a llama
// run by one byte fails here rather than in a Reddit card. The text card's
// half of the same contract is the committed goldens under
// internal/card/testdata (TestTextGolden).
func TestLlamaFlagMarksUnchanged(t *testing.T) {
	cases := []struct {
		name  string
		s     *tape.RunSummary
		strip string
		row1  string
		row2  string
	}{
		{"example", card.Example(),
			"-ngl 99  -fa on  -b 2048  -ub 512  -ctk q8_0  -ctv q8_0  -t 16",
			"fa on · ctk q8_0 · ctv q8_0",
			"b 2048 · ub 512 · ngl 99"},
		{"speculative", card.ExampleSpeculative(),
			"-ngl 99  -fa on  -b 2048  -ub 512  -ctk q8_0  -ctv q8_0  -ncmoe 48  -t 16  -md DSpark-0.6B-Q8_0.gguf  --draft-max 3  --draft-min 1",
			"fa on · ctk q8_0 · ctv q8_0",
			"b 2048 · ub 512 · ngl 99"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cv, err := renderCanvas(c.s)
			if err != nil {
				t.Fatalf("renderCanvas: %v", err)
			}
			for id, want := range map[string]string{
				"strip.flags":      c.strip,
				"footer.col2.row1": c.row1,
				"footer.col2.row2": c.row2,
			} {
				m, ok := cv.markByID(id)
				if !ok {
					t.Fatalf("%s was never drawn", id)
				}
				if m.Text != want {
					t.Errorf("%s = %q, want the pre-change %q", id, m.Text, want)
				}
			}
		})
	}
}

// TestExLlamaV3Card: the engine fixture's picture. The flag strip carries the
// engine's own argv, no mark anywhere teaches a llama.cpp flag, and every
// text mark sits inside the content box — the TestShardedStaysInsideTheGrid
// shape, on the fixture whose rows all changed.
func TestExLlamaV3Card(t *testing.T) {
	s := card.ExampleExLlamaV3()
	img, err := Render(s)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if got, want := img.Bounds(), image.Rect(0, 0, Width, Height); got != want {
		t.Fatalf("bounds = %v, want %v", got, want)
	}
	cv, err := renderCanvas(s)
	if err != nil {
		t.Fatalf("renderCanvas: %v", err)
	}
	m, ok := cv.markByID("strip.flags")
	if !ok {
		t.Fatal("strip.flags was never drawn")
	}
	if got, want := m.Text, "-gs 44,21 -mcs 185 -mct 32 -mtp"; got != want {
		t.Errorf("strip.flags = %q, want the engine argv %q", got, want)
	}
	for _, m := range cv.marks {
		if m.Kind != "text" || m.Text == "" {
			continue
		}
		for _, llama := range []string{"-fa ", "-ctk ", "-ctv ", "-ub ", "-b 2048", "-ngl "} {
			if strings.Contains(m.Text, llama) {
				t.Errorf("%s (%q) teaches a llama.cpp flag an engine run never had", m.ID, m.Text)
			}
		}
		// The engine named no per-device split, so no bandwidth clause may be
		// derived: absent, not "?" — the text card's contract, on the image.
		for _, absent := range []string{"from RAM", "of peak"} {
			if strings.Contains(m.Text, absent) {
				t.Errorf("%s (%q) prints %q the engine's silence should have withheld", m.ID, m.Text, absent)
			}
		}
		if m.Rect.Min.X < contentL || m.Rect.Max.X > contentR {
			t.Errorf("%s (%q) spans x=%d..%d, outside the content box (%d..%d)",
				m.ID, m.Text, m.Rect.Min.X, m.Rect.Max.X, contentL, contentR)
		}
	}
	// The footer's engine column: rows 1 and 2 carry the argv between them,
	// inside the column, with row 0 and row 3 untouched by this change.
	left, right := contentL+2*footerColStep, contentL+2*footerColStep+footerColW
	var sawArgs bool
	for _, r := range []int{1, 2} {
		m, ok := cv.markByID(colID(2, fmt.Sprintf("row%d", r)))
		if !ok {
			t.Fatalf("footer.col2.row%d was never drawn", r)
		}
		if m.Rect.Min.X < left || m.Rect.Max.X > right {
			t.Errorf("footer.col2.row%d (%q) spans x=%d..%d, outside its column (%d..%d)",
				r, m.Text, m.Rect.Min.X, m.Rect.Max.X, left, right)
		}
		if strings.Contains(m.Text, "-gs 44,21") {
			sawArgs = true
		}
	}
	if !sawArgs {
		t.Error("no footer engine row carries the argv")
	}
}

// TestExLlamaV3SplitBandwidth: the fixture variant whose engine reported a
// consistent per-device active split gets bandwidth figures back — the
// engine's own, not the class estimate.
//
// The clause a two-stream hero can carry is the verify-step one: the plain
// "from RAM" spelling only renders under a single-stream number
// (content.go's hero build), and this run drafted with -mtp, so the
// per-accepted-token view is replaced by the per-step view in the draft
// clause — the same precedence the text card applies. When the column is
// full, pickWidest gives up the "of peak" ratio before the rate
// (content.go's fallback order), so the image's guarantee is the rate; the
// ratio is pinned on the text card, where the room is.
func TestExLlamaV3SplitBandwidth(t *testing.T) {
	s := card.ExampleExLlamaV3Split()
	cv, err := renderCanvas(s)
	if err != nil {
		t.Fatalf("renderCanvas: %v", err)
	}
	var sawRate bool
	for _, m := range cv.marks {
		if m.Kind == "text" && strings.Contains(m.Text, "GB/s RAM/step") {
			sawRate = true
		}
	}
	if !sawRate {
		t.Error("no mark carries the verify-step RAM rate for the engine's consistent split")
	}
}
